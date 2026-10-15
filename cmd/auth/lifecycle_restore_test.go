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

func TestLifecycleRestorationRequiresFreshLogin(t *testing.T) {
	dsn := os.Getenv("AUTH_DSN")
	if dsn == "" {
		t.Skip("AUTH_DSN is required for the MySQL integration test")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil || config.Net != "tcp" || config.Addr != "127.0.0.1:3307" || config.DBName != "taskly_auth" {
		t.Fatal("requires local development database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := &app{db: db, uiURL: "http://localhost:3068", alerts: &alerter{}}
	email := fmt.Sprintf("restore-%d@example.test", time.Now().UnixNano())
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	result, err := db.Exec(`INSERT INTO users (email,hashed_password,email_code_allowed,is_registered) VALUES (?,?,0,1)`, email, string(hash))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	userID := uint64(id)
	defer func() {
		if _, err := a.applyDeletedState(t.Context(), userID, time.Now().UTC()); err != nil {
			t.Error(err)
		}
		if err := a.eraseDeletedUser(t.Context(), userID); err != nil {
			t.Error(err)
		}
	}()
	login := func(want int) {
		t.Helper()
		req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"test-password"}`, email)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", a.uiURL)
		req.Header.Set("X-Auth-UI", "1")
		res := httptest.NewRecorder()
		a.login(res, req)
		if res.Code != want {
			t.Fatalf("login status=%d want=%d body=%s", res.Code, want, res.Body.String())
		}
	}
	login(http.StatusOK)
	var oldSession string
	if err := db.QueryRow(`SELECT token_hash FROM auth_sessions WHERE user_id=?`, userID).Scan(&oldSession); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyDeletedState(t.Context(), userID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	login(http.StatusUnauthorized)
	for range 2 {
		if err := a.applyRestoredState(t.Context(), userID); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash=?`, oldSession).Scan(&count); err != nil || count != 0 {
		t.Fatalf("old sessions=%d error=%v", count, err)
	}
	var deleted bool
	var deletedAt sql.NullTime
	if err := db.QueryRow(`SELECT is_deleted,deleted_at FROM users WHERE id=?`, userID).Scan(&deleted, &deletedAt); err != nil || deleted || deletedAt.Valid {
		t.Fatalf("deleted=%v date=%v error=%v", deleted, deletedAt, err)
	}
	login(http.StatusOK)
}
