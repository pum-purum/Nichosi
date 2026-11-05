package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	result = execution(map[string]any{"sql": "INSERT INTO entries VALUES(3); SELECT * FROM absent;"})
	if result.Entry.Success || !result.RolledBack || len(result.Results) != 0 || result.Entry.RowLimit != 200 || !strings.Contains(result.Entry.Error, "Statement 2 failed.") {
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
		result = execution(map[string]any{"sql": "SELECT 1;"})
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
	result = execution(map[string]any{"sql": "INSERT INTO entries VALUES(99); SELECT * FROM missing_partial;", "atomic": false})
	if result.Entry.Success || result.Entry.Atomic || result.RolledBack || len(result.Results) != 1 || result.Results[0].RowsAffected != 1 || !strings.Contains(result.Entry.Error, "Some changes may already be saved") {
		t.Fatalf("unexpected partial execution: %+v", result)
	}
	if err := item.pool.QueryRow("SELECT count(*) FROM entries WHERE id=99").Scan(&n); err != nil || n != 1 {
		t.Fatalf("partial changes lost: %d %v", n, err)
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
