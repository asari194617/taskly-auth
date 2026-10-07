package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var errUserNotDeleted = errors.New("user is not deleted")

type lifecycleSyncRequest struct {
	Deleted   *bool  `json:"deleted"`
	DeletedAt string `json:"deleted_at"`
}

func (a *app) authorizeInternal(w http.ResponseWriter, r *http.Request) bool {
	if a.internalToken == "" {
		http.Error(w, "internal API unavailable", http.StatusServiceUnavailable)
		return false
	}
	authorization := r.Header.Get("Authorization")
	provided := strings.TrimPrefix(authorization, "Bearer ")
	if provided == authorization || subtle.ConstantTimeCompare([]byte(provided), []byte(a.internalToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (a *app) syncUserLifecycle(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeInternal(w, r) {
		return
	}
	userID, err := internalUserID(r)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	var input lifecycleSyncRequest
	if !parseJSON(w, r, &input) {
		return
	}
	deletedAt, err := validateLifecycleSync(input, time.Now().UTC())
	if err != nil {
		http.Error(w, "invalid lifecycle state", http.StatusBadRequest)
		return
	}
	appliedAt, err := a.applyDeletedState(r.Context(), userID, deletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, "lifecycle synchronization failed", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"deleted": true, "deleted_at": appliedAt.Format(time.RFC3339)})
}

func (a *app) eraseUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeInternal(w, r) {
		return
	}
	userID, err := internalUserID(r)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	if err := a.eraseDeletedUser(r.Context(), userID); err != nil {
		if errors.Is(err, errUserNotDeleted) {
			http.Error(w, "user is not deleted", http.StatusConflict)
			return
		}
		http.Error(w, "account erasure failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func internalUserID(r *http.Request) (uint64, error) {
	userID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || userID == 0 {
		return 0, errors.New("invalid user")
	}
	return userID, nil
}

func validateLifecycleSync(input lifecycleSyncRequest, now time.Time) (time.Time, error) {
	if input.Deleted == nil || !*input.Deleted {
		return time.Time{}, errors.New("deleted must be true")
	}
	deletedAt, err := time.Parse(time.RFC3339, input.DeletedAt)
	if err != nil || deletedAt.After(now) {
		return time.Time{}, errors.New("invalid deleted_at")
	}
	return deletedAt.UTC().Truncate(time.Second), nil
}

func (a *app) applyDeletedState(ctx context.Context, userID uint64, requestedAt time.Time) (time.Time, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	var email string
	var existingAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT email,deleted_at FROM users WHERE id=? FOR UPDATE`, userID).Scan(&email, &existingAt); err != nil {
		return time.Time{}, err
	}
	deletedAt := requestedAt
	if existingAt.Valid && existingAt.Time.Before(deletedAt) {
		deletedAt = existingAt.Time.UTC()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET is_deleted=1,deleted_at=? WHERE id=?`, deletedAt, userID); err != nil {
		return time.Time{}, err
	}
	statements := []string{
		`DELETE FROM sessions WHERE user_id=?`,
		`DELETE FROM auth_security_codes WHERE user_id=?`,
		`DELETE FROM auth_sessions WHERE user_id=?`,
		`DELETE FROM auth_challenges WHERE user_id=?`,
		`DELETE FROM auth_webauthn_sessions WHERE user_id=?`,
		`DELETE FROM auth_totp_setups WHERE user_id=?`,
		`DELETE FROM auth_email_verifications WHERE user_id=?`,
		`DELETE FROM oauth_codes WHERE user_id=?`,
		`UPDATE oauth_access_tokens SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP()) WHERE user_id=?`,
		`UPDATE oauth_refresh_tokens SET revoked_at=COALESCE(revoked_at,UTC_TIMESTAMP()) WHERE user_id=?`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, userID); err != nil {
			return time.Time{}, err
		}
	}
	for _, table := range []string{"login_codes", "auth_login_codes"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET consumed_at=COALESCE(consumed_at,UTC_TIMESTAMP()) WHERE email=?`, email); err != nil {
			return time.Time{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return deletedAt, nil
}

func (a *app) eraseDeletedUser(ctx context.Context, userID uint64) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var email string
	var deleted bool
	err = tx.QueryRowContext(ctx, `SELECT email,is_deleted FROM users WHERE id=? FOR UPDATE`, userID).Scan(&email, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !deleted {
		return errUserNotDeleted
	}
	for _, table := range []string{
		"auth_totp_setups", "auth_email_verifications", "auth_security_codes", "auth_security_totp_uses", "auth_webauthn_sessions", "auth_challenges", "auth_sessions", "auth_login_history",
		"oauth_codes", "oauth_access_tokens", "oauth_refresh_tokens", "sessions", "blocks",
		"recovery_codes", "totp_secrets", "passkeys",
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE user_id=?`, userID); err != nil {
			return err
		}
	}
	for _, table := range []string{"login_codes", "auth_login_codes"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE email=?`, email); err != nil {
			return err
		}
	}
	rateLimitKeys := []string{
		"login:" + email,
		"email-code:" + email,
		"passkey:" + email,
		"passkey-register:" + strconv.FormatUint(userID, 10),
		"recovery-generate:" + strconv.FormatUint(userID, 10),
		"email-settings:" + strconv.FormatUint(userID, 10),
		"totp-setup:" + strconv.FormatUint(userID, 10),
		"totp-activate:" + strconv.FormatUint(userID, 10),
		"totp-disable:" + strconv.FormatUint(userID, 10),
		"password-change:" + strconv.FormatUint(userID, 10),
		"security-code:" + strconv.FormatUint(userID, 10),
		"security-verify:" + strconv.FormatUint(userID, 10),
	}
	for _, key := range rateLimitKeys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_rate_limits WHERE key_hash=?`, digest(key)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
