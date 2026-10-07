package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

type loginInput struct {
	Email     string `json:"email"`
	Password  string `json:"password"`
	Code      string `json:"code"`
	TOTP      string `json:"totp"`
	Recovery  string `json:"recovery_code"`
	Challenge string `json:"auth_challenge"`
}

func (a *app) requireUI(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != a.uiURL || r.Header.Get("X-Auth-UI") != "1" {
		oauthError(w, 403, "invalid_origin")
		return false
	}
	return true
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	var input loginInput
	if !parseJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) {
		oauthError(w, 400, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "login:"+email)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth rate-limit database failed")
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	var id uint64
	var hash sql.NullString
	var emailAllowed, deleted bool
	err = a.db.QueryRowContext(r.Context(), `SELECT id,hashed_password,email_code_allowed,is_deleted FROM users WHERE email=?`, email).Scan(&id, &hash, &emailAllowed, &deleted)
	if err != nil || deleted || a.blocked(r, id) {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	if input.Challenge != "" {
		if input.Password != "" || !a.consumeChallenge(r, input.Challenge, id) {
			oauthError(w, 401, "invalid_credentials")
			return
		}
		if !a.verifySecondFactor(r, id, email, input, emailAllowed) {
			oauthError(w, 401, "invalid_credentials")
			return
		}
		a.finishLogin(w, r, id, "password_"+secondFactorMethod(input))
		return
	}
	if hash.Valid && hash.String != "" {
		if input.Password == "" || input.Code != "" || input.TOTP != "" || input.Recovery != "" {
			oauthError(w, 401, "invalid_credentials")
			return
		}
		if bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(input.Password)) != nil {
			oauthError(w, 401, "invalid_credentials")
			return
		}
		hasMFA := emailAllowed
		var count int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM totp_secrets WHERE user_id=? AND is_active=1`, id).Scan(&count); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		hasMFA = hasMFA || count > 0
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM recovery_codes WHERE user_id=? AND is_used=0`, id).Scan(&count); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		hasMFA = hasMFA || count > 0
		if hasMFA {
			challenge, err := randomToken()
			if err != nil {
				oauthError(w, 500, "server_error")
				return
			}
			_, err = a.db.ExecContext(r.Context(), `INSERT INTO auth_challenges (token_hash,user_id,expires_at) VALUES (?,?,?)`, digest(challenge), id, time.Now().UTC().Add(5*time.Minute))
			if err != nil {
				oauthError(w, 500, "server_error")
				return
			}
			jsonResponse(w, 202, map[string]any{"mfa_required": true, "auth_challenge": challenge, "expires_in": 300})
			return
		}
		a.finishLogin(w, r, id, "password")
		return
	}
	if input.Password != "" || !a.verifySecondFactor(r, id, email, input, emailAllowed) {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	a.finishLogin(w, r, id, secondFactorMethod(input))
}

// Only call this after verifySecondFactor has accepted exactly one method.
// The method recorded in history is derived from credentials verified by the
// server, never from a client-provided history label.
func secondFactorMethod(input loginInput) string {
	switch {
	case input.Code != "":
		return "email_code"
	case input.TOTP != "":
		return "totp"
	default:
		return "recovery_code"
	}
}

func (a *app) blocked(r *http.Request, id uint64) bool {
	var count int
	jst := time.Now().In(time.FixedZone("JST", 9*3600))
	err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM blocks WHERE user_id=? AND (expires_at IS NULL OR expires_at>?)`, id, jst).Scan(&count)
	return err != nil || count > 0
}

func (a *app) consumeChallenge(r *http.Request, challenge string, id uint64) bool {
	result, err := a.db.ExecContext(r.Context(), `DELETE FROM auth_challenges WHERE token_hash=? AND user_id=? AND expires_at>UTC_TIMESTAMP()`, digest(challenge), id)
	if err != nil {
		return false
	}
	n, _ := result.RowsAffected()
	return n == 1
}

func (a *app) verifySecondFactor(r *http.Request, id uint64, email string, input loginInput, emailAllowed bool) bool {
	methods := 0
	if input.Code != "" {
		methods++
	}
	if input.TOTP != "" {
		methods++
	}
	if input.Recovery != "" {
		methods++
	}
	if methods != 1 {
		return false
	}
	if input.Code != "" && emailAllowed {
		return a.consumeEmailCode(r, id, email, input.Code)
	}
	if input.TOTP != "" {
		var encrypted string
		err := a.db.QueryRowContext(r.Context(), `SELECT secret FROM totp_secrets WHERE user_id=? AND is_active=1`, id).Scan(&encrypted)
		if err != nil {
			return false
		}
		secret, err := decryptTOTP(encrypted, osEnv("TOTP_ENC_KEY"))
		return err == nil && totp.Validate(input.TOTP, secret)
	}
	if input.Recovery != "" {
		result, err := a.db.ExecContext(r.Context(), `UPDATE recovery_codes SET is_used=1 WHERE user_id=? AND hashed_code=? AND is_used=0`, id, digest(input.Recovery))
		if err != nil {
			return false
		}
		n, _ := result.RowsAffected()
		return n == 1
	}
	return false
}

func osEnv(key string) string { return strings.TrimSpace(env(key, "")) }

func decryptTOTP(encoded, key string) (string, error) {
	if key == "" {
		return "", errors.New("missing TOTP key")
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

func (a *app) consumeEmailCode(r *http.Request, userID uint64, email, code string) bool {
	if len(code) != 6 {
		return false
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	// Lock the account before its email codes, matching the order used by user
	// updates. A code must never verify an address reassigned to another user.
	var currentEmail string
	var emailAllowed bool
	if err := tx.QueryRowContext(r.Context(), `SELECT email,email_code_allowed FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, userID).Scan(&currentEmail, &emailAllowed); err != nil || !strings.EqualFold(currentEmail, email) || !emailAllowed {
		return false
	}
	var codeID uint64
	var hashed string
	var expires time.Time
	var consumed, locked sql.NullTime
	var attempts int
	err = tx.QueryRowContext(r.Context(), `SELECT id,hashed_code,expires_at,consumed_at,locked_until,failed_attempts FROM auth_login_codes WHERE email=? ORDER BY id DESC LIMIT 1 FOR UPDATE`, email).Scan(&codeID, &hashed, &expires, &consumed, &locked, &attempts)
	if err != nil || consumed.Valid || time.Now().UTC().After(expires) || (locked.Valid && time.Now().UTC().Before(locked.Time)) {
		return false
	}
	if !equal(hashed, digest(code)) {
		attempts++
		if attempts >= 5 {
			_, _ = tx.ExecContext(r.Context(), `UPDATE auth_login_codes SET failed_attempts=?,locked_until=expires_at WHERE id=?`, attempts, codeID)
		} else {
			_, _ = tx.ExecContext(r.Context(), `UPDATE auth_login_codes SET failed_attempts=? WHERE id=?`, attempts, codeID)
		}
		_ = tx.Commit()
		return false
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE auth_login_codes SET consumed_at=UTC_TIMESTAMP() WHERE id=?`, codeID); err != nil {
		return false
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO auth_email_verifications (user_id,email,verified_at) VALUES (?,?,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE email=VALUES(email),hashed_code=NULL,expires_at=NULL,failed_attempts=0,verified_at=UTC_TIMESTAMP(),requested_at=NULL`, userID, currentEmail); err != nil {
		return false
	}
	return tx.Commit() == nil
}

func (a *app) finishLogin(w http.ResponseWriter, r *http.Request, id uint64, method string) {
	token, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	loggedInAt := time.Now().UTC()
	expires := loggedInAt.Add(12 * time.Hour)
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth session storage failed")
		return
	}
	defer tx.Rollback()
	var deleted bool
	err = tx.QueryRowContext(r.Context(), `SELECT is_deleted FROM users WHERE id=? FOR UPDATE`, id).Scan(&deleted)
	if err == nil && deleted {
		err = sql.ErrNoRows
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE users SET is_registered=1 WHERE id=?`, id)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO auth_sessions (token_hash,user_id,expires_at,created_at) VALUES (?,?,?,?)`, digest(token), id, expires, loggedInAt)
	}
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO auth_login_history (user_id,method,logged_in_at) VALUES (?,?,?)`, id, method, loggedInAt)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth session storage failed")
		return
	}
	pruneContext, cancelPrune := context.WithTimeout(context.Background(), 2*time.Second)
	if err := a.pruneLoginHistory(pruneContext, id); err != nil {
		a.alerts.send("Taskly auth login history pruning failed")
	}
	cancelPrune()
	http.SetCookie(w, &http.Cookie{Name: "taskly_auth_session", Value: token, Path: "/", Expires: expires, MaxAge: 12 * 3600, Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	jsonResponse(w, 200, map[string]bool{"authenticated": true})
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	if cookie, err := r.Cookie("taskly_auth_session"); err == nil {
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE session_hash=?`, digest(cookie.Value)); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM auth_sessions WHERE token_hash=?`, digest(cookie.Value)); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "taskly_auth_session", Value: "", Path: "/", MaxAge: -1, Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(204)
}

func (a *app) requestCode(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validEmail(email) {
		oauthError(w, 400, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "email-code:"+email)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	var id uint64
	var allowed, deleted bool
	err = a.db.QueryRowContext(r.Context(), `SELECT id,email_code_allowed,is_deleted FROM users WHERE email=?`, email).Scan(&id, &allowed, &deleted)
	if err != nil || !allowed || deleted || a.blocked(r, id) {
		w.WriteHeader(202)
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO auth_login_codes (email,hashed_code,expires_at) VALUES (?,?,?)`, email, digest(code), time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth email code storage failed")
		return
	}
	if err := sendLoginCode(email, code); err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth email delivery failed")
		return
	}
	w.WriteHeader(202)
}

func sendLoginCode(email, code string) error {
	return sendMailCode(email, code, "authentication")
}

func sendMailCode(email, code, purpose string) error {
	host := env("SMTP_HOST", "")
	port := env("SMTP_PORT", "587")
	from := env("SMTP_FROM", "")
	if host == "" || from == "" {
		return errors.New("SMTP not configured")
	}
	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if user := env("SMTP_USER", ""); user != "" {
		auth = smtp.PlainAuth("", user, env("SMTP_PASSWORD", ""), host)
	}
	if purpose != "authentication" && purpose != "email verification" && purpose != "security verification" {
		return errors.New("invalid email purpose")
	}
	message := []byte("From: " + from + "\r\nTo: " + email + "\r\nSubject: Taskly " + purpose + " code\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nYour Taskly " + purpose + " code is " + code + ". It expires in 10 minutes.\r\n")
	mode := strings.ToLower(env("SMTP_TLS", "starttls"))
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if mode == "smtps" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
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
	} else if mode != "smtps" && mode != "none" {
		return errors.New("invalid SMTP_TLS")
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(email); err != nil {
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

func validEmail(email string) bool {
	if email == "" || len(email) > 255 || strings.ContainsAny(email, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email
}
