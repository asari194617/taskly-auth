package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryCodeLegacyFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 19 || code[4] != '-' || code[9] != '-' || code[14] != '-' {
			t.Fatalf("invalid code format: %q", code)
		}
		for index, character := range code {
			if index == 4 || index == 9 || index == 14 {
				continue
			}
			if !strings.ContainsRune(recoveryAlphabet, character) {
				t.Fatalf("invalid code character: %q", code)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate recovery code")
		}
		seen[code] = true
	}
}

func TestRecoveryStatusDistinguishesNeverIssuedFromUsed(t *testing.T) {
	for _, test := range []struct {
		name             string
		total, remaining int64
		issued           bool
	}{
		{"never issued", 0, 0, false},
		{"unused codes", 12, 4, true},
		{"all codes used", 3, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &securityTestConnector{recoveryTotal: test.total, recoveryCount: test.remaining}
			a := securityTestApp(state, true)
			defer a.db.Close()
			r := httptest.NewRequest(http.MethodGet, "/auth/recovery/status", nil)
			r.Header.Set("Origin", a.uiURL)
			r.Header.Set("X-Auth-UI", "1")
			r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: "test-session"})
			w := httptest.NewRecorder()
			a.recoveryStatus(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			var body struct {
				Issued    bool `json:"issued"`
				Remaining int  `json:"remaining"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Issued != test.issued || body.Remaining != int(test.remaining) {
				t.Fatalf("body = %+v, err = %v", body, err)
			}
			if state.recoveryStatusQueries != 1 || !strings.Contains(state.recoveryStatusQuery, "WHERE user_id=?") || state.recoveryStatusUserID != int64(7) {
				t.Fatalf("recovery status is not scoped to the session user: query=%q user=%v", state.recoveryStatusQuery, state.recoveryStatusUserID)
			}
		})
	}
}

func TestRecoveryStatusRequiresAuthenticatedUIAndFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, cookie, origin string
		storageFails         bool
		wantStatus           int
		wantQueries          int
	}{
		{"missing session", "", "http://localhost:3068", false, http.StatusUnauthorized, 0},
		{"invalid session", "invalid-session", "http://localhost:3068", false, http.StatusUnauthorized, 0},
		{"other origin", "test-session", "http://other.example.test", false, http.StatusForbidden, 0},
		{"storage failure", "test-session", "http://localhost:3068", true, http.StatusServiceUnavailable, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &securityTestConnector{failRecoveryStatus: test.storageFails}
			a := securityTestApp(state, true)
			defer a.db.Close()
			r := httptest.NewRequest(http.MethodGet, "/auth/recovery/status", nil)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("X-Auth-UI", "1")
			if test.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: test.cookie})
			}
			w := httptest.NewRecorder()
			a.recoveryStatus(w, r)
			if w.Code != test.wantStatus || state.recoveryStatusQueries != test.wantQueries {
				t.Fatalf("status=%d queries=%d, want status=%d queries=%d: %s", w.Code, state.recoveryStatusQueries, test.wantStatus, test.wantQueries, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "remaining") || strings.Contains(w.Body.String(), "issued") {
				t.Fatalf("failure disclosed recovery status: %s", w.Body.String())
			}
		})
	}
}
