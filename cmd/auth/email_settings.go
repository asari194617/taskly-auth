package main

import (
	"database/sql"
	"net/http"
	"strconv"
)

func canDisableEmailCode(hash sql.NullString, passkeyCount int) bool {
	return (hash.Valid && hash.String != "") || passkeyCount > 0
}

func (a *app) emailCodeSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	var enabled bool
	if err := a.db.QueryRowContext(r.Context(), `SELECT email_code_allowed FROM users WHERE id=? AND is_deleted=0`, id).Scan(&enabled); err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	jsonResponse(w, 200, map[string]bool{"enabled": enabled})
}

func (a *app) updateEmailCodeSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.recentSessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	if input.Enabled == nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "email-settings:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	defer tx.Rollback()
	var hash sql.NullString
	var email string
	var current bool
	if err := tx.QueryRowContext(r.Context(), `SELECT hashed_password,email,email_code_allowed FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&hash, &email, &current); err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	if current == *input.Enabled {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !*input.Enabled && !canDisableEmailCode(hash, 0) {
		rows, err := tx.QueryContext(r.Context(), `SELECT id FROM passkeys WHERE user_id=? FOR UPDATE`, id)
		if err != nil {
			a.emailSettingsStorageError(w)
			return
		}
		count := 0
		for rows.Next() {
			var passkeyID uint64
			if err := rows.Scan(&passkeyID); err != nil {
				rows.Close()
				a.emailSettingsStorageError(w)
				return
			}
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			a.emailSettingsStorageError(w)
			return
		}
		if !canDisableEmailCode(hash, count) {
			oauthError(w, 409, "last_login_method")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE users SET email_code_allowed=? WHERE id=?`, *input.Enabled, id); err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	if !*input.Enabled {
		if _, err := tx.ExecContext(r.Context(), `UPDATE auth_login_codes SET consumed_at=UTC_TIMESTAMP() WHERE email=? AND consumed_at IS NULL`, email); err != nil {
			a.emailSettingsStorageError(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.emailSettingsStorageError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) emailSettingsStorageError(w http.ResponseWriter) {
	a.alerts.send("Taskly auth email code setting storage failed")
	oauthError(w, 503, "temporarily_unavailable")
}
