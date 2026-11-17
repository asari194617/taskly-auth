package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type mcpAuthorization struct {
	clientID, redirectURI, state, challenge, scope, resource string
	expires                                                  time.Time
	userID                                                   sql.NullInt64
	csrf                                                     sql.NullString
}

func (a *app) loadMCPAuthorization(ctx context.Context, id string, tx *sql.Tx) (mcpAuthorization, error) {
	var req mcpAuthorization
	query := `SELECT client_id,redirect_uri,state_value,code_challenge,scope_value,resource_uri,expires_at,consent_user_id,consent_csrf_hash FROM oauth_authorization_requests WHERE id_hash=?`
	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, query+` FOR UPDATE`, digest(id))
	} else {
		row = a.db.QueryRowContext(ctx, query, digest(id))
	}
	err := row.Scan(&req.clientID, &req.redirectURI, &req.state, &req.challenge, &req.scope, &req.resource, &req.expires, &req.userID, &req.csrf)
	return req, err
}

func (a *app) validMCPAuthorization(req mcpAuthorization) bool {
	scopes, ok := mcpScopes(req.scope)
	return a.mcp != nil && req.clientID == mcpPublicClientID && req.redirectURI == a.mcp.redirectURI && req.resource == a.mcp.resource && ok && scopes == req.scope && req.expires.After(time.Now().UTC())
}

func (a *app) authorizeMCP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope, ok := mcpScopes(q.Get("scope"))
	if a.mcp == nil || !ok || !singleResource(q, a.mcp.resource) || q.Get("redirect_uri") != a.mcp.redirectURI || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !challengePattern.MatchString(q.Get("code_challenge")) || len(q.Get("state")) < 16 || len(q.Get("state")) > 1024 {
		oauthError(w, 400, "invalid_request")
		return
	}
	id, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	if !a.allowAuthorizationRequest(w, r) {
		return
	}
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO oauth_authorization_requests (id_hash,client_id,redirect_uri,state_value,code_challenge,scope_value,resource_uri,expires_at) VALUES (?,?,?,?,?,?,?,?)`, digest(id), mcpPublicClientID, a.mcp.redirectURI, q.Get("state"), q.Get("code_challenge"), scope, a.mcp.resource, time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	a.resumeMCPAuthorization(w, r, id)
}

func (a *app) resumeMCPAuthorization(w http.ResponseWriter, r *http.Request, id string) {
	if a.mcp == nil || !verifierPattern.MatchString(id) {
		oauthError(w, 400, "invalid_request")
		return
	}
	uid, ok := a.sessionUser(r)
	if !ok {
		http.Redirect(w, r, a.uiURL+"/login?request_id="+url.QueryEscape(id), http.StatusFound)
		return
	}
	req, err := a.loadMCPAuthorization(r.Context(), id, nil)
	if err != nil || !a.validMCPAuthorization(req) || (req.userID.Valid && uint64(req.userID.Int64) != uid) {
		oauthError(w, 400, "invalid_request")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE oauth_authorization_requests SET consent_user_id=? WHERE id_hash=? AND expires_at>UTC_TIMESTAMP() AND (consent_user_id IS NULL OR consent_user_id=?)`, uid, digest(id), uid)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	// MySQL reports zero changed rows for the same bound user; validity above
	// and consent's locked, live lookup remain authoritative in that case.
	if _, err = result.RowsAffected(); err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	http.Redirect(w, r, a.uiURL+"/consent?request_id="+url.QueryEscape(id), http.StatusFound)
}

func (a *app) consent(w http.ResponseWriter, r *http.Request) {
	if a.mcp == nil {
		oauthError(w, 404, "not_found")
		return
	}
	if r.Header.Get("Origin") != a.uiURL || r.Header.Get("X-Auth-UI") != "1" {
		oauthError(w, 403, "invalid_origin")
		return
	}
	id := r.URL.Query().Get("request_id")
	if !verifierPattern.MatchString(id) {
		oauthError(w, 400, "invalid_request")
		return
	}
	uid, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	req, err := a.loadMCPAuthorization(r.Context(), id, nil)
	if err != nil || !a.validMCPAuthorization(req) || !req.userID.Valid || uint64(req.userID.Int64) != uid {
		oauthError(w, 400, "invalid_request")
		return
	}
	csrf, err := randomToken()
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	result, err := a.db.ExecContext(r.Context(), `UPDATE oauth_authorization_requests SET consent_csrf_hash=? WHERE id_hash=? AND consent_user_id=? AND expires_at>UTC_TIMESTAMP()`, digest(csrf), digest(id), uid)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		oauthError(w, 400, "invalid_request")
		return
	}
	jsonResponse(w, 200, map[string]any{"client_name": "ChatGPT · Taskly企業ガイダンス", "redirect_uri": req.redirectURI, "resource": req.resource, "scopes": strings.Fields(req.scope), "csrf_token": csrf})
}

func (a *app) approveConsent(w http.ResponseWriter, r *http.Request) {
	if a.mcp == nil {
		oauthError(w, 404, "not_found")
		return
	}
	if r.Header.Get("Origin") != a.uiURL || r.Header.Get("X-Auth-UI") != "1" {
		oauthError(w, 403, "invalid_origin")
		return
	}
	var body struct {
		RequestID string `json:"request_id"`
		CSRFToken string `json:"csrf_token"`
		Approve   *bool  `json:"approve"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Approve == nil || !verifierPattern.MatchString(body.RequestID) || !verifierPattern.MatchString(body.CSRFToken) {
		oauthError(w, 400, "invalid_request")
		return
	}
	uid, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, 401, "login_required")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	defer tx.Rollback()
	req, err := a.loadMCPAuthorization(r.Context(), body.RequestID, tx)
	if err != nil || !a.validMCPAuthorization(req) || !req.userID.Valid || uint64(req.userID.Int64) != uid || !req.csrf.Valid || !equal(req.csrf.String, digest(body.CSRFToken)) {
		oauthError(w, 400, "invalid_request")
		return
	}
	if !a.subjectActive(r.Context(), uid) {
		oauthError(w, 401, "login_required")
		return
	}
	result, err := tx.ExecContext(r.Context(), `DELETE FROM oauth_authorization_requests WHERE id_hash=?`, digest(body.RequestID))
	if err != nil {
		oauthError(w, 500, "server_error")
		return
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		oauthError(w, 400, "invalid_request")
		return
	}
	code, failure := "", "access_denied"
	if *body.Approve {
		code, err = randomToken()
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO oauth_codes (code_hash,user_id,client_id,redirect_uri,code_challenge,scope_value,resource_uri,expires_at) VALUES (?,?,?,?,?,?,?,?)`, digest(code), uid, mcpPublicClientID, req.redirectURI, req.challenge, req.scope, req.resource, time.Now().UTC().Add(2*time.Minute))
		if err != nil {
			oauthError(w, 500, "server_error")
			return
		}
		failure = ""
	}
	if tx.Commit() != nil {
		oauthError(w, 500, "server_error")
		return
	}
	jsonResponse(w, 200, map[string]string{"redirect_uri": mcpConsentRedirect(req.redirectURI, req.state, a.issuer, code, failure)})
}
