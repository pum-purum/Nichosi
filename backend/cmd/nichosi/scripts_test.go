package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scripts.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE entries(id INTEGER PRIMARY KEY); INSERT INTO entries VALUES(1),(2)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writable := false
	a := &app{}
	item, err := a.openConnection(context.Background(), ConnectionInput{Driver: Sqlite, Database: path, Readonly: &writable})
	if err != nil {
		t.Fatal(err)
	}
	defer item.pool.Close()
	a.connections = []*profile{item}
	callRaw := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.SetPathValue("connId", item.Id)
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	call := func(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return callRaw(handler, string(raw))
	}
	execution := func(body any) ScriptExecution {
		w := call(a.executeScript, body)
		if w.Code != 200 {
			t.Fatalf("execution status %d: %s", w.Code, w.Body.String())
		}
		var result ScriptExecution
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := execution(map[string]any{"sql": "SELECT * FROM entries ORDER BY id;", "rowLimit": 1})
	if !result.Entry.Success || !result.Entry.Atomic || result.Entry.RowLimit != 1 || len(result.Results) != 1 || !result.Results[0].Truncated || len(result.Results[0].Rows) != 1 {
		t.Fatalf("unexpected successful execution: %+v", result)
	}
	result = execution(map[string]any{"sql": "SELECT * FROM absent;"})
	if result.Entry.Success || !result.RolledBack || len(result.Results) != 0 || result.Entry.RowLimit != 200 || result.Entry.Error != "SQL logic error: no such table: absent (1)" {
		t.Fatalf("unexpected rollback: %+v", result)
	}
	var n int
	if err := item.pool.QueryRow("SELECT count(*) FROM entries").Scan(&n); err != nil || n != 2 {
		t.Fatalf("rollback failed: %d %v", n, err)
	}
	w := call(a.listScriptHistory, nil)
	var history []ScriptHistoryEntry
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Success || !history[1].Success {
		t.Fatal(w.Body.String())
	}
	if w = call(a.clearScriptHistory, nil); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	if w = call(a.listScriptHistory, nil); w.Body.String() != "[]\n" {
		t.Fatal(w.Body.String())
	}
	for i := 0; i < 101; i++ {
		result = execution(map[string]any{"sql": fmt.Sprintf("SELECT %d;", i)})
		if !result.Entry.Success {
			t.Fatal(result.Entry.Error)
		}
	}
	w = call(a.listScriptHistory, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 100 || history[0].Id == history[1].Id {
		t.Fatal("history limit or execution identity broken")
	}
	first := execution(map[string]any{"sql": "SELECT 999;", "rowLimit": 1})
	for count := 2; count <= 5; count++ {
		repeated := execution(map[string]any{"sql": "SELECT 999;", "rowLimit": count})
		if repeated.Entry.Id != first.Entry.Id || repeated.Entry.ExecutionCount != count || repeated.Entry.RowLimit != count || !repeated.Entry.ExecutedAt.After(first.Entry.ExecutedAt) {
			t.Fatalf("repeat did not update latest execution: %+v", repeated.Entry)
		}
	}
	w = call(a.listScriptHistory, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &history); err != nil || len(history) != 100 || history[0].ExecutionCount != 5 || history[1].Sql != "SELECT 100;" {
		t.Fatalf("unexpected grouped history: %s", w.Body.String())
	}
	spaced := execution(map[string]any{"sql": "SELECT 999; "})
	separated := execution(map[string]any{"sql": "SELECT 999;"})
	if spaced.Entry.ExecutionCount != 1 || separated.Entry.ExecutionCount != 1 || separated.Entry.Id == first.Entry.Id {
		t.Fatal("different SQL or nonconsecutive executions were merged")
	}
	for _, atomic := range []bool{true, false} {
		w = call(a.executeScript, map[string]any{"sql": "INSERT INTO entries VALUES(99); SELECT 1;", "atomic": atomic})
		if w.Code != 400 || !strings.Contains(w.Body.String(), "exactly one SQL statement") {
			t.Fatalf("multiple statements accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if err := item.pool.QueryRow("SELECT count(*) FROM entries WHERE id=99").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected script changed data: %d %v", n, err)
	}
	for _, tc := range []struct {
		suffix    string
		want      int
		truncated bool
	}{
		{"", 200, true},
		{" LIMIT 50", 50, false},
		{" LIMIT 500", 500, false},
		{" LIMIT 1200", 1000, true},
	} {
		result = execution(map[string]any{"sql": "WITH RECURSIVE nums(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM nums WHERE n<1200) SELECT n FROM nums" + tc.suffix})
		if !result.Entry.Success || len(result.Results) != 1 || len(result.Results[0].Rows) != tc.want || result.Results[0].Truncated != tc.truncated {
			t.Fatalf("SQL limit %q: %+v", tc.suffix, result)
		}
	}
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/json; broken"} {
		t.Run("reject content type "+contentType, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"sql":"INSERT INTO entries VALUES(777)","atomic":false}`))
			r.SetPathValue("connId", item.Id)
			if contentType != "" {
				r.Header.Set("Content-Type", contentType)
			}
			w := httptest.NewRecorder()
			a.executeScript(w, r)
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	if err := item.pool.QueryRow("SELECT count(*) FROM entries WHERE id=777").Scan(&n); err != nil || n != 0 {
		t.Fatalf("unsupported content type executed SQL: %d %v", n, err)
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"sql":"SELECT 1"}`))
	r.SetPathValue("connId", item.Id)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	charsetResponse := httptest.NewRecorder()
	a.executeScript(charsetResponse, r)
	if charsetResponse.Code != http.StatusOK {
		t.Fatal(charsetResponse.Body.String())
	}
	for _, tc := range []struct{ name, body string }{
		{"unknown field", `{"sql":"SELECT 1","unexpected":true}`},
		{"legacy preview token", `{"sql":"SELECT 1","previewToken":"obsolete"}`},
		{"zero limit", `{"sql":"SELECT 1","rowLimit":0}`},
		{"excessive limit", `{"sql":"SELECT 1","rowLimit":1001}`},
		{"missing SQL", `{}`},
		{"empty SQL", `{"sql":""}`},
		{"comments only", `{"sql":"-- comment\n/* comment */"}`},
		{"malformed SQL literal", `{"sql":"SELECT 'unfinished"}`},
		{"unsupported atomic command", `{"sql":"BEGIN"}`},
		{"malformed JSON", `{"sql":`},
		{"multiple bodies", `{"sql":"SELECT 1"} {"sql":"SELECT 2"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := callRaw(a.executeScript, tc.body); w.Code != 400 {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		name       string
		readonly   bool
		configured *bool
	}{
		{"default readonly", false, nil},
		{"explicit readonly", true, &writable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item.Readonly = tc.readonly
			item.input.Readonly = tc.configured
			if w := call(a.executeScript, map[string]any{"sql": "SELECT 1", "atomic": false}); w.Code != 403 {
				t.Fatalf("non-atomic readonly bypass: %d %s", w.Code, w.Body.String())
			}
			if w := call(a.executeScript, map[string]any{"sql": "DELETE FROM entries"}); w.Code != 400 {
				t.Fatalf("atomic readonly bypass: %d %s", w.Code, w.Body.String())
			}
			result = execution(map[string]any{"sql": "SELECT 1"})
			if !result.Entry.Success {
				t.Fatal(result.Entry.Error)
			}
		})
	}
	item.Readonly = false
	item.input.Readonly = &writable
	pool := item.pool
	item.pool = nil
	if w := call(a.executeScript, map[string]any{"sql": "SELECT 1"}); w.Code != 409 {
		t.Fatalf("disconnected execution: %d", w.Code)
	}
	item.pool = pool
	a.connections = nil
	if w := call(a.executeScript, map[string]any{"sql": "SELECT 1"}); w.Code != 404 {
		t.Fatalf("unknown connection execution: %d", w.Code)
	}
}

func TestRedactScriptError(t *testing.T) {
	secret := "test secret/p@ss?&"
	for _, tc := range []struct{ name, message, want string }{
		{"ordinary syntax", "Error 1064 (42000): syntax error near 'SELECT' at line 1", "Error 1064 (42000): syntax error near 'SELECT' at line 1"},
		{"raw password", "rejected value test secret/p@ss?&", "rejected value [redacted]"},
		{"query escaped password", "rejected value test+secret%2Fp%40ss%3F%26", "rejected value [redacted]"},
		{"path escaped password", "rejected value test%20secret%2Fp@ss%3F&", "rejected value [redacted]"},
		{"lowercase percent escapes", "rejected value test+secret%2fp%40ss%3f%26", "rejected value [redacted]"},
		{"postgres URL", "cannot use postgres://alice:another-secret@localhost/app", "cannot use [redacted DSN]"},
		{"mysql URL", "cannot use mysql://alice:another-secret@localhost/app", "cannot use [redacted DSN]"},
		{"mysql DSN", "cannot use alice:another-secret@tcp(localhost:3306)/app?charset=utf8mb4", "cannot use [redacted DSN]"},
		{"mysql default network DSN", "cannot use alice:another-secret@/app", "cannot use [redacted DSN]"},
		{"pgx fields without password", "rejected host=localhost user=alice dbname=app", "rejected [redacted DSN] [redacted DSN] [redacted DSN]"},
		{"password field", "rejected password='another secret'", "rejected password=[redacted]"},
		{"pgx fields", "rejected host=localhost user=alice password='another secret' dbname=app", "rejected [redacted DSN] [redacted DSN] [redacted DSN] [redacted DSN]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactScriptError(tc.message, &secret); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestRedactSQLEscapedConnectionPassword(t *testing.T) {
	for _, tc := range []struct{ name, secret, echo string }{
		{"doubled single quote", "p'ass", "p''ass"},
		{"backslash single quote", "p'ass", `p\'ass`},
		{"doubled double quote", `p"ass`, `p""ass`},
		{"backslash double quote", `p"ass`, `p\"ass`},
		{"backslash", `p\ass`, `p\\ass`},
		{"combined mysql escapes", `p'\ass`, `p\'\\ass`},
		{"combined postgres escapes", `p'\ass`, `p''\\ass`},
		{"unicode quote", "пар'оль", "пар''оль"},
		{"newline", "pass\nword", `pass\nword`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := "Error 1064 (42000): syntax error near '" + tc.echo + "' at line 1"
			want := "Error 1064 (42000): syntax error near '[redacted]' at line 1"
			if got := redactScriptError(message, &tc.secret); got != want {
				t.Fatalf("got %q; want %q", got, want)
			}
		})
	}
}
