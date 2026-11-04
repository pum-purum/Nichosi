package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteHTTP(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE entries(id INTEGER PRIMARY KEY,name TEXT);INSERT INTO entries VALUES(1,'one');`); err != nil {
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
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.SetPathValue("connId", item.Id)
		r.SetPathValue("table", "entries")
		w := httptest.NewRecorder()
		a.queryRows(w, r)
		return w
	}
	w := request(`{"limit":10}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var page RowsPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || !page.Editable || page.Columns[0].Nullable {
		t.Fatalf("page: %s %v", w.Body.String(), err)
	}
	for _, apply := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(`{"changes":[{"op":"update","key":{"id":1},"values":{"name":"updated"}}]}`))
		r.SetPathValue("connId", item.Id)
		r.SetPathValue("table", "entries")
		w := httptest.NewRecorder()
		a.changeRows(w, r, apply)
		if w.Code != 200 {
			t.Fatalf("changes %v: %s", apply, w.Body.String())
		}
	}
	var name string
	if err := item.pool.QueryRow("SELECT name FROM entries").Scan(&name); err != nil || name != "updated" {
		t.Fatalf("apply %s %v", name, err)
	}
	item.Readonly = true
	item.input.Readonly = nil
	w = request(`{}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if page.Editable {
		t.Fatal("default readonly bypassed")
	}
}
func TestSQLiteConnectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input ConnectionInput
		valid bool
	}{
		{"file", ConnectionInput{Driver: Sqlite, Database: "/data/demo.sqlite"}, true},
		{"relative", ConnectionInput{Driver: Sqlite, Database: "demo.sqlite"}, false},
		{"URI", ConnectionInput{Driver: Sqlite, Database: "file:/data/demo.sqlite?mode=rwc"}, false},
		{"server fields", ConnectionInput{Driver: Sqlite, Database: "/data/demo.sqlite", Host: "remote"}, false},
		{"network password required", ConnectionInput{Driver: Mysql, Database: "demo", Host: "localhost", User: "user", Port: 3306}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validConnectionInput(tc.input) != tc.valid {
				t.Fatal("unexpected validation")
			}
		})
	}
}
