package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/pum-purum/nichosi/internal/driver"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeRowsAccess(t *testing.T) {
	writable := false
	for _, tc := range []struct {
		name    string
		profile *profile
		status  int
	}{
		{"unknown", nil, 404},
		{"readonly default", &profile{Connection: Connection{Id: "test"}}, 403},
		{"readonly explicit", &profile{Connection: Connection{Id: "test", Readonly: true}, input: ConnectionInput{Readonly: &writable}}, 403},
		{"disconnected", &profile{Connection: Connection{Id: "test"}, input: ConnectionInput{Readonly: &writable}}, 409},
	} {
		for _, apply := range []bool{false, true} {
			t.Run(tc.name, func(t *testing.T) {
				a := &app{}
				if tc.profile != nil {
					a.connections = []*profile{tc.profile}
				}
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"changes":[]}`))
				r.SetPathValue("connId", "test")
				r.SetPathValue("table", "t")
				w := httptest.NewRecorder()
				a.changeRows(w, r, apply)
				if w.Code != tc.status {
					t.Fatalf("got %d: %s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestInsertRowsPreviewAndApply(t *testing.T) {
	pool, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "insert.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec("CREATE TABLE entries(id INTEGER PRIMARY KEY, created TEXT DEFAULT CURRENT_TIMESTAMP, label TEXT)"); err != nil {
		t.Fatal(err)
	}
	writable := false
	item := &profile{Connection: Connection{Id: "test", Driver: ConnectionDriverSqlite}, input: ConnectionInput{Readonly: &writable}, pool: pool}
	a := &app{connections: []*profile{item}}
	body := `{"changes":[{"op":"insert","values":{"label":"literal NOW()"}}]}`
	for _, apply := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		request.SetPathValue("connId", "test")
		request.SetPathValue("table", "entries")
		response := httptest.NewRecorder()
		a.changeRows(response, request, apply)
		if response.Code != 200 {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
		if !apply {
			var preview ChangePreview
			if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			if len(preview.Statements) != 1 || preview.Statements[0] != "INSERT INTO `main`.`entries` (`label`) VALUES (?);" {
				t.Fatal(preview)
			}
		}
		var count int
		if err := pool.QueryRow("SELECT count(*) FROM entries").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (!apply && count != 0) || (apply && count != 1) {
			t.Fatal(count)
		}
	}
	var created, label string
	if err := pool.QueryRow("SELECT created,label FROM entries").Scan(&created, &label); err != nil {
		t.Fatal(err)
	}
	if created == "" || created == "CURRENT_TIMESTAMP" || label != "literal NOW()" {
		t.Fatal(created, label)
	}
	// A writable connection does not grant insert access to a keyless table.
	if _, err := pool.Exec("CREATE TABLE keyless(label TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.SQLiteEditMetadata(context.Background(), pool, "keyless"); err == nil {
		t.Fatal("keyless table must remain read-only")
	}
}

func TestChangeErrorDiagnostics(t *testing.T) {
	password := "private-password"
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{
		{"database diagnostic", &mysql.MySQLError{Number: 1364, Message: "Field 'id' doesn't have a default value"}, 502, "Error 1364: Field 'id' doesn't have a default value"},
		{"wrapped diagnostic", fmt.Errorf("apply changes: %w", &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '123' for key 'PRIMARY'"}), 502, "Error 1062: Duplicate entry '123' for key 'PRIMARY'"},
		{"redacted multiline", errors.New("invalid private-password\ncannot use mysql://user:secret@localhost/app"), 502, "invalid [redacted]\ncannot use [redacted DSN]"},
		{"timeout", context.DeadlineExceeded, 502, "Execution timed out."},
		{"row conflict", driver.ErrRowConflict, 409, "The row no longer exists or its key is not unique; changes were rolled back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeChangeError(response, tc.err, &password)
			var result Error
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || result.Message != tc.message {
				t.Fatalf("status=%d message=%q", response.Code, result.Message)
			}
		})
	}
}
