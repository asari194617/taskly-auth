package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserSyncRoutesRequireInternalAuthorization(t *testing.T) {
	a := &app{internalToken: "test-internal-token"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /internal/users", a.provisionUser)
	mux.HandleFunc("PATCH /internal/users/{id}", a.updateUserProjection)

	request := httptest.NewRequest(http.MethodPost, "/internal/users", strings.NewReader(`{"email":"user@example.test"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("POST status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPatch, "/internal/users/42", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test-internal-token")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("empty PATCH status = %d", response.Code)
	}
}
