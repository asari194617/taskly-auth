package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func boolPointer(value bool) *bool { return &value }

func TestValidateBlockSync(t *testing.T) {
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Format(time.RFC3339)
	past := now.Add(-time.Hour).Format(time.RFC3339)
	reason := "  abuse  "
	longReason := string(make([]byte, 1001))

	tests := []struct {
		name       string
		input      blockSyncRequest
		wantActive bool
		wantReason string
		wantExpiry bool
		wantError  bool
	}{
		{name: "missing active", input: blockSyncRequest{}, wantError: true},
		{name: "unblock", input: blockSyncRequest{Active: boolPointer(false)}},
		{name: "unblock with details", input: blockSyncRequest{Active: boolPointer(false), Reason: &reason}, wantError: true},
		{name: "permanent block", input: blockSyncRequest{Active: boolPointer(true), Reason: &reason}, wantActive: true, wantReason: "abuse"},
		{name: "temporary block", input: blockSyncRequest{Active: boolPointer(true), ExpiresAt: &future}, wantActive: true, wantExpiry: true},
		{name: "expired block", input: blockSyncRequest{Active: boolPointer(true), ExpiresAt: &past}, wantError: true},
		{name: "long reason", input: blockSyncRequest{Active: boolPointer(true), Reason: &longReason}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			active, gotReason, expiresAt, err := validateBlockSync(test.input, now)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %v", err, test.wantError)
			}
			if err != nil {
				return
			}
			if active != test.wantActive {
				t.Fatalf("active = %v, want %v", active, test.wantActive)
			}
			if test.wantReason == "" && gotReason != nil {
				t.Fatalf("reason = %q, want nil", *gotReason)
			}
			if test.wantReason != "" && (gotReason == nil || *gotReason != test.wantReason) {
				t.Fatalf("reason = %v, want %q", gotReason, test.wantReason)
			}
			if (expiresAt != nil) != test.wantExpiry {
				t.Fatalf("expiresAt = %v, want present %v", expiresAt, test.wantExpiry)
			}
		})
	}
}

func TestSyncBlockRequiresInternalAuthorization(t *testing.T) {
	a := &app{internalToken: "test-internal-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /internal/users/{id}/block", a.syncBlock)

	request := httptest.NewRequest(http.MethodPut, "/internal/users/42/block", strings.NewReader(`{"active":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodPut, "/internal/users/42/block", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-internal-token")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
