package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresBuildChanges(t *testing.T) {
	metadata := EditMetadata{source: `"schema.with.dot"."ta""ble"`, Columns: map[string]EditColumn{"id": {Type: "int8", PrimaryKey: true}, "tenant": {Type: "text"}, `val"ue`: {Nullable: true}, "identity": {Generated: true}, "computed": {Generated: true}}, Key: []string{"tenant", "id"}}
	for _, tc := range []struct {
		name       string
		changes    []RowChange
		sql        string
		parameters []any
		invalid    bool
	}{
		{name: "insert literal", changes: []RowChange{{Op: "insert", Values: map[string]any{"id": "9007199254740993", `val"ue`: "NOW(); DROP TABLE x"}}}, sql: `INSERT INTO "schema.with.dot"."ta""ble" ("id", "val""ue") VALUES ($1, $2);`, parameters: []any{"9007199254740993", "NOW(); DROP TABLE x"}},
		{name: "default insert", changes: []RowChange{{Op: "insert"}}, sql: `INSERT INTO "schema.with.dot"."ta""ble" DEFAULT VALUES;`, parameters: []any{}},
		{name: "update composite", changes: []RowChange{{Op: "update", Values: map[string]any{`val"ue`: nil}, Key: map[string]any{"tenant": "x' OR 1=1 --", "id": "7"}}}, sql: `UPDATE "schema.with.dot"."ta""ble" SET "val""ue" = $1 WHERE "tenant" = $2 AND "id" = $3;`, parameters: []any{nil, "x' OR 1=1 --", int64(7)}},
		{name: "delete composite", changes: []RowChange{{Op: "delete", Key: map[string]any{"tenant": "x", "id": "7"}}}, sql: `DELETE FROM "schema.with.dot"."ta""ble" WHERE "tenant" = $1 AND "id" = $2;`, parameters: []any{"x", int64(7)}},
		{name: "identity always", changes: []RowChange{{Op: "insert", Values: map[string]any{"identity": "1"}}}, invalid: true},
		{name: "generated", changes: []RowChange{{Op: "insert", Values: map[string]any{"computed": "1"}}}, invalid: true},
		{name: "update primary key", changes: []RowChange{{Op: "update", Values: map[string]any{"id": "1"}, Key: map[string]any{"tenant": "x", "id": "7"}}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			statements, err := BuildChanges("ignored database", "untrusted.table", metadata, tc.changes, "postgres")
			if tc.invalid {
				if !errors.Is(err, ErrInvalidChange) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if statements[0].SQL != tc.sql || !reflect.DeepEqual(statements[0].Parameters, tc.parameters) {
				t.Fatalf("%+v", statements[0])
			}
		})
	}
	metadata.source = ""
	if _, err := BuildChanges("db", "unsafe", metadata, []RowChange{{Op: "insert"}}, "postgres"); !errors.Is(err, ErrInvalidChange) {
		t.Fatal("unresolved relation accepted", err)
	}
}

func TestPostgresEditMetadata(t *testing.T) {
	for _, tc := range []struct {
		name           string
		nullable       bool
		keyType        string
		indexes        [][]sqldriver.Value
		want           []string
		missing, empty bool
	}{
		{name: "primary composite", indexes: [][]sqldriver.Value{{int64(1), "id"}, {int64(1), "tenant"}}, want: []string{"id", "tenant"}},
		{name: "unique fallback", nullable: true, indexes: [][]sqldriver.Value{{int64(1), "id"}, {int64(2), "tenant"}}, want: []string{"tenant"}},
		{name: "nullable unique", nullable: true, indexes: [][]sqldriver.Value{{int64(1), "id"}}},
		{name: "binary key", keyType: "bytea", indexes: [][]sqldriver.Value{{int64(1), "id"}}},
		{name: "temporal key", keyType: "timestamptz", indexes: [][]sqldriver.Value{{int64(1), "id"}}},
		{name: "float key", keyType: "float8", indexes: [][]sqldriver.Value{{int64(1), "id"}}},
		{name: "expression key", indexes: [][]sqldriver.Value{{int64(1), nil}}},
		{name: "keyless"}, {name: "view or missing", missing: true}, {name: "inheritance parent", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyType := tc.keyType
			if keyType == "" {
				keyType = "int8"
			}
			c := &postgresCatalogConnector{query: func(q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if strings.Contains(q, "FROM pg_class c JOIN pg_namespace") {
					if len(args) != 2 || args[0].Value != `"schema.dot"."ta""ble"` || args[1].Value != "r,p" {
						t.Fatalf("unbounded relation lookup: %s %+v", q, args)
					}
					values := [][]sqldriver.Value{{int64(42), `"schema.dot"."ta""ble"`}}
					if tc.missing {
						values = nil
					}
					return &editRows{columns: []string{"oid", "source"}, values: values}, nil
				}
				if len(args) != 1 || args[0].Value != int64(42) {
					t.Fatalf("metadata not bound to OID: %s %+v", q, args)
				}
				if strings.Contains(q, "FROM pg_attribute a JOIN pg_type") {
					if !strings.Contains(q, "LIMIT 1001") || !strings.Contains(q, "pg_inherits") {
						t.Fatal("unbounded metadata or unsafe inheritance")
					}
					values := [][]sqldriver.Value{{"id", keyType, tc.nullable, false, true, nil, true}, {"tenant", "text", false, false, false, nil, false}, {"computed", "int4", false, true, false, "1+1", false}, {"serial", "int8", false, false, false, "nextval('seq'::regclass)", false}, {"always", "int8", false, true, true, nil, false}}
					if tc.empty {
						values = nil
					}
					return &editRows{columns: []string{"name", "type", "nullable", "generated", "identity", "default", "primary"}, values: values}, nil
				}
				if strings.Contains(q, "FROM pg_index i CROSS JOIN LATERAL") {
					for _, guard := range []string{"i.indisunique", "i.indisvalid", "i.indisready", "i.indimmediate", "i.indpred IS NULL", "i.indexprs IS NULL", "k.position<=i.indnkeyatts", "ORDER BY i.indisprimary DESC", "LIMIT 1001"} {
						if !strings.Contains(q, guard) {
							t.Fatal("missing key guard", guard)
						}
					}
					return &editRows{columns: []string{"index", "column"}, values: tc.indexes}, nil
				}
				return nil, errors.New("unexpected metadata query")
			}}
			db := sql.OpenDB(c)
			defer db.Close()
			m, err := PostgresEditMetadata(context.Background(), db, `"schema.dot"."ta""ble"`)
			if tc.missing {
				if !errors.Is(err, ErrRowsObjectNotFound) {
					t.Fatal(err)
				}
				return
			}
			if tc.want == nil {
				if !errors.Is(err, ErrReadOnlyTable) {
					t.Fatal(err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(m.Key, tc.want) || m.source != `"schema.dot"."ta""ble"` || !m.Columns["computed"].Generated || !m.Columns["serial"].AutoIncrement || !m.Columns["always"].Generated || !m.Columns["always"].AutoIncrement || !m.Columns["id"].PrimaryKey {
				t.Fatalf("%+v %v", m, err)
			}
		})
	}
}

func TestPostgresApplyChangesAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		failAt   int
		success  bool
	}{
		{"success", 1, 0, true}, {"missing row", 0, 0, false}, {"duplicate row", 2, 0, false}, {"second write fails", 1, 3, false}, {"standby refuses writes", 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &postgresChangeConnector{affected: tc.affected, failAt: tc.failAt}
			db := sql.OpenDB(c)
			defer db.Close()
			statements := []UpdateStatement{{SQL: `INSERT INTO "public"."t" ("value") VALUES ($1);`, Parameters: []any{"literal"}}, {SQL: `UPDATE "public"."t" SET "value"=$1 WHERE "id"=$2;`, Parameters: []any{"updated", int64(1)}}}
			applied, err := PostgresApplyUpdates(context.Background(), db, statements)
			if tc.success {
				if err != nil || applied != 2 || !c.committed || c.rolledBack {
					t.Fatalf("%d %v %+v", applied, err, c)
				}
			} else if err == nil || applied != 0 || c.committed || !c.rolledBack {
				t.Fatalf("%d %v %+v", applied, err, c)
			}
			if len(c.sql) == 0 || c.sql[0] != "SET TRANSACTION READ WRITE" {
				t.Fatal(c.sql)
			}
			if tc.success && (c.sql[1] != statements[0].SQL || len(c.arguments[1]) != 1 || c.arguments[1][0].Value != "literal" || c.arguments[2][1].Value != int64(1)) {
				t.Fatalf("%+v", c)
			}
		})
	}
}

type postgresChangeConnector struct {
	affected              int64
	failAt                int
	sql                   []string
	arguments             [][]sqldriver.NamedValue
	committed, rolledBack bool
}

func (c *postgresChangeConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &postgresChangeConn{c}, nil
}
func (*postgresChangeConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type postgresChangeConn struct{ c *postgresChangeConnector }

func (*postgresChangeConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (*postgresChangeConn) Close() error                   { return nil }
func (c *postgresChangeConn) Begin() (sqldriver.Tx, error) { return c, nil }
func (c *postgresChangeConn) Commit() error                { c.c.committed = true; return nil }
func (c *postgresChangeConn) Rollback() error              { c.c.rolledBack = true; return nil }
func (c *postgresChangeConn) ExecContext(_ context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	c.c.sql = append(c.c.sql, q)
	c.c.arguments = append(c.c.arguments, args)
	if len(c.c.sql) == c.c.failAt {
		return nil, errors.New("write failed")
	}
	return sqldriver.RowsAffected(c.c.affected), nil
}
