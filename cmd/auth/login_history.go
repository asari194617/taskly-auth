package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

const loginHistoryLimit = 50

type loginHistoryEntry struct {
	ID         uint64    `json:"-"`
	Method     string    `json:"method"`
	LoggedInAt time.Time `json:"logged_in_at"`
}

// Keep the newest 50 entries per user. Pruning runs after session issuance so
// a transient maintenance failure cannot reject an otherwise valid login.
func (a *app) pruneLoginHistory(ctx context.Context, userID uint64) error {
	var cutoffTime time.Time
	var cutoffID uint64
	err := a.db.QueryRowContext(ctx, `SELECT logged_in_at,id FROM auth_login_history WHERE user_id=? ORDER BY logged_in_at DESC,id DESC LIMIT 1 OFFSET ?`, userID, loginHistoryLimit).Scan(&cutoffTime, &cutoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, `DELETE FROM auth_login_history WHERE user_id=? AND (logged_in_at<? OR (logged_in_at=? AND id<=?))`, userID, cutoffTime, cutoffTime, cutoffID)
	return err
}

func (a *app) loginHistory(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	userID, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,method,logged_in_at FROM auth_login_history WHERE user_id=? ORDER BY logged_in_at DESC,id DESC LIMIT ?`, userID, loginHistoryLimit)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	defer rows.Close()
	history := make([]loginHistoryEntry, 0)
	for rows.Next() {
		var entry loginHistoryEntry
		if err := rows.Scan(&entry.ID, &entry.Method, &entry.LoggedInAt); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
			return
		}
		history = append(history, entry)
	}
	if err := rows.Err(); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "history_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"history": history})
}
