package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestUTCBlockDeadlinesAndExplicitReleaseOnMySQL(t *testing.T) {
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
	email := fmt.Sprintf("block-deadline-%d@example.test", time.Now().UnixNano())
	result, err := db.ExecContext(t.Context(), `INSERT INTO users (email) VALUES (?)`, email)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM blocks WHERE user_id=?`, id)
		_, _ = db.Exec(`DELETE FROM users WHERE id=?`, id)
	})
	a := &app{db: db}
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	for _, test := range []struct {
		name    string
		expires any
		want    bool
	}{
		{"future", time.Now().UTC().Add(time.Hour), true},
		{"past", time.Now().UTC().Add(-time.Hour), false},
		{"permanent", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := db.ExecContext(t.Context(), `DELETE FROM blocks WHERE user_id=?`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), `INSERT INTO blocks (user_id,expires_at) VALUES (?,?)`, id, test.expires); err != nil {
				t.Fatal(err)
			}
			if got := a.blocked(r, uint64(id)); got != test.want {
				t.Fatalf("blocked=%v, want %v", got, test.want)
			}
		})
	}
	if err := a.applyBlockState(t.Context(), uint64(id), false, nil, nil); err != nil {
		t.Fatal(err)
	}
	if a.blocked(r, uint64(id)) {
		t.Fatal("explicitly released permanent block remains active")
	}
}
