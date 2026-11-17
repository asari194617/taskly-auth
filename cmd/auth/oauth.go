package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var challengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type authorizationRequest struct {
	clientID, redirectURI, state, challenge, scope string
	expires                                        time.Time
}

func (a *app) metadata(w http.ResponseWriter, r *http.Request) {
	authMethods := []string{"client_secret_basic"}
	grants := []string{"authorization_code", "refresh_token"}
	scopes := []string{"taskly:login"}
	if a.mcp != nil {
		grants = append(grants, mcpExchangeGrant)
		scopes = append(scopes, "guidance:read", "guidance:bookmarks:write", "guidance:tasks:write")
	}
	if len(a.publicClients) != 0 || a.mcp != nil {
		authMethods = append(authMethods, "none")
	}
	jsonResponse(w, 200, map[string]any{
		"issuer":                                a.issuer,
		"authorization_endpoint":                a.issuer + "/oauth/authorize",
		"token_endpoint":                        a.issuer + "/oauth/token",
		"introspection_endpoint":                a.issuer + "/oauth/introspect",
		"revocation_endpoint":                   a.issuer + "/oauth/revoke",
		"userinfo_endpoint":                     a.issuer + "/oauth/userinfo",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 grants,
		"scopes_supported":                      scopes,
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": authMethods,
	})
}

func (a *app) authorize(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := r.URL.Query().Get("request_id")
	if requestID == "" && r.URL.Query().Get("client_id") == mcpPublicClientID {
		a.authorizeMCP(w, r)
		return
	}
	if requestID == "" {
		q := r.URL.Query()
		if len(q["resource"]) != 0 {
			oauthError(w, 400, "invalid_target")
			return
		}
		if !a.registeredRedirect(q.Get("client_id"), q.Get("redirect_uri")) || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !challengePattern.MatchString(q.Get("code_challenge")) || len(q.Get("state")) < 16 || len(q.Get("state")) > 1024 || q.Get("scope") != "taskly:login" {
			oauthError(w, 400, "invalid_request")
			return
		}
		if !a.allowAuthorizationRequest(w, r) {
			return
		}
		var err error
		requestID, err = randomToken()
		if err != nil {
			oauthError(w, 500, "server_error")
			a.alerts.send("Taskly auth random generation failed")
			return
		}
		_, err = a.db.ExecContext(ctx, `INSERT INTO oauth_authorization_requests (id_hash,client_id,redirect_uri,state_value,code_challenge,scope_value,expires_at) VALUES (?,?,?,?,?,?,?)`, digest(requestID), q.Get("client_id"), q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge"), q.Get("scope"), time.Now().UTC().Add(10*time.Minute))
		if err != nil {
			oauthError(w, 500, "server_error")
			a.alerts.send("Taskly auth authorization request storage failed")
			return
		}
	}
	if !verifierPattern.MatchString(requestID) {
		oauthError(w, 400, "invalid_request")
		return
	}
	var req authorizationRequest
	err := a.db.QueryRowContext(ctx, `SELECT client_id,redirect_uri,state_value,code_challenge,scope_value,expires_at FROM oauth_authorization_requests WHERE id_hash=?`, digest(requestID)).Scan(&req.clientID, &req.redirectURI, &req.state, &req.challenge, &req.scope, &req.expires)
	if err != nil || time.Now().UTC().After(req.expires) || !a.registeredRedirect(req.clientID, req.redirectURI) {
		oauthError(w, 400, "invalid_request")
		return
	}
	if req.clientID == mcpPublicClientID {
		a.resumeMCPAuthorization(w, r, requestID)
		return
	}
	userID, ok := a.sessionUser(r)
	if !ok {
		http.Redirect(w, r, a.uiURL+"/login?request_id="+url.QueryEscape(requestID), http.StatusFound)
		return
	}
	code, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM oauth_authorization_requests WHERE id_hash=? AND expires_at>UTC_TIMESTAMP()`, digest(requestID))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		oauthError(w, 400, "invalid_request")
		return
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_codes (code_hash,user_id,client_id,redirect_uri,code_challenge,scope_value,expires_at) VALUES (?,?,?,?,?,?,?)`, digest(code), userID, req.clientID, req.redirectURI, req.challenge, req.scope, time.Now().UTC().Add(2*time.Minute))
	if err != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth authorization code storage failed")
		return
	}
	redirect, _ := url.Parse(req.redirectURI)
	query := redirect.Query()
	query.Set("code", code)
	query.Set("state", req.state)
	query.Set("iss", a.issuer)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (a *app) authenticateClient(w http.ResponseWriter, r *http.Request) bool {
	id, secret, ok := r.BasicAuth()
	if !ok || !equal(id, a.clientID) || !equal(secret, a.clientSecret) {
		w.Header().Set("WWW-Authenticate", `Basic realm="taskly-auth"`)
		oauthError(w, 401, "invalid_client")
		return false
	}
	return true
}

func (a *app) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	if r.Form.Get("grant_type") == mcpExchangeGrant {
		a.exchangeMCPToken(w, r)
		return
	}
	clientID, ok := a.authenticateTokenClient(w, r)
	if !ok {
		return
	}
	if clientID == mcpPublicClientID {
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			a.exchangeMCPCode(w, r)
		case "refresh_token":
			a.refreshMCP(w, r)
		default:
			oauthError(w, 400, "unsupported_grant_type")
		}
		return
	}
	if len(r.Form["resource"]) != 0 {
		oauthError(w, 400, "invalid_target")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		a.exchangeCode(w, r, clientID)
	case "refresh_token":
		a.refresh(w, r, clientID)
	default:
		oauthError(w, 400, "unsupported_grant_type")
	}
}

func (a *app) authenticateTokenClient(w http.ResponseWriter, r *http.Request) (string, bool) {
	if id, secret, hasBasic := r.BasicAuth(); hasBasic {
		if equal(id, a.clientID) && equal(secret, a.clientSecret) && (r.Form.Get("client_id") == "" || r.Form.Get("client_id") == id) {
			return id, true
		}
	} else if r.Header.Get("Authorization") == "" && r.Form.Get("client_secret") == "" {
		id := r.Form.Get("client_id")
		if a.mcp != nil && id == mcpPublicClientID {
			return id, true
		}
		if _, registered := a.publicClients[id]; registered {
			return id, true
		}
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="taskly-auth"`)
	oauthError(w, 401, "invalid_client")
	return "", false
}

func (a *app) exchangeCode(w http.ResponseWriter, r *http.Request, authenticatedClientID string) {
	code, verifier := r.Form.Get("code"), r.Form.Get("code_verifier")
	if code == "" || !verifierPattern.MatchString(verifier) || !a.registeredRedirect(authenticatedClientID, r.Form.Get("redirect_uri")) {
		oauthError(w, 400, "invalid_request")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	var userID uint64
	var codeClientID, redirectURI, challenge, scope string
	var expires time.Time
	var used sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,client_id,redirect_uri,code_challenge,scope_value,expires_at,used_at FROM oauth_codes WHERE code_hash=? FOR UPDATE`, digest(code)).Scan(&userID, &codeClientID, &redirectURI, &challenge, &scope, &expires, &used)
	if err != nil || used.Valid || time.Now().UTC().After(expires) || codeClientID != authenticatedClientID || redirectURI != r.Form.Get("redirect_uri") {
		oauthError(w, 400, "invalid_grant")
		return
	}
	h := sha256.Sum256([]byte(verifier))
	actual := base64.RawURLEncoding.EncodeToString(h[:])
	if !equal(actual, challenge) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if !a.subjectActive(r.Context(), userID) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE oauth_codes SET used_at=UTC_TIMESTAMP() WHERE code_hash=?`, digest(code)); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	access, refresh, family, err := newTokenSet()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if err = a.storeTokenSet(r.Context(), tx, userID, authenticatedClientID, scope, access, refresh, family); err != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		a.alerts.send("Taskly auth token storage failed")
		return
	}
	writeTokens(w, access, refresh, scope)
}

func newTokenSet() (string, string, string, error) {
	access, err := randomToken()
	if err != nil {
		return "", "", "", err
	}
	refresh, err := randomToken()
	if err != nil {
		return "", "", "", err
	}
	family, err := randomToken()
	return access, refresh, digest(family), err
}

func (a *app) storeTokenSet(ctx context.Context, tx *sql.Tx, userID uint64, clientID, scope, access, refresh, family string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO oauth_access_tokens (token_hash,family_id,user_id,client_id,scope_value,expires_at) VALUES (?,?,?,?,?,?)`, digest(access), family, userID, clientID, scope, time.Now().UTC().Add(15*time.Minute))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens (token_hash,family_id,user_id,client_id,scope_value,expires_at) VALUES (?,?,?,?,?,?)`, digest(refresh), family, userID, clientID, scope, time.Now().UTC().Add(30*24*time.Hour))
	return err
}

func revokeFamily(ctx context.Context, tx *sql.Tx, family string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET revoked_at=UTC_TIMESTAMP() WHERE family_id=? AND revoked_at IS NULL`, family); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE oauth_access_tokens SET revoked_at=UTC_TIMESTAMP() WHERE family_id=? AND revoked_at IS NULL`, family)
	return err
}

func writeTokens(w http.ResponseWriter, access, refresh, scope string) {
	jsonResponse(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 900, "refresh_token": refresh, "scope": scope})
}

func (a *app) refresh(w http.ResponseWriter, r *http.Request, authenticatedClientID string) {
	old := r.Form.Get("refresh_token")
	if old == "" {
		oauthError(w, 400, "invalid_request")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	var userID uint64
	var tokenClientID, scope, family string
	var expires time.Time
	var used, revoked sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,client_id,scope_value,family_id,expires_at,used_at,revoked_at FROM oauth_refresh_tokens WHERE token_hash=? FOR UPDATE`, digest(old)).Scan(&userID, &tokenClientID, &scope, &family, &expires, &used, &revoked)
	if err != nil || tokenClientID != authenticatedClientID || revoked.Valid || time.Now().UTC().After(expires) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if !a.subjectActive(r.Context(), userID) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if used.Valid {
		if err := revokeFamily(r.Context(), tx, family); err != nil || tx.Commit() != nil {
			oauthError(w, 500, "server_error")
			return
		}
		oauthError(w, 400, "invalid_grant")
		a.alerts.send("Taskly auth refresh token reuse detected")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE oauth_refresh_tokens SET used_at=UTC_TIMESTAMP() WHERE token_hash=?`, digest(old)); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	access, newRefresh, _, err := newTokenSet()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if err = a.storeTokenSet(r.Context(), tx, userID, authenticatedClientID, scope, access, newRefresh, family); err != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	writeTokens(w, access, newRefresh, scope)
}

func (a *app) introspect(w http.ResponseWriter, r *http.Request) {
	if a.mcp != nil {
		a.introspectWithMCP(w, r)
		return
	}
	if !a.authenticateClient(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	userID, clientID, scope, expires, ok := a.accessToken(r.Context(), r.Form.Get("token"))
	if !ok || clientID == mcpPublicClientID || clientID == mcpBackendClientID {
		jsonResponse(w, 200, map[string]bool{"active": false})
		return
	}
	jsonResponse(w, 200, map[string]any{"active": true, "client_id": clientID, "sub": strconv.FormatUint(userID, 10), "scope": scope, "aud": "taskly-api", "iss": a.issuer, "exp": expires.Unix(), "token_type": "Bearer"})
}

func (a *app) revoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	clientID, ok := a.authenticateTokenClient(w, r)
	if !ok {
		return
	}
	token := r.Form.Get("token")
	if token != "" {
		tx, err := a.db.BeginTx(r.Context(), nil)
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(), `UPDATE oauth_access_tokens SET revoked_at=UTC_TIMESTAMP() WHERE token_hash=? AND client_id=?`, digest(token), clientID); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		var family string
		err = tx.QueryRowContext(r.Context(), `SELECT family_id FROM oauth_refresh_tokens WHERE token_hash=? AND client_id=?`, digest(token), clientID).Scan(&family)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			oauthError(w, 500, "server_error")
			return
		}
		if err == nil {
			if err := revokeFamily(r.Context(), tx, family); err != nil {
				oauthError(w, 500, "server_error")
				return
			}
		}
		if err := tx.Commit(); err != nil {
			oauthError(w, 500, "server_error")
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (a *app) userinfo(w http.ResponseWriter, r *http.Request) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		oauthError(w, 401, "invalid_token")
		return
	}
	userID, clientID, _, _, ok := a.accessToken(r.Context(), parts[1])
	if !ok || clientID == mcpPublicClientID || clientID == mcpBackendClientID {
		oauthError(w, 401, "invalid_token")
		return
	}
	var email, displayName string
	var deleted bool
	err := a.db.QueryRowContext(r.Context(), `SELECT email,COALESCE(display_name,''),is_deleted FROM users WHERE id=?`, userID).Scan(&email, &displayName, &deleted)
	if err != nil || deleted {
		oauthError(w, 401, "invalid_token")
		return
	}
	jsonResponse(w, 200, map[string]string{"sub": strconv.FormatUint(userID, 10), "email": email, "name": displayName})
}

func (a *app) accessToken(ctx context.Context, token string) (uint64, string, string, time.Time, bool) {
	if token == "" {
		return 0, "", "", time.Time{}, false
	}
	var id uint64
	var clientID, scope string
	var expires time.Time
	jst := time.Now().In(time.FixedZone("JST", 9*3600))
	err := a.db.QueryRowContext(ctx, `SELECT t.user_id,t.client_id,t.scope_value,t.expires_at FROM oauth_access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND t.revoked_at IS NULL AND t.expires_at>UTC_TIMESTAMP() AND u.is_deleted=0 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id=u.id AND (b.expires_at IS NULL OR b.expires_at>?))`, digest(token), jst).Scan(&id, &clientID, &scope, &expires)
	return id, clientID, scope, expires, err == nil
}

func (a *app) subjectActive(ctx context.Context, id uint64) bool {
	var active uint64
	jst := time.Now().In(time.FixedZone("JST", 9*3600))
	err := a.db.QueryRowContext(ctx, `SELECT u.id FROM users u WHERE u.id=? AND u.is_deleted=0 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id=u.id AND (b.expires_at IS NULL OR b.expires_at>?))`, id, jst).Scan(&active)
	return err == nil && active == id
}

func (a *app) sessionUser(r *http.Request) (uint64, bool) {
	return a.sessionUserWithFreshness(r, false)
}

func (a *app) recentSessionUser(r *http.Request) (uint64, bool) {
	return a.sessionUserWithFreshness(r, true)
}

func (a *app) sessionUserWithFreshness(r *http.Request, recent bool) (uint64, bool) {
	cookie, err := r.Cookie(a.sessionCookieName())
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	jst := time.Now().In(time.FixedZone("JST", 9*3600))
	query := `SELECT s.user_id FROM auth_sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>UTC_TIMESTAMP() AND u.is_deleted=0 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id=u.id AND (b.expires_at IS NULL OR b.expires_at>?))`
	if recent {
		query += ` AND s.created_at>UTC_TIMESTAMP()-INTERVAL 10 MINUTE`
	}
	id, err := scanUserID(a.db.QueryRowContext(r.Context(), query, digest(cookie.Value), jst))
	return id, err == nil
}

func redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, issuer string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	q := u.Query()
	q.Set("error", code)
	q.Set("state", state)
	q.Set("iss", issuer)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}
