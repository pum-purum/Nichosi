package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

type definitionConnector struct {
	columns      []string
	values       []sqldriver.Value
	query        string
	err          error
	metadataOnly bool
}

func (c *definitionConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &definitionConn{c}, nil
}
func (c *definitionConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type definitionDriver struct{}

func (definitionDriver) Open(string) (sqldriver.Conn, error) { return nil, errors.New("use connector") }

type definitionConn struct{ c *definitionConnector }

func (c *definitionConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (c *definitionConn) Close() error                 { return nil }
func (c *definitionConn) Begin() (sqldriver.Tx, error) { return nil, errors.New("not supported") }
func (c *definitionConn) QueryContext(_ context.Context, query string, _ []sqldriver.NamedValue) (sqldriver.Rows, error) {
	c.c.query = query
	if c.c.metadataOnly && !strings.Contains(query, ".nichosi_account_metadata") {
		return &definitionRows{read: true}, nil
	}
	if c.c.err != nil {
		return nil, c.c.err
	}
	return &definitionRows{columns: c.c.columns, values: c.c.values}, nil
}

type definitionRows struct {
	columns []string
	values  []sqldriver.Value
	read    bool
}

func (r *definitionRows) Columns() []string { return r.columns }
func (r *definitionRows) Close() error      { return nil }
func (r *definitionRows) Next(dest []sqldriver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.values)
	return nil
}

func TestMySQLObjectDefinition(t *testing.T) {
	for _, tc := range []struct {
		name, kind, column string
		value              sqldriver.Value
		queryErr           error
		wantError          bool
	}{
		{"view", "view", "Create View", "CREATE VIEW v AS SELECT 1", nil, false},
		{"trigger", "trigger", "SQL Original Statement", "CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW SET NEW.a = 1", nil, false},
		{"MariaDB trigger", "trigger", "Create Trigger", "CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW SET NEW.a = 1", nil, false},
		{"procedure", "procedure", "Create Procedure", "CREATE PROCEDURE p() SELECT 1", nil, false},
		{"function", "function", "Create Function", "CREATE FUNCTION f() RETURNS INT RETURN 1", nil, false},
		{"event", "event", "Create Event", "CREATE EVENT e ON SCHEDULE EVERY 1 DAY DISABLE DO SELECT 1", nil, false},
		{"hidden", "procedure", "Create Procedure", nil, nil, true},
		{"missing column", "event", "Event", "e", nil, true},
		{"permission denied", "event", "Create Event", nil, errors.New("denied"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &definitionConnector{columns: []string{"sql_mode", tc.column, "collation_connection"}, values: []sqldriver.Value{"", tc.value, "utf8mb4_general_ci"}, err: tc.queryErr}
			db := sql.OpenDB(connector)
			defer db.Close()
			object := Object{Name: "obj`name", Type: tc.kind, Details: map[string]string{}}
			err := mysqlObjectDefinition(context.Background(), db, "db`name", &object)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError = %v", err, tc.wantError)
			}
			if !strings.HasSuffix(connector.query, "`db``name`.`obj``name`") {
				t.Fatalf("unsafe identifier quoting: %s", connector.query)
			}
			if tc.wantError {
				if object.Definition != "" {
					t.Fatal("unavailable definition must remain empty")
				}
			} else if object.Definition != tc.value || object.Details["Definition source"] == "" {
				t.Fatalf("unexpected definition: %+v", object)
			}
		})
	}
}

func TestMySQLObjectDefinitionAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, kind           string
		wantError, wantQuery bool
	}{
		{"permission denied", "event", false, true},
		{"accounts forbidden", "user", true, false},
		{"roles forbidden", "role", true, false},
		{"injected type", "EVENT; SHOW CREATE USER root", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &definitionConnector{err: errors.New("server error containing sensitive data")}
			db := sql.OpenDB(connector)
			defer db.Close()
			object, err := MySQLObjectDefinition(context.Background(), db, "database", tc.kind, "name")
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if (connector.query != "") != tc.wantQuery {
				t.Fatalf("unexpected query: %s", connector.query)
			}
			if !tc.wantError && (object.Definition != "" || object.Details["Definition status"] != connector.err.Error()) {
				t.Fatalf("database error was not preserved: %+v", object)
			}
		})
	}
}

func TestMySQLAccountMetadataErrorIsPreserved(t *testing.T) {
	source := errors.New("Error 1142: SELECT denied\n<account metadata>")
	connector := &definitionConnector{err: source, metadataOnly: true}
	db := sql.OpenDB(connector)
	defer db.Close()
	_, warnings, err := MySQLObjects(context.Background(), db, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0] != source.Error() {
		t.Fatalf("warning does not preserve the database error: %v", warnings)
	}
}

func TestMySQLAccountMetadataViewIsOptional(t *testing.T) {
	connector := &definitionConnector{
		err:          &mysql.MySQLError{Number: 1146, Message: "Table 'demo.nichosi_account_metadata' doesn't exist"},
		metadataOnly: true,
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	objects, warnings, err := MySQLObjects(context.Background(), db, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if objects == nil || len(warnings) != 1 {
		t.Fatalf("expected an available catalog and one availability warning: objects=%v warnings=%v", objects, warnings)
	}
	if !strings.Contains(warnings[0], "optional nichosi_account_metadata view is not installed") || strings.Contains(warnings[0], "1146") {
		t.Fatalf("expected an actionable warning instead of a raw table error: %v", warnings)
	}
}
