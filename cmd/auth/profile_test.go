package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfileSyncSendsOnlyDisplayName(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	var gotPath, gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		data := make([]byte, 1024)
		n, _ := r.Body.Read(data)
		gotBody = string(data[:n])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("TASKLY_PROFILE_SYNC_URL", server.URL)
	t.Setenv("AUTH_INTERNAL_TOKEN", token)
	t.Setenv("NODE_ENV", "development")
	if err := syncTasklyProfile(t.Context(), 42, "テスト"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/internal/auth/users/42/profile" || gotAuth != "Bearer "+token || !strings.Contains(gotBody, `"display_name":"テスト"`) || strings.Contains(gotBody, "email") {
		t.Fatalf("unexpected sync request: %q %q %q", gotPath, gotAuth, gotBody)
	}
}

func TestProfileSyncRejectsUntrustedURL(t *testing.T) {
	t.Setenv("AUTH_INTERNAL_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("NODE_ENV", "production")
	for _, raw := range []string{"http://example.com", "https://user@example.com", "https://example.com/path", "https://example.com?token=secret"} {
		t.Setenv("TASKLY_PROFILE_SYNC_URL", raw)
		if err := syncTasklyProfile(t.Context(), 1, "Name"); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestProfileSyncURLAllowsProductionServiceAlias(t *testing.T) {
	parsed, err := profileSyncURL("http://server:8080", true)
	if err != nil || parsed.String() != "http://server:8080" {
		t.Fatalf("production service URL: parsed=%v err=%v", parsed, err)
	}
	for _, raw := range []string{"http://server.example.com:8080", "http://127.0.0.1:8080", "http://server:8080/path"} {
		if _, err := profileSyncURL(raw, true); err == nil {
			t.Fatalf("accepted untrusted URL %q", raw)
		}
	}
}
