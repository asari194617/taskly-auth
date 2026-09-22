package main

import (
	"database/sql"
	"testing"
)

func TestDisablingEmailCodeKeepsPrimaryLoginMethod(t *testing.T) {
	for _, test := range []struct {
		name     string
		hash     sql.NullString
		passkeys int
		want     bool
	}{
		{"none", sql.NullString{}, 0, false},
		{"empty hash", sql.NullString{Valid: true}, 0, false},
		{"password", sql.NullString{String: "bcrypt-hash", Valid: true}, 0, true},
		{"passkey", sql.NullString{}, 1, true},
	} {
		if got := canDisableEmailCode(test.hash, test.passkeys); got != test.want {
			t.Errorf("%s: got %v, want %v", test.name, got, test.want)
		}
	}
}
