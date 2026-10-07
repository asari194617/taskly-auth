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
				valueFromPtr(entry.IPAddress), city, region, country, valueFromPtr(entry.Device), valueFromPtr(entry.IPSource)})
		}
		return &historyTestRows{columns: []string{"id", "method", "logged_in_at", "ip_address", "geo_city", "geo_region", "geo_country", "user_agent", "ip_source"}, values: values}, nil
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

func TestLoginRequestEvidenceTrustAndSanitization(t *testing.T) {
	const proxyToken = "test-proxy-token-with-at-least-32-characters"
	r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	r.RemoteAddr = "172.18.0.1:54321"
	r.Header.Set("X-Auth-Proxy-Token", proxyToken)
	r.Header.Set("X-Auth-Client-IP", "2001:db8::1")
	r.Header.Set("X-Auth-IP-Source", "cloudflare")
	r.Header.Set("X-Auth-Geo-City", "Tokyo")
	r.Header.Set("X-Auth-Geo-Region", "Tokyo")
	r.Header.Set("X-Auth-Geo-Country", "jp")
	r.Header.Set("User-Agent", "ExampleBrowser/1.0")
	ip, location, device, source := loginRequestEvidence(r, proxyToken)
	if stringValue(ip) != "2001:db8::1" || location == nil || location.City != "Tokyo" || location.Country != "JP" || stringValue(device) != "ExampleBrowser/1.0" || stringValue(source) != "cloudflare" {
		t.Fatalf("trusted proxy evidence: ip=%v location=%+v device=%v source=%v", ip, location, device, source)
	}
	r.RemoteAddr = "203.0.113.10:12345"
	ip, location, _, source = loginRequestEvidence(r, proxyToken)
	if stringValue(ip) != "203.0.113.10" || location != nil || stringValue(source) != "direct_socket" {
		t.Fatalf("untrusted proxy headers were accepted: ip=%v location=%+v source=%v", ip, location, source)
	}
	r.RemoteAddr = "172.18.0.1:54321"
	r.Header.Set("X-Auth-IP-Source", "origin_socket")
	_, location, _, _ = loginRequestEvidence(r, proxyToken)
	if location != nil {
		t.Fatalf("direct-origin geolocation was trusted: %+v", location)
	}
	r.Header.Set("X-Auth-IP-Source", "cloudflare")
	r.Header.Set("X-Auth-Geo-Country", "XX")
	r.Header.Del("X-Auth-Geo-City")
	r.Header.Del("X-Auth-Geo-Region")
	_, location, _, _ = loginRequestEvidence(r, proxyToken)
	if location != nil {
		t.Fatalf("unknown Cloudflare country was recorded: %+v", location)
	}
	r.Header.Set("X-Auth-Proxy-Token", "forged-token")
	ip, location, _, source = loginRequestEvidence(r, proxyToken)
	if ip != nil || location != nil || source != nil {
		t.Fatalf("untrusted Docker peer headers were accepted: ip=%v location=%+v source=%v", ip, location, source)
	}
	r.Header.Set("X-Auth-Proxy-Token", proxyToken)
	ip, location, _, source = loginRequestEvidence(r, "")
	if ip != nil || location != nil || source != nil {
		t.Fatalf("empty proxy token trusted headers: ip=%v location=%+v source=%v", ip, location, source)
	}
	if got := cleanHistoryText("abc\r\n=HYPERLINK(\"x\")", 100); got != "abc=HYPERLINK(\"x\")" {
		t.Fatalf("history text sanitization = %q", got)
	}
}

func TestFinishLoginCommitsSessionAndEvidenceTogether(t *testing.T) {
	for _, failHistory := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "history failure"}[failHistory], func(t *testing.T) {
			state := &historyTestConnector{failHistoryInsert: failHistory}
			const proxyToken = "test-proxy-token-with-at-least-32-characters"
			a := &app{db: sql.OpenDB(state), alerts: &alerter{}, historyProxyToken: proxyToken}
			defer a.db.Close()
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			r.RemoteAddr = "172.18.0.1:1234"
			r.Header.Set("X-Auth-Proxy-Token", proxyToken)
			r.Header.Set("X-Auth-Client-IP", "203.0.113.10")
			r.Header.Set("X-Auth-IP-Source", "cloudflare")
			r.Header.Set("X-Auth-Geo-City", "Tokyo")
			r.Header.Set("X-Auth-Geo-Country", "JP")
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
			if entry.Method != "passkey" || time.Since(entry.LoggedInAt) > time.Minute || stringValue(entry.IPAddress) != "203.0.113.10" || entry.Location == nil || entry.Location.City != "Tokyo" || stringValue(entry.Device) != "ExampleBrowser/1.0" {
				t.Fatalf("unexpected history evidence: %+v", entry)
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
	if records[1][0] != "7" || records[1][1] != "1" || records[1][2] != "2026-10-07T00:00:01Z" || records[1][10] != "'"+injection || records[55][1] != "55" {
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
