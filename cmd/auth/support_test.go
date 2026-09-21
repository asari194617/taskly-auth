package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiscordAlertUsesAcceptedUserAgent(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	newAlerter(server.URL).send("staging check")
	select {
	case request := <-requests:
		if got := request.Header.Get("User-Agent"); got != "TasklyAuth/1.0" {
			t.Fatalf("unexpected Discord user agent: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Discord alert was not sent")
	}
}

func TestProductionMigrationGate(t *testing.T) {
	a := &app{ready: false}
	handler := a.wrapAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/auth/login", want: http.StatusServiceUnavailable},
		{path: "/oauth/authorize", want: http.StatusServiceUnavailable},
		{path: "/oauth/token", want: http.StatusServiceUnavailable},
		{path: "/.well-known/oauth-authorization-server", want: http.StatusOK},
		{path: "/healthz", want: http.StatusOK},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("%s: got %d, want %d", test.path, response.Code, test.want)
		}
	}
}
