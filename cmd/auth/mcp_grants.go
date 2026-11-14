package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"
)

func (a *app) storeMCPTokenSet(ctx context.Context, tx *sql.Tx, uid uint64, scope, resource, access, refresh, family string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO oauth_access_tokens (token_hash,family_id,user_id,client_id,scope_value,resource_uri,expires_at) VALUES (?,?,?,?,?,?,?)`, digest(access), family, uid, mcpPublicClientID, scope, resource, time.Now().UTC().Add(15*time.Minute))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens (token_hash,family_id,user_id,client_id,scope_value,resource_uri,expires_at) VALUES (?,?,?,?,?,?,?)`, digest(refresh), family, uid, mcpPublicClientID, scope, resource, time.Now().UTC().Add(30*24*time.Hour))
	return err
}

func (a *app) exchangeMCPCode(w http.ResponseWriter, r *http.Request) {
	if a.mcp == nil || !singleResource(r.Form, a.mcp.resource) || r.Form.Get("redirect_uri") != a.mcp.redirectURI || !verifierPattern.MatchString(r.Form.Get("code_verifier")) || !verifierPattern.MatchString(r.Form.Get("code")) {
		oauthError(w, 400, "invalid_request")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	var uid uint64
	var client, redirect, challenge, scope, resource string
	var expires time.Time
	var used sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,client_id,redirect_uri,code_challenge,scope_value,resource_uri,expires_at,used_at FROM oauth_codes WHERE code_hash=? FOR UPDATE`, digest(r.Form.Get("code"))).Scan(&uid, &client, &redirect, &challenge, &scope, &resource, &expires, &used)
	canonical, valid := mcpScopes(scope)
	h := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if err != nil || used.Valid || !expires.After(time.Now().UTC()) || client != mcpPublicClientID || redirect != a.mcp.redirectURI || resource != a.mcp.resource || !valid || canonical != scope || !equal(base64.RawURLEncoding.EncodeToString(h[:]), challenge) || !a.subjectActive(r.Context(), uid) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE oauth_codes SET used_at=UTC_TIMESTAMP() WHERE code_hash=?`, digest(r.Form.Get("code"))); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	access, refresh, family, err := newTokenSet()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if a.storeMCPTokenSet(r.Context(), tx, uid, scope, resource, access, refresh, family) != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	writeTokens(w, access, refresh, scope)
}

func (a *app) refreshMCP(w http.ResponseWriter, r *http.Request) {
	if a.mcp == nil || !singleResource(r.Form, a.mcp.resource) || !verifierPattern.MatchString(r.Form.Get("refresh_token")) {
		oauthError(w, 400, "invalid_request")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	var uid uint64
	var client, scope, resource, family string
	var expires time.Time
	var used, revoked sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT user_id,client_id,scope_value,resource_uri,family_id,expires_at,used_at,revoked_at FROM oauth_refresh_tokens WHERE token_hash=? FOR UPDATE`, digest(r.Form.Get("refresh_token"))).Scan(&uid, &client, &scope, &resource, &family, &expires, &used, &revoked)
	canonical, valid := mcpScopes(scope)
	if err != nil || client != mcpPublicClientID || resource != a.mcp.resource || !valid || canonical != scope || revoked.Valid || !expires.After(time.Now().UTC()) || !a.subjectActive(r.Context(), uid) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	if used.Valid {
		if revokeFamily(r.Context(), tx, family) != nil || tx.Commit() != nil {
			oauthError(w, 500, "server_error")
			return
		}
		oauthError(w, 400, "invalid_grant")
		return
	}
	if requested := r.Form.Get("scope"); requested != "" {
		reduced, ok := mcpScopes(requested)
		if !ok {
			oauthError(w, 400, "invalid_scope")
			return
		}
		for _, candidate := range []string{"guidance:read", "guidance:bookmarks:write", "guidance:tasks:write"} {
			if hasMCPScope(reduced, candidate) && !hasMCPScope(scope, candidate) {
				oauthError(w, 400, "invalid_scope")
				return
			}
		}
		scope = reduced
	}
	if _, err = tx.ExecContext(r.Context(), `UPDATE oauth_refresh_tokens SET used_at=UTC_TIMESTAMP() WHERE token_hash=?`, digest(r.Form.Get("refresh_token"))); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	access, refresh, _, err := newTokenSet()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if a.storeMCPTokenSet(r.Context(), tx, uid, scope, resource, access, refresh, family) != nil || tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	writeTokens(w, access, refresh, scope)
}

type mcpTokenDetails struct {
	uid                                         uint64
	client, scope, resource, family, parentHash string
	expires                                     time.Time
}

func (a *app) mcpToken(ctx context.Context, token string) (mcpTokenDetails, bool) {
	var d mcpTokenDetails
	var parent sql.NullString
	jst := time.Now().In(time.FixedZone("JST", 9*3600))
	err := a.db.QueryRowContext(ctx, `SELECT t.user_id,t.client_id,t.scope_value,t.resource_uri,t.family_id,t.parent_token_hash,t.expires_at FROM oauth_access_tokens t JOIN users u ON u.id=t.user_id WHERE t.token_hash=? AND t.revoked_at IS NULL AND t.expires_at>UTC_TIMESTAMP() AND u.is_deleted=0 AND NOT EXISTS (SELECT 1 FROM blocks b WHERE b.user_id=u.id AND (b.expires_at IS NULL OR b.expires_at>?))`, digest(token), jst).Scan(&d.uid, &d.client, &d.scope, &d.resource, &d.family, &parent, &d.expires)
	d.parentHash = parent.String
	return d, err == nil
}

func (a *app) exchangeMCPToken(w http.ResponseWriter, r *http.Request) {
	if !a.authenticateMCPBackend(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="taskly-auth"`)
		oauthError(w, 401, "invalid_client")
		return
	}
	if !singleResource(r.Form, a.mcp.tasklyResource) || r.Form.Get("audience") != "" || r.Form.Get("subject_token_type") != mcpAccessTokenType || (r.Form.Get("requested_token_type") != "" && r.Form.Get("requested_token_type") != mcpAccessTokenType) || r.Form.Get("actor_token") != "" || r.Form.Get("actor_token_type") != "" {
		oauthError(w, 400, "invalid_target")
		return
	}
	scope := r.Form.Get("scope")
	if scope != "guidance:tasks:read" && scope != "guidance:tasks:write" {
		oauthError(w, 400, "invalid_scope")
		return
	}
	if !verifierPattern.MatchString(r.Form.Get("subject_token")) {
		oauthError(w, 400, "invalid_request")
		return
	}
	parent, ok := a.mcpToken(r.Context(), r.Form.Get("subject_token"))
	canonical, valid := mcpScopes(parent.scope)
	if !ok || parent.client != mcpPublicClientID || parent.resource != a.mcp.resource || parent.parentHash != "" || !valid || parent.scope != canonical || (scope == "guidance:tasks:write" && !hasMCPScope(parent.scope, "guidance:tasks:write")) {
		oauthError(w, 400, "invalid_grant")
		return
	}
	expires := mcpExchangeExpiry(time.Now(), parent.expires)
	seconds := int64(time.Until(expires) / time.Second)
	if seconds < 1 {
		oauthError(w, 400, "invalid_grant")
		return
	}
	token, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO oauth_access_tokens (token_hash,family_id,user_id,client_id,scope_value,resource_uri,parent_token_hash,expires_at) VALUES (?,?,?,?,?,?,?,?)`, digest(token), parent.family, parent.uid, mcpBackendClientID, scope, a.mcp.tasklyResource, digest(r.Form.Get("subject_token")), expires)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	jsonResponse(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "issued_token_type": mcpAccessTokenType, "expires_in": seconds, "scope": scope})
}

func mcpExchangeExpiry(now, parentExpiry time.Time) time.Time {
	expires := now.UTC().Add(time.Minute)
	if parentExpiry.Before(expires) {
		expires = parentExpiry
	}
	// DATETIME(0) rounds fractional seconds on insert. Floor before storage so
	// the persisted/introspected deadline never exceeds either lifetime bound.
	return expires.UTC().Truncate(time.Second)
}

func (a *app) introspectWithMCP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthError(w, 400, "invalid_request")
		return
	}
	service := a.authenticateMCPBackend(r)
	if !service && !a.authenticateClient(w, r) {
		return
	}
	uid, client, scope, expires, ok := a.accessToken(r.Context(), r.Form.Get("token"))
	if !ok {
		jsonResponse(w, 200, map[string]bool{"active": false})
		return
	}
	claims := map[string]any{"active": true, "client_id": client, "sub": strconv.FormatUint(uid, 10), "scope": scope, "aud": "taskly-api", "iss": a.issuer, "exp": expires.Unix(), "token_type": "Bearer"}
	if client != mcpPublicClientID && client != mcpBackendClientID {
		if service {
			jsonResponse(w, 200, map[string]bool{"active": false})
			return
		}
		jsonResponse(w, 200, claims)
		return
	}
	d, live := a.mcpToken(r.Context(), r.Form.Get("token"))
	if !live {
		jsonResponse(w, 200, map[string]bool{"active": false})
		return
	}
	if client == mcpPublicClientID {
		canonical, valid := mcpScopes(scope)
		if !service || d.resource != a.mcp.resource || d.parentHash != "" || !valid || canonical != scope {
			jsonResponse(w, 200, map[string]bool{"active": false})
			return
		}
	} else {
		if service || d.resource != a.mcp.tasklyResource || (scope != "guidance:tasks:read" && scope != "guidance:tasks:write") {
			jsonResponse(w, 200, map[string]bool{"active": false})
			return
		}
		var parent mcpTokenDetails
		var revoked sql.NullTime
		err := a.db.QueryRowContext(r.Context(), `SELECT user_id,client_id,scope_value,resource_uri,family_id,expires_at,revoked_at FROM oauth_access_tokens WHERE token_hash=?`, d.parentHash).Scan(&parent.uid, &parent.client, &parent.scope, &parent.resource, &parent.family, &parent.expires, &revoked)
		canonical, valid := mcpScopes(parent.scope)
		if err != nil || revoked.Valid || !parent.expires.After(time.Now().UTC()) || parent.uid != uid || parent.client != mcpPublicClientID || parent.resource != a.mcp.resource || parent.family != d.family || !valid || canonical != parent.scope || (scope == "guidance:tasks:write" && !hasMCPScope(parent.scope, "guidance:tasks:write")) || expires.After(parent.expires) {
			jsonResponse(w, 200, map[string]bool{"active": false})
			return
		}
		claims["act"] = map[string]string{"sub": mcpBackendClientID}
		claims["original_client_id"] = parent.client
		claims["original_resource"] = parent.resource
	}
	claims["aud"] = d.resource
	jsonResponse(w, 200, claims)
}
