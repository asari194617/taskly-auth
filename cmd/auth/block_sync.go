package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type blockSyncRequest struct {
	Active    *bool   `json:"active"`
	Reason    *string `json:"reason"`
	ExpiresAt *string `json:"expires_at"`
}

func (a *app) syncBlock(w http.ResponseWriter, r *http.Request) {
	if !a.authorizeInternal(w, r) {
		return
	}
	userID, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil || userID == 0 {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	var input blockSyncRequest
	if !parseJSON(w, r, &input) {
		return
	}
	active, reason, expiresAt, err := validateBlockSync(input, time.Now().UTC())
	if err != nil {
		http.Error(w, "invalid block state", http.StatusBadRequest)
		return
	}
	if err := a.applyBlockState(r.Context(), userID, active, reason, expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, "block synchronization failed", http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]bool{"active": active})
}

func validateBlockSync(input blockSyncRequest, now time.Time) (bool, *string, *time.Time, error) {
	if input.Active == nil {
		return false, nil, nil, errors.New("active is required")
	}
	if !*input.Active {
		if input.Reason != nil || input.ExpiresAt != nil {
			return false, nil, nil, errors.New("inactive state must not contain block details")
		}
		return false, nil, nil, nil
	}
	var reason *string
	if input.Reason != nil {
		trimmed := strings.TrimSpace(*input.Reason)
		if len(trimmed) > 1000 {
			return false, nil, nil, errors.New("reason too long")
		}
		if trimmed != "" {
			reason = &trimmed
		}
	}
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *input.ExpiresAt)
		if err != nil || !parsed.After(now) {
			return false, nil, nil, errors.New("invalid expiration")
		}
		utc := parsed.UTC()
		expiresAt = &utc
	}
	return true, reason, expiresAt, nil
}

func (a *app) applyBlockState(ctx context.Context, userID uint64, active bool, reason *string, expiresAt *time.Time) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var found uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? FOR UPDATE`, userID).Scan(&found); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE blocks SET expires_at=?,updated_at=? WHERE user_id=? AND (expires_at IS NULL OR expires_at>?)`, now, now, userID, now); err != nil {
		return err
	}
	if active {
		if _, err := tx.ExecContext(ctx, `INSERT INTO blocks (user_id,reason,expires_at,created_at,updated_at) VALUES (?,?,?,?,?)`, userID, reason, expiresAt, now, now); err != nil {
			return err
		}
		statements := []string{
			`DELETE FROM auth_security_codes WHERE user_id=?`,
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
	}
	return tx.Commit()
}
