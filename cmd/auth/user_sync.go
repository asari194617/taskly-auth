package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

var errEmailConflict = errors.New("email conflict")

type provisionUserRequest struct {
	Email            string `json:"email"`
	DisplayName      string `json:"display_name"`
	NotificationLang string `json:"notification_lang"`
}

type updateUserRequest struct {
	Email            *string `json:"email"`
	DisplayName      *string `json:"display_name"`
	NotificationLang *string `json:"notification_lang"`
	EmailCodeAllowed *bool   `json:"email_code_allowed"`
}

func (a *app) provisionUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeInternal(w, r) {
		return
	}
	var input provisionUserRequest
	if !parseJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.NotificationLang = strings.ToLower(strings.TrimSpace(input.NotificationLang))
	if input.NotificationLang == "" {
		input.NotificationLang = "ja"
	}
	if !validEmail(input.Email) || len(input.Email) > 255 || len(input.DisplayName) > 255 || (input.NotificationLang != "ja" && input.NotificationLang != "en") {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	id, created, err := a.createOrFindUser(r.Context(), input)
	if err != nil {
		http.Error(w, "user provisioning failed", http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	jsonResponse(w, status, map[string]any{"id": id, "created": created})
}

func (a *app) updateUserProjection(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeInternal(w, r) {
		return
	}
	userID, err := internalUserID(r)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	var input updateUserRequest
	if !parseJSON(w, r, &input) {
		return
	}
	if input.Email == nil && input.DisplayName == nil && input.NotificationLang == nil && input.EmailCodeAllowed == nil {
		http.Error(w, "invalid user update", http.StatusBadRequest)
		return
	}
	if input.Email != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Email))
		if !validEmail(value) || len(value) > 255 {
			http.Error(w, "invalid user update", http.StatusBadRequest)
			return
		}
		input.Email = &value
	}
	if input.DisplayName != nil {
		value := strings.TrimSpace(*input.DisplayName)
		if len(value) > 255 {
			http.Error(w, "invalid user update", http.StatusBadRequest)
			return
		}
		input.DisplayName = &value
	}
	if input.NotificationLang != nil {
		value := strings.ToLower(strings.TrimSpace(*input.NotificationLang))
		if value != "ja" && value != "en" {
			http.Error(w, "invalid user update", http.StatusBadRequest)
			return
		}
		input.NotificationLang = &value
	}
	if err := a.applyUserUpdate(r.Context(), userID, input); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "user not found", http.StatusNotFound)
		case errors.Is(err, errEmailConflict):
			http.Error(w, "email conflict", http.StatusConflict)
		default:
			http.Error(w, "user update failed", http.StatusInternalServerError)
		}
		return
	}
	jsonResponse(w, http.StatusOK, map[string]uint64{"id": userID})
}

func (a *app) createOrFindUser(ctx context.Context, input provisionUserRequest) (uint64, bool, error) {
	result, err := a.db.ExecContext(ctx, `INSERT INTO users (email,display_name,notification_lang) VALUES (?,?,?) ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`, input.Email, input.DisplayName, input.NotificationLang)
	if err != nil {
		return 0, false, err
	}
	id, err := result.LastInsertId()
	if err != nil || id <= 0 {
		return 0, false, errors.New("user id unavailable")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	return uint64(id), rows == 1, nil
}

func (a *app) applyUserUpdate(ctx context.Context, userID uint64, input updateUserRequest) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldEmail, email, displayName, lang string
	var emailAllowed bool
	if err := tx.QueryRowContext(ctx, `SELECT email,email,COALESCE(display_name,''),notification_lang,email_code_allowed FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, userID).Scan(&oldEmail, &email, &displayName, &lang, &emailAllowed); err != nil {
		return err
	}
	if input.Email != nil {
		email = *input.Email
	}
	if input.DisplayName != nil {
		displayName = *input.DisplayName
	}
	if input.NotificationLang != nil {
		lang = *input.NotificationLang
	}
	if input.EmailCodeAllowed != nil {
		emailAllowed = *input.EmailCodeAllowed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET email=?,display_name=?,notification_lang=?,email_code_allowed=? WHERE id=?`, email, displayName, lang, emailAllowed, userID); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return errEmailConflict
		}
		return err
	}
	if !strings.EqualFold(oldEmail, email) {
		statements := []string{
			`DELETE FROM auth_email_verifications WHERE user_id=?`,
			`DELETE FROM sessions WHERE user_id=?`,
			`DELETE FROM auth_sessions WHERE user_id=?`,
			`DELETE FROM auth_challenges WHERE user_id=?`,
			`DELETE FROM auth_webauthn_sessions WHERE user_id=?`,
			`DELETE FROM oauth_codes WHERE user_id=?`,
			`UPDATE oauth_access_tokens SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP()) WHERE user_id=?`,
			`UPDATE oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP()) WHERE user_id=?`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
				return err
			}
		}
		for _, table := range []string{"login_codes", "auth_login_codes"} {
			if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET consumed_at=COALESCE(consumed_at,UTC_TIMESTAMP()) WHERE email IN (?,?)`, oldEmail, email); err != nil {
				return err
			}
		}
	} else if input.EmailCodeAllowed != nil && !*input.EmailCodeAllowed {
		for _, table := range []string{"login_codes", "auth_login_codes"} {
			if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET consumed_at=COALESCE(consumed_at,UTC_TIMESTAMP()) WHERE email=?`, email); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
