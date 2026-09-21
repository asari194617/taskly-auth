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
