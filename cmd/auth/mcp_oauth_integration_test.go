package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

type mcpMySQLFixture struct {
	t        *testing.T
	a        *app
	users    []uint64
	requests []string
}

func newMCPMySQLFixture(t *testing.T) *mcpMySQLFixture {
	t.Helper()
	dsn := os.Getenv("AUTH_MCP_TEST_DSN")
	if dsn == "" {
		t.Skip("AUTH_MCP_TEST_DSN is required for isolated MySQL MCP integration tests")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated MCP test database configuration")
	}
	host, _, err := net.SplitHostPort(config.Addr)
	address := net.ParseIP(host)
	if err != nil || config.Net != "tcp" || address == nil || !address.IsLoopback() || !regexp.MustCompile(`^taskly_auth_mcp_test(?:_[a-z0-9_]+)?$`).MatchString(config.DBName) || !config.ParseTime || config.Loc != time.UTC {
		t.Fatal("MCP integration tests require a loopback, UTC, isolated taskly_auth_mcp_test database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("could not open isolated MCP test database")
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(8)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal("isolated MCP test database is unavailable")
	}
	// Begin with the pre-feature OAuth schema to exercise an existing installation
	// upgrading resource and consent columns, rather than only fresh-table DDL.
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS users (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,email VARCHAR(255) NOT NULL UNIQUE,display_name VARCHAR(255),is_deleted BOOLEAN NOT NULL DEFAULT FALSE) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS blocks (id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,user_id BIGINT UNSIGNED NOT NULL,expires_at DATETIME) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS oauth_authorization_requests (id_hash CHAR(64) PRIMARY KEY,client_id VARCHAR(128) NOT NULL,redirect_uri VARCHAR(2048) NOT NULL,state_value VARCHAR(2048) NOT NULL,code_challenge VARCHAR(128) NOT NULL,scope_value VARCHAR(255) NOT NULL,expires_at DATETIME NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS oauth_codes (code_hash CHAR(64) PRIMARY KEY,user_id BIGINT UNSIGNED NOT NULL,client_id VARCHAR(128) NOT NULL,redirect_uri VARCHAR(2048) NOT NULL,code_challenge VARCHAR(128) NOT NULL,scope_value VARCHAR(255) NOT NULL,expires_at DATETIME NOT NULL,used_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS oauth_access_tokens (token_hash CHAR(64) PRIMARY KEY,family_id CHAR(64),user_id BIGINT UNSIGNED NOT NULL,client_id VARCHAR(128) NOT NULL,scope_value VARCHAR(255) NOT NULL,expires_at DATETIME NOT NULL,revoked_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX idx_oauth_access_family(family_id)) ENGINE=InnoDB`,
		`CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (token_hash CHAR(64) PRIMARY KEY,family_id CHAR(64) NOT NULL,user_id BIGINT UNSIGNED NOT NULL,client_id VARCHAR(128) NOT NULL,scope_value VARCHAR(255) NOT NULL,expires_at DATETIME NOT NULL,used_at DATETIME,revoked_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,INDEX idx_family(family_id)) ENGINE=InnoDB`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	a := mcpTestApp()
	a.db = db
	if err := a.ensureOAuthSchema(t.Context()); err != nil {
		t.Fatalf("upgrade OAuth schema: %v", err)
	}
	f := &mcpMySQLFixture{t: t, a: a}
	t.Cleanup(func() {
		for _, request := range f.requests {
			if _, err := db.Exec(`DELETE FROM oauth_authorization_requests WHERE id_hash=?`, digest(request)); err != nil {
				t.Error(err)
			}
		}
		for _, id := range f.users {
			for _, table := range []string{"oauth_codes", "oauth_access_tokens", "oauth_refresh_tokens", "auth_sessions", "blocks"} {
				if _, err := db.Exec("DELETE FROM "+table+" WHERE user_id=?", id); err != nil {
					t.Error(err)
				}
			}
			if _, err := db.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *mcpMySQLFixture) user() (uint64, string) {
	f.t.Helper()
	token, err := randomToken()
	if err != nil {
		f.t.Fatal(err)
	}
	result, err := f.a.db.ExecContext(f.t.Context(), `INSERT INTO users (email,display_name) VALUES (?,?)`, "mcp-"+token+"@example.test", "Synthetic MCP Student")
	if err != nil {
		f.t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		f.t.Fatal(err)
	}
	f.users = append(f.users, uint64(id))
	if _, err := f.a.db.ExecContext(f.t.Context(), `INSERT INTO auth_sessions (token_hash,user_id,expires_at) VALUES (?,?,?)`, digest(token), id, time.Now().UTC().Add(time.Hour)); err != nil {
		f.t.Fatal(err)
	}
	return uint64(id), token
}

func (f *mcpMySQLFixture) authorize(session, scope string) (requestID, verifier, state string) {
	f.t.Helper()
	verifier, err := randomToken()
	if err != nil {
		f.t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(verifier))
	state = "mcp-test-state-" + verifier
	q := url.Values{"client_id": {mcpTestPublicClient}, "redirect_uri": {mcpTestRedirect}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {state}, "scope": {scope}, "resource": {mcpTestResource}}
	r := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: session})
	w := httptest.NewRecorder()
	f.a.authorize(w, r)
	if w.Code != http.StatusFound {
		f.t.Fatalf("authorize status=%d body=%s", w.Code, w.Body.String())
	}
	u, err := url.Parse(w.Header().Get("Location"))
	if err != nil || u.Path != "/consent" {
		f.t.Fatalf("authorization skipped consent: %q", w.Header().Get("Location"))
	}
	requestID = u.Query().Get("request_id")
	if requestID == "" {
		f.t.Fatal("consent request identifier missing")
	}
	f.requests = append(f.requests, requestID)
	return requestID, verifier, state
}

func (f *mcpMySQLFixture) consent(session, requestID string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/oauth/consent?request_id="+url.QueryEscape(requestID), nil)
	r.Header.Set("Origin", f.a.uiURL)
	r.Header.Set("X-Auth-UI", "1")
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: session})
	w := httptest.NewRecorder()
	f.a.consent(w, r)
	return w
}

func (f *mcpMySQLFixture) approve(session, requestID, csrf string, approve bool) *httptest.ResponseRecorder {
	input, _ := json.Marshal(map[string]any{"request_id": requestID, "csrf_token": csrf, "approve": approve})
	r := httptest.NewRequest(http.MethodPost, "/oauth/consent", strings.NewReader(string(input)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", f.a.uiURL)
	r.Header.Set("X-Auth-UI", "1")
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: session})
	w := httptest.NewRecorder()
	f.a.approveConsent(w, r)
	return w
}

func (f *mcpMySQLFixture) code(session, scope string) (string, string) {
	f.t.Helper()
	requestID, verifier, state := f.authorize(session, scope)
	read := f.consent(session, requestID)
	if read.Code != 200 {
		f.t.Fatalf("consent status=%d body=%s", read.Code, read.Body.String())
	}
	csrf, _ := mcpDecodeJSON(f.t, read)["csrf_token"].(string)
	if csrf == "" {
		f.t.Fatal("consent did not include CSRF token")
	}
	response := f.approve(session, requestID, csrf, true)
	if response.Code != 200 {
		f.t.Fatalf("approve status=%d body=%s", response.Code, response.Body.String())
	}
	raw, _ := mcpDecodeJSON(f.t, response)["redirect_uri"].(string)
	redirect, err := url.Parse(raw)
	if err != nil || redirect.Scheme != "https" || redirect.Host != "chatgpt.com" || redirect.Query().Get("state") != state || redirect.Query().Get("iss") != f.a.issuer || redirect.Query().Get("code") == "" {
		f.t.Fatal("consent callback lost client, code, state, or issuer binding")
	}
	return redirect.Query().Get("code"), verifier
}

func (f *mcpMySQLFixture) token(form url.Values, basic bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	f.a.token(w, mcpFormRequest(form, basic))
	return w
}

func (f *mcpMySQLFixture) login(session, scope string) map[string]any {
	f.t.Helper()
	code, verifier := f.code(session, scope)
	response := f.token(url.Values{"client_id": {mcpTestPublicClient}, "grant_type": {"authorization_code"}, "redirect_uri": {mcpTestRedirect}, "code": {code}, "code_verifier": {verifier}, "resource": {mcpTestResource}}, false)
	if response.Code != 200 {
		f.t.Fatalf("token status=%d body=%s", response.Code, response.Body.String())
	}
	return mcpDecodeJSON(f.t, response)
}

func (f *mcpMySQLFixture) exchange(parent, scope string) *httptest.ResponseRecorder {
	return f.token(url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {parent}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "resource": {mcpTestTasklyResource}, "scope": {scope}}, true)
}

func (f *mcpMySQLFixture) inspect(token string, backend bool) map[string]any {
	f.t.Helper()
	r := mcpFormRequest(url.Values{"token": {token}}, false)
	if backend {
		r.SetBasicAuth(mcpTestBackendClient, mcpTestBackendSecret)
	} else {
		r.SetBasicAuth(f.a.clientID, f.a.clientSecret)
	}
	w := httptest.NewRecorder()
	f.a.introspect(w, r)
	if w.Code != 200 {
		f.t.Fatalf("introspection status=%d body=%s", w.Code, w.Body.String())
	}
	return mcpDecodeJSON(f.t, w)
}

func mcpAccess(t *testing.T, tokens map[string]any) string {
	t.Helper()
	token, _ := tokens["access_token"].(string)
	if token == "" {
		t.Fatal("access token missing")
	}
	return token
}

func TestMCPOAuthMySQLConsentExchangeAndRevocation(t *testing.T) {
	f := newMCPMySQLFixture(t)
	t.Run("migration is repeatable", func(t *testing.T) {
		if err := f.a.ensureOAuthSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"oauth_authorization_requests", "oauth_codes", "oauth_access_tokens", "oauth_refresh_tokens"} {
			var count int
			if err := f.a.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='resource_uri'`, table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("resource column in %s: count=%d err=%v", table, count, err)
			}
		}
	})
	t.Run("consent is bound to user and single use", func(t *testing.T) {
		_, session := f.user()
		_, other := f.user()
		requestID, _, state := f.authorize(session, "guidance:read guidance:bookmarks:write guidance:tasks:write")
		read := f.consent(session, requestID)
		if read.Code != 200 {
			t.Fatalf("consent status=%d", read.Code)
		}
		body := mcpDecodeJSON(t, read)
		if body["resource"] != mcpTestResource || body["redirect_uri"] != mcpTestRedirect {
			t.Fatal("consent omitted resource or callback")
		}
		csrf, _ := body["csrf_token"].(string)
		for _, response := range []*httptest.ResponseRecorder{f.consent(other, requestID), f.approve(other, requestID, csrf, true), f.approve(session, requestID, "wrong-csrf", true)} {
			if response.Code < 400 {
				t.Fatal("cross-user or CSRF consent was accepted")
			}
		}
		response := f.approve(session, requestID, csrf, false)
		if response.Code != 200 {
			t.Fatalf("deny status=%d body=%s", response.Code, response.Body.String())
		}
		raw, _ := mcpDecodeJSON(t, response)["redirect_uri"].(string)
		callback, err := url.Parse(raw)
		if err != nil || callback.Query().Get("error") != "access_denied" || callback.Query().Get("state") != state || callback.Query().Get("iss") != f.a.issuer || callback.Query().Get("code") != "" {
			t.Fatal("denied consent emitted an invalid callback")
		}
		if replay := f.approve(session, requestID, csrf, true); replay.Code < 400 {
			t.Fatal("denied consent could be replayed")
		}
	})
	t.Run("simultaneous consent issues only one code", func(t *testing.T) {
		_, session := f.user()
		requestID, _, _ := f.authorize(session, "guidance:read")
		csrf, _ := mcpDecodeJSON(t, f.consent(session, requestID))["csrf_token"].(string)
		var wg sync.WaitGroup
		statuses := make(chan int, 2)
		for range 2 {
			wg.Go(func() { statuses <- f.approve(session, requestID, csrf, true).Code })
		}
		wg.Wait()
		close(statuses)
		success := 0
		for status := range statuses {
			if status == 200 {
				success++
			} else if status < 400 || status >= 500 {
				t.Errorf("unexpected concurrent consent status=%d", status)
			}
		}
		if success != 1 {
			t.Fatalf("consent successful responses=%d, want1", success)
		}
	})
	t.Run("code is PKCE and resource bound before one-time redemption", func(t *testing.T) {
		_, session := f.user()
		code, verifier := f.code(session, "guidance:read")
		form := url.Values{"client_id": {mcpTestPublicClient}, "grant_type": {"authorization_code"}, "redirect_uri": {mcpTestRedirect}, "code": {code}, "code_verifier": {verifier}, "resource": {mcpTestTasklyResource}}
		if bad := f.token(form, false); bad.Code < 400 {
			t.Fatal("redeemed code for a different resource")
		}
		form.Set("resource", mcpTestResource)
		form.Set("code_verifier", strings.Repeat("a", 43))
		if bad := f.token(form, false); bad.Code < 400 {
			t.Fatal("redeemed code without PKCE secret")
		}
		form.Set("code_verifier", verifier)
		if good := f.token(form, false); good.Code != 200 {
			t.Fatalf("valid redemption status=%d body=%s", good.Code, good.Body.String())
		}
		if replay := f.token(form, false); replay.Code < 400 {
			t.Fatal("authorization code could be replayed")
		}
	})
	t.Run("refresh preserves audience and scope and detects reuse", func(t *testing.T) {
		_, session := f.user()
		tokens := f.login(session, "guidance:read guidance:tasks:write")
		parent := mcpAccess(t, tokens)
		refresh, _ := tokens["refresh_token"].(string)
		form := url.Values{"client_id": {mcpTestPublicClient}, "grant_type": {"refresh_token"}, "refresh_token": {refresh}, "resource": {mcpTestTasklyResource}}
		if bad := f.token(form, false); bad.Code < 400 {
			t.Fatal("refresh changed audience")
		}
		form.Set("resource", mcpTestResource)
		response := f.token(form, false)
		if response.Code != 200 {
			t.Fatalf("refresh status=%d body=%s", response.Code, response.Body.String())
		}
		updated := mcpDecodeJSON(t, response)
		if updated["scope"] != tokens["scope"] || updated["refresh_token"] == refresh {
			t.Fatal("refresh failed scope preservation or rotation")
		}
		fresh := mcpAccess(t, updated)
		if identity := f.inspect(fresh, true); identity["active"] != true || identity["aud"] != mcpTestResource {
			t.Fatal("refreshed token lost resource")
		}
		if replay := f.token(form, false); replay.Code < 400 {
			t.Fatal("refresh token could be replayed")
		}
		if f.inspect(parent, true)["active"] != false || f.inspect(fresh, true)["active"] != false {
			t.Fatal("refresh reuse did not revoke entire family")
		}
	})
	t.Run("delegation is narrow short lived and parent revocation is immediate", func(t *testing.T) {
		id, session := f.user()
		tokens := f.login(session, "guidance:read guidance:tasks:write")
		parent := mcpAccess(t, tokens)
		if _, err := f.a.db.Exec(`UPDATE oauth_access_tokens SET expires_at=UTC_TIMESTAMP()+INTERVAL 20 SECOND WHERE token_hash=?`, digest(parent)); err != nil {
			t.Fatal(err)
		}
		response := f.exchange(parent, "guidance:tasks:write")
		if response.Code != 200 {
			t.Fatalf("exchange status=%d body=%s", response.Code, response.Body.String())
		}
		child := mcpDecodeJSON(t, response)
		access := mcpAccess(t, child)
		expires, _ := child["expires_in"].(float64)
		if expires <= 0 || expires > 20 || child["refresh_token"] != nil || child["scope"] != "guidance:tasks:write" || child["issued_token_type"] != "urn:ietf:params:oauth:token-type:access_token" {
			t.Fatalf("exchange output exceeded delegation policy: expires=%.0f", expires)
		}
		identity := f.inspect(access, false)
		actor, _ := identity["act"].(map[string]any)
		if identity["active"] != true || identity["aud"] != mcpTestTasklyResource || identity["client_id"] != mcpTestBackendClient || identity["sub"] != fmt.Sprint(id) || identity["original_client_id"] != mcpTestPublicClient || actor["sub"] != mcpTestBackendClient {
			t.Fatal("delegated identity lost audience, subject, or actor binding")
		}
		if f.inspect(access, true)["active"] != false {
			t.Fatal("MCP resource introspection could inspect a downstream-only token")
		}
		r := mcpFormRequest(url.Values{"client_id": {mcpTestPublicClient}, "token": {parent}}, false)
		w := httptest.NewRecorder()
		f.a.revoke(w, r)
		if w.Code != 200 || f.inspect(access, false)["active"] != false {
			t.Fatal("revoking the parent access token did not invalidate its child")
		}
	})
	t.Run("read consent cannot escalate to write and account closure revokes delegation", func(t *testing.T) {
		id, session := f.user()
		tokens := f.login(session, "guidance:read")
		parent := mcpAccess(t, tokens)
		if denied := f.exchange(parent, "guidance:tasks:write"); denied.Code < 400 {
			t.Fatal("read-only consent escalated to task writes")
		}
		response := f.exchange(parent, "guidance:tasks:read")
		if response.Code != 200 {
			t.Fatalf("read exchange status=%d body=%s", response.Code, response.Body.String())
		}
		child := mcpAccess(t, mcpDecodeJSON(t, response))
		if _, err := f.a.db.Exec(`INSERT INTO blocks (user_id) VALUES (?)`, id); err != nil {
			t.Fatal(err)
		}
		if f.inspect(parent, true)["active"] != false || f.inspect(child, false)["active"] != false {
			t.Fatal("blocked user retained token access")
		}
		if denied := f.exchange(parent, "guidance:tasks:read"); denied.Code < 400 {
			t.Fatal("blocked user minted delegated access")
		}
	})
	t.Run("expiry and family revocation invalidate existing delegated tokens", func(t *testing.T) {
		_, session := f.user()
		tokens := f.login(session, "guidance:read")
		parent := mcpAccess(t, tokens)
		response := f.exchange(parent, "guidance:tasks:read")
		if response.Code != 200 {
			t.Fatalf("exchange status=%d", response.Code)
		}
		child := mcpAccess(t, mcpDecodeJSON(t, response))
		if _, err := f.a.db.Exec(`UPDATE oauth_access_tokens SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE token_hash=?`, digest(parent)); err != nil {
			t.Fatal(err)
		}
		if f.inspect(child, false)["active"] != false {
			t.Fatal("expired parent retained delegated access")
		}
		tokens = f.login(session, "guidance:read")
		parent = mcpAccess(t, tokens)
		response = f.exchange(parent, "guidance:tasks:read")
		if response.Code != 200 {
			t.Fatalf("exchange status=%d", response.Code)
		}
		child = mcpAccess(t, mcpDecodeJSON(t, response))
		refresh, _ := tokens["refresh_token"].(string)
		r := mcpFormRequest(url.Values{"client_id": {mcpTestPublicClient}, "token": {refresh}}, false)
		w := httptest.NewRecorder()
		f.a.revoke(w, r)
		if w.Code != 200 || f.inspect(parent, true)["active"] != false || f.inspect(child, false)["active"] != false {
			t.Fatal("family revocation did not invalidate parent and child")
		}
	})
}
