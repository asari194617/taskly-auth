package main

import (
	"strings"
	"testing"
)

func TestValidateAuthDSNRequiresUTCDateTimeParsing(t *testing.T) {
	tests := []struct {
		name    string
		dsn     string
		wantErr string
	}{
		{
			name: "valid",
			dsn:  "auth:secret@tcp(db:3306)/taskly_auth?parseTime=true&charset=utf8mb4&loc=UTC",
		},
		{
			name:    "invalid",
			dsn:     "not-a-dsn",
			wantErr: "valid MySQL DSN",
		},
		{
			name:    "parse time disabled",
			dsn:     "auth:secret@tcp(db:3306)/taskly_auth?parseTime=false&loc=UTC",
			wantErr: "parseTime=true",
		},
		{
			name:    "local timezone",
			dsn:     "auth:secret@tcp(db:3306)/taskly_auth?parseTime=true&loc=Asia%2FTokyo",
			wantErr: "loc=UTC",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAuthDSN(test.dsn)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAuthDSN() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateAuthDSN() error = %v, want text %q", err, test.wantErr)
			}
		})
	}
}
