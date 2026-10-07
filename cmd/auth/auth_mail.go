package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

func sendMailCode(ctx context.Context, email, code string, purpose authCodePurpose, locale string) error {
	composed, err := composeAuthCode(ctx, env("EMAIL_COMPOSE_GRPC_ADDR", "email-compose:50051"), locale, email, code, purpose)
	if err != nil {
		return fmt.Errorf("compose auth email: %w", err)
	}
	return sendComposedMail(email, composed)
}

func buildAuthMail(from, to string, content composedEmail) ([]byte, string, error) {
	fromAddress, err := mail.ParseAddress(from)
	if err != nil || fromAddress.Address == "" {
		return nil, "", errors.New("invalid SMTP_FROM")
	}
	if !validEmail(to) {
		return nil, "", errors.New("invalid recipient")
	}
	if strings.ContainsAny(content.subject, "\r\n") || strings.TrimSpace(content.subject) == "" || content.html == "" || content.text == "" {
		return nil, "", errors.New("invalid composed email")
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	for _, part := range []struct{ mediaType, content string }{
		{"text/plain", content.text}, {"text/html", content.html},
	} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", part.mediaType+"; charset=UTF-8")
		header.Set("Content-Transfer-Encoding", "base64")
		writer, err := multipartWriter.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(part.content))
		for len(encoded) > 76 {
			if _, err := fmt.Fprint(writer, encoded[:76]+"\r\n"); err != nil {
				return nil, "", err
			}
			encoded = encoded[76:]
		}
		if _, err := fmt.Fprint(writer, encoded+"\r\n"); err != nil {
			return nil, "", err
		}
	}
	if err := multipartWriter.Close(); err != nil {
		return nil, "", err
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", fromAddress.String())
	fmt.Fprintf(&message, "To: %s\r\n", to)
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", content.subject))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprint(&message, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", multipartWriter.Boundary())
	message.Write(body.Bytes())
	return message.Bytes(), fromAddress.Address, nil
}

func sendComposedMail(to string, content composedEmail) error {
	host := env("SMTP_HOST", "")
	port := env("SMTP_PORT", "587")
	from := env("SMTP_FROM", "")
	if host == "" || from == "" {
		return errors.New("SMTP not configured")
	}
	message, envelopeFrom, err := buildAuthMail(from, to, content)
	if err != nil {
		return err
	}
	mode := strings.ToLower(env("SMTP_TLS", "starttls"))
	if mode != "starttls" && mode != "smtps" && mode != "none" {
		return errors.New("invalid SMTP_TLS")
	}
	address := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	if mode == "smtps" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP STARTTLS unavailable")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if user := env("SMTP_USER", ""); user != "" {
		if err := client.Auth(smtp.PlainAuth("", user, env("SMTP_PASSWORD", ""), host)); err != nil {
			return err
		}
	}
	if err := client.Mail(envelopeFrom); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
