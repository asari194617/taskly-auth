package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sort"
	"strings"
	"testing"
	"time"
)

type storedHistory struct {
	userID uint64
	entry  loginHistoryEntry
}

type historyTestConnector struct {
	sessions          map[string]uint64
	history           []storedHistory
	failHistoryInsert bool
	commits           int
	rollbacks         int
	lastHistoryQuery  string
	lastHistoryArgs   []driver.NamedValue
	currentHash       string
	currentEmail      string
	currentEpoch      int64
	blocked           bool
}

func (c *historyTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &historyTestConn{state: c}, nil
}
func (c *historyTestConnector) Driver() driver.Driver { return historyTestDriver{} }

type historyTestDriver struct{}

func (historyTestDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type historyTestConn struct {
	state          *historyTestConnector
	transaction    bool
	pendingSession string
	pendingUserID  uint64
	pendingHistory *storedHistory
}

func (*historyTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*historyTestConn) Close() error { return nil }
func (c *historyTestConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *historyTestConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.transaction {
		return nil, errors.New("nested transaction")
	}
	c.transaction = true
	return &historyTestTx{conn: c}, nil
}

type historyTestTx struct{ conn *historyTestConn }

func (t *historyTestTx) Commit() error {
	c := t.conn
	if c.pendingSession != "" {
		if c.state.sessions == nil {
			c.state.sessions = make(map[string]uint64)
		}
		c.state.sessions[c.pendingSession] = c.pendingUserID
	}
	if c.pendingHistory != nil {
		c.state.history = append(c.state.history, *c.pendingHistory)
	}
	c.state.commits++
	c.clear()
	return nil
}

func (t *historyTestTx) Rollback() error {
	t.conn.state.rollbacks++
	t.conn.clear()
	return nil
}

func (c *historyTestConn) clear() {
	c.transaction = false
	c.pendingSession = ""
	c.pendingUserID = 0
	c.pendingHistory = nil
}

func (c *historyTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	switch {
	case strings.Contains(query, "UPDATE users SET is_registered=1"):
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "INSERT INTO auth_sessions"):
		if !c.transaction {
			return nil, errors.New("session insert outside transaction")
		}
		c.pendingSession = args[0].Value.(string)
		c.pendingUserID = uint64(args[1].Value.(int64))
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "INSERT INTO auth_login_history"):
		if !c.transaction {
			return nil, errors.New("history insert outside transaction")
		}
		if c.state.failHistoryInsert {
			return nil, errors.New("history insert failed")
		}
		entry := loginHistoryEntry{
			ID: uint64(len(c.state.history) + 1), Method: args[1].Value.(string),
			LoggedInAt: args[2].Value.(time.Time), IPAddress: ptrFromValue(args[3].Value),
			Device: ptrFromValue(args[7].Value), IPSource: ptrFromValue(args[8].Value),
			GeoSource: ptrFromValue(args[9].Value), GeoRelease: ptrFromValue(args[10].Value), GeoDBSHA256: ptrFromValue(args[11].Value),
		}
		city, region, country := ptrFromValue(args[4].Value), ptrFromValue(args[5].Value), ptrFromValue(args[6].Value)
		if city != nil || region != nil || country != nil {
			entry.Location = &loginLocation{City: stringValue(city), Region: stringValue(region), Country: stringValue(country)}
		}
		c.pendingHistory = &storedHistory{userID: uint64(args[0].Value.(int64)), entry: entry}
		return driver.RowsAffected(1), nil
	default:
		return nil, errors.New("unexpected exec: " + query)
	}
}

func ptrFromValue(value driver.Value) *string {
	if value == nil {
		return nil
	}
	s := value.(string)
	return &s
}

func (c *historyTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT is_deleted,auth_epoch,hashed_password,email,email_code_allowed FROM users"):
		return &historyTestRows{columns: []string{"is_deleted", "auth_epoch", "hashed_password", "email", "email_code_allowed"}, values: [][]driver.Value{{false, c.state.currentEpoch, c.state.currentHash, c.state.currentEmail, true}}}, nil
	case strings.Contains(query, "SELECT COUNT(*) FROM blocks"):
		count := int64(0)
		if c.state.blocked {
			count = 1
		}
		return &historyTestRows{columns: []string{"count"}, values: [][]driver.Value{{count}}}, nil
	case strings.Contains(query, "SELECT is_deleted FROM users"):
		return &historyTestRows{columns: []string{"is_deleted"}, values: [][]driver.Value{{false}}}, nil
	case strings.Contains(query, "FROM auth_sessions s JOIN users"):
		userID, ok := c.state.sessions[args[0].Value.(string)]
		if !ok {
			return &historyTestRows{columns: []string{"user_id"}}, nil
		}
		return &historyTestRows{columns: []string{"user_id"}, values: [][]driver.Value{{int64(userID)}}}, nil
	case strings.Contains(query, "FROM auth_login_history"):
		c.state.lastHistoryQuery = query
		c.state.lastHistoryArgs = append([]driver.NamedValue(nil), args...)
		userID := uint64(args[0].Value.(int64))
		ordered := make([]loginHistoryEntry, 0)
		for _, stored := range c.state.history {
			if stored.userID == userID {
				ordered = append(ordered, stored.entry)
			}
		}
		desc := strings.Contains(query, "ORDER BY logged_in_at DESC")
		sort.Slice(ordered, func(i, j int) bool {
			if !ordered[i].LoggedInAt.Equal(ordered[j].LoggedInAt) {
				if desc {
					return ordered[i].LoggedInAt.After(ordered[j].LoggedInAt)
				}
				return ordered[i].LoggedInAt.Before(ordered[j].LoggedInAt)
			}
			if desc {
				return ordered[i].ID > ordered[j].ID
			}
			return ordered[i].ID < ordered[j].ID
		})
		if strings.Contains(query, "LIMIT ?") && len(ordered) > int(args[1].Value.(int64)) {
			ordered = ordered[:int(args[1].Value.(int64))]
		}
		values := make([][]driver.Value, 0, len(ordered))
		for _, entry := range ordered {
			var city, region, country driver.Value
			if entry.Location != nil {
				city, region, country = entry.Location.City, entry.Location.Region, entry.Location.Country
			}
			values = append(values, []driver.Value{int64(entry.ID), entry.Method, entry.LoggedInAt,
				valueFromPtr(entry.IPAddress), city, region, country, valueFromPtr(entry.Device), valueFromPtr(entry.IPSource),
				valueFromPtr(entry.GeoSource), valueFromPtr(entry.GeoRelease), valueFromPtr(entry.GeoDBSHA256)})
		}
		return &historyTestRows{columns: []string{"id", "method", "logged_in_at", "ip_address", "geo_city", "geo_region", "geo_country", "user_agent", "ip_source", "geo_source", "geo_db_release", "geo_db_sha256"}, values: values}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
}

func valueFromPtr(value *string) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

type historyTestRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *historyTestRows) Columns() []string { return r.columns }
func (*historyTestRows) Close() error        { return nil }
func (r *historyTestRows) Next(dest []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}

func TestSecondFactorMethod(t *testing.T) {
	for _, test := range []struct {
		input loginInput
		want  string
	}{
		{loginInput{Code: "123456"}, "email_code"},
		{loginInput{TOTP: "123456"}, "totp"},
		{loginInput{Recovery: "recovery"}, "recovery_code"},
	} {
		if got := secondFactorMethod(test.input); got != test.want {
			t.Errorf("secondFactorMethod(%+v) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestLoginRequestEvidenceDirectSocketAndSanitization(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "8.8.8.8:54321"
	r.Header.Set("X-Auth-Client-IP", "2001:db8::1")
	r.Header.Set("X-Auth-Geo-City", "Spoofed location must be ignored")
	r.Header.Set("X-Forwarded-For", "1.1.1.1")
	r.Header.Set("X-Real-IP", "1.1.1.1")
	r.Header.Set("User-Agent", "ExampleBrowser/1.0")
	ip, device, source := loginRequestEvidence(r, netip.MustParseAddr("172.18.0.1"))
	if stringValue(ip) != "8.8.8.8" || stringValue(device) != "ExampleBrowser/1.0" || stringValue(source) != "direct_socket" {
		t.Fatalf("untrusted proxy headers were accepted: ip=%v source=%v", ip, source)
	}
	r.RemoteAddr = "172.18.0.2:54321"
	ip, _, source = loginRequestEvidence(r, netip.MustParseAddr("172.18.0.1"))
	if ip != nil || source != nil {
		t.Fatalf("untrusted Docker peer headers were accepted: ip=%v source=%v", ip, source)
	}
	if got := cleanHistoryText("abc\r\n=HYPERLINK(\"x\")", 100); got != "abc=HYPERLINK(\"x\")" {
		t.Fatalf("history text sanitization = %q", got)
	}
}

func TestLoginRequestEvidenceExistingNginxFallback(t *testing.T) {
	trustedPeer := netip.MustParseAddr("172.18.0.1")
	for _, test := range []struct {
		name, peer, realIP, cfIP, wantIP, wantSource string
	}{
		{"Cloudflare IPv4", "172.18.0.1:5000", "173.245.48.10", "203.0.113.10", "203.0.113.10", "cloudflare"},
		{"Cloudflare IPv6", "172.18.0.1:5000", "2606:4700::1", "2001:db8::10", "2001:db8::10", "cloudflare"},
		{"direct origin ignores forged CF and X-Auth", "172.18.0.1:5000", "198.51.100.8", "203.0.113.10", "198.51.100.8", "nginx_remote_addr"},
		{"invalid real IP", "172.18.0.1:5000", "garbage", "203.0.113.10", "", ""},
		{"missing Cloudflare visitor IP", "172.18.0.1:5000", "173.245.48.10", "", "", ""},
		{"invalid Cloudflare visitor IP", "172.18.0.1:5000", "173.245.48.10", "127.0.0.1", "", ""},
		{"Cloudflare pseudo IPv4", "172.18.0.1:5000", "173.245.48.10", "240.1.2.3", "", ""},
		{"sibling Docker container", "172.18.0.2:5000", "173.245.48.10", "203.0.113.10", "", ""},
		{"wrong configured gateway", "172.18.0.3:5000", "173.245.48.10", "203.0.113.10", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			r.RemoteAddr = test.peer
			r.Header.Set("X-Real-IP", test.realIP)
			r.Header.Set("CF-Connecting-IP", test.cfIP)
			r.Header.Set("X-Forwarded-For", "192.0.2.200")
			r.Header.Set("X-Auth-Client-IP", "198.51.100.55")
			r.Header.Set("X-Auth-IP-Source", "cloudflare")
			r.Header.Set("X-Auth-Geo-City", "Fake City")
			ip, _, source := loginRequestEvidence(r, trustedPeer)
			if stringValue(ip) != test.wantIP || stringValue(source) != test.wantSource {
				t.Fatalf("fallback evidence: ip=%q source=%q, want ip=%q source=%q", stringValue(ip), stringValue(source), test.wantIP, test.wantSource)
			}
		})
	}
}

func TestHistoryTrustedProxyPeerValidationAndCloudflareRanges(t *testing.T) {
	for _, value := range []string{"172.18.0.1/32", "8.8.8.8", "fe80::1%eth0", "not an IP"} {
		if _, err := parseHistoryTrustedProxyPeer(value); err == nil {
			t.Errorf("accepted invalid trusted peer %q", value)
		}
	}
	if ip, err := parseHistoryTrustedProxyPeer("172.18.0.1"); err != nil || ip.String() != "172.18.0.1" {
		t.Fatalf("valid Docker gateway rejected: ip=%v err=%v", ip, err)
	}
	if ip, err := parseHistoryTrustedProxyPeer(""); err != nil || ip.IsValid() {
		t.Fatalf("empty trusted peer should disable fallback: ip=%v err=%v", ip, err)
	}
	for _, test := range []struct {
		ip   string
		want bool
	}{
		{"173.245.48.10", true}, {"2606:4700::1", true},
		{"198.51.100.8", false}, {"2001:db8::8", false},
	} {
		if got := isCloudflareAddress(netip.MustParseAddr(test.ip)); got != test.want {
			t.Errorf("isCloudflareAddress(%s)=%v, want %v", test.ip, got, test.want)
		}
	}
}

func TestFinishLoginCommitsSessionAndEvidenceTogether(t *testing.T) {
	for _, failHistory := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "history failure"}[failHistory], func(t *testing.T) {
			state := &historyTestConnector{failHistoryInsert: failHistory}
			a := &app{db: sql.OpenDB(state), alerts: &alerter{}, historyTrustedProxyPeer: netip.MustParseAddr("172.18.0.1"), geoDB: makeTestGeoDB(t)}
			defer a.db.Close()
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			r.RemoteAddr = "172.18.0.1:1234"
			r.Header.Set("X-Real-IP", "173.245.48.10")
			r.Header.Set("CF-Connecting-IP", "8.8.8.8")
			r.Header.Set("X-Auth-Geo-City", "Spoofed")
			r.Header.Set("X-Auth-Geo-Country", "ZZ")
			r.Header.Set("User-Agent", "ExampleBrowser/1.0")
			w := httptest.NewRecorder()
			a.finishLogin(w, r, 7, "passkey")
			if failHistory {
				if w.Code != http.StatusInternalServerError || len(w.Result().Cookies()) != 0 || len(state.sessions) != 0 || len(state.history) != 0 || state.commits != 0 || state.rollbacks != 1 {
					t.Fatalf("failed history insert left a login: status=%d cookies=%d sessions=%d history=%d commits=%d rollbacks=%d", w.Code, len(w.Result().Cookies()), len(state.sessions), len(state.history), state.commits, state.rollbacks)
				}
				return
			}
			if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 || len(state.sessions) != 1 || len(state.history) != 1 || state.commits != 1 {
				t.Fatalf("login was not committed atomically: status=%d cookies=%d sessions=%d history=%d commits=%d", w.Code, len(w.Result().Cookies()), len(state.sessions), len(state.history), state.commits)
			}
			entry := state.history[0].entry
			if entry.Method != "passkey" || time.Since(entry.LoggedInAt) > time.Minute || stringValue(entry.IPAddress) != "8.8.8.8" || stringValue(entry.IPSource) != "cloudflare" || entry.Location == nil || entry.Location.City != "Mountain View" || entry.Location.Region != "California" || entry.Location.Country != "US" || stringValue(entry.Device) != "ExampleBrowser/1.0" || stringValue(entry.GeoSource) != geoIPSource || stringValue(entry.GeoRelease) != "2026-10" || stringValue(entry.GeoDBSHA256) != a.geoDB.sha256 {
				t.Fatalf("unexpected history evidence: %+v", entry)
			}
		})
	}
}

func TestFinishLoginPersistsDirectSocketEvidence(t *testing.T) {
	state := &historyTestConnector{}
	a := &app{db: sql.OpenDB(state), alerts: &alerter{}, historyTrustedProxyPeer: netip.MustParseAddr("172.18.0.1"), geoDB: makeTestGeoDB(t)}
	defer a.db.Close()
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "8.8.8.8:5000"
	r.Header.Set("X-Real-IP", "173.245.48.10")
	r.Header.Set("CF-Connecting-IP", "1.1.1.1")
	r.Header.Set("X-Auth-Client-IP", "198.51.100.55")
	r.Header.Set("X-Auth-Geo-City", "Spoofed")
	r.Header.Set("User-Agent", "ExampleBrowser/1.0")
	w := httptest.NewRecorder()
	a.finishLogin(w, r, 7, "password")
	if w.Code != http.StatusOK || len(state.history) != 1 {
		t.Fatalf("fallback login failed: status=%d history=%d", w.Code, len(state.history))
	}
	entry := state.history[0].entry
	if stringValue(entry.IPAddress) != "8.8.8.8" || stringValue(entry.IPSource) != "direct_socket" || entry.Location == nil || entry.Location.City != "Mountain View" || stringValue(entry.GeoSource) != geoIPSource || stringValue(entry.GeoRelease) != "2026-10" || stringValue(entry.GeoDBSHA256) != a.geoDB.sha256 || stringValue(entry.Device) != "ExampleBrowser/1.0" {
		t.Fatalf("direct socket persisted wrong evidence: %+v", entry)
	}
}

func TestFinishLoginRejectsChangedCredentialOrBlockBeforeSessionInsert(t *testing.T) {
	for _, test := range []struct {
		name    string
		hash    string
		blocked bool
		epoch   int64
	}{
		{"password changed", "new-hash", false, 0},
		{"block added", "old-hash", true, 0},
		{"MFA or passkey changed", "old-hash", false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &historyTestConnector{currentHash: test.hash, currentEmail: "user@example.test", blocked: test.blocked, currentEpoch: test.epoch}
			a := &app{db: sql.OpenDB(state), alerts: &alerter{}}
			defer a.db.Close()
			w := httptest.NewRecorder()
			a.finishLoginWithProof(w, httptest.NewRequest(http.MethodPost, "/auth/login", nil), 7, "password", loginProof{passwordHash: "old-hash", passwordHashSet: true, email: "user@example.test"})
			if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 || len(state.sessions) != 0 || state.commits != 0 {
				t.Fatalf("stale authentication issued a session: status=%d sessions=%d commits=%d", w.Code, len(state.sessions), state.commits)
			}
		})
	}
}

func historyRequest(path, cookie, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("X-Auth-UI", "1")
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: cookie})
	}
	return r
}

func TestLoginHistoryAndCSVAuthorizationScopeAndFullExport(t *testing.T) {
	state := &historyTestConnector{sessions: map[string]uint64{digest("my-session"): 7}}
	old := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= 55; i++ {
		state.history = append(state.history, storedHistory{userID: 7, entry: loginHistoryEntry{ID: uint64(i), Method: "password", LoggedInAt: old.Add(time.Duration(i) * time.Second)}})
	}
	injection := "=HYPERLINK(\"https://bad.example\")"
	state.history[0].entry.Device = &injection
	geoSource, geoRelease, geoHash := geoIPSource, "2026-10", strings.Repeat("a", 64)
	state.history[0].entry.Location = &loginLocation{City: "Mountain View", Region: "California", Country: "US"}
	state.history[0].entry.GeoSource = &geoSource
	state.history[0].entry.GeoRelease = &geoRelease
	state.history[0].entry.GeoDBSHA256 = &geoHash
	state.history = append(state.history, storedHistory{userID: 8, entry: loginHistoryEntry{ID: 56, Method: "passkey", LoggedInAt: old}})
	a := &app{db: sql.OpenDB(state), uiURL: "https://auth.example.test"}
	defer a.db.Close()
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/auth/login-history", a.loginHistory},
		{"/auth/login-history/export", a.exportLoginHistory},
	} {
		for _, test := range []struct {
			name, cookie, origin string
			wantStatus           int
		}{
			{"missing session", "", "https://auth.example.test", http.StatusUnauthorized},
			{"other session", "another-session", "https://auth.example.test", http.StatusUnauthorized},
			{"other origin", "my-session", "https://other.example.test", http.StatusForbidden},
		} {
			t.Run(endpoint.path+" "+test.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				endpoint.handler(w, historyRequest(endpoint.path, test.cookie, test.origin))
				if w.Code != test.wantStatus {
					t.Fatalf("status = %d, want %d", w.Code, test.wantStatus)
				}
			})
		}
	}
	w := httptest.NewRecorder()
	a.loginHistory(w, historyRequest("/auth/login-history", "my-session", "https://auth.example.test"))
	if w.Code != http.StatusOK {
		t.Fatalf("history status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		History []loginHistoryEntry `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.History) != 50 || response.History[0].ID != 0 || response.History[0].Method != "password" {
		t.Fatalf("history response = %+v, err=%v", response, err)
	}
	if strings.Contains(w.Body.String(), `"id"`) || !strings.Contains(state.lastHistoryQuery, "WHERE user_id=? ORDER BY logged_in_at DESC,id DESC LIMIT ?") || len(state.lastHistoryArgs) != 2 || state.lastHistoryArgs[0].Value != int64(7) || state.lastHistoryArgs[1].Value != int64(loginHistoryPageSize) {
		t.Fatalf("history query leaked or was not bounded: query=%q args=%v body=%s", state.lastHistoryQuery, state.lastHistoryArgs, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.exportLoginHistory(w, historyRequest("/auth/login-history/export", "my-session", "https://auth.example.test"))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "\ufeff") || !strings.Contains(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("CSV status/headers: status=%d headers=%v", w.Code, w.Header())
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff")))
	records, err := reader.ReadAll()
	if err != nil || len(records) != 56 {
		t.Fatalf("CSV rows=%d, err=%v", len(records), err)
	}
	if records[1][0] != "7" || records[1][1] != "1" || records[1][2] != "2026-10-07T00:00:01Z" || records[1][6] != "Mountain View" || records[1][9] != geoIPSource || records[1][10] != "'"+injection || records[1][11] != geoRelease || records[1][12] != geoHash || records[1][13] != geoIPURL || records[2][9] != "" || records[55][1] != "55" {
		t.Fatalf("CSV first/last row: first=%v last=%v", records[1], records[55])
	}
	if strings.Contains(w.Body.String(), "passkey") || !strings.Contains(state.lastHistoryQuery, "WHERE user_id=? ORDER BY logged_in_at ASC,id ASC") || strings.Contains(state.lastHistoryQuery, "LIMIT") || len(state.lastHistoryArgs) != 1 || state.lastHistoryArgs[0].Value != int64(7) {
		t.Fatalf("CSV query leaked or was limited: query=%q args=%v", state.lastHistoryQuery, state.lastHistoryArgs)
	}
}

func TestCSVFormulaEscaping(t *testing.T) {
	for _, value := range []string{"=2+2", "+2+2", "-2+2", "@SUM(1)", "  =2+2", "\t=2+2", "\ufeff=2+2", "\u200b=2+2", "\u200d=2+2"} {
		if got := csvSafe(value); got != "'"+value {
			t.Errorf("csvSafe(%q) = %q", value, got)
		}
	}
	if got := csvSafe("Chrome/1.0"); got != "Chrome/1.0" {
		t.Fatalf("safe value changed: %q", got)
	}
}
