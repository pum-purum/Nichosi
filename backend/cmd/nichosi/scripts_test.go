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
	"time"
)

func TestScriptHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scripts.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("CREATE TABLE entries(id INTEGER PRIMARY KEY); INSERT INTO entries VALUES(1),(2)")
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	writable := false
	a := &app{}
	item, e := a.openConnection(context.Background(), ConnectionInput{Driver: Sqlite, Database: path, Readonly: &writable})
	if e != nil {
		t.Fatal(e)
	}
	defer item.pool.Close()
	a.connections = []*profile{item}
	call := func(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/", strings.NewReader(string(raw)))
		r.SetPathValue("connId", item.Id)
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	prepare := func(source string) ScriptPreview {
		w := call(a.previewScript, map[string]any{"sql": source, "rowLimit": 1})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var p ScriptPreview
		json.Unmarshal(w.Body.Bytes(), &p)
		return p
	}
	source := "SELECT * FROM entries ORDER BY id;"
	p := prepare(source)
	w := call(a.executeScript, map[string]any{"sql": source + "SELECT 2;", "rowLimit": 1, "previewToken": p.PreviewToken})
	if w.Code != 409 {
		t.Fatal("changed SQL accepted", w.Body.String())
	}
	p = prepare(source)
	body := map[string]any{"sql": source, "rowLimit": 1, "previewToken": p.PreviewToken}
	w = call(a.executeScript, body)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result ScriptExecution
	json.Unmarshal(w.Body.Bytes(), &result)
	if !result.Entry.Success || !result.Results[0].Truncated {
		t.Fatal(w.Body.String())
	}
	if w = call(a.executeScript, body); w.Code != 409 {
		t.Fatal("token reuse accepted")
	}
	p = prepare(source)
	item.scripts.mu.Lock()
	record := item.scripts.previews[p.PreviewToken]
	record.expires = time.Now().Add(-time.Second)
	item.scripts.previews[p.PreviewToken] = record
	item.scripts.mu.Unlock()
	body["previewToken"] = p.PreviewToken
	if w = call(a.executeScript, body); w.Code != 409 {
		t.Fatal("expired token accepted")
	}
	source = "INSERT INTO entries VALUES(3); SELECT * FROM absent;"
	p = prepare(source)
	w = call(a.executeScript, map[string]any{"sql": source, "rowLimit": 1, "previewToken": p.PreviewToken})
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Entry.Success || !result.RolledBack || len(result.Results) != 0 {
		t.Fatal(w.Body.String())
	}
	var n int
	item.pool.QueryRow("SELECT count(*) FROM entries").Scan(&n)
	if n != 2 {
		t.Fatal("rollback failed")
	}
	w = call(a.listScriptHistory, nil)
	var history []ScriptHistoryEntry
	json.Unmarshal(w.Body.Bytes(), &history)
	if len(history) != 2 || history[0].Success || !history[1].Success {
		t.Fatal(w.Body.String())
	}
	if w = call(a.clearScriptHistory, nil); w.Code != 204 {
		t.Fatal(w.Body.String())
	}
	w = call(a.listScriptHistory, nil)
	if w.Body.String() != "[]\n" {
		t.Fatal(w.Body.String())
	}
	for i := 0; i < 101; i++ {
		p = prepare("SELECT 1;")
		w = call(a.executeScript, map[string]any{"sql": "SELECT 1;", "rowLimit": 1, "previewToken": p.PreviewToken})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w = call(a.listScriptHistory, nil)
	json.Unmarshal(w.Body.Bytes(), &history)
	if len(history) != 100 || history[0].Id == history[1].Id {
		t.Fatal("history limit or execution identity broken")
	}
	source = "INSERT INTO entries VALUES(99); SELECT * FROM missing_partial;"
	w = call(a.previewScript, map[string]any{"sql": source, "rowLimit": 1, "atomic": false})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	w = call(a.executeScript, map[string]any{"sql": source, "rowLimit": 1, "atomic": false, "previewToken": p.PreviewToken})
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Entry.Success || result.Entry.Atomic || result.RolledBack || len(result.Results) != 1 || result.Results[0].RowsAffected != 1 {
		t.Fatal("partial result incorrect", w.Body.String())
	}
	item.pool.QueryRow("SELECT count(*) FROM entries WHERE id=99").Scan(&n)
	if n != 1 {
		t.Fatal("non-atomic changes not retained")
	}
	w = call(a.previewScript, map[string]any{"sql": "SELECT 1", "rowLimit": 1, "atomic": false})
	json.Unmarshal(w.Body.Bytes(), &p)
	w = call(a.executeScript, map[string]any{"sql": "SELECT 1", "rowLimit": 1, "atomic": true, "previewToken": p.PreviewToken})
	if w.Code != 409 {
		t.Fatal("execution mode not bound to preview")
	}
	item.Readonly = true
	item.input.Readonly = nil
	w = call(a.previewScript, map[string]any{"sql": "SELECT 1", "atomic": false})
	if w.Code != 403 {
		t.Fatal("non-atomic defaultreadonly bypass", w.Body.String())
	}
	w = call(a.previewScript, map[string]any{"sql": "DELETE FROM entries"})
	if w.Code != 400 {
		t.Fatal("readonly bypass", w.Body.String())
	}
	w = call(a.previewScript, map[string]any{"sql": "SELECT 1", "rowLimit": 1001})
	if w.Code != 400 {
		t.Fatal("unbounded query accepted")
	}
	w = call(a.previewScript, map[string]any{"sql": "SELECT 1", "unexpected": true})
	if w.Code != 400 {
		t.Fatal("unknown field accepted")
	}
}
