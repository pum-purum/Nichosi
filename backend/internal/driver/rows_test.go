package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"strings"
	"testing"
)

type rowSourceConnector struct {
	sourceKind       string
	queries          []string
	arguments        [][]sqldriver.NamedValue
	deadline         bool
	hiddenDefinition bool
}

func (c *rowSourceConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &rowSourceConn{c}, nil
}
func (*rowSourceConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type rowSourceConn struct{ c *rowSourceConnector }

func (*rowSourceConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unsupported") }
func (*rowSourceConn) Close() error                           { return nil }
func (*rowSourceConn) Begin() (sqldriver.Tx, error)           { return nil, errors.New("unsupported") }
func (c *rowSourceConn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	c.c.queries = append(c.c.queries, q)
	c.c.arguments = append(c.c.arguments, args)
	_, c.c.deadline = ctx.Deadline()
	if strings.Contains(q, "information_schema.tables") {
		kind := "VIEW"
		if len(args) == 3 {
			kind = args[2].Value.(string)
		}
		var count int64
		if kind == c.c.sourceKind {
			count = 1
		}
		return &definitionRows{columns: []string{"COUNT(*)"}, values: []sqldriver.Value{count}}, nil
	}
	if strings.Contains(q, "information_schema.views") {
		return &definitionRows{columns: []string{"Security", "Check option"}, values: []sqldriver.Value{"DEFINER", "NONE"}}, nil
	}
	if strings.Contains(q, "information_schema.columns") {
		return &definitionRows{columns: []string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_KEY"}, values: []sqldriver.Value{"col`name", "decimal(30,10)", "YES", "PRI"}}, nil
	}
	if strings.HasPrefix(q, "SHOW CREATE VIEW") {
		var definition sqldriver.Value = "CREATE ALGORITHM=MERGE VIEW v AS SELECT 1"
		if c.c.hiddenDefinition {
			definition = nil
		}
		return &definitionRows{columns: []string{"Create View"}, values: []sqldriver.Value{definition}}, nil
	}
	if strings.HasPrefix(q, "SELECT COUNT(*)") {
		return &definitionRows{columns: []string{"COUNT(*)"}, values: []sqldriver.Value{int64(8)}}, nil
	}
	return &definitionRows{columns: []string{"col`name"}, values: []sqldriver.Value{[]byte("12345678901234567890.1234567890")}}, nil
}

func TestMySQLRowsKindsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, kind                           string
		view                                 bool
		limit, offset, wantLimit, wantOffset int
		missing                              bool
	}{
		{"view", "VIEW", true, 25, 50, 25, 50, false},
		{"table", "BASE TABLE", false, 200, 0, 200, 0, false},
		{"view cannot read table", "BASE TABLE", true, 25, 0, 25, 0, true},
		{"table cannot read view", "VIEW", false, 25, 0, 25, 0, true},
		{"limit clamped", "VIEW", true, 5000, -5, 1000, 0, false},
		{"minimum", "VIEW", true, 0, 0, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &rowSourceConnector{sourceKind: tc.kind}
			db := sql.OpenDB(connector)
			defer db.Close()
			page, err := MySQLRows(context.Background(), db, "db`name", "v`name", tc.view, tc.limit, tc.offset)
			if tc.missing {
				if !errors.Is(err, ErrRowsObjectNotFound) {
					t.Fatal(err)
				}
				if len(connector.queries) != 1 {
					t.Fatal("must reject before accessing rows")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if page.TotalRows != 8 || len(page.Rows) != 1 || page.Rows[0][0] != "12345678901234567890.1234567890" {
				t.Fatalf("bad page: %+v", page)
			}
			if tc.view && page.Columns[0].PrimaryKey {
				t.Fatal("view must not expose editable primary key")
			}
			query := connector.queries[len(connector.queries)-1]
			args := connector.arguments[len(connector.arguments)-1]
			if !strings.Contains(query, "SELECT `col``name` FROM `db``name`.`v``name` LIMIT ? OFFSET ?") || args[0].Value != int64(tc.wantLimit) || args[1].Value != int64(tc.wantOffset) {
				t.Fatalf("unsafe query or incorrect bounds: %s %v", query, args)
			}
			if connector.arguments[0][0].Value != "db`name" || connector.arguments[0][1].Value != "v`name" {
				t.Fatal("metadata names must be parameters")
			}
		})
	}
}

func TestMySQLViewMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, kind      string
		hidden, missing bool
	}{{"normal", "VIEW", false, false}, {"hidden definition", "VIEW", true, false}, {"reject table", "BASE TABLE", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &rowSourceConnector{sourceKind: tc.kind, hiddenDefinition: tc.hidden}
			db := sql.OpenDB(connector)
			defer db.Close()
			result, err := MySQLViewMetadata(context.Background(), db, "db`name", "v`name")
			if tc.missing {
				if !errors.Is(err, ErrViewNotFound) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Indexes) != 0 || len(result.ForeignKeys) != 0 || len(result.Triggers) != 0 || result.Details["Data bytes"] != "" {
				t.Fatalf("views have no own storage or indexes: %+v", result)
			}
			if tc.hidden {
				if result.Definition != "" || len(result.Warnings) == 0 {
					t.Fatal("hidden definition needs warning")
				}
			} else if result.Definition == "" || result.Details["Algorithm"] != "MERGE" {
				t.Fatalf("missing definition: %+v", result)
			}
		})
	}
}

func TestMySQLRowsOrdering(t *testing.T) {
	for _, tc := range []struct {
		name, column, direction, clause string
		invalid                         bool
	}{
		{"ascending default", "col" + "`" + "name", "", " ORDER BY `col``name` ASC", false},
		{"descending", "col" + "`" + "name", "desc", " ORDER BY `col``name` DESC", false},
		{"unknown column", "missing", "asc", "", true},
		{"column injection", "id; DROP TABLE t", "asc", "", true},
		{"direction injection", "col" + "`" + "name", "DESC; DROP TABLE t", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &rowSourceConnector{sourceKind: "BASE TABLE"}
			db := sql.OpenDB(connector)
			defer db.Close()
			_, err := MySQLRows(context.Background(), db, "db", "t", false, 25, 50, RowOrder{Column: tc.column, Direction: tc.direction})
			if tc.invalid {
				if !errors.Is(err, ErrInvalidOrder) {
					t.Fatalf("expected invalid ordering, got %v", err)
				}
				if len(connector.queries) != 2 {
					t.Fatal("invalid order must fail before reading data")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			query := connector.queries[len(connector.queries)-1]
			if !strings.Contains(query, tc.clause+" LIMIT ? OFFSET ?") {
				t.Fatalf("unexpected query: %s", query)
			}
		})
	}
}
