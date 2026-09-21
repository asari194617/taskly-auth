package main

import (
	"encoding/json"
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

func TestFatalAlertCompletesBeforeReturn(t *testing.T) {
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode alert: %v", err)
		}
		requests <- body.Content
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	newAlerter(server.URL).sendSync("startup failed")
	select {
	case message := <-requests:
		if message != "startup failed" {
			t.Fatalf("unexpected alert: %q", message)
		}
	default:
		t.Fatal("fatal alert returned before delivery")
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
