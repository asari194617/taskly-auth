package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type passwordInput struct {
	Current string `json:"current"`
	Next    string `json:"next"`
}

func validNewPassword(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) >= 8 &&
		len([]byte(value)) <= 72 && strings.TrimSpace(value) != ""
}

func (a *app) passwordStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	var hash sql.NullString
	if err := a.db.QueryRowContext(r.Context(), `SELECT hashed_password FROM users WHERE id=? AND is_deleted=0`, id).Scan(&hash); err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	jsonResponse(w, 200, map[string]bool{"has_password": hash.Valid && hash.String != ""})
}

func (a *app) changePassword(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.recentSessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	var input passwordInput
	if !parseJSON(w, r, &input) {
		return
	}
	if !validNewPassword(input.Next) {
		oauthError(w, 400, "invalid_password")
		return
	}
	limited, err := a.rateLimited(r.Context(), "password-change:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.passwordStorageError(w)
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		a.passwordStorageError(w)
		return
	}
	defer tx.Rollback()
	var old sql.NullString
	if err := tx.QueryRowContext(r.Context(), `SELECT hashed_password FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&old); err != nil {
		a.passwordStorageError(w)
		return
	}
	if old.Valid && old.String != "" && bcrypt.CompareHashAndPassword([]byte(old.String), []byte(input.Current)) != nil {
		oauthError(w, 401, "invalid_credentials")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Next), bcrypt.DefaultCost)
	if err != nil {
		oauthError(w, 400, "invalid_password")
		return
	}
	queries := []string{
		`UPDATE users SET hashed_password=? WHERE id=?`,
		`DELETE FROM auth_sessions WHERE user_id=?`,
		`UPDATE oauth_codes SET used_at=UTC_TIMESTAMP() WHERE user_id=? AND used_at IS NULL`,
		`UPDATE oauth_access_tokens SET revoked_at=UTC_TIMESTAMP() WHERE user_id=? AND revoked_at IS NULL`,
		`UPDATE oauth_refresh_tokens SET revoked_at=UTC_TIMESTAMP() WHERE user_id=? AND revoked_at IS NULL`,
		`DELETE FROM auth_challenges WHERE user_id=?`,
		`DELETE FROM auth_webauthn_sessions WHERE user_id=?`,
	}
	for index, query := range queries {
		var err error
		if index == 0 {
			_, err = tx.ExecContext(r.Context(), query, string(hash), id)
		} else {
			_, err = tx.ExecContext(r.Context(), query, id)
		}
		if err != nil {
			a.passwordStorageError(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.passwordStorageError(w)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "taskly_auth_session", Value: "", Path: "/", MaxAge: -1, Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) passwordStorageError(w http.ResponseWriter) {
	a.alerts.send("Taskly auth password update storage failed")
	oauthError(w, 503, "temporarily_unavailable")
}
