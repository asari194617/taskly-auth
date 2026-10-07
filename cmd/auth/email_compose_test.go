package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestBuildAuthMailPreservesComposedPartsAndEncodesSubject(t *testing.T) {
	content := composedEmail{
		subject: "認証設定の確認コード",
		text:    "確認コード: 123456\n10分で期限切れになります。",
		html:    "<html><body>確認コード: <b>123456</b></body></html>",
	}
	raw, envelope, err := buildAuthMail("Taskly <noreply@example.test>", "user@example.test", content)
	if err != nil {
		t.Fatal(err)
	}
	if envelope != "noreply@example.test" {
		t.Fatalf("SMTP envelope sender = %q", envelope)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	decodedSubject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || decodedSubject != content.subject {
		t.Fatalf("decoded subject = %q, err = %v", decodedSubject, err)
	}
	_, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	parts := multipart.NewReader(parsed.Body, params["boundary"])
	for _, want := range []string{content.text, content.html} {
		part, err := parts.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(string(encoded))))
		if err != nil || string(decoded) != want {
			t.Fatalf("decoded MIME part = %q, err = %v", decoded, err)
		}
	}
	if _, err := parts.NextPart(); !errors.Is(err, io.EOF) {
		t.Fatalf("extra MIME part: %v", err)
	}
}

func TestComposeAuthCodeUsesPurposeRPCAndTenMinuteLifetime(t *testing.T) {
	requestDesc, responseDesc, err := emailComposeDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var called []string
	server := grpc.NewServer()
	methods := []string{"ComposeLoginCode", "ComposeSecurityCode", "ComposeEmailVerificationCode"}
	descriptors := make([]grpc.MethodDesc, 0, len(methods))
	for _, method := range methods {
		method := method
		descriptors = append(descriptors, grpc.MethodDesc{
			MethodName: method,
			Handler: func(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				request := dynamicpb.NewMessage(requestDesc)
				if err := decode(request); err != nil {
					return nil, err
				}
				field := func(name protoreflect.Name) protoreflect.Value { return request.Get(requestDesc.Fields().ByName(name)) }
				code := field("code").String()
				if field("locale").String() != "en" || field("email").String() != "user@example.test" || (code != "123456" && code != "000000") || field("app_name").String() != "Taskly" || field("expiry_minutes").Int() != 10 {
					return nil, errors.New("incorrect auth code request")
				}
				called = append(called, method)
				response := dynamicpb.NewMessage(responseDesc)
				subject := "Code"
				if code == "000000" {
					subject = ""
				}
				for name, value := range map[protoreflect.Name]string{"subject": subject, "html": "<p>123456</p>", "text": "123456"} {
					response.Set(responseDesc.Fields().ByName(name), protoreflect.ValueOfString(value))
				}
				return response, nil
			},
		})
	}
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "emailcompose.EmailComposer", HandlerType: (*interface{})(nil), Methods: descriptors}, struct{}{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	for _, purpose := range []authCodePurpose{loginCodePurpose, securityCodePurpose, emailVerificationCodePurpose} {
		result, err := composeAuthCode(t.Context(), listener.Addr().String(), "en", "user@example.test", "123456", purpose)
		if err != nil || result.subject != "Code" || result.html != "<p>123456</p>" || result.text != "123456" {
			t.Fatalf("purpose %s: response = %+v, err = %v", purpose, result, err)
		}
	}
	if strings.Join(called, ",") != strings.Join(methods, ",") {
		t.Fatalf("RPCs called = %v", called)
	}
	if _, err := composeAuthCode(t.Context(), listener.Addr().String(), "en", "user@example.test", "000000", loginCodePurpose); err == nil {
		t.Fatal("accepted incomplete composer response")
	}
}

func TestComposeAuthCodeRejectsUnknownPurposeAndUnsafeSubject(t *testing.T) {
	if _, err := composeAuthCode(t.Context(), "127.0.0.1:1", "ja", "user@example.test", "123456", "unexpected"); err == nil {
		t.Fatal("accepted unknown mail purpose")
	}
	if _, _, err := buildAuthMail("noreply@example.test", "user@example.test", composedEmail{subject: "ok\r\nBcc: x@example.test", html: "x", text: "x"}); err == nil {
		t.Fatal("accepted injected subject")
	}
}

// Set AUTH_TEST_EMAIL_COMPOSE_ADDR when testing the Go client against a running
// Taskly email-compose build. The ordinary unit suite remains self-contained.
func TestComposeAuthCodeAgainstEmailCompose(t *testing.T) {
	address := os.Getenv("AUTH_TEST_EMAIL_COMPOSE_ADDR")
	if address == "" {
		t.Skip("email-compose service address not supplied")
	}
	for _, test := range []struct {
		purpose authCodePurpose
		want    string
	}{
		{loginCodePurpose, "ログインコード"},
		{securityCodePurpose, "認証設定の確認コード"},
		{emailVerificationCodePurpose, "メールアドレスの確認コード"},
	} {
		t.Run(string(test.purpose), func(t *testing.T) {
			result, err := composeAuthCode(t.Context(), address, "ja", "user@example.test", "123456", test.purpose)
			if err != nil {
				t.Fatal(err)
			}
			if result.subject != test.want || !strings.Contains(result.text, "10分") || !strings.Contains(result.html, "123456") {
				t.Fatalf("unexpected composed email: subject=%q, ten-minute-expiry=%t, code-in-html=%t", result.subject, strings.Contains(result.text, "10分"), strings.Contains(result.html, "123456"))
			}
		})
	}
}
