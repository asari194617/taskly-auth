package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type publicClientRegistration struct {
	ClientID    string `json:"client_id"`
	RedirectURI string `json:"redirect_uri"`
}

func parsePublicClients(raw, confidentialID string) (map[string]string, error) {
	clients := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return clients, nil
	}
	var registrations []publicClientRegistration
	if err := json.Unmarshal([]byte(raw), &registrations); err != nil {
		return nil, fmt.Errorf("invalid public client registration")
	}
	for _, registration := range registrations {
		id, redirect := registration.ClientID, registration.RedirectURI
		if id == "" || len(id) > 128 || id == confidentialID || redirect == "" || len(redirect) > 2048 {
			return nil, fmt.Errorf("invalid or duplicate public client ID")
		}
		if _, exists := clients[id]; exists || !validPublicRedirect(redirect) {
			return nil, fmt.Errorf("invalid or duplicate public client redirect")
		}
		clients[id] = redirect
	}
	return clients, nil
}

func validPublicRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil || u.Opaque != "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Hostname() != ""
	case "http":
		return isLoopbackRedirectHost(u.Hostname())
	default:
		return strings.Contains(u.Scheme, ".") && u.Host == "" && u.Path != ""
	}
}

func isLoopbackRedirectHost(host string) bool {
	return host == "127.0.0.1" || host == "[::1]" || host == "::1"
}

func (a *app) registeredRedirect(clientID, redirect string) bool {
	if a.mcp != nil && clientID == mcpPublicClientID {
		return redirect == a.mcp.redirectURI
	}
	if clientID == a.clientID {
		return redirect == a.redirectURI
	}
	registered, ok := a.publicClients[clientID]
	if !ok {
		return false
	}
	if redirect == registered {
		return true
	}
	base, err := url.Parse(registered)
	if err != nil || base.Scheme != "http" || !isLoopbackRedirectHost(base.Hostname()) {
		return false
	}
	actual, err := url.Parse(redirect)
	if err != nil || actual.Scheme != "http" || actual.User != nil || actual.Fragment != "" || actual.Opaque != "" {
		return false
	}
	port, err := strconv.Atoi(actual.Port())
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	return base.Hostname() == actual.Hostname() && base.EscapedPath() == actual.EscapedPath() && base.RawQuery == actual.RawQuery
}
