package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
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

type historyTestConnector struct {
	sessions          map[string]uint64
	history           []loginHistoryEntry
	failHistoryInsert bool
	failPrune         bool
	commits           int
	rollbacks         int
	pruneQueries      int
	pruneDeletes      int
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
	pendingHistory *loginHistoryEntry
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
		c.pendingHistory = &loginHistoryEntry{
			ID:         uint64(len(c.state.history) + 1),
			Method:     args[1].Value.(string),
			LoggedInAt: args[2].Value.(time.Time),
		}
		return driver.RowsAffected(1), nil
	case strings.Contains(query, "DELETE FROM auth_login_history"):
		c.state.pruneDeletes++
		if c.state.failPrune {
			return nil, errors.New("pruning failed")
		}
		cutoffTime := args[1].Value.(time.Time)
		cutoffID := uint64(args[3].Value.(int64))
		kept := c.state.history[:0]
		for _, entry := range c.state.history {
			if entry.LoggedInAt.Before(cutoffTime) || (entry.LoggedInAt.Equal(cutoffTime) && entry.ID <= cutoffID) {
				continue
			}
			kept = append(kept, entry)
		}
		c.state.history = kept
		return driver.RowsAffected(1), nil
	default:
		return nil, errors.New("unexpected exec: " + query)
	}
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
	case strings.Contains(query, "SELECT logged_in_at,id FROM auth_login_history"):
		c.state.pruneQueries++
		ordered := append([]loginHistoryEntry(nil), c.state.history...)
		sort.Slice(ordered, func(i, j int) bool {
			if !ordered[i].LoggedInAt.Equal(ordered[j].LoggedInAt) {
				return ordered[i].LoggedInAt.After(ordered[j].LoggedInAt)
			}
			return ordered[i].ID > ordered[j].ID
		})
		if len(ordered) <= loginHistoryLimit {
			return &historyTestRows{columns: []string{"logged_in_at", "id"}}, nil
		}
		cutoff := ordered[loginHistoryLimit]
		return &historyTestRows{columns: []string{"logged_in_at", "id"}, values: [][]driver.Value{{cutoff.LoggedInAt, int64(cutoff.ID)}}}, nil
	case strings.Contains(query, "FROM auth_login_history"):
		c.state.lastHistoryQuery = query
		c.state.lastHistoryArgs = append([]driver.NamedValue(nil), args...)
		values := make([][]driver.Value, 0, len(c.state.history))
		for _, entry := range c.state.history {
			values = append(values, []driver.Value{int64(entry.ID), entry.Method, entry.LoggedInAt})
		}
		return &historyTestRows{columns: []string{"id", "method", "logged_in_at"}, values: values}, nil
	default:
		return nil, errors.New("unexpected query: " + query)
	}
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

func TestFinishLoginCommitsSessionAndHistoryTogether(t *testing.T) {
	for _, failHistory := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "history failure"}[failHistory], func(t *testing.T) {
			state := &historyTestConnector{failHistoryInsert: failHistory}
			a := &app{db: sql.OpenDB(state), alerts: &alerter{}}
			defer a.db.Close()
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
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
			if state.history[0].Method != "passkey" || time.Since(state.history[0].LoggedInAt) > time.Minute {
				t.Fatalf("unexpected history entry: %+v", state.history[0])
			}
		})
	}
}

func TestLoginHistoryPrunesPerUserAfterSuccessfulLogin(t *testing.T) {
	state := &historyTestConnector{}
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= loginHistoryLimit; i++ {
		state.history = append(state.history, loginHistoryEntry{ID: uint64(i), Method: "password", LoggedInAt: old.Add(time.Duration(i) * time.Second)})
	}
	a := &app{db: sql.OpenDB(state), alerts: &alerter{}}
	defer a.db.Close()
	w := httptest.NewRecorder()
	a.finishLogin(w, httptest.NewRequest(http.MethodPost, "/auth/login", nil), 7, "passkey")
	if w.Code != http.StatusOK || len(state.history) != loginHistoryLimit || state.pruneQueries != 1 || state.pruneDeletes != 1 {
		t.Fatalf("pruning after login: status=%d history=%d queries=%d deletes=%d", w.Code, len(state.history), state.pruneQueries, state.pruneDeletes)
	}
	for _, entry := range state.history {
		if entry.ID == 1 {
			t.Fatal("oldest login survived pruning")
		}
	}
	state.failPrune = true
	w = httptest.NewRecorder()
	a.finishLogin(w, httptest.NewRequest(http.MethodPost, "/auth/login", nil), 7, "passkey")
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 || state.commits != 2 || len(state.history) != loginHistoryLimit+1 {
		t.Fatalf("pruning failure rejected a valid login: status=%d cookies=%d commits=%d history=%d", w.Code, len(w.Result().Cookies()), state.commits, len(state.history))
	}
}

func TestLoginHistoryRequiresSessionAndScopesQuery(t *testing.T) {
	state := &historyTestConnector{
		sessions: map[string]uint64{digest("my-session"): 7},
		history:  []loginHistoryEntry{{ID: 4, Method: "passkey", LoggedInAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)}},
	}
	a := &app{db: sql.OpenDB(state), uiURL: "https://auth.example.test"}
	defer a.db.Close()
	request := func(cookie, origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/auth/login-history", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Auth-UI", "1")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "taskly_auth_session", Value: cookie})
		}
		return r
	}
	for _, test := range []struct {
		name, cookie, origin string
		wantStatus           int
	}{
		{"missing session", "", "https://auth.example.test", http.StatusUnauthorized},
		{"other session", "another-session", "https://auth.example.test", http.StatusUnauthorized},
		{"other origin", "my-session", "https://other.example.test", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			a.loginHistory(w, request(test.cookie, test.origin))
			if w.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, test.wantStatus)
			}
		})
	}
	w := httptest.NewRecorder()
	a.loginHistory(w, request("my-session", "https://auth.example.test"))
	if w.Code != http.StatusOK {
		t.Fatalf("history status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		History []loginHistoryEntry `json:"history"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.History) != 1 || response.History[0].Method != "passkey" {
		t.Fatalf("history response = %+v, err=%v", response, err)
	}
	if strings.Contains(w.Body.String(), `"id"`) {
		t.Fatalf("response disclosed a database row ID: %s", w.Body.String())
	}
	if !strings.Contains(state.lastHistoryQuery, "WHERE user_id=? ORDER BY logged_in_at DESC,id DESC LIMIT ?") || len(state.lastHistoryArgs) != 2 || state.lastHistoryArgs[0].Value != int64(7) || state.lastHistoryArgs[1].Value != int64(loginHistoryLimit) {
		t.Fatalf("history query is not scoped and bounded: query=%q args=%v", state.lastHistoryQuery, state.lastHistoryArgs)
	}
}
