package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	mcpTestPublicClient   = "nnct-guidance-chatgpt"
	mcpTestBackendClient  = "nnct-guidance-mcp-backend"
	mcpTestResource       = "https://guidance.example.test/mcp"
	mcpTestTasklyResource = "https://taskly-api.example.test/integrations/nnct-guidance/tasks"
	mcpTestRedirect       = "https://chatgpt.com/connector/oauth/test-guidance-callback"
	mcpTestBackendSecret  = "synthetic-mcp-backend-secret-for-tests-only"
)

// The scripted driver checks resource and subject boundaries without requiring
// an external database in the ordinary unit-test job. MySQL behavior is tested
// separately by the opt-in integration flow.
type mcpSQLStep struct {
	query    string
	columns  []string
	values   [][]driver.Value
	check    func([]driver.NamedValue)
	err      error
	affected int64
}

type mcpSQLConnector struct {
	t         *testing.T
	steps     []mcpSQLStep
	beginErr  error
	commitErr error
}

func (c *mcpSQLConnector) Connect(context.Context) (driver.Conn, error) { return &mcpSQLConn{c}, nil }
func (c *mcpSQLConnector) Driver() driver.Driver                        { return mcpSQLDriver{} }

type mcpSQLDriver struct{}

func (mcpSQLDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type mcpSQLConn struct{ connector *mcpSQLConnector }

func (*mcpSQLConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (*mcpSQLConn) Close() error                        { return nil }
func (c *mcpSQLConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *mcpSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &mcpSQLTx{commitErr: c.connector.commitErr}, c.connector.beginErr
}

type mcpSQLTx struct{ commitErr error }

func (tx *mcpSQLTx) Commit() error { return tx.commitErr }
func (*mcpSQLTx) Rollback() error  { return nil }

func (c *mcpSQLConn) take(query string, args []driver.NamedValue) (mcpSQLStep, error) {
	if len(c.connector.steps) == 0 {
		c.connector.t.Errorf("unexpected SQL operation: %s", query)
		return mcpSQLStep{}, errors.New("unexpected SQL operation")
	}
	step := c.connector.steps[0]
	c.connector.steps = c.connector.steps[1:]
	if !strings.Contains(query, step.query) {
		c.connector.t.Errorf("SQL operation = %q, want substring %q", query, step.query)
		return step, errors.New("SQL operation mismatch")
	}
	if step.check != nil {
		step.check(args)
	}
	return step, step.err
}
func (c *mcpSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step, err := c.take(query, args)
	return driver.RowsAffected(step.affected), err
}
func (c *mcpSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.take(query, args)
	if err != nil {
		return nil, err
	}
	return &mcpSQLRows{columns: step.columns, values: step.values}, nil
}

type mcpSQLRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *mcpSQLRows) Columns() []string { return r.columns }
func (*mcpSQLRows) Close() error        { return nil }
func (r *mcpSQLRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
func mcpScriptDB(t *testing.T, steps ...mcpSQLStep) *sql.DB {
	t.Helper()
	c := &mcpSQLConnector{t: t, steps: steps}
	return mcpScriptConnectorDB(t, c)
}
func mcpScriptConnectorDB(t *testing.T, c *mcpSQLConnector) *sql.DB {
	t.Helper()
	db := sql.OpenDB(c)
	t.Cleanup(func() {
		_ = db.Close()
		if len(c.steps) > 0 {
			t.Errorf("%d expected SQL operations did not execute", len(c.steps))
		}
	})
	return db
}

func mcpTestApp() *app {
	return &app{
		issuer: "https://auth.example.test", uiURL: "https://login.example.test", clientID: "taskly-web", clientSecret: "synthetic-legacy-client-secret", redirectURI: "https://taskly.example.test/callback", ready: true, alerts: &alerter{},
		mcp: &mcpOAuthConfig{redirectURI: mcpTestRedirect, resource: mcpTestResource, tasklyResource: mcpTestTasklyResource, backendSecret: mcpTestBackendSecret},
	}
}

func mcpFormRequest(form url.Values, basic bool) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if basic {
		r.SetBasicAuth(mcpTestBackendClient, mcpTestBackendSecret)
	}
	return r
}

func mcpDecodeJSON(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("response %d was not JSON: %v", w.Code, err)
	}
	return result
}

func TestMCPStaticClientRequiresExactRegisteredRedirect(t *testing.T) {
	a := mcpTestApp()
	for _, test := range []struct {
		client, redirect string
		want             bool
	}{
		{mcpTestPublicClient, mcpTestRedirect, true},
		{mcpTestPublicClient, mcpTestRedirect + "/extra", false},
		{mcpTestPublicClient, "https://chatgpt.com/connector/oauth/other", false},
		{mcpTestBackendClient, mcpTestRedirect, false},
		{"unknown", mcpTestRedirect, false},
	} {
		if got := a.registeredRedirect(test.client, test.redirect); got != test.want {
			t.Errorf("client=%q redirect=%q accepted=%v want=%v", test.client, test.redirect, got, test.want)
		}
	}
}

func TestMCPMetadataDoesNotAdvertiseUnimplementedRegistration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		a := mcpTestApp()
		if !enabled {
			a.mcp = nil
		}
		w := httptest.NewRecorder()
		a.metadata(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil))
		body := mcpDecodeJSON(t, w)
		if w.Code != http.StatusOK {
			t.Fatalf("metadata status=%d", w.Code)
		}
		if _, ok := body["registration_endpoint"]; ok {
			t.Error("advertised unimplemented dynamic registration")
		}
		if supported, _ := body["client_id_metadata_document_supported"].(bool); supported {
			t.Error("advertised unimplemented client metadata documents")
		}
		if !reflect.DeepEqual(body["code_challenge_methods_supported"], []any{"S256"}) {
			t.Error("metadata lost S256 PKCE")
		}
		if enabled {
			methods, _ := body["token_endpoint_auth_methods_supported"].([]any)
			found := false
			for _, method := range methods {
				if method == "none" {
					found = true
				}
			}
			if !found {
				t.Error("metadata did not advertise public-client token authentication")
			}
		}
	}
}

func TestMCPPublicClientTokenAuthentication(t *testing.T) {
	a := mcpTestApp()
	for _, test := range []struct {
		name, auth, secret string
		want               bool
	}{
		{"public PKCE", "", "", true},
		{"body secret", "", "unexpected", false},
		{"bearer credential", "Bearer unrelated", "", false},
		{"Basic public client", "Basic bm5jdC1ndWlkYW5jZS1jaGF0Z3B0OnVudHJ1c3RlZA==", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"client_id": {mcpTestPublicClient}}
			if test.secret != "" {
				form.Set("client_secret", test.secret)
			}
			r := mcpFormRequest(form, false)
			if test.auth != "" {
				r.Header.Set("Authorization", test.auth)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			id, ok := a.authenticateTokenClient(w, r)
			if ok != test.want || (ok && id != mcpTestPublicClient) {
				t.Fatalf("authenticated=%v id=%q want=%v", ok, id, test.want)
			}
		})
	}
}

func TestMCPServiceCannotUseBrowserOAuthGrants(t *testing.T) {
	for _, grant := range []string{"authorization_code", "refresh_token", "client_credentials"} {
		t.Run(grant, func(t *testing.T) {
			a := mcpTestApp()
			w := httptest.NewRecorder()
			a.token(w, mcpFormRequest(url.Values{"grant_type": {grant}, "client_id": {mcpTestBackendClient}, "resource": {mcpTestResource}}, true))
			if w.Code < 400 || w.Code >= 500 {
				t.Fatalf("service grant %q status=%d body=%s", grant, w.Code, w.Body.String())
			}
		})
	}
}

func TestMCPAuthorizeRejectsResourceAndScopeEscalation(t *testing.T) {
	for _, test := range []struct{ name, client, scope, resource, challengeMethod string }{
		{"missing resource", mcpTestPublicClient, "guidance:read", "", "S256"},
		{"foreign resource", mcpTestPublicClient, "guidance:read", mcpTestTasklyResource, "S256"},
		{"legacy scope", mcpTestPublicClient, "taskly:login", mcpTestResource, "S256"},
		{"unknown scope", mcpTestPublicClient, "guidance:read guidance:admin", mcpTestResource, "S256"},
		{"write without read", mcpTestPublicClient, "guidance:tasks:write", mcpTestResource, "S256"},
		{"plain PKCE", mcpTestPublicClient, "guidance:read", mcpTestResource, "plain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := mcpTestApp()
			q := url.Values{"client_id": {test.client}, "redirect_uri": {mcpTestRedirect}, "response_type": {"code"}, "code_challenge": {strings.Repeat("a", 43)}, "code_challenge_method": {test.challengeMethod}, "state": {strings.Repeat("s", 32)}, "scope": {test.scope}}
			if test.resource != "" {
				q.Set("resource", test.resource)
			}
			w := httptest.NewRecorder()
			a.authorize(w, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("authorization status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMCPOAuthConfigurationIsExplicitAndResourceBound(t *testing.T) {
	if got, err := parseMCPOAuthConfig("0", "", "", "", "", true); err != nil || got != nil {
		t.Fatalf("disabled config=%v err=%v", got, err)
	}
	if got, err := parseMCPOAuthConfig("1", mcpTestRedirect, mcpTestResource, mcpTestTasklyResource, mcpTestBackendSecret, true); err != nil || got == nil {
		t.Fatalf("valid config=%v err=%v", got, err)
	}
	for _, test := range []struct{ name, enabled, redirect, resource, downstream, secret string }{
		{"unknown gate", "yes", mcpTestRedirect, mcpTestResource, mcpTestTasklyResource, mcpTestBackendSecret},
		{"missing redirect", "1", "", mcpTestResource, mcpTestTasklyResource, mcpTestBackendSecret},
		{"missing resource", "1", mcpTestRedirect, "", mcpTestTasklyResource, mcpTestBackendSecret},
		{"missing downstream", "1", mcpTestRedirect, mcpTestResource, "", mcpTestBackendSecret},
		{"missing secret", "1", mcpTestRedirect, mcpTestResource, mcpTestTasklyResource, ""},
		{"weak secret", "1", mcpTestRedirect, mcpTestResource, mcpTestTasklyResource, "weak"},
		{"HTTP redirect", "1", "http://chatgpt.com/callback", mcpTestResource, mcpTestTasklyResource, mcpTestBackendSecret},
		{"fragment redirect", "1", mcpTestRedirect + "#fragment", mcpTestResource, mcpTestTasklyResource, mcpTestBackendSecret},
		{"credentialed resource", "1", mcpTestRedirect, "https://user:secret@guidance.example.test/mcp", mcpTestTasklyResource, mcpTestBackendSecret},
		{"HTTP resource", "1", mcpTestRedirect, "http://guidance.example.test/mcp", mcpTestTasklyResource, mcpTestBackendSecret},
		{"fragment resource", "1", mcpTestRedirect, mcpTestResource + "#fragment", mcpTestTasklyResource, mcpTestBackendSecret},
		{"query resource", "1", mcpTestRedirect, mcpTestResource + "?target=other", mcpTestTasklyResource, mcpTestBackendSecret},
		{"same resources", "1", mcpTestRedirect, mcpTestResource, mcpTestResource, mcpTestBackendSecret},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseMCPOAuthConfig(test.enabled, test.redirect, test.resource, test.downstream, test.secret, true); err == nil {
				t.Fatal("accepted invalid OAuth configuration")
			}
		})
	}
}

func TestMCPExchangeRejectsInvalidTargetAndPrivilegeBeforeLookup(t *testing.T) {
	valid := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {"opaque-parent"}, "subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "requested_token_type": {"urn:ietf:params:oauth:token-type:access_token"}, "resource": {mcpTestTasklyResource}, "scope": {"guidance:tasks:read"}}
	for _, test := range []struct{ name, key, value string }{
		{"missing subject", "subject_token", ""},
		{"refresh subject", "subject_token_type", "urn:ietf:params:oauth:token-type:refresh_token"},
		{"JWT output", "requested_token_type", "urn:ietf:params:oauth:token-type:jwt"},
		{"missing target", "resource", ""},
		{"wrong target", "resource", mcpTestResource},
		{"scope escalation", "scope", "taskly:login"},
		{"multiple scopes", "scope", "guidance:tasks:read guidance:tasks:write"},
		{"missing scope", "scope", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{}
			for key, values := range valid {
				form[key] = append([]string(nil), values...)
			}
			form.Set(test.key, test.value)
			a := mcpTestApp()
			a.db = mcpScriptDB(t)
			w := httptest.NewRecorder()
			a.token(w, mcpFormRequest(form, true))
			if w.Code < 400 || w.Code >= 500 {
				t.Fatalf("invalid exchange status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func mcpRow(query string, values ...driver.Value) mcpSQLStep {
	columns := make([]string, len(values))
	for i := range columns {
		columns[i] = "column"
	}
	return mcpSQLStep{query: query, columns: columns, values: [][]driver.Value{values}}
}
func mcpActiveSubjectStep() mcpSQLStep { return mcpRow("SELECT u.id FROM users", int64(7)) }
func mcpSessionStep() mcpSQLStep       { return mcpRow("FROM auth_sessions s JOIN users", int64(7)) }
func mcpAuthorizationStep(scope string) mcpSQLStep {
	return mcpRow("FROM oauth_authorization_requests WHERE id_hash=", mcpTestPublicClient, mcpTestRedirect, "state-value-with-entropy", strings.Repeat("c", 43), scope, mcpTestResource, time.Now().UTC().Add(time.Minute), int64(7), digest(strings.Repeat("x", 43)))
}

func TestMCPScopesAreCanonicalAndLeastPrivilege(t *testing.T) {
	for _, test := range []struct {
		input, want string
		ok          bool
	}{
		{"guidance:read", "guidance:read", true},
		{"guidance:read guidance:tasks:write guidance:bookmarks:write", "guidance:bookmarks:write guidance:read guidance:tasks:write", true},
		{"  guidance:read\tguidance:tasks:write  ", "guidance:read guidance:tasks:write", true},
		{"", "", false}, {"guidance:tasks:write", "", false}, {"taskly:login", "", false}, {"guidance:read guidance:read", "", false}, {"guidance:read admin", "", false},
	} {
		got, ok := mcpScopes(test.input)
		if got != test.want || ok != test.ok {
			t.Errorf("scopes=%q got=%q,%v want=%q,%v", test.input, got, ok, test.want, test.ok)
		}
	}
	if !hasMCPScope("guidance:read guidance:tasks:write", "guidance:read") || hasMCPScope("guidance:read", "guidance:tasks:write") {
		t.Fatal("scope comparison granted unrelated authority")
	}
	for _, resources := range [][]string{nil, {mcpTestResource, mcpTestResource}, {mcpTestTasklyResource}} {
		if singleResource(url.Values{"resource": resources}, mcpTestResource) {
			t.Fatal("accepted missing, duplicated, or foreign resource")
		}
	}
}

func TestMCPBackendAuthenticationIsConfidentialAndDedicated(t *testing.T) {
	for _, test := range []struct {
		name, id, secret, formID, bodySecret string
		enabled, want                        bool
	}{
		{"valid", mcpTestBackendClient, mcpTestBackendSecret, "", "", true, true},
		{"valid explicit client", mcpTestBackendClient, mcpTestBackendSecret, mcpTestBackendClient, "", true, true},
		{"disabled", mcpTestBackendClient, mcpTestBackendSecret, "", "", false, false},
		{"wrong secret", mcpTestBackendClient, "wrong", "", "", true, false},
		{"public client", mcpTestPublicClient, mcpTestBackendSecret, "", "", true, false},
		{"conflicting client", mcpTestBackendClient, mcpTestBackendSecret, mcpTestPublicClient, "", true, false},
		{"secret in body", mcpTestBackendClient, mcpTestBackendSecret, "", "unexpected", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := mcpTestApp()
			if !test.enabled {
				a.mcp = nil
			}
			r := mcpFormRequest(url.Values{"client_id": {test.formID}, "client_secret": {test.bodySecret}}, false)
			r.SetBasicAuth(test.id, test.secret)
			_ = r.ParseForm()
			if got := a.authenticateMCPBackend(r); got != test.want {
				t.Fatalf("authenticated=%v want=%v", got, test.want)
			}
		})
	}
}

func TestMCPConsentRequiresUIOriginAndStrictRequest(t *testing.T) {
	for _, test := range []struct {
		name, method, body, origin, header string
		disabled                           bool
		want                               int
	}{
		{"disabled GET", "GET", "", "https://login.example.test", "1", true, 404},
		{"disabled POST", "POST", "{}", "https://login.example.test", "1", true, 404},
		{"missing origin", "GET", "", "", "1", false, 403},
		{"foreign origin", "POST", "{}", "https://attacker.example.test", "1", false, 403},
		{"missing UI marker", "GET", "", "https://login.example.test", "", false, 403},
		{"bad request identifier", "GET", "", "https://login.example.test", "1", false, 400},
		{"malformed JSON", "POST", "{", "https://login.example.test", "1", false, 400},
		{"unknown JSON field", "POST", `{"unexpected":true}`, "https://login.example.test", "1", false, 400},
		{"missing approval", "POST", `{"request_id":"` + strings.Repeat("r", 43) + `","csrf_token":"` + strings.Repeat("x", 43) + `"}`, "https://login.example.test", "1", false, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := mcpTestApp()
			if test.disabled {
				a.mcp = nil
			}
			a.db = mcpScriptDB(t)
			r := httptest.NewRequest(test.method, "/oauth/consent?request_id=invalid", strings.NewReader(test.body))
			r.Header.Set("Origin", test.origin)
			r.Header.Set("X-Auth-UI", test.header)
			w := httptest.NewRecorder()
			if test.method == "GET" {
				a.consent(w, r)
			} else {
				a.approveConsent(w, r)
			}
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMCPConsentReadIsBoundToLiveRequest(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      func(*mcpSQLStep)
		updateError bool
		want        int
	}{
		{"valid", nil, false, 200},
		{"wrong bound user", func(s *mcpSQLStep) { s.values[0][7] = int64(8) }, false, 400},
		{"expired", func(s *mcpSQLStep) { s.values[0][6] = time.Now().Add(-time.Minute) }, false, 400},
		{"wrong client", func(s *mcpSQLStep) { s.values[0][0] = "unrelated-client" }, false, 400},
		{"wrong resource", func(s *mcpSQLStep) { s.values[0][5] = mcpTestTasklyResource }, false, 400},
		{"scope changed", func(s *mcpSQLStep) { s.values[0][4] = "taskly:login" }, false, 400},
		{"storage unavailable", nil, true, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			step := mcpAuthorizationStep("guidance:read")
			if test.mutate != nil {
				test.mutate(&step)
			}
			steps := []mcpSQLStep{mcpSessionStep(), step}
			if test.mutate == nil {
				update := mcpSQLStep{query: "UPDATE oauth_authorization_requests SET consent_csrf_hash=", affected: 1}
				if test.updateError {
					update.err = errors.New("storage unavailable")
				}
				steps = append(steps, update)
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			r := httptest.NewRequest("GET", "/oauth/consent?request_id="+strings.Repeat("r", 43), nil)
			r.Header.Set("Origin", a.uiURL)
			r.Header.Set("X-Auth-UI", "1")
			r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "session"})
			w := httptest.NewRecorder()
			a.consent(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				body := mcpDecodeJSON(t, w)
				if body["resource"] != mcpTestResource || body["csrf_token"] == "" {
					t.Fatal("consent did not identify granted resource or protect approval")
				}
			}
		})
	}
}

func mcpApproveRequest(approve bool) *http.Request {
	body, _ := json.Marshal(map[string]any{"request_id": strings.Repeat("r", 43), "csrf_token": strings.Repeat("x", 43), "approve": approve})
	r := httptest.NewRequest("POST", "/oauth/consent", strings.NewReader(string(body)))
	r.Header.Set("Origin", "https://login.example.test")
	r.Header.Set("X-Auth-UI", "1")
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "session"})
	return r
}

func TestMCPConsentApprovalCommitsExactlyOneGrant(t *testing.T) {
	for _, test := range []struct {
		name    string
		approve bool
		failAt  string
		want    int
	}{
		{"approve", true, "", 200}, {"deny", false, "", 200}, {"bad csrf", true, "csrf", 400}, {"inactive subject", true, "subject", 401}, {"already consumed", true, "delete-zero", 400}, {"delete unavailable", true, "delete", 500}, {"code storage unavailable", true, "insert", 500}, {"commit unavailable", true, "commit", 500}, {"transaction unavailable", true, "begin", 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := []mcpSQLStep{mcpSessionStep()}
			if test.failAt != "begin" {
				req := mcpAuthorizationStep("guidance:read")
				if test.failAt == "csrf" {
					req.values[0][8] = digest("different-csrf")
				}
				steps = append(steps, req)
				if test.failAt != "csrf" {
					subject := mcpActiveSubjectStep()
					if test.failAt == "subject" {
						subject.values = nil
					}
					steps = append(steps, subject)
					if test.failAt != "subject" {
						remove := mcpSQLStep{query: "DELETE FROM oauth_authorization_requests", affected: 1}
						if test.failAt == "delete" {
							remove.err = errors.New("unavailable")
						}
						if test.failAt == "delete-zero" {
							remove.affected = 0
						}
						steps = append(steps, remove)
						if test.failAt != "delete" && test.failAt != "delete-zero" && test.approve {
							insert := mcpSQLStep{query: "INSERT INTO oauth_codes", affected: 1}
							if test.failAt == "insert" {
								insert.err = errors.New("unavailable")
							}
							steps = append(steps, insert)
						}
					}
				}
			}
			connector := &mcpSQLConnector{t: t, steps: steps}
			if test.failAt == "begin" {
				connector.beginErr = errors.New("unavailable")
			}
			if test.failAt == "commit" {
				connector.commitErr = errors.New("unavailable")
			}
			a := mcpTestApp()
			a.db = mcpScriptConnectorDB(t, connector)
			w := httptest.NewRecorder()
			a.approveConsent(w, mcpApproveRequest(test.approve))
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				raw, _ := mcpDecodeJSON(t, w)["redirect_uri"].(string)
				callback, _ := url.Parse(raw)
				if test.approve {
					if callback.Query().Get("code") == "" {
						t.Fatal("approved grant omitted code")
					}
				} else if callback.Query().Get("error") != "access_denied" || callback.Query().Get("code") != "" {
					t.Fatal("denial granted a code")
				}
			}
		})
	}
}

func mcpTokenStep(scope string) mcpSQLStep {
	return mcpRow("t.parent_token_hash", int64(7), mcpTestPublicClient, scope, mcpTestResource, "family", nil, time.Now().UTC().Add(time.Minute))
}
func mcpExchangeForm() url.Values {
	return url.Values{"grant_type": {mcpExchangeGrant}, "subject_token": {strings.Repeat("p", 43)}, "subject_token_type": {mcpAccessTokenType}, "requested_token_type": {mcpAccessTokenType}, "resource": {mcpTestTasklyResource}, "scope": {"guidance:tasks:write"}}
}
func TestMCPExchangeValidatesParentAndRestrictsChild(t *testing.T) {
	for _, test := range []struct {
		name        string
		mutate      func(*mcpSQLStep)
		insertError bool
		want        int
	}{
		{"valid", nil, false, 200},
		{"read cannot write", func(s *mcpSQLStep) { s.values[0][2] = "guidance:read" }, false, 400},
		{"wrong parent client", func(s *mcpSQLStep) { s.values[0][1] = "taskly-web" }, false, 400},
		{"wrong parent resource", func(s *mcpSQLStep) { s.values[0][3] = mcpTestTasklyResource }, false, 400},
		{"already delegated", func(s *mcpSQLStep) { s.values[0][5] = "parent-of-parent" }, false, 400},
		{"noncanonical scope", func(s *mcpSQLStep) { s.values[0][2] = "guidance:tasks:write guidance:read" }, false, 400},
		{"too near expiry", func(s *mcpSQLStep) { s.values[0][6] = time.Now().UTC().Add(100 * time.Millisecond) }, false, 400},
		{"revoked or blocked", func(s *mcpSQLStep) { s.values = nil }, false, 400},
		{"lookup unavailable", func(s *mcpSQLStep) { s.err = errors.New("unavailable") }, false, 400},
		{"storage unavailable", nil, true, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := mcpTokenStep("guidance:read guidance:tasks:write")
			if test.mutate != nil {
				test.mutate(&parent)
			}
			steps := []mcpSQLStep{parent}
			if test.mutate == nil {
				insert := mcpSQLStep{query: "INSERT INTO oauth_access_tokens", affected: 1, check: func(args []driver.NamedValue) {
					if args[2].Value != int64(7) || args[3].Value != mcpTestBackendClient || args[4].Value != "guidance:tasks:write" || args[5].Value != mcpTestTasklyResource || args[6].Value != digest(strings.Repeat("p", 43)) {
						t.Error("delegated token lost subject, client, scope, resource, or parent binding")
					}
					expiry, ok := args[7].Value.(time.Time)
					if !ok || expiry.After(time.Now().Add(time.Minute)) {
						t.Error("delegated token exceeded sixty seconds")
					}
				}}
				if test.insertError {
					insert.err = errors.New("unavailable")
				}
				steps = append(steps, insert)
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			w := httptest.NewRecorder()
			a.token(w, mcpFormRequest(mcpExchangeForm(), true))
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				body := mcpDecodeJSON(t, w)
				if body["refresh_token"] != nil || body["issued_token_type"] != mcpAccessTokenType || body["token_type"] != "Bearer" {
					t.Fatal("exchange exposed refresh authority or wrong token type")
				}
			}
		})
	}
}

func mcpCodeStep() mcpSQLStep {
	hash := sha256.Sum256([]byte(strings.Repeat("v", 43)))
	return mcpRow("FROM oauth_codes WHERE code_hash=", int64(7), mcpTestPublicClient, mcpTestRedirect, base64.RawURLEncoding.EncodeToString(hash[:]), "guidance:read", mcpTestResource, time.Now().UTC().Add(time.Minute), nil)
}
func mcpCodeForm() url.Values {
	return url.Values{"client_id": {mcpTestPublicClient}, "grant_type": {"authorization_code"}, "redirect_uri": {mcpTestRedirect}, "code": {strings.Repeat("c", 43)}, "code_verifier": {strings.Repeat("v", 43)}, "resource": {mcpTestResource}}
}
func TestMCPCodeExchangeRequiresBoundUnusedGrantAndSuccessfulCommit(t *testing.T) {
	for _, test := range []struct {
		name, failAt string
		mutate       func(*mcpSQLStep)
		want         int
	}{
		{"valid", "", nil, 200}, {"used code", "", func(s *mcpSQLStep) { s.values[0][7] = time.Now() }, 400},
		{"wrong client", "", func(s *mcpSQLStep) { s.values[0][1] = "taskly-web" }, 400},
		{"wrong resource", "", func(s *mcpSQLStep) { s.values[0][5] = mcpTestTasklyResource }, 400},
		{"wrong PKCE", "", func(s *mcpSQLStep) { s.values[0][3] = strings.Repeat("q", 43) }, 400},
		{"expired", "", func(s *mcpSQLStep) { s.values[0][6] = time.Now().Add(-time.Minute) }, 400},
		{"inactive subject", "subject", nil, 400}, {"consume failure", "consume", nil, 500}, {"access storage failure", "access", nil, 500}, {"refresh storage failure", "refresh", nil, 500}, {"commit failure", "commit", nil, 500}, {"begin failure", "begin", nil, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := []mcpSQLStep{}
			if test.failAt != "begin" {
				code := mcpCodeStep()
				if test.mutate != nil {
					test.mutate(&code)
				}
				steps = append(steps, code)
				if test.mutate == nil {
					subject := mcpActiveSubjectStep()
					if test.failAt == "subject" {
						subject.values = nil
					}
					steps = append(steps, subject)
					if test.failAt != "subject" {
						consume := mcpSQLStep{query: "UPDATE oauth_codes SET used_at=", affected: 1}
						if test.failAt == "consume" {
							consume.err = errors.New("unavailable")
						}
						steps = append(steps, consume)
						if test.failAt != "consume" {
							access := mcpSQLStep{query: "INSERT INTO oauth_access_tokens", affected: 1}
							if test.failAt == "access" {
								access.err = errors.New("unavailable")
							}
							steps = append(steps, access)
							if test.failAt != "access" {
								refresh := mcpSQLStep{query: "INSERT INTO oauth_refresh_tokens", affected: 1}
								if test.failAt == "refresh" {
									refresh.err = errors.New("unavailable")
								}
								steps = append(steps, refresh)
							}
						}
					}
				}
			}
			connector := &mcpSQLConnector{t: t, steps: steps}
			if test.failAt == "begin" {
				connector.beginErr = errors.New("unavailable")
			}
			if test.failAt == "commit" {
				connector.commitErr = errors.New("unavailable")
			}
			a := mcpTestApp()
			a.db = mcpScriptConnectorDB(t, connector)
			w := httptest.NewRecorder()
			a.token(w, mcpFormRequest(mcpCodeForm(), false))
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				body := mcpDecodeJSON(t, w)
				if body["scope"] != "guidance:read" || body["refresh_token"] == "" {
					t.Fatal("valid grant did not preserve scope or refresh support")
				}
			}
		})
	}
}

func mcpRefreshStep() mcpSQLStep {
	return mcpRow("FROM oauth_refresh_tokens WHERE token_hash=", int64(7), mcpTestPublicClient, "guidance:read guidance:tasks:write", mcpTestResource, "family", time.Now().UTC().Add(time.Hour), nil, nil)
}
func TestMCPRefreshLimitsScopeAndRevokesReusedFamily(t *testing.T) {
	for _, test := range []struct {
		name, scope, failAt string
		used                bool
		want                int
	}{
		{"unchanged", "", "", false, 200}, {"reduce scope", "guidance:read", "", false, 200}, {"escalate scope", "guidance:read guidance:bookmarks:write", "scope", false, 400}, {"invalid scope", "taskly:login", "scope", false, 400}, {"revoked", "", "revoked", false, 400}, {"used refresh", "", "", true, 400}, {"reuse storage failure", "", "revoke", true, 500}, {"consume failure", "", "consume", false, 500}, {"commit failure", "", "commit", false, 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			refresh := mcpRefreshStep()
			if test.used {
				refresh.values[0][6] = time.Now()
			}
			if test.failAt == "revoked" {
				refresh.values[0][7] = time.Now()
			}
			steps := []mcpSQLStep{refresh}
			if test.failAt != "revoked" {
				steps = append(steps, mcpActiveSubjectStep())
				if test.used {
					revoke := mcpSQLStep{query: "UPDATE oauth_refresh_tokens SET revoked_at=", affected: 1}
					if test.failAt == "revoke" {
						revoke.err = errors.New("unavailable")
					}
					steps = append(steps, revoke)
					if test.failAt != "revoke" {
						steps = append(steps, mcpSQLStep{query: "UPDATE oauth_access_tokens SET revoked_at=", affected: 1})
					}
				} else if test.failAt != "scope" {
					consume := mcpSQLStep{query: "UPDATE oauth_refresh_tokens SET used_at=", affected: 1}
					if test.failAt == "consume" {
						consume.err = errors.New("unavailable")
					}
					steps = append(steps, consume)
					if test.failAt != "consume" {
						steps = append(steps, mcpSQLStep{query: "INSERT INTO oauth_access_tokens", affected: 1}, mcpSQLStep{query: "INSERT INTO oauth_refresh_tokens", affected: 1})
					}
				}
			}
			connector := &mcpSQLConnector{t: t, steps: steps}
			if test.failAt == "commit" {
				connector.commitErr = errors.New("unavailable")
			}
			a := mcpTestApp()
			a.db = mcpScriptConnectorDB(t, connector)
			form := url.Values{"client_id": {mcpTestPublicClient}, "grant_type": {"refresh_token"}, "refresh_token": {strings.Repeat("r", 43)}, "resource": {mcpTestResource}}
			if test.scope != "" {
				form.Set("scope", test.scope)
			}
			w := httptest.NewRecorder()
			a.token(w, mcpFormRequest(form, false))
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.want == 200 {
				wantScope := "guidance:read guidance:tasks:write"
				if test.scope != "" {
					wantScope = test.scope
				}
				if mcpDecodeJSON(t, w)["scope"] != wantScope {
					t.Fatal("refresh did not preserve or reduce consented scope")
				}
			}
		})
	}
}

func mcpAccessInspectionStep(client, scope string, expiry time.Time) mcpSQLStep {
	return mcpRow("SELECT t.user_id,t.client_id,t.scope_value,t.expires_at", int64(7), client, scope, expiry)
}
func TestMCPIntrospectionKeepsLegacyAndMCPResourcesSeparated(t *testing.T) {
	for _, test := range []struct {
		name, client, scope string
		service, live       bool
		resource            string
	}{
		{"MCP resource server", mcpTestPublicClient, "guidance:read", true, true, mcpTestResource},
		{"Taskly cannot inspect MCP", mcpTestPublicClient, "guidance:read", false, false, mcpTestResource},
		{"legacy Taskly", "taskly-web", "taskly:login", false, true, "taskly-api"},
		{"MCP cannot inspect legacy", "taskly-web", "taskly:login", true, false, "taskly-api"},
		{"wrong MCP resource", mcpTestPublicClient, "guidance:read", true, false, mcpTestTasklyResource},
		{"legacy scope on MCP", mcpTestPublicClient, "taskly:login", true, false, mcpTestResource},
	} {
		t.Run(test.name, func(t *testing.T) {
			expires := time.Now().UTC().Add(time.Minute)
			steps := []mcpSQLStep{mcpAccessInspectionStep(test.client, test.scope, expires)}
			if test.client == mcpTestPublicClient {
				detail := mcpTokenStep(test.scope)
				detail.values[0][3] = test.resource
				steps = append(steps, detail)
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			r := mcpFormRequest(url.Values{"token": {strings.Repeat("p", 43)}}, false)
			if test.service {
				r.SetBasicAuth(mcpTestBackendClient, mcpTestBackendSecret)
			} else {
				r.SetBasicAuth(a.clientID, a.clientSecret)
			}
			w := httptest.NewRecorder()
			a.introspect(w, r)
			body := mcpDecodeJSON(t, w)
			if w.Code != 200 || body["active"] != test.live {
				t.Fatalf("introspection status=%d claims=%v", w.Code, body)
			}
			if test.live && (body["aud"] != test.resource || body["sub"] != "7" || body["iss"] != a.issuer) {
				t.Fatal("introspection lost resource or subject authority")
			}
		})
	}
}

func TestMCPDelegatedIntrospectionRevalidatesOriginalAuthority(t *testing.T) {
	for _, test := range []struct {
		name                      string
		parentMutation            func(*mcpSQLStep)
		childScope, childResource string
		service, expected         bool
		skipParent                bool
	}{
		{name: "valid delegated write", expected: true},
		{name: "parent revoked", parentMutation: func(s *mcpSQLStep) { s.values[0][6] = time.Now() }},
		{name: "parent expired", parentMutation: func(s *mcpSQLStep) { s.values[0][5] = time.Now().Add(-time.Minute) }},
		{name: "wrong parent subject", parentMutation: func(s *mcpSQLStep) { s.values[0][0] = int64(8) }},
		{name: "wrong parent client", parentMutation: func(s *mcpSQLStep) { s.values[0][1] = "taskly-web" }},
		{name: "wrong parent resource", parentMutation: func(s *mcpSQLStep) { s.values[0][3] = mcpTestTasklyResource }},
		{name: "different family", parentMutation: func(s *mcpSQLStep) { s.values[0][4] = "other-family" }},
		{name: "parent scope removed", parentMutation: func(s *mcpSQLStep) { s.values[0][2] = "guidance:read" }},
		{name: "noncanonical parent scope", parentMutation: func(s *mcpSQLStep) { s.values[0][2] = "guidance:tasks:write guidance:read" }},
		{name: "child outlives parent", parentMutation: func(s *mcpSQLStep) { s.values[0][5] = time.Now().Add(10 * time.Second) }},
		{name: "missing parent", parentMutation: func(s *mcpSQLStep) { s.values = nil }},
		{name: "parent lookup unavailable", parentMutation: func(s *mcpSQLStep) { s.err = errors.New("unavailable") }},
		{name: "MCP service cannot use downstream token", service: true, skipParent: true},
		{name: "wrong child scope", childScope: "taskly:login", skipParent: true},
		{name: "wrong child resource", childResource: mcpTestResource, skipParent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope := test.childScope
			if scope == "" {
				scope = "guidance:tasks:write"
			}
			resource := test.childResource
			if resource == "" {
				resource = mcpTestTasklyResource
			}
			expiry := time.Now().UTC().Add(30 * time.Second)
			detail := mcpRow("t.parent_token_hash", int64(7), mcpTestBackendClient, scope, resource, "family", digest("parent"), expiry)
			steps := []mcpSQLStep{mcpAccessInspectionStep(mcpTestBackendClient, scope, expiry), detail}
			if !test.skipParent {
				parent := mcpRow("FROM oauth_access_tokens WHERE token_hash=", int64(7), mcpTestPublicClient, "guidance:read guidance:tasks:write", mcpTestResource, "family", time.Now().UTC().Add(time.Minute), nil)
				if test.parentMutation != nil {
					test.parentMutation(&parent)
				}
				steps = append(steps, parent)
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			r := mcpFormRequest(url.Values{"token": {strings.Repeat("c", 43)}}, false)
			if test.service {
				r.SetBasicAuth(mcpTestBackendClient, mcpTestBackendSecret)
			} else {
				r.SetBasicAuth(a.clientID, a.clientSecret)
			}
			w := httptest.NewRecorder()
			a.introspect(w, r)
			claims := mcpDecodeJSON(t, w)
			if w.Code != 200 || claims["active"] != test.expected {
				t.Fatalf("status=%d claims=%v", w.Code, claims)
			}
			if test.expected {
				act, _ := claims["act"].(map[string]any)
				if claims["original_client_id"] != mcpTestPublicClient || claims["original_resource"] != mcpTestResource || act["sub"] != mcpTestBackendClient {
					t.Fatal("delegated introspection omitted original client or actor")
				}
			}
		})
	}
}

func TestMCPSchemaUpgradesAreNullableAndRepeatable(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "already upgraded"}[existing], func(t *testing.T) {
			steps := []mcpSQLStep{}
			for _, column := range []struct{ table, name string }{
				{"oauth_authorization_requests", "resource_uri"}, {"oauth_authorization_requests", "consent_user_id"}, {"oauth_authorization_requests", "consent_csrf_hash"}, {"oauth_codes", "resource_uri"}, {"oauth_access_tokens", "resource_uri"}, {"oauth_access_tokens", "parent_token_hash"}, {"oauth_refresh_tokens", "resource_uri"},
			} {
				count := int64(0)
				if existing {
					count = 1
				}
				check := mcpRow("information_schema.COLUMNS", count)
				check.check = func(args []driver.NamedValue) {
					if args[0].Value != column.table || args[1].Value != column.name {
						t.Error("migration inspected wrong table or column")
					}
				}
				steps = append(steps, check)
				if !existing {
					steps = append(steps, mcpSQLStep{query: "ALTER TABLE " + column.table + " ADD COLUMN " + column.name, affected: 1})
				}
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			if err := a.ensureMCPSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, failAt := range []string{"inspect", "upgrade"} {
		t.Run(failAt+" unavailable", func(t *testing.T) {
			step := mcpRow("information_schema.COLUMNS", int64(0))
			steps := []mcpSQLStep{step}
			if failAt == "inspect" {
				steps[0].err = errors.New("unavailable")
			} else {
				steps = append(steps, mcpSQLStep{query: "ALTER TABLE", err: errors.New("unavailable")})
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			if err := a.ensureMCPSchema(t.Context()); err == nil {
				t.Fatal("migration suppressed a database failure")
			}
		})
	}
}

func TestMCPAuthorizationAlwaysNavigatesThroughExplicitConsent(t *testing.T) {
	for _, test := range []struct {
		name      string
		cookie    bool
		failAt    string
		boundUser driver.Value
		want      int
		wantPath  string
	}{
		{"signed in", true, "", nil, 302, "/consent"},
		{"same user resumed", true, "unchanged", int64(7), 302, "/consent"},
		{"signed out", false, "", nil, 302, "/login"},
		{"authorization storage failure", true, "insert", nil, 500, ""},
		{"other user's request", true, "", int64(8), 400, ""},
		{"expired request", true, "expired", nil, 400, ""},
		{"binding storage failure", true, "bind", nil, 500, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			insert := mcpSQLStep{query: "INSERT INTO oauth_authorization_requests", affected: 1, check: func(args []driver.NamedValue) {
				if args[1].Value != mcpTestPublicClient || args[2].Value != mcpTestRedirect || args[5].Value != "guidance:read guidance:tasks:write" || args[6].Value != mcpTestResource {
					t.Error("authorization request lost canonical client, callback, scopes, or resource")
				}
			}}
			if test.failAt == "insert" {
				insert.err = errors.New("unavailable")
			}
			steps := []mcpSQLStep{insert}
			if test.cookie && test.failAt != "insert" {
				steps = append(steps, mcpSessionStep())
				request := mcpAuthorizationStep("guidance:read guidance:tasks:write")
				request.values[0][7] = test.boundUser
				request.values[0][8] = nil
				if test.failAt == "expired" {
					request.values[0][6] = time.Now().Add(-time.Minute)
				}
				steps = append(steps, request)
				if test.wantPath == "/consent" || test.failAt == "bind" {
					bind := mcpSQLStep{query: "UPDATE oauth_authorization_requests SET consent_user_id=", affected: 1}
					if test.failAt == "unchanged" {
						bind.affected = 0
					}
					if test.failAt == "bind" {
						bind.err = errors.New("unavailable")
					}
					steps = append(steps, bind)
				}
			}
			a := mcpTestApp()
			a.db = mcpScriptDB(t, steps...)
			q := url.Values{"client_id": {mcpTestPublicClient}, "redirect_uri": {mcpTestRedirect}, "response_type": {"code"}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("c", 43)}, "state": {strings.Repeat("s", 32)}, "scope": {"guidance:tasks:write guidance:read"}, "resource": {mcpTestResource}}
			r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
			if test.cookie {
				r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "session"})
			}
			w := httptest.NewRecorder()
			a.authorize(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.wantPath != "" {
				location, err := url.Parse(w.Header().Get("Location"))
				if err != nil || location.Host != "login.example.test" || location.Path != test.wantPath || location.Query().Get("request_id") == "" || location.Query().Get("code") != "" {
					t.Fatal("authorization bypassed login or consent navigation")
				}
			}
		})
	}
}

func TestMCPConsentAndResumptionRequireValidAuthenticatedFlow(t *testing.T) {
	for _, test := range []struct {
		name                    string
		disabled, missingCookie bool
		id                      string
		want                    int
	}{
		{"disabled", true, false, strings.Repeat("r", 43), 400},
		{"invalid request identifier", false, false, "invalid", 400},
		{"no user session", false, true, strings.Repeat("r", 43), 302},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := mcpTestApp()
			if test.disabled {
				a.mcp = nil
			}
			a.db = mcpScriptDB(t)
			r := httptest.NewRequest("GET", "/oauth/authorize", nil)
			if !test.missingCookie {
				r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "session"})
			}
			w := httptest.NewRecorder()
			a.resumeMCPAuthorization(w, r, test.id)
			if w.Code != test.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, approve := range []bool{false, true} {
		a := mcpTestApp()
		a.db = mcpScriptDB(t)
		var r *http.Request
		if approve {
			r = mcpApproveRequest(true)
			r.Header.Del("Cookie")
		} else {
			r = httptest.NewRequest("GET", "/oauth/consent?request_id="+strings.Repeat("r", 43), nil)
			r.Header.Set("Origin", a.uiURL)
			r.Header.Set("X-Auth-UI", "1")
		}
		w := httptest.NewRecorder()
		if approve {
			a.approveConsent(w, r)
		} else {
			a.consent(w, r)
		}
		if w.Code != 401 {
			t.Fatalf("unauthenticated consent status=%d", w.Code)
		}
	}
}
