package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateLifecycleSync(t *testing.T) {
	now := time.Date(2026, 9, 24, 1, 2, 3, 0, time.UTC)
	truth := true
	falsehood := false
	tests := []struct {
		name      string
		input     lifecycleSyncRequest
		want      time.Time
		wantError bool
	}{
		{name: "deleted", input: lifecycleSyncRequest{Deleted: &truth, DeletedAt: "2026-09-24T00:00:00+09:00"}, want: time.Date(2026, 9, 23, 15, 0, 0, 0, time.UTC)},
		{name: "missing deleted", input: lifecycleSyncRequest{DeletedAt: "2026-09-24T00:00:00Z"}, wantError: true},
		{name: "restore", input: lifecycleSyncRequest{Deleted: &falsehood}},
		{name: "restore with deletion date", input: lifecycleSyncRequest{Deleted: &falsehood, DeletedAt: "2026-09-24T00:00:00Z"}, wantError: true},
		{name: "invalid time", input: lifecycleSyncRequest{Deleted: &truth, DeletedAt: "today"}, wantError: true},
		{name: "future", input: lifecycleSyncRequest{Deleted: &truth, DeletedAt: now.Add(time.Second).Format(time.RFC3339)}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateLifecycleSync(test.input, now)
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v, wantError %v", err, test.wantError)
			}
			if err == nil && !got.Equal(test.want) {
				t.Fatalf("deletedAt = %s, want %s", got, test.want)
			}
		})
	}
}

func TestLifecycleRoutesRequireInternalAuthorization(t *testing.T) {
	a := &app{internalToken: "test-internal-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /internal/users/{id}/lifecycle", a.syncUserLifecycle)
	mux.HandleFunc("DELETE /internal/users/{id}", a.eraseUser)

	request := httptest.NewRequest(http.MethodPut, "/internal/users/42/lifecycle", strings.NewReader(`{"deleted":true,"deleted_at":"2026-09-24T00:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("PUT status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodDelete, "/internal/users/42", nil)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("DELETE status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodPut, "/internal/users/42/lifecycle", strings.NewReader(`{"deleted":false,"deleted_at":"2026-09-24T00:00:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-internal-token")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
