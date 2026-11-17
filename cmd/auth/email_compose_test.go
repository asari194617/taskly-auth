package main

import (
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

func TestAuthCodeMailStaysLocalAndContainsOnlyItsOwnCode(t *testing.T) {
	for _, test := range []struct {
		purpose authCodePurpose
		locale  string
		subject string
	}{
		{loginCodePurpose, "ja", "ログインコード"},
		{securityCodePurpose, "ja", "認証設定の確認コード"},
		{emailVerificationCodePurpose, "ja", "メールアドレスの確認コード"},
		{loginCodePurpose, "en", "Your login code"},
		{securityCodePurpose, "en", "Your security verification code"},
		{emailVerificationCodePurpose, "en", "Verify your email address"},
	} {
		t.Run(string(test.purpose)+"-"+test.locale, func(t *testing.T) {
			content, err := composeAuthCode(test.locale, "user@example.test", "123456", test.purpose)
			if err != nil || content.subject != test.subject || !strings.Contains(content.html, "123456") || !strings.Contains(content.text, "123456") || !strings.Contains(content.text, "10") {
				t.Fatalf("invalid locally composed mail: subject=%q error=%v", content.subject, err)
			}
			raw, envelope, err := buildAuthMail("Taskly <noreply@example.test>", "user@example.test", content)
			if err != nil || envelope != "noreply@example.test" {
				t.Fatalf("buildAuthMail: envelope=%q error=%v", envelope, err)
			}
			message, err := mail.ReadMessage(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			subject, err := (&mime.WordDecoder{}).DecodeHeader(message.Header.Get("Subject"))
			if err != nil || subject != test.subject {
				t.Fatalf("subject=%q error=%v", subject, err)
			}
			_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			parts := multipart.NewReader(message.Body, params["boundary"])
			for _, want := range []string{content.text, content.html} {
				part, err := parts.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, part))
				if err != nil || string(decoded) != want {
					t.Fatalf("part mismatch: error=%v", err)
				}
			}
		})
	}
	if _, err := composeAuthCode("ja", "user@example.test", "123456", "unknown"); err == nil {
		t.Fatal("unknown purpose accepted")
	}
}
