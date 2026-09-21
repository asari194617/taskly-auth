package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicClientRegistrationAndLoopbackRedirect(t *testing.T) {
	clients, err := parsePublicClients(`[{"client_id":"taskly-dev","redirect_uri":"http://127.0.0.1/callback"},{"client_id":"taskly-mobile","redirect_uri":"com.example.taskly:/callback"}]`, "taskly-web")
	if err != nil {
		t.Fatal(err)
	}
	a := &app{clientID: "taskly-web", redirectURI: "https://api.example.test/auth/oauth/callback", publicClients: clients}
	for _, test := range []struct {
		clientID string
		uri      string
		want     bool
	}{
		{"taskly-web", "https://api.example.test/auth/oauth/callback", true},
		{"taskly-web", "http://127.0.0.1:9000/callback", false},
		{"taskly-dev", "http://127.0.0.1:49152/callback", true},
		{"taskly-dev", "http://127.0.0.1:49152/other", false},
		{"taskly-dev", "http://localhost:49152/callback", false},
		{"taskly-dev", "http://127.0.0.1:0/callback", false},
		{"taskly-mobile", "com.example.taskly:/callback", true},
		{"taskly-mobile", "com.example.taskly:/other", false},
		{"unknown", "com.example.taskly:/callback", false},
	} {
		if got := a.registeredRedirect(test.clientID, test.uri); got != test.want {
			t.Errorf("%s %s: got %t, want %t", test.clientID, test.uri, got, test.want)
		}
	}
}

func TestInvalidPublicRegistration(t *testing.T) {
	for _, raw := range []string{
		`[{"client_id":"taskly-web","redirect_uri":"https://example.test/callback"}]`,
		`[{"client_id":"x","redirect_uri":"http://example.test/callback"}]`,
		`[{"client_id":"x","redirect_uri":"https://example.test/callback#fragment"}]`,
		`[{"client_id":"x","redirect_uri":"https://example.test/callback"},{"client_id":"x","redirect_uri":"https://other.test/callback"}]`,
		`[{"client_id":"x","redirect_uri":"taskly:/callback"}]`,
	} {
		if _, err := parsePublicClients(raw, "taskly-web"); err == nil {
			t.Errorf("accepted unsafe registration: %s", raw)
		}
	}
}

func TestPublicClientTokenAuthentication(t *testing.T) {
	a := &app{clientID: "taskly-web", clientSecret: "web-secret", publicClients: map[string]string{"taskly-dev": "http://127.0.0.1/callback"}}
	for _, test := range []struct {
		name, auth, form string
		wantStatus       int
		wantClient       string
	}{
		{"public", "", "client_id=taskly-dev", 200, "taskly-dev"},
		{"confidential", "Basic dGFza2x5LXdlYjp3ZWItc2VjcmV0", "", 200, "taskly-web"},
		{"missing", "", "", 401, ""},
		{"public with secret", "", "client_id=taskly-dev&client_secret=bad", 401, ""},
		{"public with bearer", "Bearer bad", "client_id=taskly-dev", 401, ""},
		{"confidential without basic", "", "client_id=taskly-web", 401, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(test.form))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if test.auth != "" {
				r.Header.Set("Authorization", test.auth)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			clientID, ok := a.authenticateTokenClient(w, r)
			if ok != (test.wantStatus == 200) || clientID != test.wantClient {
				t.Fatalf("got client=%q ok=%t", clientID, ok)
			}
			if !ok && w.Code != test.wantStatus {
				t.Fatalf("got status=%d, want=%d", w.Code, test.wantStatus)
			}
		})
	}
}
