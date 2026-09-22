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
	"net/http"
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
		`CREATE TABLE IF NOT EXISTS auth_login_codes (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT, email VARCHAR(255) NOT NULL, hashed_code VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, consumed_at DATETIME, failed_attempts INT NOT NULL DEFAULT 0, locked_until DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_auth_login_codes_email (email))`,
		`CREATE TABLE IF NOT EXISTS oauth_authorization_requests (id_hash CHAR(64) PRIMARY KEY, client_id VARCHAR(128) NOT NULL, redirect_uri VARCHAR(2048) NOT NULL, state_value VARCHAR(2048) NOT NULL, code_challenge VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS oauth_codes (code_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, redirect_uri VARCHAR(2048) NOT NULL, code_challenge VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, used_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS oauth_access_tokens (token_hash CHAR(64) PRIMARY KEY, family_id CHAR(64), user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, revoked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_oauth_access_family (family_id))`,
		`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (token_hash CHAR(64) PRIMARY KEY, family_id CHAR(64) NOT NULL, user_id BIGINT UNSIGNED NOT NULL, client_id VARCHAR(128) NOT NULL, scope_value VARCHAR(255) NOT NULL, expires_at DATETIME NOT NULL, used_at DATETIME, revoked_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_family (family_id))`,
		`CREATE TABLE IF NOT EXISTS auth_sessions (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_challenges (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, attempts INT NOT NULL DEFAULT 0, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_webauthn_sessions (token_hash CHAR(64) PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, purpose VARCHAR(16) NOT NULL, session_data TEXT NOT NULL, expires_at DATETIME NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_rate_limits (key_hash CHAR(64) PRIMARY KEY, attempts INT NOT NULL, window_start DATETIME NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := a.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create OAuth table: %w", err)
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
	var count int
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("users table missing: %w", err)
	}
	return nil
}

func (a *app) rateLimited(ctx context.Context, key string) (bool, error) {
	h := digest(key)
	_, err := a.db.ExecContext(ctx, `INSERT INTO auth_rate_limits (key_hash,attempts,window_start) VALUES (?,1,UTC_TIMESTAMP()) ON DUPLICATE KEY UPDATE attempts=IF(window_start < UTC_TIMESTAMP()-INTERVAL 10 MINUTE,1,attempts+1),window_start=IF(window_start < UTC_TIMESTAMP()-INTERVAL 10 MINUTE,UTC_TIMESTAMP(),window_start)`, h)
	if err != nil {
		return true, err
	}
	var attempts int
	if err := a.db.QueryRowContext(ctx, "SELECT attempts FROM auth_rate_limits WHERE key_hash=?", h).Scan(&attempts); err != nil {
		return true, err
	}
	return attempts > 10, nil
}

func scanUserID(row *sql.Row) (uint64, error) { var id uint64; err := row.Scan(&id); return id, err }
