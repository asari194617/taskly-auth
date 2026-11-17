package main

import (
	"errors"
	"fmt"
	"html"
	"strings"
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

// Authentication codes are inserted into the message inside this process.
// No code or destination address is sent to the shared email-compose service.
func composeAuthCode(locale, email, code string, purpose authCodePurpose) (composedEmail, error) {
	if !validEmail(email) || !validTOTPCode(code) {
		return composedEmail{}, errors.New("invalid auth email")
	}
	if locale != "en" {
		locale = "ja"
	}
	type copy struct{ subject, label, intro string }
	var content copy
	switch purpose {
	case loginCodePurpose:
		if locale == "en" {
			content = copy{"Your login code", "Login code:", "Use the following code to sign in."}
		} else {
			content = copy{"ログインコード", "ログインコード:", "以下のコードを使用してログインしてください。"}
		}
	case securityCodePurpose:
		if locale == "en" {
			content = copy{"Your security verification code", "Verification code:", "Enter this code to confirm a change to your authentication settings."}
		} else {
			content = copy{"認証設定の確認コード", "確認コード:", "認証設定の変更を確認するため、以下のコードを入力してください。"}
		}
	case emailVerificationCodePurpose:
		if locale == "en" {
			content = copy{"Verify your email address", "Verification code:", "Enter this code to verify your email address."}
		} else {
			content = copy{"メールアドレスの確認コード", "確認コード:", "メールアドレスを確認するため、以下のコードを入力してください。"}
		}
	default:
		return composedEmail{}, errors.New("invalid auth email purpose")
	}
	var greeting, expiry, noShare string
	if locale == "en" {
		greeting = "Hi " + email
		expiry = "This code expires in 10 minutes. If you didn't request it, you can ignore this email."
		noShare = "Do not share this code with anyone."
	} else {
		greeting = email + " さん"
		expiry = "このコードは10分で期限切れになります。心当たりがない場合はこのメールを無視してください。"
		noShare = "このコードを他人と共有しないでください。"
	}
	paragraphs := []string{greeting, content.intro, content.label + " " + code, expiry, noShare}
	escaped := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		escaped[i] = "<p>" + html.EscapeString(paragraph) + "</p>"
	}
	return composedEmail{
		subject: content.subject,
		html:    fmt.Sprintf("<!doctype html><html lang=%q><body>%s</body></html>", locale, strings.Join(escaped, "")),
		text:    strings.Join(paragraphs, "\n\n"),
	}, nil
}
