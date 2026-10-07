package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var profileSyncClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func validDisplayName(name string) bool {
	return name != "" && utf8.ValidString(name) && len([]byte(name)) <= 255
}

func (a *app) emailVerificationDigest(userID uint64, email, code string) string {
	return digest(a.internalToken + ":" + strconv.FormatUint(userID, 10) + ":" + email + ":" + code)
}

func (a *app) profile(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var email, displayName string
	if err := a.db.QueryRowContext(r.Context(), "SELECT email,COALESCE(display_name,'') FROM users WHERE id=? AND is_deleted=0", id).Scan(&email, &displayName); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "profile_unavailable")
		return
	}
	var verified bool
	err := a.db.QueryRowContext(r.Context(), `SELECT verified_at IS NOT NULL FROM auth_email_verifications WHERE user_id=? AND email=?`, id, email).Scan(&verified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		oauthError(w, http.StatusServiceUnavailable, "profile_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"email": email, "display_name": displayName, "email_verified": verified})
}

func (a *app) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.recentSessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	limited, err := a.rateLimited(r.Context(), "email-verify:"+strconv.FormatUint(id, 10))
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	var email string
	if err := a.db.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=? AND is_deleted=0`, id).Scan(&email); err != nil {
		oauthError(w, 503, "profile_unavailable")
		return
	}
	var verified bool
	err = a.db.QueryRowContext(r.Context(), `SELECT verified_at IS NOT NULL FROM auth_email_verifications WHERE user_id=? AND email=?`, id, email).Scan(&verified)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if verified {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO auth_email_verifications (user_id,email,hashed_code,expires_at,failed_attempts,requested_at) VALUES (?,?,?,?,0,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE email=VALUES(email),hashed_code=VALUES(hashed_code),expires_at=VALUES(expires_at),failed_attempts=0,verified_at=NULL,requested_at=UTC_TIMESTAMP()`, id, email, a.emailVerificationDigest(id, email, code), time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if err := sendMailCode(email, code, "email verification"); err != nil {
		a.alerts.send("Taskly auth verification email delivery failed")
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *app) confirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.recentSessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var input struct {
		Code string `json:"code"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	if len(input.Code) != 6 {
		oauthError(w, 400, "invalid_code")
		return
	}
	for _, c := range input.Code {
		if c < '0' || c > '9' {
			oauthError(w, 400, "invalid_code")
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	var email, expected, current string
	var expires time.Time
	var attempts int
	if err := tx.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&current); err != nil {
		oauthError(w, 503, "profile_unavailable")
		return
	}
	err = tx.QueryRowContext(r.Context(), `SELECT email,hashed_code,expires_at,failed_attempts FROM auth_email_verifications WHERE user_id=? AND verified_at IS NULL FOR UPDATE`, id).Scan(&email, &expected, &expires, &attempts)
	if err != nil {
		oauthError(w, 400, "invalid_code")
		return
	}
	if email != current || !expires.After(time.Now().UTC()) || attempts >= 5 {
		oauthError(w, 400, "invalid_code")
		return
	}
	if !equal(expected, a.emailVerificationDigest(id, email, input.Code)) {
		if _, err := tx.ExecContext(r.Context(), `UPDATE auth_email_verifications SET failed_attempts=failed_attempts+1 WHERE user_id=?`, id); err != nil {
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		if err := tx.Commit(); err != nil {
			oauthError(w, 503, "temporarily_unavailable")
			return
		}
		oauthError(w, 400, "invalid_code")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE auth_email_verifications SET verified_at=UTC_TIMESTAMP(),hashed_code=NULL,expires_at=NULL WHERE user_id=?`, id); err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		oauthError(w, 503, "temporarily_unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.recentSessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var input struct {
		DisplayName string `json:"display_name"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.DisplayName)
	if !validDisplayName(name) {
		oauthError(w, http.StatusBadRequest, "invalid_display_name")
		return
	}
	limited, err := a.rateLimited(r.Context(), "profile-update:"+strconv.FormatUint(id, 10))
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "profile_unavailable")
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	result, err := a.db.ExecContext(r.Context(), "UPDATE users SET display_name=? WHERE id=? AND is_deleted=0", name, id)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "profile_unavailable")
		return
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	if err := syncTasklyProfile(r.Context(), id, name); err != nil {
		a.alerts.send("Taskly auth profile synchronization pending")
		oauthError(w, http.StatusServiceUnavailable, "profile_sync_pending")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func syncTasklyProfile(ctx context.Context, id uint64, name string) error {
	raw := strings.TrimSpace(os.Getenv("TASKLY_PROFILE_SYNC_URL"))
	if raw == "" {
		if strings.EqualFold(os.Getenv("NODE_ENV"), "production") {
			return errors.New("profile sync not configured")
		}
		return nil
	}
	parsed, err := profileSyncURL(raw, strings.EqualFold(os.Getenv("NODE_ENV"), "production"))
	if err != nil {
		return err
	}
	token := os.Getenv("AUTH_INTERNAL_TOKEN")
	if token == "" || (strings.EqualFold(os.Getenv("NODE_ENV"), "production") && len(token) < 32) {
		return errors.New("profile sync token missing")
	}
	body, _ := json.Marshal(map[string]string{"display_name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, strings.TrimRight(parsed.String(), "/")+"/internal/auth/users/"+strconv.FormatUint(id, 10)+"/profile", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := profileSyncClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return errors.New("profile sync rejected")
	}
	return nil
}

func profileSyncURL(raw string, production bool) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("invalid profile sync URL")
	}
	if production && parsed.Scheme != "https" && parsed.Hostname() != "taskly-server" && parsed.Hostname() != "server" {
		return nil, errors.New("profile sync URL must be private or HTTPS")
	}
	return parsed, nil
}
