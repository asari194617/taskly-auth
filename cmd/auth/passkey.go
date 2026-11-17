package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type passkeyResponse struct {
	RequestID  string          `json:"request_id"`
	Credential json.RawMessage `json:"credential"`
	DeviceName string          `json:"device_name,omitempty"`
}

func parsePasskeyResponse(w http.ResponseWriter, r *http.Request, dst *passkeyResponse) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		oauthError(w, 400, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		oauthError(w, 400, "invalid_request")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || !verifierPattern.MatchString(dst.RequestID) || len(dst.Credential) == 0 {
		oauthError(w, 400, "invalid_request")
		return false
	}
	return true
}

func (a *app) storeWebauthnSession(ctx context.Context, purpose string, userID uint64, data *webauthn.SessionData) (string, error) {
	var authEpoch uint64
	if err := a.db.QueryRowContext(ctx, `SELECT auth_epoch FROM users WHERE id=? AND is_deleted=0`, userID).Scan(&authEpoch); err != nil {
		return "", err
	}
	requestID, err := randomToken()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	if _, err := a.db.ExecContext(ctx, `DELETE FROM auth_webauthn_sessions WHERE expires_at<UTC_TIMESTAMP()`); err != nil {
		return "", err
	}
	_, err = a.db.ExecContext(ctx, `INSERT INTO auth_webauthn_sessions (token_hash,user_id,auth_epoch,purpose,session_data,expires_at) VALUES (?,?,?,?,?,?)`, digest(requestID), userID, authEpoch, purpose, string(raw), time.Now().UTC().Add(5*time.Minute))
	return requestID, err
}

func (a *app) consumeWebauthnSession(ctx context.Context, purpose, requestID string) (uint64, uint64, webauthn.SessionData, error) {
	var data webauthn.SessionData
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, data, err
	}
	defer tx.Rollback()
	var userID, authEpoch uint64
	var raw string
	var expires time.Time
	err = tx.QueryRowContext(ctx, `SELECT user_id,auth_epoch,session_data,expires_at FROM auth_webauthn_sessions WHERE token_hash=? AND purpose=? FOR UPDATE`, digest(requestID), purpose).Scan(&userID, &authEpoch, &raw, &expires)
	if err != nil || !time.Now().UTC().Before(expires) {
		return 0, 0, data, errors.New("passkey request missing or expired")
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM auth_webauthn_sessions WHERE token_hash=? AND purpose=?`, digest(requestID), purpose)
	if err != nil {
		return 0, 0, data, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return 0, 0, data, errors.New("passkey request already consumed")
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return 0, 0, data, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, data, err
	}
	return userID, authEpoch, data, nil
}

func (a *app) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	if a.webAuthn == nil {
		oauthError(w, 503, "temporarily_unavailable")
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
	var id uint64
	err := a.db.QueryRowContext(r.Context(), `SELECT id FROM users WHERE email=? AND is_deleted=0`, email).Scan(&id)
	limited, limitErr := a.authAttemptLimited(r, id)
	if limitErr != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	if err != nil || a.blocked(r, id) {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	user, err := a.loadPasskeyUser(r.Context(), id, nil)
	if err != nil || len(user.credentials) == 0 {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	assertion, data, err := a.webAuthn.BeginLogin(user, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	requestID, err := a.storeWebauthnSession(r.Context(), "login", id, data)
	if err != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth passkey challenge storage failed")
		return
	}
	jsonResponse(w, 200, map[string]any{"request_id": requestID, "options": assertion})
}

func (a *app) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	if a.webAuthn == nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	var input passkeyResponse
	if !parsePasskeyResponse(w, r, &input) {
		return
	}
	id, authEpoch, data, err := a.consumeWebauthnSession(r.Context(), "login", input.RequestID)
	if err != nil || data.UserVerification != protocol.VerificationRequired || a.blocked(r, id) {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/", bytes.NewReader(input.Credential))
	if err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	parsed, err := protocol.ParseCredentialRequestResponse(request)
	if err != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	if !parsed.Response.AuthenticatorData.Flags.HasUserVerified() {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	backupEligible := parsed.Response.AuthenticatorData.Flags.HasBackupEligible()
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	user, err := loadPasskeyUser(r.Context(), tx, id, &backupEligible, true)
	if err != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	credential, err := a.webAuthn.ValidateLogin(user, data, parsed)
	if err != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	stored, err := encodeStoredWebauthnCredential(credential)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	credentialID := base64.RawStdEncoding.EncodeToString(credential.ID)
	result, err := tx.ExecContext(r.Context(), `UPDATE passkeys SET public_key=?,sign_count=? WHERE user_id=? AND credential_id=?`, stored, credential.Authenticator.SignCount, id, credentialID)
	if err != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth passkey counter storage failed")
		return
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		var present int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM passkeys WHERE user_id=? AND credential_id=?`, id, credentialID).Scan(&present); err != nil || present != 1 {
			oauthError(w, 401, "invalid_credentials")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth passkey counter commit failed")
		return
	}
	a.finishLoginWithProof(w, r, id, "passkey", loginProof{authEpoch: authEpoch, email: user.email, passkeyID: credentialID, passkeyData: stored})
}
