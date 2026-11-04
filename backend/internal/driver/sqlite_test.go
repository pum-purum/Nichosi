package driver

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sqliteFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`PRAGMA foreign_keys=ON;
 CREATE TABLE parent(id INTEGER PRIMARY KEY, name TEXT NOT NULL);
 CREATE TABLE entries(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id), name TEXT, doubled TEXT GENERATED ALWAYS AS (name||name) STORED);
 CREATE UNIQUE INDEX unique_name ON parent(name);
 CREATE INDEX entries_parent ON entries(parent_id);
 CREATE VIEW entry_names AS SELECT id,name FROM entries;
 CREATE TABLE audit(entry_id INTEGER);
 CREATE TRIGGER entry_changed AFTER UPDATE ON entries BEGIN INSERT INTO audit VALUES(new.id); END;
 CREATE TABLE nullable_key(k TEXT PRIMARY KEY,name TEXT);
 CREATE TABLE explicit_key(k TEXT PRIMARY KEY NOT NULL,name TEXT);
 CREATE TABLE descending_key(k INTEGER PRIMARY KEY DESC,name TEXT);
 CREATE TABLE no_key(name TEXT);
 INSERT INTO parent VALUES(1,'root');
 INSERT INTO entries(id,parent_id,name) VALUES(1,1,'one'),(2,1,'two');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, _, err = OpenSQLite(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}
func TestSQLiteBrowse(t *testing.T) {
	db, _ := sqliteFixture(t)
	ctx := context.Background()
	catalog, err := SQLiteCatalog(ctx, db)
	if err != nil || len(catalog.Tables) != 7 || len(catalog.Objects) != 4 {
		t.Fatalf("catalog: %#v %v", catalog, err)
	}
	for _, view := range []bool{false, true} {
		name := "entries"
		if view {
			name = "entry_names"
		}
		page, err := SQLiteFilteredRows(ctx, db, name, view, 1, 0, []RowFilter{{Column: "name", Op: "like", Value: "%o%"}}, RowOrder{Column: "id", Direction: "desc"})
		if err != nil || page.TotalRows != 2 || len(page.Rows) != 1 || page.Rows[0][0] != int64(2) {
			t.Fatalf("page: %#v %v", page, err)
		}
	}
	m, err := SQLiteMetadata(ctx, db, "entries", false)
	if err != nil || len(m.Columns) != 4 || len(m.ForeignKeys) != 1 || len(m.Triggers) != 1 || m.Definition == "" {
		t.Fatalf("metadata: %#v %v", m, err)
	}
	for _, tc := range []struct{ kind, name string }{{"view", "entry_names"}, {"trigger", "entry_changed"}, {"index", "entries_parent"}} {
		o, err := SQLiteObjectDefinition(ctx, db, tc.kind, tc.name)
		if err != nil || o.Definition == "" {
			t.Fatalf("object %v: %v", tc, err)
		}
	}
	if _, err := SQLiteFilteredRows(ctx, db, "entries", true, 10, 0, nil); !errors.Is(err, ErrRowsObjectNotFound) {
		t.Fatal(err)
	}
	if _, err := SQLiteFilteredRows(ctx, db, "entries", false, 10, 0, nil, RowOrder{Column: `id"; DROP TABLE parent;--`}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatal(err)
	}
}
func TestSQLiteEditing(t *testing.T) {
	db, _ := sqliteFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		editable bool
	}{{"entries", true}, {"parent", true}, {"no_key", false}, {"nullable_key", false}, {"explicit_key", true}, {"descending_key", false}} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SQLiteEditMetadata(ctx, db, tc.name)
			if (err == nil) != tc.editable {
				t.Fatalf("editable %v: %v", tc.editable, err)
			}
		})
	}
	m, err := SQLiteEditMetadata(ctx, db, "entries")
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{{"id": int64(3)}, {"doubled": "x"}} {
		if _, err := BuildUpdates("main", "entries", m, []RowChange{{Op: "update", Key: map[string]any{"id": int64(1)}, Values: values}}); !errors.Is(err, ErrInvalidChange) {
			t.Fatal(err)
		}
	}
	changes := []RowChange{{Op: "update", Key: map[string]any{"id": int64(1)}, Values: map[string]any{"name": "updated"}}, {Op: "delete", Key: map[string]any{"id": int64(999)}}}
	statements, err := BuildUpdates("main", "entries", m, changes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SQLiteApplyUpdates(ctx, db, statements); !errors.Is(err, ErrRowConflict) {
		t.Fatal(err)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM entries WHERE id=1").Scan(&name); err != nil || name != "one" {
		t.Fatalf("rollback: %s %v", name, err)
	}
	if n, err := SQLiteApplyUpdates(ctx, db, statements[:1]); err != nil || n != 1 {
		t.Fatalf("apply: %d %v", n, err)
	}
	var count int
	db.QueryRow("SELECT count(*) FROM audit").Scan(&count)
	if count != 1 {
		t.Fatalf("trigger rollback: %d", count)
	}
}
func TestSQLiteOpen(t *testing.T) {
	_, path := sqliteFixture(t)
	ctx := context.Background()
	ro, _, err := OpenSQLite(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.Exec("DELETE FROM entries"); err == nil {
		t.Fatal("read-only file accepted writes")
	}
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	for _, path := range []string{missing, "relative.sqlite", path + "?mode=rwc"} {
		if db, _, err := OpenSQLite(ctx, path, false); err == nil {
			db.Close()
			t.Fatalf("accepted invalid path %s", path)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing database created")
	}
}

func TestSQLiteDynamicKeyTypes(t *testing.T) {
	db, _ := sqliteFixture(t)
	_, err := db.Exec(`CREATE TABLE mixed(k TEXT NOT NULL UNIQUE, value TEXT);INSERT INTO mixed VALUES('abc','text'),(x'616263','blob');CREATE TABLE sqliteDemo(id INTEGER PRIMARY KEY);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SQLiteEditMetadata(context.Background(), db, "mixed"); !errors.Is(err, ErrReadOnlyTable) {
		t.Fatalf("unsafe dynamic key accepted: %v", err)
	}
	page, err := SQLiteFilteredRows(context.Background(), db, "mixed", false, 10, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var blob any
	for _, row := range page.Rows {
		if _, ok := row[0].(map[string]any); ok {
			blob = row[0]
		}
	}
	if blob == nil {
		t.Fatal("BLOB key lost its runtime type")
	}
	if _, err := db.Exec("DELETE FROM mixed WHERE typeof(k)='blob'"); err != nil {
		t.Fatal(err)
	}
	metadata, err := SQLiteEditMetadata(context.Background(), db, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildUpdates("main", "mixed", metadata, []RowChange{{Op: "delete", Key: map[string]any{"k": blob}}}); !errors.Is(err, ErrInvalidChange) {
		t.Fatalf("stale blob key accepted: %v", err)
	}
	if _, err := SQLiteColumns(context.Background(), db, "sqliteDemo", false); err != nil {
		t.Fatalf("legal prefix rejected: %v", err)
	}
}
