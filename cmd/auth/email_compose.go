package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

type authCodePurpose string

const (
	loginCodePurpose             authCodePurpose = "login"
	securityCodePurpose          authCodePurpose = "security"
	emailVerificationCodePurpose authCodePurpose = "email_verification"
)

type composedEmail struct {
	subject string
	html    string
	text    string
}

func authCodeMethod(purpose authCodePurpose) (string, error) {
	switch purpose {
	case loginCodePurpose:
		return "/emailcompose.EmailComposer/ComposeLoginCode", nil
	case securityCodePurpose:
		return "/emailcompose.EmailComposer/ComposeSecurityCode", nil
	case emailVerificationCodePurpose:
		return "/emailcompose.EmailComposer/ComposeEmailVerificationCode", nil
	default:
		return "", errors.New("invalid auth email purpose")
	}
}

func emailComposeDescriptors() (protoreflect.MessageDescriptor, protoreflect.MessageDescriptor, error) {
	stringField := func(name string, number int32) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name: proto.String(name), Number: proto.Int32(number),
			Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		}
	}
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("taskly_auth_email_compose.proto"),
		Package: proto.String("emailcompose"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("ComposeAuthCodeRequest"), Field: []*descriptorpb.FieldDescriptorProto{
				stringField("locale", 1), stringField("email", 2), stringField("code", 3), stringField("app_name", 4),
				{Name: proto.String("expiry_minutes"), Number: proto.Int32(5), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()},
			}},
			{Name: proto.String("ComposeResponse"), Field: []*descriptorpb.FieldDescriptorProto{
				stringField("subject", 1), stringField("html", 2), stringField("text", 3),
			}},
		},
	}, nil)
	if err != nil {
		return nil, nil, err
	}
	return file.Messages().ByName("ComposeAuthCodeRequest"), file.Messages().ByName("ComposeResponse"), nil
}

func composeAuthCode(ctx context.Context, address, locale, email, code string, purpose authCodePurpose) (composedEmail, error) {
	method, err := authCodeMethod(purpose)
	if err != nil {
		return composedEmail{}, err
	}
	requestDesc, responseDesc, err := emailComposeDescriptors()
	if err != nil {
		return composedEmail{}, err
	}
	if locale != "en" {
		locale = "ja"
	}
	request := dynamicpb.NewMessage(requestDesc)
	for name, value := range map[protoreflect.Name]string{
		"locale": locale, "email": email, "code": code, "app_name": "Taskly",
	} {
		request.Set(requestDesc.Fields().ByName(name), protoreflect.ValueOfString(value))
	}
	request.Set(requestDesc.Fields().ByName("expiry_minutes"), protoreflect.ValueOfInt32(10))
	response := dynamicpb.NewMessage(responseDesc)
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return composedEmail{}, err
	}
	defer conn.Close()
	if err := conn.Invoke(callCtx, method, request, response); err != nil {
		return composedEmail{}, err
	}
	result := composedEmail{
		subject: response.Get(responseDesc.Fields().ByName("subject")).String(),
		html:    response.Get(responseDesc.Fields().ByName("html")).String(),
		text:    response.Get(responseDesc.Fields().ByName("text")).String(),
	}
	if strings.TrimSpace(result.subject) == "" || strings.TrimSpace(result.html) == "" || strings.TrimSpace(result.text) == "" {
		return composedEmail{}, fmt.Errorf("email-compose returned incomplete %s email", purpose)
	}
	return result, nil
}
