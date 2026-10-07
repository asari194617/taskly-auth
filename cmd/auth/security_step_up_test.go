package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type securityTestConnector struct {
	verified      driver.Value
	totpCount     int64
	recoveryCount int64
	email         string
	codeHash      string
	codeExpires   time.Time
	codeFailures  int64
	hasCode       bool
}

func (c *securityTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &securityTestConn{c}, nil
}
func (c *securityTestConnector) Driver() driver.Driver { return securityTestDriver{} }

type securityTestDriver struct{}

func (securityTestDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type securityTestConn struct{ config *securityTestConnector }

func (*securityTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*securityTestConn) Close() error              { return nil }
func (*securityTestConn) Begin() (driver.Tx, error) { return &securityTestTx{}, nil }
func (*securityTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return &securityTestTx{}, nil
}

type securityTestTx struct{}

func (*securityTestTx) Commit() error   { return nil }
func (*securityTestTx) Rollback() error { return nil }

func (c *securityTestConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.Contains(query, "INSERT INTO auth_rate_limits"):
	case strings.Contains(query, "DELETE FROM auth_security_codes"):
		c.config.hasCode = false
	case strings.Contains(query, "UPDATE auth_security_codes SET failed_attempts"):
		c.config.codeFailures++
	case strings.Contains(query, "UPDATE auth_sessions SET security_verified_at"):
		c.config.verified = time.Now().UTC()
	default:
		return nil, errors.New("unexpected exec: " + query)
	}
	return driver.RowsAffected(1), nil
}
func (c *securityTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "FROM auth_sessions s JOIN users"):
		return &securityTestRows{columns: []string{"user_id"}, values: [][]driver.Value{{int64(7)}}}, nil
	case strings.Contains(query, "SELECT attempts FROM auth_rate_limits"):
		return &securityTestRows{columns: []string{"attempts"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(query, "SELECT email FROM users"):
		return &securityTestRows{columns: []string{"email"}, values: [][]driver.Value{{c.config.email}}}, nil
	case strings.Contains(query, "SELECT user_id FROM auth_sessions"):
		return &securityTestRows{columns: []string{"user_id"}, values: [][]driver.Value{{int64(7)}}}, nil
	case strings.Contains(query, "SELECT secret FROM totp_secrets"):
		if c.config.totpCount > 0 {
			return &securityTestRows{columns: []string{"secret"}, values: [][]driver.Value{{"encrypted-test-secret"}}}, nil
		}
		return &securityTestRows{columns: []string{"secret"}}, nil
	case strings.Contains(query, "FROM auth_security_codes WHERE session_hash"):
		if !c.config.hasCode {
			return &securityTestRows{columns: []string{"email", "hashed_code", "expires_at", "failed_attempts"}}, nil
		}
		return &securityTestRows{columns: []string{"email", "hashed_code", "expires_at", "failed_attempts"}, values: [][]driver.Value{{c.config.email, c.config.codeHash, c.config.codeExpires, c.config.codeFailures}}}, nil
	case strings.Contains(query, "SELECT security_verified_at FROM auth_sessions"):
		return &securityTestRows{columns: []string{"security_verified_at"}, values: [][]driver.Value{{c.config.verified}}}, nil
	case strings.Contains(query, "SELECT COUNT(*) FROM totp_secrets"):
		return &securityTestRows{columns: []string{"count"}, values: [][]driver.Value{{c.config.totpCount}}}, nil
	case strings.Contains(query, "SELECT COUNT(*) FROM recovery_codes"):
		return &securityTestRows{columns: []string{"count"}, values: [][]driver.Value{{c.config.recoveryCount}}}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

type securityTestRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *securityTestRows) Columns() []string { return r.columns }
func (*securityTestRows) Close() error        { return nil }
func (r *securityTestRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func securityTestRequest() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/security", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://localhost:3068")
	r.Header.Set("X-Auth-UI", "1")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "test-session"})
	return r
}

func securityTestApp(config *securityTestConnector, required bool) *app {
	return &app{db: sql.OpenDB(config), uiURL: "http://localhost:3068", securityStepUpRequired: required, webAuthn: &webauthn.WebAuthn{}, internalToken: "test-secret"}
}

func TestSecurityMutationHandlersRequireVerifiedSession(t *testing.T) {
	handlers := []struct {
		name string
		fn   func(*app, http.ResponseWriter, *http.Request)
	}{
		{"password", (*app).changePassword},
		{"email code", (*app).updateEmailCodeSettings},
		{"recovery", (*app).generateRecoveryCodes},
		{"TOTP setup", (*app).totpSetup},
		{"TOTP activate", (*app).totpActivate},
		{"TOTP disable", (*app).totpDisable},
		{"passkey begin", (*app).passkeyRegisterBegin},
		{"passkey finish", (*app).passkeyRegisterFinish},
		{"passkey rename", (*app).passkeyRename},
		{"passkey delete", (*app).passkeyDelete},
	}
	for _, test := range handlers {
		t.Run(test.name, func(t *testing.T) {
			a := securityTestApp(&securityTestConnector{}, true)
			defer a.db.Close()
			w := httptest.NewRecorder()
			test.fn(a, w, securityTestRequest())
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "step_up_required") {
				t.Fatalf("response = %d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestSecurityGrantIsBoundToSessionAndExpires(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name     string
		verified driver.Value
		allowed  bool
	}{
		{"unverified", nil, false},
		{"recent", now.Add(-4 * time.Minute), true},
		{"expired", now.Add(-6 * time.Minute), false},
		{"future", now.Add(time.Minute), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := securityTestApp(&securityTestConnector{verified: test.verified}, true)
			defer a.db.Close()
			w := httptest.NewRecorder()
			id, ok := a.securitySettingsUser(w, securityTestRequest())
			if ok != test.allowed || (ok && id != 7) {
				t.Fatalf("id=%d allowed=%v response=%d", id, ok, w.Code)
			}
		})
	}
}

func TestSecurityRolloutGatePreservesExistingFreshSession(t *testing.T) {
	a := securityTestApp(&securityTestConnector{}, false)
	defer a.db.Close()
	w := httptest.NewRecorder()
	id, ok := a.securitySettingsUser(w, securityTestRequest())
	if !ok || id != 7 {
		t.Fatalf("disabled gate rejected the existing recent-session path: id=%d, response=%d", id, w.Code)
	}
}

func TestSecurityStatusOffersOnlyConfiguredMethods(t *testing.T) {
	a := securityTestApp(&securityTestConnector{totpCount: 1, recoveryCount: 1}, true)
	defer a.db.Close()
	w := httptest.NewRecorder()
	a.securityStepUpStatus(w, securityTestRequest())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"methods":["totp","recovery"]`) || !strings.Contains(w.Body.String(), `"verified":false`) {
		t.Fatalf("status = %d %q", w.Code, w.Body.String())
	}
}

func TestSecurityMethodsAndCodeBinding(t *testing.T) {
	if got := securityMethods(false, 10); len(got) != 1 || got[0] != "email" {
		t.Fatalf("no TOTP methods = %v", got)
	}
	if got := securityMethods(true, 0); len(got) != 1 || got[0] != "totp" {
		t.Fatalf("TOTP methods = %v", got)
	}
	if got := securityMethods(true, 1); len(got) != 2 || got[0] != "totp" || got[1] != "recovery" {
		t.Fatalf("recovery methods = %v", got)
	}
	a := &app{internalToken: "test-secret"}
	code := a.securityCodeDigest("email", 7, "session-1", "user@example.com", "123456")
	for _, other := range []string{
		a.securityCodeDigest("email", 7, "session-2", "user@example.com", "123456"),
		a.securityCodeDigest("email", 7, "session-1", "new@example.com", "123456"),
		a.securityCodeDigest("totp", 7, "session-1", "user@example.com", "123456"),
	} {
		if code == other {
			t.Fatal("security code can be replayed in another context")
		}
	}
}

func TestSecurityEmailCodeIsConsumedOnce(t *testing.T) {
	state := &securityTestConnector{email: "user@example.com", codeExpires: time.Now().UTC().Add(time.Minute), hasCode: true}
	a := securityTestApp(state, true)
	defer a.db.Close()
	state.codeHash = a.securityCodeDigest("email", 7, digest("test-session"), state.email, "123456")
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/auth/security/step-up/verify", strings.NewReader(`{"method":"email","code":"123456"}`))
		r.Header.Set("Origin", "http://localhost:3068")
		r.Header.Set("X-Auth-UI", "1")
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "test-session"})
		return r
	}
	first := httptest.NewRecorder()
	a.verifySecurityStepUp(first, request())
	if first.Code != http.StatusNoContent || state.hasCode || state.verified == nil {
		t.Fatalf("first verification = %d %q, code still present=%v", first.Code, first.Body.String(), state.hasCode)
	}
	second := httptest.NewRecorder()
	a.verifySecurityStepUp(second, request())
	if second.Code != http.StatusUnauthorized || !strings.Contains(second.Body.String(), "invalid_code") {
		t.Fatalf("replayed verification = %d %q", second.Code, second.Body.String())
	}
}

func TestSecurityEmailCannotBypassTOTP(t *testing.T) {
	state := &securityTestConnector{email: "user@example.com", totpCount: 1, hasCode: true, codeExpires: time.Now().UTC().Add(time.Minute)}
	a := securityTestApp(state, true)
	defer a.db.Close()
	state.codeHash = a.securityCodeDigest("email", 7, digest("test-session"), state.email, "123456")
	requestCode := httptest.NewRecorder()
	a.requestSecurityStepUpCode(requestCode, securityTestRequest())
	if requestCode.Code != http.StatusConflict || !strings.Contains(requestCode.Body.String(), "totp_required") {
		t.Fatalf("email request with TOTP = %d %q", requestCode.Code, requestCode.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/security/step-up/verify", strings.NewReader(`{"method":"email","code":"123456"}`))
	request.Header.Set("Origin", "http://localhost:3068")
	request.Header.Set("X-Auth-UI", "1")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "test-session"})
	verify := httptest.NewRecorder()
	a.verifySecurityStepUp(verify, request)
	if verify.Code != http.StatusConflict || !strings.Contains(verify.Body.String(), "method_unavailable") || state.verified != nil {
		t.Fatalf("email verification with TOTP = %d %q", verify.Code, verify.Body.String())
	}
}
