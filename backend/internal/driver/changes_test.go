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
		{"delete", RowChange{Op: "delete", Key: map[string]any{"id": "1"}}, false},
		{"delete with values", RowChange{Op: "delete", Key: map[string]any{"id": "1"}, Values: map[string]any{"other": "x"}}, true},
		{"delete missing key", RowChange{Op: "delete"}, true},
		{"delete null key", RowChange{Op: "delete", Key: map[string]any{"id": nil}}, true},
		{"delete extra key", RowChange{Op: "delete", Key: map[string]any{"id": "1", "other": "x"}}, true},
		{"insert with key", RowChange{Op: "insert", Key: map[string]any{"id": "1"}, Values: map[string]any{"other": "x"}}, true},
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
			if tc.name == "delete" {
				if statements[0].SQL != "DELETE FROM `db``name`.`table` WHERE `id` = ?;" || !reflect.DeepEqual(statements[0].Parameters, []any{"1"}) {
					t.Fatalf("%+v", statements[0])
				}
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
		values := make([][]sqldriver.Value, len(c.c.columns))
		for i, row := range c.c.columns {
			values[i] = append(append([]sqldriver.Value{}, row...), nil, "8.0.40")
		}
		return &editRows{columns: []string{"COLUMN_NAME", "IS_NULLABLE", "EXTRA", "DATA_TYPE", "COLUMN_DEFAULT", "VERSION()"}, values: values}, nil
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

func TestDeleteCompositeKey(t *testing.T) {
	metadata := EditMetadata{Columns: map[string]EditColumn{"tenant": {}, "id": {}}, Key: []string{"tenant", "id"}}
	statements, err := BuildUpdates("db", "t`able", metadata, []RowChange{{Op: "delete", Key: map[string]any{"id": json.Number("9007199254740993"), "tenant": "x' OR 1=1 --"}}})
	if err != nil {
		t.Fatal(err)
	}
	if statements[0].SQL != "DELETE FROM `db`.`t``able` WHERE `tenant` = ? AND `id` = ?;" || !reflect.DeepEqual(statements[0].Parameters, []any{"x' OR 1=1 --", "9007199254740993"}) {
		t.Fatalf("%+v", statements[0])
	}
}

func TestApplyDeleteAtomicity(t *testing.T) {
	metadata := EditMetadata{Columns: map[string]EditColumn{"id": {}}, Key: []string{"id"}}
	statements, err := BuildUpdates("db", "t", metadata, []RowChange{{Op: "delete", Key: map[string]any{"id": "1"}}, {Op: "delete", Key: map[string]any{"id": "2"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		matches []int
		failAt  int
		success bool
	}{
		{"success", []int{1, 1}, 0, true},
		{"missing second row", []int{1, 0}, 0, false},
		{"duplicate key", []int{2}, 0, false},
		{"second delete fails", []int{1, 1}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &updateConnector{matches: tc.matches, affected: 1, failAt: tc.failAt}
			db := sql.OpenDB(c)
			defer db.Close()
			applied, err := MySQLApplyUpdates(context.Background(), db, "db", "t", statements)
			if tc.success {
				if err != nil || applied != 2 || c.execs != 2 || !c.committed || c.rolledBack {
					t.Fatalf("%d %v %+v", applied, err, c)
				}
			} else if err == nil || applied != 0 || c.committed || !c.rolledBack {
				t.Fatalf("%d %v %+v", applied, err, c)
			}
		})
	}
}

func TestIntegerPreviewParameters(t *testing.T) {
	for _, tc := range []struct {
		name, columnType string
		input, want      any
	}{
		{"JSON bigint", "bigint", json.Number("1"), int64(1)},
		{"text bigint", "bigint", "1", int64(1)},
		{"negative integer", "int", "-42", int64(-42)},
		{"safe boundary", "bigint", "9007199254740991", int64(9007199254740991)},
		{"large bigint", "bigint", "9007199254740993", "9007199254740993"},
		{"unsigned bigint", "bigint", "18446744073709551615", "18446744073709551615"},
		{"text identifier", "varchar", "001", "001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := EditMetadata{Columns: map[string]EditColumn{"id": {Type: tc.columnType}, "value": {Type: tc.columnType}}, Key: []string{"id"}}
			statements, err := BuildUpdates("db", "table", metadata, []RowChange{{Op: "update", Key: map[string]any{"id": tc.input}, Values: map[string]any{"value": tc.input}}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(statements[0].Parameters, []any{tc.want, tc.want}) {
				t.Fatalf("parameters: %#v", statements[0].Parameters)
			}
		})
	}
}

func TestInsertStatements(t *testing.T) {
	metadata := EditMetadata{Columns: map[string]EditColumn{"id": {PrimaryKey: true}, "na`me": {Nullable: true}, "computed": {Generated: true}}, Key: []string{"id"}}
	for _, tc := range []struct {
		name, dialect string
		values        map[string]any
		sql           string
		invalid       bool
	}{
		{"bound values", "mysql", map[string]any{"id": "9007199254740993", "na`me": "NOW(); DROP TABLE t"}, "INSERT INTO `db``name`.`table` (`id`, `na``me`) VALUES (?, ?);", false},
		{"mysql defaults", "mysql", nil, "INSERT INTO `db``name`.`table` () VALUES ();", false},
		{"sqlite defaults", "sqlite", nil, "INSERT INTO `db``name`.`table` DEFAULT VALUES;", false},
		{"generated", "mysql", map[string]any{"computed": "x"}, "", true},
		{"unknown", "mysql", map[string]any{"unknown": "x"}, "", true},
		{"null key", "mysql", map[string]any{"id": nil}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := BuildChanges("db`name", "table", metadata, []RowChange{{Op: "insert", Values: tc.values}}, tc.dialect)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidChange) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if statements[0].SQL != tc.sql {
				t.Fatal(statements[0].SQL)
			}
			if tc.name == "bound values" && !reflect.DeepEqual(statements[0].Parameters, []any{"9007199254740993", "NOW(); DROP TABLE t"}) {
				t.Fatal(statements[0].Parameters)
			}
		})
	}
}

func TestMySQLInsertAtomicity(t *testing.T) {
	for _, failAt := range []int{0, 2} {
		c := &updateConnector{affected: 1, failAt: failAt}
		db := sql.OpenDB(c)
		statements := []UpdateStatement{{insert: true, SQL: "INSERT INTO `db`.`t` () VALUES ();"}, {insert: true, SQL: "INSERT INTO `db`.`t` () VALUES ();"}}
		applied, err := MySQLApplyUpdates(context.Background(), db, "db", "t", statements)
		db.Close()
		if failAt == 0 {
			if err != nil || applied != 2 || !c.committed {
				t.Fatalf("%d %v %+v", applied, err, c)
			}
		} else if err == nil || applied != 0 || !c.rolledBack || c.committed {
			t.Fatalf("%d %v %+v", applied, err, c)
		}
		if c.queries != 0 {
			t.Fatal("inserts must not lock a nonexistent original row")
		}
	}
}

func TestMySQLDefaultSQL(t *testing.T) {
	for _, tc := range []struct{ value, typ, extra, version, want string }{
		{"NOW()", "varchar", "", "8.0", "'NOW()'"},
		{"", "varchar", "", "8.0", "''"},
		{"O'Reilly", "varchar", "", "8.0", "'O''Reilly'"},
		{"CURRENT_TIMESTAMP(6)", "timestamp", "", "5.7", "CURRENT_TIMESTAMP(6)"},
		{"CURRENT_TIMESTAMP", "varchar", "", "8.0", "'CURRENT_TIMESTAMP'"},
		{"uuid()", "varchar", "DEFAULT_GENERATED", "8.0", "uuid()"},
		{"42", "int", "", "8.0", "42"},
		{"'NOW()'", "varchar", "", "11.4-MariaDB", "'NOW()'"},
		{"current_timestamp()", "timestamp", "", "11.4-MariaDB", "current_timestamp()"},
	} {
		t.Run(tc.value+tc.typ+tc.version, func(t *testing.T) {
			got := mysqlDefaultSQL(&tc.value, tc.typ, tc.extra, tc.version)
			if got == nil || *got != tc.want {
				t.Fatalf("got %v, want %q", got, tc.want)
			}
		})
	}
	if mysqlDefaultSQL(nil, "text", "", "8.0") != nil {
		t.Fatal("missing default must stay absent")
	}
}
