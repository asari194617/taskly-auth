package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
)

func encryptTOTP(secret, key string) (string, error) {
	if key == "" {
		return "", errors.New("missing TOTP key")
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
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(secret), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func validTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func (a *app) totpStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var count int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM totp_secrets WHERE user_id=? AND is_active=1`, id).Scan(&count); err != nil {
		a.totpStorageError(w)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"enabled": count > 0})
}

func (a *app) totpSetup(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.securitySettingsUser(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !parseJSON(w, r, &input) {
		return
	}
	limited, err := a.rateLimited(r.Context(), "totp-setup:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var email string
	if err := a.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=? AND is_deleted=0`, id).Scan(&email); err != nil {
		a.totpStorageError(w)
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Taskly", AccountName: email, SecretSize: 20})
	if err != nil {
		a.totpStorageError(w)
		return
	}
	encrypted, err := encryptTOTP(key.Secret(), osEnv("TOTP_ENC_KEY"))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO auth_totp_setups (user_id,encrypted_secret,expires_at) VALUES (?,?,?) ON DUPLICATE KEY UPDATE encrypted_secret=VALUES(encrypted_secret),expires_at=VALUES(expires_at),created_at=UTC_TIMESTAMP()`, id, encrypted, time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"secret": key.Secret(), "otpauth": key.URL()})
}

func (a *app) totpActivate(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.securitySettingsUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	input.Code = strings.TrimSpace(input.Code)
	if !validTOTPCode(input.Code) {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "totp-activate:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		a.totpStorageError(w)
		return
	}
	defer tx.Rollback()
	var activeUser uint64
	if err := tx.QueryRowContext(r.Context(), `SELECT id FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&activeUser); err != nil {
		a.totpStorageError(w)
		return
	}
	var encrypted string
	var expires time.Time
	if err := tx.QueryRowContext(r.Context(), `SELECT encrypted_secret,expires_at FROM auth_totp_setups WHERE user_id=? FOR UPDATE`, id).Scan(&encrypted, &expires); err != nil || !time.Now().UTC().Before(expires) {
		oauthError(w, http.StatusBadRequest, "setup_expired")
		return
	}
	secret, err := decryptTOTP(encrypted, osEnv("TOTP_ENC_KEY"))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	if !totp.Validate(input.Code, secret) {
		oauthError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO totp_secrets (user_id,secret,is_active) VALUES (?,?,1) ON DUPLICATE KEY UPDATE secret=VALUES(secret),is_active=1`, id, encrypted); err != nil {
		a.totpStorageError(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_totp_setups WHERE user_id=?`, id); err != nil || revokeOAuthCredentials(r.Context(), tx, id) != nil {
		a.totpStorageError(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE auth_sessions SET security_verified_at=NULL WHERE user_id=?`, id); err != nil {
		a.totpStorageError(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE user_id=?`, id); err != nil {
		a.totpStorageError(w)
		return
	}
	if err := tx.Commit(); err != nil {
		a.totpStorageError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) totpDisable(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.securitySettingsUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	input.Code = strings.TrimSpace(input.Code)
	if !validTOTPCode(input.Code) {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "totp-disable:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		a.totpStorageError(w)
		return
	}
	defer tx.Rollback()
	var hasPassword, emailAllowed bool
	if err := tx.QueryRowContext(r.Context(), `SELECT hashed_password IS NOT NULL AND hashed_password<>'',email_code_allowed FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&hasPassword, &emailAllowed); err != nil {
		a.totpStorageError(w)
		return
	}
	var encrypted string
	if err := tx.QueryRowContext(r.Context(), `SELECT secret FROM totp_secrets WHERE user_id=? AND is_active=1 FOR UPDATE`, id).Scan(&encrypted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			oauthError(w, http.StatusConflict, "not_enabled")
		} else {
			a.totpStorageError(w)
		}
		return
	}
	secret, err := decryptTOTP(encrypted, osEnv("TOTP_ENC_KEY"))
	if err != nil {
		a.totpStorageError(w)
		return
	}
	if !totp.Validate(input.Code, secret) {
		oauthError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	var passkeys, recovery int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM passkeys WHERE user_id=?`, id).Scan(&passkeys); err != nil {
		a.totpStorageError(w)
		return
	}
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM recovery_codes WHERE user_id=? AND is_used=0`, id).Scan(&recovery); err != nil {
		a.totpStorageError(w)
		return
	}
	if !hasPassword && !emailAllowed && passkeys == 0 && recovery == 0 {
		oauthError(w, http.StatusConflict, "last_login_method")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE totp_secrets SET is_active=0 WHERE user_id=?`, id); err != nil || revokeOAuthCredentials(r.Context(), tx, id) != nil {
		a.totpStorageError(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE auth_sessions SET security_verified_at=NULL WHERE user_id=?`, id); err != nil {
		a.totpStorageError(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE user_id=?`, id); err != nil {
		a.totpStorageError(w)
		return
	}
	if err := tx.Commit(); err != nil {
		a.totpStorageError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func revokeOAuthCredentials(ctx context.Context, tx *sql.Tx, id uint64) error {
	queries := []string{
		`UPDATE oauth_codes SET used_at=UTC_TIMESTAMP() WHERE user_id=? AND used_at IS NULL`,
		`UPDATE oauth_access_tokens SET revoked_at=UTC_TIMESTAMP() WHERE user_id=? AND revoked_at IS NULL`,
		`UPDATE oauth_refresh_tokens SET revoked_at=UTC_TIMESTAMP() WHERE user_id=? AND revoked_at IS NULL`,
		`DELETE FROM auth_challenges WHERE user_id=?`,
	}
	for _, query := range queries {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) totpStorageError(w http.ResponseWriter) {
	a.alerts.send("Taskly auth TOTP management failed")
	oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
}
