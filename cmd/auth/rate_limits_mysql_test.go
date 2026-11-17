package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestAuthenticationSourceLimitDoesNotExhaustOtherSourceOnMySQL(t *testing.T) {
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
	a := &app{db: db}
	accountID := uint64(time.Now().UnixNano())
	octet := time.Now().UnixNano()%100 + 20
	source1 := fmt.Sprintf("203.0.113.%d", octet)
	source2 := fmt.Sprintf("203.0.113.%d", octet+100)
	t.Cleanup(func() {
		for _, key := range []string{"auth-source:" + source1, "auth-source:" + source2, "auth-account:" + strconv.FormatUint(accountID, 10)} {
			_, _ = db.Exec(`DELETE FROM auth_rate_limits WHERE key_hash=?`, digest(key))
		}
	})
	request := func(source string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		r.RemoteAddr = source + ":1234"
		r.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", time.Now().UnixNano()%100))
		return r
	}
	for i := 0; i < 30; i++ {
		limited, err := a.authAttemptLimited(request(source1), accountID)
		if err != nil || limited {
			t.Fatalf("attempt %d: limited=%v error=%v", i+1, limited, err)
		}
	}
	limited, err := a.authAttemptLimited(request(source1), accountID)
	if err != nil || !limited {
		t.Fatalf("spoofed proxy headers bypassed source budget: limited=%v error=%v", limited, err)
	}
	limited, err = a.authAttemptLimited(request(source2), accountID)
	if err != nil || limited {
		t.Fatalf("other source could not authenticate: limited=%v error=%v", limited, err)
	}
}

func TestExpiredAnonymousRowsAreCollectedWithoutRemovingLiveFlowOnMySQL(t *testing.T) {
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
	oldID := digest(fmt.Sprintf("expired-%d", time.Now().UnixNano()))
	liveID := digest(fmt.Sprintf("live-%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM oauth_authorization_requests WHERE id_hash IN (?,?)`, oldID, liveID)
	})
	for _, item := range []struct {
		id      string
		expires time.Time
	}{{oldID, time.Now().UTC().Add(-time.Hour)}, {liveID, time.Now().UTC().Add(10 * time.Minute)}} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO oauth_authorization_requests (id_hash,client_id,redirect_uri,state_value,code_challenge,scope_value,expires_at) VALUES (?,'test','https://example.test/callback','state','challenge','taskly:login',?)`, item.id, item.expires); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&app{db: db}).cleanupExpiredRows(t.Context()); err != nil {
		t.Fatal(err)
	}
	var oldCount, liveCount int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_authorization_requests WHERE id_hash=?`, oldID).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM oauth_authorization_requests WHERE id_hash=?`, liveID).Scan(&liveCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || liveCount != 1 {
		t.Fatalf("cleanup state: expired=%d live=%d", oldCount, liveCount)
	}
}
