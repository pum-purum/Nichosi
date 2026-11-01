package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestBuildUpdates(t *testing.T) {
	metadata := EditMetadata{Columns: map[string]EditColumn{"id": {PrimaryKey: true}, "na`me": {Nullable: true}, "other": {}, "computed": {Generated: true}}, Key: []string{"id"}}
	for _, tc := range []struct {
		name    string
		change  RowChange
		invalid bool
	}{
		{"changed only", RowChange{Op: "update", Key: map[string]any{"id": json.Number("9007199254740993")}, Values: map[string]any{"na`me": "O'Reilly; DROP TABLE x"}}, false},
		{"explicit null", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"na`me": nil}}, false},
		{"empty", RowChange{Op: "update", Key: map[string]any{"id": "1"}}, true},
		{"missing key", RowChange{Op: "update", Values: map[string]any{"other": "x"}}, true},
		{"extra key", RowChange{Op: "update", Key: map[string]any{"id": "1", "other": "x"}, Values: map[string]any{"other": "x"}}, true},
		{"unknown column", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"evil": "x"}}, true},
		{"primary key", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"id": "2"}}, true},
		{"generated", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"computed": "x"}}, true},
		{"not null", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"other": nil}}, true},
		{"non scalar", RowChange{Op: "update", Key: map[string]any{"id": "1"}, Values: map[string]any{"other": map[string]any{}}}, true},
		{"delete", RowChange{Op: "delete", Key: map[string]any{"id": "1"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := BuildUpdates("db`name", "table", metadata, []RowChange{tc.change})
			if tc.invalid {
				if !errors.Is(err, ErrInvalidChange) {
					t.Fatalf("want invalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(statements) != 1 {
				t.Fatal(statements)
			}
			if strings.Contains(statements[0].SQL, "O'Reilly") || strings.Contains(statements[0].SQL, "other") {
				t.Fatal(statements[0].SQL)
			}
			if tc.name == "changed only" {
				if statements[0].SQL != "UPDATE `db``name`.`table` SET `na``me` = ? WHERE `id` = ?;" || !reflect.DeepEqual(statements[0].Parameters, []any{"O'Reilly; DROP TABLE x", "9007199254740993"}) {
					t.Fatalf("%+v", statements[0])
				}
			}
		})
	}
}

type updateConnector struct {
	matches               []int
	affected              int64
	failAt                int
	queries, execs        int
	committed, rolledBack bool
}

func (c *updateConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &updateConn{c}, nil
}
func (*updateConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type updateConn struct{ c *updateConnector }

func (*updateConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unsupported") }
func (*updateConn) Close() error                           { return nil }
func (c *updateConn) Begin() (sqldriver.Tx, error)         { return c, nil }
func (c *updateConn) Commit() error                        { c.c.committed = true; return nil }
func (c *updateConn) Rollback() error                      { c.c.rolledBack = true; return nil }
func (c *updateConn) QueryContext(_ context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if !strings.HasSuffix(q, "LIMIT 2 FOR UPDATE") || len(args) != 1 {
		return nil, errors.New("unsafe row lock")
	}
	n := c.c.matches[c.c.queries]
	c.c.queries++
	return &matchRows{n: n}, nil
}
func (c *updateConn) ExecContext(_ context.Context, _ string, _ []sqldriver.NamedValue) (sqldriver.Result, error) {
	c.c.execs++
	if c.c.execs == c.c.failAt {
		return nil, errors.New("write failed")
	}
	return sqldriver.RowsAffected(c.c.affected), nil
}

type matchRows struct{ n int }

func (*matchRows) Columns() []string { return []string{"1"} }
func (*matchRows) Close() error      { return nil }
func (r *matchRows) Next(dest []sqldriver.Value) error {
	if r.n == 0 {
		return io.EOF
	}
	r.n--
	dest[0] = int64(1)
	return nil
}
func TestApplyUpdatesAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		matches  []int
		affected int64
		failAt   int
		success  bool
	}{
		{"success", []int{1, 1}, 1, 0, true}, {"unchanged value", []int{1, 1}, 0, 0, true}, {"missing row", []int{1, 0}, 1, 0, false}, {"duplicate key", []int{2}, 1, 0, false}, {"second write fails", []int{1, 1}, 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &updateConnector{matches: tc.matches, affected: tc.affected, failAt: tc.failAt}
			db := sql.OpenDB(c)
			defer db.Close()
			s := UpdateStatement{SQL: "UPDATE `db`.`t` SET `value` = ? WHERE `id` = ?;", Parameters: []any{"value", 1}, where: "`id` = ?", keyParameters: []any{1}}
			applied, err := MySQLApplyUpdates(context.Background(), db, "db", "t", []UpdateStatement{s, s})
			if tc.success {
				if err != nil || applied != 2 || !c.committed || c.rolledBack {
					t.Fatalf("%d %v %+v", applied, err, c)
				}
			} else if err == nil || applied != 0 || c.committed || !c.rolledBack {
				t.Fatalf("%d %v %+v", applied, err, c)
			}
		})
	}
}

type editMetadataConnector struct {
	engine           string
	columns, indexes [][]sqldriver.Value
}

func (c *editMetadataConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return editMetadataConn{c}, nil
}
func (*editMetadataConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type editMetadataConn struct{ c *editMetadataConnector }

func (editMetadataConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (editMetadataConn) Close() error                 { return nil }
func (editMetadataConn) Begin() (sqldriver.Tx, error) { return nil, errors.New("unsupported") }
func (c editMetadataConn) QueryContext(_ context.Context, q string, _ []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if strings.Contains(q, "information_schema.tables") {
		return &editRows{columns: []string{"ENGINE"}, values: [][]sqldriver.Value{{c.c.engine}}}, nil
	}
	if strings.Contains(q, "information_schema.columns") {
		return &editRows{columns: []string{"COLUMN_NAME", "IS_NULLABLE", "EXTRA", "DATA_TYPE"}, values: c.c.columns}, nil
	}
	return &editRows{columns: []string{"INDEX_NAME", "COLUMN_NAME", "SUB_PART"}, values: c.c.indexes}, nil
}

type editRows struct {
	columns []string
	values  [][]sqldriver.Value
}

func (r *editRows) Columns() []string { return r.columns }
func (*editRows) Close() error        { return nil }
func (r *editRows) Next(dest []sqldriver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
func TestEditMetadataKeys(t *testing.T) {
	for _, tc := range []struct {
		name, engine string
		nullable     bool
		indexes      [][]sqldriver.Value
		want         []string
		readonly     bool
	}{
		{name: "primary composite", engine: "InnoDB", indexes: [][]sqldriver.Value{{"PRIMARY", "id", nil}, {"PRIMARY", "tenant", nil}}, want: []string{"id", "tenant"}},
		{name: "unique fallback", engine: "InnoDB", indexes: [][]sqldriver.Value{{"unique_name", "id", nil}}, want: []string{"id"}},
		{name: "nullable unique", engine: "InnoDB", nullable: true, indexes: [][]sqldriver.Value{{"unique_name", "id", nil}}, readonly: true},
		{name: "prefix index", engine: "InnoDB", indexes: [][]sqldriver.Value{{"unique_name", "id", int64(4)}}, readonly: true},
		{name: "functional index", engine: "InnoDB", indexes: [][]sqldriver.Value{{"unique_name", nil, nil}}, readonly: true},
		{name: "no key", engine: "InnoDB", readonly: true},
		{name: "non transactional", engine: "MyISAM", indexes: [][]sqldriver.Value{{"PRIMARY", "id", nil}}, readonly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nullable := "NO"
			if tc.nullable {
				nullable = "YES"
			}
			c := &editMetadataConnector{engine: tc.engine, columns: [][]sqldriver.Value{{"id", nullable, "", "bigint"}, {"tenant", "NO", "", "bigint"}, {"computed", "YES", "STORED GENERATED", "int"}}, indexes: tc.indexes}
			db := sql.OpenDB(c)
			defer db.Close()
			m, err := MySQLEditMetadata(context.Background(), db, "db", "table")
			if tc.readonly {
				if !errors.Is(err, ErrReadOnlyTable) {
					t.Fatal(err)
				}
				return
			}
			if tc.name == "primary composite" && (!m.Columns["id"].PrimaryKey || !m.Columns["tenant"].PrimaryKey) {
				t.Fatal("composite primary key columns must be read-only")
			}
			if err != nil || !reflect.DeepEqual(m.Key, tc.want) || !m.Columns["computed"].Generated {
				t.Fatalf("%+v %v", m, err)
			}
		})
	}
}

func TestSafeEditKeyTypes(t *testing.T) {
	for _, name := range []string{"binary", "varbinary", "blob", "longblob", "bit", "timestamp", "datetime", "date", "time"} {
		if safeEditKeyType(name) {
			t.Fatalf("unsafe key type accepted: %s", name)
		}
	}
	for _, name := range []string{"bigint", "varchar", "decimal", "int"} {
		if !safeEditKeyType(name) {
			t.Fatalf("safe key type rejected: %s", name)
		}
	}
}
