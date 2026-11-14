package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMCPExchangeExpiryMySQLSecondPrecision(t *testing.T) {
	f := newMCPMySQLFixture(t)
	uid, _ := f.user()
	conn, err := f.a.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var originalMode string
	if err := conn.QueryRowContext(t.Context(), "SELECT @@SESSION.sql_mode").Scan(&originalMode); err != nil {
		t.Fatal(err)
	}
	// Exercise MySQL's documented default rounding, independent of the host's
	// optional truncation mode. Only this test connection's mode is changed.
	var modes []string
	for _, mode := range strings.Split(originalMode, ",") {
		if mode != "TIME_TRUNCATE_FRACTIONAL" {
			modes = append(modes, mode)
		}
	}
	if _, err := conn.ExecContext(t.Context(), "SET SESSION sql_mode=?", strings.Join(modes, ",")); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.ExecContext(context.Background(), "SET SESSION sql_mode=?", originalMode); err != nil {
			t.Error(err)
		}
	}()
	base := time.Date(2035, time.November, 14, 12, 0, 0, 0, time.UTC)
	roundTrip := func(name string, expiry time.Time) time.Time {
		t.Helper()
		hash := digest(name)
		_, err := conn.ExecContext(t.Context(), `INSERT INTO oauth_access_tokens (token_hash,user_id,client_id,scope_value,expires_at) VALUES (?,?,?,?,?)`, hash, uid, mcpTestBackendClient, "guidance:tasks:read", expiry)
		if err != nil {
			t.Fatal(err)
		}
		var stored time.Time
		if err := conn.QueryRowContext(t.Context(), `SELECT expires_at FROM oauth_access_tokens WHERE token_hash=?`, hash).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	legacyNow := base.Add(500000 * time.Microsecond)
	legacyStored := roundTrip("legacy-fractional-expiry", legacyNow.Add(time.Minute))
	if legacyStored.Unix() != legacyNow.Unix()+61 {
		t.Fatalf("default MySQL rounding did not reproduce the old 61-second expiry: stored=%s", legacyStored)
	}
	for _, fraction := range []time.Duration{0, 499999 * time.Microsecond, 500000 * time.Microsecond, 999999 * time.Microsecond} {
		t.Run(fraction.String(), func(t *testing.T) {
			now := base.Add(fraction)
			parent := now.Add(15 * time.Minute)
			expiry := mcpExchangeExpiry(now, parent)
			stored := roundTrip("fixed-"+fraction.String(), expiry)
			if !stored.Equal(expiry) || stored.Nanosecond() != 0 || stored.Unix() > now.Unix()+60 || stored.After(parent) {
				t.Fatalf("persisted expiry exceeded policy: now=%s stored=%s parent=%s", now, stored, parent)
			}
		})
	}
	parent := base.Add(20*time.Second + 999999*time.Microsecond)
	stored := roundTrip("fractional-parent-expiry", mcpExchangeExpiry(base, parent))
	if stored.After(parent) || !stored.Equal(parent.Truncate(time.Second)) {
		t.Fatalf("persisted expiry exceeded shorter parent: stored=%s parent=%s", stored, parent)
	}
}
