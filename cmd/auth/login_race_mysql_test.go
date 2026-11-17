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
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordChangeWinsBeforePendingLoginSessionOnMySQL(t *testing.T) {
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
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := (&app{db: db}).ensureOAuthSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("race-%d@example.test", time.Now().UnixNano())
	const oldPassword = "old-password-for-test"
	const nextPassword = "next-password-for-test"
	oldHash, err := bcrypt.GenerateFromPassword([]byte(oldPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.ExecContext(t.Context(), `INSERT INTO users (email,hashed_password,email_code_allowed) VALUES (?,?,0)`, email, string(oldHash))
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM auth_sessions WHERE user_id=?`, userID)
		_, _ = db.Exec(`DELETE FROM auth_login_history WHERE user_id=?`, userID)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, userID)
	})
	const session = "race-password-change-session"
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_sessions (token_hash,user_id,expires_at,created_at) VALUES (?,?,?,?)`, digest(session), userID, time.Now().UTC().Add(time.Hour), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	a := &app{db: db, uiURL: "http://localhost:3068", alerts: &alerter{}, beforeSessionUserLock: func() {
		close(entered)
		<-release
	}}
	request := func(path, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Origin", a.uiURL)
		r.Header.Set("X-Auth-UI", "1")
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	loginResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		a.login(w, request("/auth/login", fmt.Sprintf(`{"email":%q,"password":%q}`, email, oldPassword)))
		loginResult <- w
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("login did not reach session transaction")
	}
	change := request("/auth/password", fmt.Sprintf(`{"current":%q,"next":%q}`, oldPassword, nextPassword))
	change.AddCookie(&http.Cookie{Name: a.sessionCookieName(), Value: session})
	changed := httptest.NewRecorder()
	a.changePassword(changed, change)
	if changed.Code != http.StatusNoContent {
		close(release)
		t.Fatalf("password change failed: %d %s", changed.Code, changed.Body.String())
	}
	close(release)
	select {
	case login := <-loginResult:
		if login.Code != http.StatusUnauthorized || len(login.Result().Cookies()) != 0 {
			t.Fatalf("stale password issued a session: %d %s", login.Code, login.Body.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not finish after password change")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM auth_sessions WHERE user_id=?`, userID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale session remains: count=%d error=%v", count, err)
	}
}
