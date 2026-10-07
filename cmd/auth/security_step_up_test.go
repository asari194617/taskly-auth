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

	"github.com/go-sql-driver/mysql"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp/totp"
)

type securityTestConnector struct {
	verified              driver.Value
	totpCount             int64
	recoveryCount         int64
	recoveryTotal         int64
	recoveryStatusQueries int
	recoveryStatusQuery   string
	recoveryStatusUserID  driver.Value
	failRecoveryStatus    bool
	email                 string
	codeHash              string
	codeExpires           time.Time
	codeFailures          int64
	hasCode               bool
	userLockEntered       chan struct{}
	releaseUserLock       chan struct{}
	recoveryMutated       bool
	totpEncrypted         string
	totpCodeUsed          bool
	recoveryUsed          bool
	totpCodeHash          string
	recoveryHash          string
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

func (c *securityTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.Contains(query, "INSERT INTO auth_rate_limits"):
	case strings.Contains(query, "DELETE FROM auth_security_codes"):
		c.config.hasCode = false
	case strings.Contains(query, "UPDATE auth_security_codes SET failed_attempts"):
		c.config.codeFailures++
	case strings.Contains(query, "UPDATE auth_sessions SET security_verified_at"):
		c.config.verified = time.Now().UTC()
	case strings.Contains(query, "DELETE FROM recovery_codes"), strings.Contains(query, "INSERT INTO recovery_codes"):
		c.config.recoveryMutated = true
	case strings.Contains(query, "DELETE FROM auth_security_totp_uses"):
	case strings.Contains(query, "INSERT INTO auth_security_totp_uses"):
		codeHash, _ := args[1].Value.(string)
		if c.config.totpCodeUsed && c.config.totpCodeHash == codeHash {
			return nil, &mysql.MySQLError{Number: 1062, Message: "duplicate TOTP code"}
		}
		c.config.totpCodeUsed = true
		c.config.totpCodeHash = codeHash
	case strings.Contains(query, "UPDATE recovery_codes SET is_used=1"):
		codeHash, _ := args[1].Value.(string)
		if c.config.recoveryUsed || codeHash != c.config.recoveryHash {
			return driver.RowsAffected(0), nil
		}
		c.config.recoveryUsed = true
	default:
		return nil, errors.New("unexpected exec: " + query)
	}
	return driver.RowsAffected(1), nil
}
func (c *securityTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "FROM auth_sessions s JOIN users"):
		if args[0].Value != digest("test-session") && args[0].Value != digest("other-session") {
			return &securityTestRows{columns: []string{"user_id"}}, nil
		}
		return &securityTestRows{columns: []string{"user_id"}, values: [][]driver.Value{{int64(7)}}}, nil
	case strings.Contains(query, "SELECT attempts FROM auth_rate_limits"):
		return &securityTestRows{columns: []string{"attempts"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(query, "SELECT email FROM users"):
		return &securityTestRows{columns: []string{"email"}, values: [][]driver.Value{{c.config.email}}}, nil
	case strings.Contains(query, "SELECT is_deleted=0 FROM users"):
		if c.config.userLockEntered != nil {
			close(c.config.userLockEntered)
			<-c.config.releaseUserLock
		}
		return &securityTestRows{columns: []string{"active"}, values: [][]driver.Value{{true}}}, nil
	case strings.Contains(query, "SELECT user_id FROM auth_sessions"):
		return &securityTestRows{columns: []string{"user_id"}, values: [][]driver.Value{{int64(7)}}}, nil
	case strings.Contains(query, "SELECT secret FROM totp_secrets"):
		if c.config.totpCount > 0 {
			return &securityTestRows{columns: []string{"secret"}, values: [][]driver.Value{{c.config.totpEncrypted}}}, nil
		}
		return &securityTestRows{columns: []string{"secret"}}, nil
	case strings.Contains(query, "FROM auth_security_codes WHERE session_hash"):
		if !c.config.hasCode || args[0].Value != digest("test-session") {
			return &securityTestRows{columns: []string{"email", "hashed_code", "expires_at", "failed_attempts"}}, nil
		}
		return &securityTestRows{columns: []string{"email", "hashed_code", "expires_at", "failed_attempts"}, values: [][]driver.Value{{c.config.email, c.config.codeHash, c.config.codeExpires, c.config.codeFailures}}}, nil
	case strings.Contains(query, "SELECT security_verified_at FROM auth_sessions"):
		if args[0].Value != digest("test-session") {
			return &securityTestRows{columns: []string{"security_verified_at"}, values: [][]driver.Value{{nil}}}, nil
		}
		return &securityTestRows{columns: []string{"security_verified_at"}, values: [][]driver.Value{{c.config.verified}}}, nil
	case strings.Contains(query, "SELECT COUNT(*) FROM totp_secrets"):
		return &securityTestRows{columns: []string{"count"}, values: [][]driver.Value{{c.config.totpCount}}}, nil
	case strings.Contains(query, "SELECT COUNT(*),COUNT(CASE WHEN is_used=0 THEN 1 END) FROM recovery_codes"):
		c.config.recoveryStatusQueries++
		c.config.recoveryStatusQuery = query
		c.config.recoveryStatusUserID = args[0].Value
		if c.config.failRecoveryStatus {
			return nil, errors.New("recovery status unavailable")
		}
		return &securityTestRows{columns: []string{"total", "remaining"}, values: [][]driver.Value{{c.config.recoveryTotal, c.config.recoveryCount}}}, nil
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

func securityVerifyRequest(method, code string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/security/step-up/verify", strings.NewReader(`{"method":"`+method+`","code":"`+code+`"}`))
	r.Header.Set("Origin", "http://localhost:3068")
	r.Header.Set("X-Auth-UI", "1")
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "test-session"})
	return r
}

func securityTestApp(config *securityTestConnector, required bool) *app {
	return &app{db: sql.OpenDB(config), uiURL: "http://localhost:3068", securityStepUpRequired: required, webAuthn: &webauthn.WebAuthn{}, internalToken: "test-secret", alerts: &alerter{}}
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

func TestSecurityGrantDoesNotAuthorizeAnotherSession(t *testing.T) {
	a := securityTestApp(&securityTestConnector{verified: time.Now().UTC()}, true)
	defer a.db.Close()
	r := securityTestRequest()
	r.Header.Set("Cookie", "taskly_auth_session=other-session")
	w := httptest.NewRecorder()
	if _, ok := a.securitySettingsUser(w, r); ok || w.Code != http.StatusForbidden {
		t.Fatalf("other session grant = allowed %v, response %d %q", ok, w.Code, w.Body.String())
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

func TestSecurityGrantInvalidatedWhileMutationWaitsForUserLock(t *testing.T) {
	state := &securityTestConnector{
		verified:        time.Now().UTC(),
		userLockEntered: make(chan struct{}),
		releaseUserLock: make(chan struct{}),
	}
	a := securityTestApp(state, true)
	defer a.db.Close()
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		a.generateRecoveryCodes(w, securityTestRequest())
		result <- w
	}()
	select {
	case <-state.userLockEntered:
	case <-time.After(5 * time.Second):
		close(state.releaseUserLock)
		t.Fatal("mutation never reached the locked user row")
	}
	// Model a concurrent TOTP transition committing while this mutation waits
	// for that same user row. Its first, unlocked grant check has already passed.
	state.verified = nil
	close(state.releaseUserLock)
	select {
	case w := <-result:
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "step_up_required") || state.recoveryMutated {
			t.Fatalf("stale grant mutation = %d %q, writes=%v", w.Code, w.Body.String(), state.recoveryMutated)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mutation did not finish after grant invalidation")
	}
}

func TestSecurityTOTPCodeCannotBeReplayed(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	t.Setenv("TOTP_ENC_KEY", "step-up-test-key")
	encrypted, err := encryptTOTP(secret, "step-up-test-key")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state := &securityTestConnector{email: "user@example.com", totpCount: 1, totpEncrypted: encrypted}
	a := securityTestApp(state, true)
	defer a.db.Close()
	first := httptest.NewRecorder()
	a.verifySecurityStepUp(first, securityVerifyRequest("totp", code))
	if first.Code != http.StatusNoContent || !state.totpCodeUsed {
		t.Fatalf("first TOTP verification = %d %q", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	a.verifySecurityStepUp(second, securityVerifyRequest("totp", code))
	if second.Code != http.StatusUnauthorized || !strings.Contains(second.Body.String(), "invalid_code") {
		t.Fatalf("replayed TOTP verification = %d %q", second.Code, second.Body.String())
	}
}

func TestSecurityRecoveryCodeCannotBeReplayed(t *testing.T) {
	code, err := newRecoveryCode()
	if err != nil {
		t.Fatal(err)
	}
	state := &securityTestConnector{email: "user@example.com", totpCount: 1, recoveryHash: digest(code)}
	a := securityTestApp(state, true)
	defer a.db.Close()
	first := httptest.NewRecorder()
	a.verifySecurityStepUp(first, securityVerifyRequest("recovery", code))
	if first.Code != http.StatusNoContent || !state.recoveryUsed {
		t.Fatalf("first recovery verification = %d %q", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	a.verifySecurityStepUp(second, securityVerifyRequest("recovery", code))
	if second.Code != http.StatusUnauthorized || !strings.Contains(second.Body.String(), "invalid_code") {
		t.Fatalf("replayed recovery verification = %d %q", second.Code, second.Body.String())
	}
}
