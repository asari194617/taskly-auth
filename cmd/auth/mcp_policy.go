package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	mcpPublicClientID  = "nnct-guidance-chatgpt"
	mcpBackendClientID = "nnct-guidance-mcp-backend"
	mcpExchangeGrant   = "urn:ietf:params:oauth:grant-type:token-exchange"
	mcpAccessTokenType = "urn:ietf:params:oauth:token-type:access_token"
)

type mcpOAuthConfig struct{ redirectURI, resource, tasklyResource, backendSecret string }

func parseMCPOAuthConfig(enabled, redirect, resource, tasklyResource, secret string, production bool) (*mcpOAuthConfig, error) {
	if enabled == "0" {
		if redirect != "" || resource != "" || tasklyResource != "" || secret != "" {
			return nil, fmt.Errorf("disabled MCP configuration must be empty")
		}
		return nil, nil
	}
	if enabled != "1" || len(secret) < 32 || resource == tasklyResource || !validMCPURL(redirect, production) || !validMCPURL(resource, production) || !validMCPURL(tasklyResource, production) {
		return nil, fmt.Errorf("invalid MCP OAuth policy")
	}
	r, _ := url.Parse(redirect)
	if r.RawQuery != "" {
		return nil, fmt.Errorf("MCP callback cannot contain a query")
	}
	return &mcpOAuthConfig{redirect, resource, tasklyResource, secret}, nil
}

func validMCPURL(raw string, production bool) bool {
	u, err := url.Parse(raw)
	if err != nil || raw == "" || len(raw) > 2048 || u.User != nil || u.Opaque != "" || u.Fragment != "" || u.RawQuery != "" || u.Hostname() == "" {
		return false
	}
	return u.Scheme == "https" || (!production && u.Scheme == "http" && isLoopbackRedirectHost(u.Hostname()))
}

func mcpScopes(raw string) (string, bool) {
	seen := map[string]bool{}
	for _, scope := range strings.Fields(raw) {
		if scope != "guidance:read" && scope != "guidance:bookmarks:write" && scope != "guidance:tasks:write" {
			return "", false
		}
		if seen[scope] {
			return "", false
		}
		seen[scope] = true
	}
	if !seen["guidance:read"] {
		return "", false
	}
	var scopes []string
	for s := range seen {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	return strings.Join(scopes, " "), true
}

func hasMCPScope(scopes, wanted string) bool {
	for _, s := range strings.Fields(scopes) {
		if s == wanted {
			return true
		}
	}
	return false
}

func singleResource(form url.Values, expected string) bool {
	resources := form["resource"]
	return len(resources) == 1 && resources[0] == expected
}

func (a *app) authenticateMCPBackend(r *http.Request) bool {
	if a.mcp == nil {
		return false
	}
	id, secret, ok := r.BasicAuth()
	return ok && equal(id, mcpBackendClientID) && equal(secret, a.mcp.backendSecret) && (r.Form.Get("client_id") == "" || r.Form.Get("client_id") == id) && r.Form.Get("client_secret") == ""
}

func mcpConsentRedirect(registered, state, issuer, code, failure string) string {
	u, _ := url.Parse(registered)
	q := u.Query()
	q.Set("state", state)
	q.Set("iss", issuer)
	if failure != "" {
		q.Set("error", failure)
	} else {
		q.Set("code", code)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
