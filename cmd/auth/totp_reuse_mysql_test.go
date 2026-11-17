package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pquerna/otp/totp"
)

func TestTOTPConsumedOnceAcrossLoginAndStepUpOnMySQL(t *testing.T) {
	dsn := os.Getenv("AUTH_DSN")
	if dsn == "" {
		t.Skip("AUTH_DSN is required for the local MySQL integration test")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil || config.Net != "tcp" || config.Addr != "127.0.0.1:3307" || config.DBName != "taskly_auth" {
		t.Fatal("test requires the local development database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := (&app{db: db}).ensureOAuthSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOTP_ENC_KEY", "isolated-totp-test-key")
	const secret = "JBSWY3DPEHPK3PXP"
	encrypted, err := encryptTOTP(secret, osEnv("TOTP_ENC_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("totp-once-%d@example.test", time.Now().UnixNano())
	result, err := db.ExecContext(t.Context(), `INSERT INTO users (email,email_code_allowed) VALUES (?,0)`, email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM auth_security_totp_uses WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM totp_secrets WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, id)
	})
	if _, err := db.ExecContext(t.Context(), `INSERT INTO totp_secrets (user_id,secret,is_active) VALUES (?,?,1)`, id, encrypted); err != nil {
		t.Fatal(err)
	}
	session := fmt.Sprintf("totp-cross-route-%d", time.Now().UnixNano())
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_sessions (token_hash,user_id,expires_at) VALUES (?,?,?)`, digest(session), id, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, uiURL: "http://localhost:3068", alerts: &alerter{}, internalToken: "isolated-internal-token"}
	results := make([]bool, 2)
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			results[i] = a.verifySecondFactor(r, uint64(id), email, loginInput{TOTP: code}, false)
		}(i)
	}
	group.Wait()
	if results[0] == results[1] {
		t.Fatalf("concurrent TOTP use results = %v; exactly one must succeed", results)
	}
	stepUp := securityVerifyRequest("totp", code)
	// Use the real session rather than the unit-test helper's default cookie.
	stepUp.Header.Set("Cookie", "taskly_auth_session="+session)
	response := httptest.NewRecorder()
	a.verifySecurityStepUp(response, stepUp)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("step-up reused a login TOTP: %d %s", response.Code, response.Body.String())
	}
}
