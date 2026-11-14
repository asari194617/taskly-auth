package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestMCPExchangeExpirySecondBoundaries(t *testing.T) {
	base := time.Date(2035, time.November, 14, 12, 0, 0, 0, time.UTC)
	for _, fraction := range []time.Duration{0, 499999 * time.Microsecond, 500000 * time.Microsecond, 999999 * time.Microsecond} {
		t.Run(fraction.String(), func(t *testing.T) {
			now := base.Add(fraction)
			parent := now.Add(15 * time.Minute)
			got := mcpExchangeExpiry(now, parent)
			want := base.Add(time.Minute)
			if !got.Equal(want) || got.Nanosecond() != 0 || got.After(now.Add(time.Minute)) || got.After(parent) || got.Unix() > now.Unix()+60 {
				t.Fatalf("expiry=%s, want whole-second expiry %s within both limits", got, want)
			}
		})
	}
	for _, remaining := range []time.Duration{20*time.Second + 999999*time.Microsecond, 999999 * time.Microsecond, -time.Second} {
		t.Run("parent-"+remaining.String(), func(t *testing.T) {
			now := base.Add(500000 * time.Microsecond)
			parent := now.Add(remaining)
			want := parent.UTC().Truncate(time.Second)
			got := mcpExchangeExpiry(now, parent)
			if !got.Equal(want) || got.After(parent) || got.After(now.Add(time.Minute)) {
				t.Fatalf("expiry=%s exceeds or loses parent bound %s", got, parent)
			}
		})
	}
}

func TestMCPExchangeRejectsSubsecondParentLifetime(t *testing.T) {
	a := mcpTestApp()
	parent := mcpTokenStep("guidance:read")
	parent.values[0][6] = time.Now().UTC().Add(500 * time.Millisecond)
	a.db = mcpScriptDB(t, parent)
	w := httptest.NewRecorder()
	form := mcpExchangeForm()
	form.Set("scope", "guidance:tasks:read")
	a.token(w, mcpFormRequest(form, true))
	if w.Code != 400 {
		t.Fatalf("subsecond parent minted a token: status=%d", w.Code)
	}
}
