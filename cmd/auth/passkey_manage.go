package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/webauthn"
)

type passkeyItem struct {
	ID        uint64 `json:"id"`
	Name      string `json:"device_name"`
	CreatedAt string `json:"created_at"`
}

func (a *app) passkeyAccount(w http.ResponseWriter, r *http.Request, recent bool) (uint64, bool) {
	if !a.requireUI(w, r) {
		return 0, false
	}
	if a.webAuthn == nil {
		oauthError(w, 503, "temporarily_unavailable")
		return 0, false
	}
	var id uint64
	var ok bool
	if recent {
		id, ok = a.recentSessionUser(r)
	} else {
		id, ok = a.sessionUser(r)
	}
	if !ok {
		oauthError(w, 401, "login_required")
		return 0, false
	}
	return id, true
}

func (a *app) passkeyList(w http.ResponseWriter, r *http.Request) {
	id, ok := a.passkeyAccount(w, r, false)
	if !ok {
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,COALESCE(device_name,'Passkey'),DATE_FORMAT(created_at,'%Y-%m-%d %H:%i:%s') FROM passkeys WHERE user_id=? ORDER BY id`, id)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer rows.Close()
	items := make([]passkeyItem, 0)
	for rows.Next() {
		var item passkeyItem
		if err := rows.Scan(&item.ID, &item.Name, &item.CreatedAt); err != nil {
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	jsonResponse(w, 200, map[string]any{"passkeys": items})
}

func (a *app) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	id, ok := a.passkeyAccount(w, r, true)
	if !ok {
		return
	}
	limited, err := a.rateLimited(r.Context(), "passkey-register:"+strconv.FormatUint(id, 10))
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	user, err := a.loadPasskeyUser(r.Context(), id, nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	creation, data, err := a.webAuthn.BeginRegistration(user, webauthn.WithExclusions(webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	requestID, err := a.storeWebauthnSession(r.Context(), "register", id, data)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth passkey registration challenge storage failed")
		return
	}
	jsonResponse(w, 200, map[string]any{"request_id": requestID, "options": creation})
}

func (a *app) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	id, ok := a.passkeyAccount(w, r, true)
	if !ok {
		return
	}
	var input passkeyResponse
	if !parsePasskeyResponse(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.DeviceName)
	if name == "" {
		name = "Passkey"
	}
	if utf8.RuneCountInString(name) > 64 || len(name) > 255 {
		oauthError(w, 400, "invalid_request")
		return
	}
	requestUserID, data, err := a.consumeWebauthnSession(r.Context(), "register", input.RequestID)
	if err != nil || requestUserID != id {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/", bytes.NewReader(input.Credential))
	if err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	user, err := loadPasskeyUser(r.Context(), tx, id, nil, true)
	if err != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	credential, err := a.webAuthn.FinishRegistration(user, data, request)
	if err != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	stored, err := encodeStoredWebauthnCredential(credential)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO passkeys (user_id,credential_id,public_key,sign_count,device_name) VALUES (?,?,?,?,?)`, id, base64.RawStdEncoding.EncodeToString(credential.ID), stored, credential.Authenticator.SignCount, name)
	if err != nil || tx.Commit() != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth passkey registration storage failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) passkeyRename(w http.ResponseWriter, r *http.Request) {
	id, ok := a.passkeyAccount(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ID   uint64 `json:"id"`
		Name string `json:"name"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	if input.ID == 0 || name == "" || utf8.RuneCountInString(name) > 64 || len(name) > 255 {
		oauthError(w, 400, "invalid_request")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE passkeys SET device_name=? WHERE id=? AND user_id=?`, name, input.ID, id)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	changed, err := result.RowsAffected()
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if changed == 0 {
		var exists int
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM passkeys WHERE id=? AND user_id=?`, input.ID, id).Scan(&exists); err != nil {
			oauthError(w, 503, "temporarily_unavailable")
			return
		} else if exists == 0 {
			oauthError(w, 404, "not_found")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) passkeyDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := a.passkeyAccount(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ID uint64 `json:"id"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	if input.ID == 0 {
		oauthError(w, 400, "invalid_request")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	var hash sql.NullString
	var emailAllowed bool
	if err := tx.QueryRowContext(r.Context(), `SELECT hashed_password,email_code_allowed FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&hash, &emailAllowed); err != nil {
		oauthError(w, 401, "login_required")
		return
	}
	rows, err := tx.QueryContext(r.Context(), `SELECT id FROM passkeys WHERE user_id=? FOR UPDATE`, id)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	count, present := 0, false
	for rows.Next() {
		var keyID uint64
		if err := rows.Scan(&keyID); err != nil {
			rows.Close()
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		count++
		present = present || keyID == input.ID
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	rows.Close()
	if !present {
		oauthError(w, 404, "not_found")
		return
	}
	if count == 1 && (!hash.Valid || hash.String == "") && !emailAllowed {
		oauthError(w, 409, "last_login_method")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM passkeys WHERE id=? AND user_id=?`, input.ID, id); err != nil || tx.Commit() != nil {
		oauthError(w, 503, "temporarily_unavailable")
		a.alerts.send("Taskly auth passkey deletion failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
