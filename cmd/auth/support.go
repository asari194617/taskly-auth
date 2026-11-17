package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func oauthError(w http.ResponseWriter, status int, code string) {
	jsonResponse(w, status, map[string]string{"error": code})
}
func parseJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		oauthError(w, 400, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		oauthError(w, 400, "invalid_request")
		return false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		oauthError(w, 400, "invalid_request")
		return false
	}
	return true
}

type alerter struct {
	url  string
	mu   sync.Mutex
	last time.Time
}

func newAlerter(url string) *alerter { return &alerter{url: url} }
func (a *alerter) send(message string) {
	if a.reserve() {
		go a.deliver(message)
	}
}

// sendSync is for fatal startup errors; an asynchronous send would be lost on exit.
func (a *alerter) sendSync(message string) {
	if a.reserve() {
		a.deliver(message)
	}
}

func (a *alerter) reserve() bool {
	if a.url == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if time.Since(a.last) < 5*time.Minute {
		return false
	}
	a.last = time.Now()
	return true
}

func (a *alerter) deliver(message string) {
	body, _ := json.Marshal(map[string]string{"content": message})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TasklyAuth/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Print("Discord alert delivery failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("Discord alert delivery status=%d", resp.StatusCode)
	}
}

func (a *app) ensureOAuthSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS auth_email_verifications (user_id BIGINT UNSIGNED PRIMARY KEY, email VARCHAR(255) NOT NULL, hashed_code CHAR(64), expires_at DATETIME, failed_attempts INT NOT NULL DEFAULT 0, verified_at DATETIME, requested_at DATETIME, INDEX idx_auth_email_verifications_email (email))`,
		`CREATE TABLE IF NOT EXISTS auth_login_codes (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT, user_id BIGINT UNSIGNED NULL, email VARCHAR(255) NOT NULL, hashed_code VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, consumed_at DATETIME, failed_attempts INT NOT NULL DEFAULT 0, locked_until DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_auth_login_codes_email (email), INDEX idx_auth_login_codes_user_recent (user_id,id))`,
		`CREATE TABLE IF NOT EXISTS oauth_authorization_requests (id_hash CHAR(64) PRIMARY KEY, client_id VARCHAR(128) NOT NULL, redirect_uri VARCHAR(2048) NOT NULL, state_value VARCHAR(2048) NOT NULL, code_challenge VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_oauth_authorization_expires (expires_at))`,
		`CREATE TABLE IF NOT EXISTS oauth_codes (code_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, redirect_uri VARCHAR(2048) NOT NULL, code_challenge VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, used_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS oauth_access_tokens (token_hash CHAR(64) PRIMARY KEY, family_id CHAR(64), user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, revoked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_oauth_access_family (family_id))`,
		`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (token_hash CHAR(64) PRIMARY KEY, family_id CHAR(64) NOT NULL, user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, used_at DATETIME, revoked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_family (family_id))`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, expires_at DATETIME NOT NULL, security_verified_at DATETIME NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_login_history (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT, user_id BIGINT UNSIGNED NOT NULL, method VARCHAR(32) NOT NULL, logged_in_at DATETIME NOT NULL, ip_address VARCHAR(45), geo_city VARCHAR(128), geo_region VARCHAR(128), geo_country CHAR(2), user_agent VARCHAR(1024), ip_source VARCHAR(32), geo_source VARCHAR(32), geo_db_release CHAR(7), geo_db_sha256 CHAR(64), INDEX idx_auth_login_history_user_recent (user_id,logged_in_at,id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS auth_security_codes (session_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, email VARCHAR(255) NOT NULL, hashed_code CHAR(64) NOT NULL, expires_at DATETIME NOT NULL, failed_attempts INT NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_auth_security_codes_user (user_id))`,
		`CREATE TABLE IF NOT EXISTS auth_security_totp_uses (user_id BIGINT UNSIGNED NOT NULL, code_hash CHAR(64) NOT NULL, expires_at DATETIME NOT NULL, PRIMARY KEY (user_id,code_hash), INDEX idx_auth_security_totp_uses_expires (expires_at))`,
		`CREATE TABLE IF NOT EXISTS auth_challenges (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0, attempts INT NOT NULL DEFAULT 0, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_webauthn_sessions (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0, purpose VARCHAR(16) NOT NULL, session_data TEXT NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_totp_setups (user_id BIGINT UNSIGNED PRIMARY KEY, encrypted_secret VARCHAR(512) NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_auth_totp_setups_expires (expires_at))`,
		`CREATE TABLE IF NOT EXISTS auth_rate_limits (key_hash CHAR(64) PRIMARY KEY, attempts INT NOT NULL, window_start DATETIME NOT NULL, INDEX idx_auth_rate_limits_window (window_start))`,
	}
	for _, statement := range statements {
		if _, err := a.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create OAuth table: %w", err)
		}
	}
	if err := a.ensureMCPSchema(ctx); err != nil {
		return err
	}
	for _, column := range []struct{ table, name string }{
		{"users", "auth_epoch"},
		{"auth_challenges", "auth_epoch"},
		{"auth_webauthn_sessions", "auth_epoch"},
	} {
		var count int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, column.table, column.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s auth epoch: %w", column.table, err)
		}
		if count == 0 {
			statement := "ALTER TABLE " + column.table + " ADD COLUMN auth_epoch BIGINT UNSIGNED NOT NULL DEFAULT 0"
			if column.table == "users" {
				var emailCodeColumn int
				if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='users' AND COLUMN_NAME='email_code_allowed'`).Scan(&emailCodeColumn); err != nil {
					return fmt.Errorf("inspect users email code column: %w", err)
				}
				if emailCodeColumn != 0 {
					statement += " AFTER email_code_allowed"
				}
			}
			if _, err := a.db.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("add %s auth epoch: %w", column.table, err)
			}
		}
	}
	for _, index := range []struct{ table, name, column string }{
		{"oauth_authorization_requests", "idx_oauth_authorization_expires", "expires_at"},
		{"auth_rate_limits", "idx_auth_rate_limits_window", "window_start"},
	} {
		var count int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?`, index.table, index.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect %s index: %w", index.table, err)
		}
		if count == 0 {
			if _, err := a.db.ExecContext(ctx, "CREATE INDEX "+index.name+" ON "+index.table+" ("+index.column+")"); err != nil {
				return fmt.Errorf("add %s index: %w", index.table, err)
			}
		}
	}
	var loginCodeUserColumnCount int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_login_codes' AND COLUMN_NAME='user_id'`).Scan(&loginCodeUserColumnCount); err != nil {
		return fmt.Errorf("inspect login code schema: %w", err)
	}
	if loginCodeUserColumnCount == 0 {
		// Pre-upgrade codes lack a reliable user binding and must expire during rollout.
		if _, err := a.db.ExecContext(ctx, `ALTER TABLE auth_login_codes ADD COLUMN user_id BIGINT UNSIGNED NULL, ADD INDEX idx_auth_login_codes_user_recent (user_id,id)`); err != nil {
			return fmt.Errorf("upgrade login code schema: %w", err)
		}
	}
	// Stage deployments may already have the table from an earlier image. Existing
	// tokens have no family and expire within 15 minutes; new tokens are linked.
	var familyColumnCount int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='oauth_access_tokens' AND COLUMN_NAME='family_id'`).Scan(&familyColumnCount); err != nil {
		return fmt.Errorf("inspect access token schema: %w", err)
	}
	if familyColumnCount == 0 {
		if _, err := a.db.ExecContext(ctx, `ALTER TABLE oauth_access_tokens ADD COLUMN family_id CHAR(64), ADD INDEX idx_oauth_access_family (family_id)`); err != nil {
			return fmt.Errorf("upgrade access token schema: %w", err)
		}
	}
	var securityVerifiedColumnCount int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_sessions' AND COLUMN_NAME='security_verified_at'`).Scan(&securityVerifiedColumnCount); err != nil {
		return fmt.Errorf("inspect security step-up schema: %w", err)
	}
	if securityVerifiedColumnCount == 0 {
		if _, err := a.db.ExecContext(ctx, `ALTER TABLE auth_sessions ADD COLUMN security_verified_at DATETIME NULL`); err != nil {
			return fmt.Errorf("upgrade security step-up schema: %w", err)
		}
	}
	for _, column := range []struct{ name, definition string }{
		{"ip_address", "VARCHAR(45) NULL"},
		{"geo_city", "VARCHAR(128) NULL"},
		{"geo_region", "VARCHAR(128) NULL"},
		{"geo_country", "CHAR(2) NULL"},
		{"user_agent", "VARCHAR(1024) NULL"},
		{"ip_source", "VARCHAR(32) NULL"},
		{"geo_source", "VARCHAR(32) NULL"},
		{"geo_db_release", "CHAR(7) NULL"},
		{"geo_db_sha256", "CHAR(64) NULL"},
	} {
		var count int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='auth_login_history' AND COLUMN_NAME=?`, column.name).Scan(&count); err != nil {
			return fmt.Errorf("inspect login history column %s: %w", column.name, err)
		}
		if count == 0 {
			if _, err := a.db.ExecContext(ctx, `ALTER TABLE auth_login_history ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("upgrade login history column %s: %w", column.name, err)
			}
		}
	}
	var count int
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("users table missing: %w", err)
	}
	return nil
}

func (a *app) rateLimited(ctx context.Context, key string) (bool, error) {
	return a.rateLimitedWithLimit(ctx, key, 10)
}

func (a *app) rateLimitedWithLimit(ctx context.Context, key string, limit int) (bool, error) {
	h := digest(key)
	_, err := a.db.ExecContext(ctx, `INSERT INTO auth_rate_limits (key_hash,attempts,window_start) VALUES (?,1,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE attempts=IF(window_start < UTC_TIMESTAMP()-INTERVAL 10 MINUTE,1,attempts+1),window_start=IF(window_start < UTC_TIMESTAMP()-INTERVAL 10 MINUTE,UTC_TIMESTAMP(),window_start)`, h)
	if err != nil {
		return true, err
	}
	var attempts int
	if err := a.db.QueryRowContext(ctx, "SELECT attempts FROM auth_rate_limits WHERE key_hash=?", h).Scan(&attempts); err != nil {
		return true, err
	}
	return attempts > limit, nil
}

func (a *app) sourceRateLimited(ctx context.Context, key string) (bool, error) {
	var exists int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_rate_limits WHERE key_hash=?`, digest(key)).Scan(&exists); err != nil {
		return true, err
	}
	if exists == 0 {
		var total int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_rate_limits`).Scan(&total); err != nil {
			return true, err
		}
		if total >= 20000 {
			return true, nil
		}
	}
	return a.rateLimitedWithLimit(ctx, key, 30)
}

// Unknown addresses consume only a source budget. Known accounts share one
// budget across login, passkeys and code requests, regardless of spelling.
func (a *app) authAttemptLimited(r *http.Request, userID uint64) (bool, error) {
	visitor, _, _ := loginRequestEvidence(r, a.historyTrustedProxyPeer)
	ip := ""
	if visitor != nil {
		ip = *visitor
	} else {
		ip = r.RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
	}
	limited, err := a.sourceRateLimited(r.Context(), "auth-source:"+ip)
	if err != nil || limited || userID == 0 {
		return limited, err
	}
	return a.rateLimitedWithLimit(r.Context(), "auth-account:"+strconv.FormatUint(userID, 10), 100)
}

func (a *app) allowAuthorizationRequest(w http.ResponseWriter, r *http.Request) bool {
	limited, err := a.authAttemptLimited(r, 0)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return false
	}
	var count int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM oauth_authorization_requests`).Scan(&count); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	if count >= 10000 {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return false
	}
	return true
}

func (a *app) cleanupExpiredRows(ctx context.Context) error {
	for _, query := range []string{
		`DELETE FROM oauth_authorization_requests WHERE expires_at<=UTC_TIMESTAMP() LIMIT 500`,
		`DELETE FROM auth_rate_limits WHERE window_start<UTC_TIMESTAMP()-INTERVAL 10 MINUTE LIMIT 500`,
	} {
		if _, err := a.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) runExpiryCleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := a.cleanupExpiredRows(cleanupCtx); err != nil {
			log.Print("auth expiry cleanup failed")
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func scanUserID(row *sql.Row) (uint64, error) { var id uint64; err := row.Scan(&id); return id, err }

func bumpAuthEpoch(ctx context.Context, tx *sql.Tx, id uint64) error {
	_, err := tx.ExecContext(ctx, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, id)
	return err
}

func (a *app) sessionCookieName() string {
	if a.secure {
		return "__Host-taskly_auth_session"
	}
	return "taskly_auth_session"
}
