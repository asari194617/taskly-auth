package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestEmailCodeLoginRecordsCurrentEmailVerification(t *testing.T) {
	dsn := os.Getenv("AUTH_DSN")
	if dsn == "" {
		t.Skip("AUTH_DSN is required for the MySQL integration test")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil || config.Net != "tcp" || config.Addr != "127.0.0.1:3307" || config.DBName != "taskly_auth" {
		t.Fatal("email verification integration test requires the local development database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := (&app{db: db}).ensureOAuthSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	email := fmt.Sprintf("verification-%d@example.test", time.Now().UnixNano())
	otherEmail := "other-" + email
	ids := make([]uint64, 0, 2)
	t.Cleanup(func() {
		for _, id := range ids {
			if _, err := db.Exec(`DELETE FROM auth_email_verifications WHERE user_id=?`, id); err != nil {
				t.Errorf("delete verification: %v", err)
			}
		}
		for _, value := range []string{email, otherEmail} {
			if _, err := db.Exec(`DELETE FROM auth_login_codes WHERE email=?`, value); err != nil {
				t.Errorf("delete login codes: %v", err)
			}
		}
		for _, id := range ids {
			if _, err := db.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
				t.Errorf("delete user: %v", err)
			}
		}
	})
	createUser := func(value string) uint64 {
		t.Helper()
		result, err := db.ExecContext(t.Context(), `INSERT INTO users (email,display_name,email_code_allowed) VALUES (?,'Verification Test',1)`, value)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, uint64(id))
		return uint64(id)
	}
	userID := createUser(email)
	otherID := createUser(otherEmail)
	const code = "123456"
	result, err := db.ExecContext(t.Context(), `INSERT INTO auth_login_codes (user_id,email,hashed_code,expires_at) VALUES (?,?,?,?)`, userID, email, digest(code), time.Now().UTC().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	codeID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	a := &app{db: db, uiURL: "http://localhost:3068", alerts: &alerter{}}
	request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	verify := func(id uint64, value, candidate string) bool {
		t.Helper()
		return a.verifySecondFactor(request, id, value, loginInput{Code: candidate}, true)
	}
	assertCodeState := func(wantConsumed bool, wantAttempts int) {
		t.Helper()
		var consumed sql.NullTime
		var attempts int
		if err := db.QueryRowContext(t.Context(), `SELECT consumed_at,failed_attempts FROM auth_login_codes WHERE id=?`, codeID).Scan(&consumed, &attempts); err != nil {
			t.Fatal(err)
		}
		if consumed.Valid != wantConsumed || attempts != wantAttempts {
			t.Fatalf("login code state: consumed=%t attempts=%d", consumed.Valid, attempts)
		}
	}
	assertUnverified := func(id uint64) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_email_verifications WHERE user_id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("unexpected email verification for user %d", id)
		}
	}

	if verify(otherID, email, code) {
		t.Fatal("accepted a code for another user ID")
	}
	assertCodeState(false, 0)
	assertUnverified(otherID)
	accentAlias := strings.Replace(email, "verification-", "vérification-", 1)
	if verify(userID, accentAlias, code) {
		t.Fatal("accepted a code through an accent-equivalent address")
	}
	requestAlias := httptest.NewRequest(http.MethodPost, "/auth/request-code", strings.NewReader(fmt.Sprintf(`{"email":%q}`, accentAlias)))
	requestAlias.Header.Set("Origin", a.uiURL)
	requestAlias.Header.Set("X-Auth-UI", "1")
	requestAlias.Header.Set("Content-Type", "application/json")
	responseAlias := httptest.NewRecorder()
	a.requestCode(responseAlias, requestAlias)
	if responseAlias.Code != http.StatusAccepted {
		t.Fatalf("accent-equivalent address was processed as a recipient: %d %s", responseAlias.Code, responseAlias.Body.String())
	}
	var codeCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_login_codes WHERE user_id=?`, userID).Scan(&codeCount); err != nil || codeCount != 1 {
		t.Fatalf("alias created a login code: count=%d error=%v", codeCount, err)
	}
	otherCode, err := db.ExecContext(t.Context(), `INSERT INTO auth_login_codes (user_id,email,hashed_code,expires_at) VALUES (?,?,?,?)`, otherID, otherEmail, digest(code), time.Now().UTC().Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	otherCodeID, err := otherCode.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE users SET email=? WHERE id=?`, "changed-"+email, otherID); err != nil {
		t.Fatal(err)
	}
	if verify(otherID, otherEmail, code) {
		t.Fatal("accepted a code sent before the user's email changed")
	}
	var obsoleteCodeConsumed sql.NullTime
	if err := db.QueryRowContext(t.Context(), `SELECT consumed_at FROM auth_login_codes WHERE id=?`, otherCodeID).Scan(&obsoleteCodeConsumed); err != nil || obsoleteCodeConsumed.Valid {
		t.Fatalf("obsolete code was consumed: consumed=%t err=%v", obsoleteCodeConsumed.Valid, err)
	}
	assertUnverified(otherID)
	if verify(userID, email, "000000") {
		t.Fatal("accepted the wrong code")
	}
	assertCodeState(false, 1)
	assertUnverified(userID)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_email_verifications (user_id,email,hashed_code,expires_at,failed_attempts,requested_at) VALUES (?,?,?,?,0,UTC_TIMESTAMP())`, userID, email, strings.Repeat("a", 64), time.Now().UTC().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if !verify(userID, email, code) {
		t.Fatal("rejected the current user's valid email code")
	}
	assertCodeState(true, 1)
	var verifiedAt, expiresAt, requestedAt sql.NullTime
	var verifiedEmail string
	var hashedCode sql.NullString
	if err := db.QueryRowContext(t.Context(), `SELECT email,verified_at,hashed_code,expires_at,requested_at FROM auth_email_verifications WHERE user_id=?`, userID).Scan(&verifiedEmail, &verifiedAt, &hashedCode, &expiresAt, &requestedAt); err != nil {
		t.Fatal(err)
	}
	if verifiedEmail != email || !verifiedAt.Valid || hashedCode.Valid || expiresAt.Valid || requestedAt.Valid {
		t.Fatalf("verification state: email_match=%t verified=%t pending_code=%t pending_expiry=%t requested=%t", verifiedEmail == email, verifiedAt.Valid, hashedCode.Valid, expiresAt.Valid, requestedAt.Valid)
	}
	// Explicit verification requests must leave an address confirmed by login
	// alone; they used to replace the row with a pending code after a stale read.
	sessionToken := fmt.Sprintf("verification-session-%d", userID)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_sessions (token_hash,user_id,expires_at,created_at) VALUES (?,?,UTC_TIMESTAMP()+INTERVAL 1 HOUR,UTC_TIMESTAMP())`, digest(sessionToken), userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DELETE FROM auth_sessions WHERE token_hash=?`, digest(sessionToken)); err != nil {
			t.Errorf("delete session: %v", err)
		}
		if _, err := db.Exec(`DELETE FROM auth_rate_limits WHERE key_hash=?`, digest(fmt.Sprintf("email-verify:%d", userID))); err != nil {
			t.Errorf("delete rate limit: %v", err)
		}
	})
	a.uiURL = "https://auth.example.test"
	verificationRequest := httptest.NewRequest(http.MethodPost, "/auth/profile/email-verification/request", nil)
	verificationRequest.Header.Set("Origin", a.uiURL)
	verificationRequest.Header.Set("X-Auth-UI", "1")
	verificationRequest.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: sessionToken})
	verificationResponse := httptest.NewRecorder()
	a.requestEmailVerification(verificationResponse, verificationRequest)
	if verificationResponse.Code != http.StatusNoContent {
		t.Fatalf("request for confirmed email returned %d", verificationResponse.Code)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT verified_at FROM auth_email_verifications WHERE user_id=?`, userID).Scan(&verifiedAt); err != nil || !verifiedAt.Valid {
		t.Fatalf("confirmed address lost after verification request: verified=%t err=%v", verifiedAt.Valid, err)
	}
	if verify(userID, email, code) {
		t.Fatal("accepted an already consumed code")
	}
}
