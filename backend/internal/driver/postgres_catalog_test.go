package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type postgresCatalogConnector struct {
	query     func(string, []sqldriver.NamedValue) (sqldriver.Rows, error)
	queries   []string
	arguments [][]sqldriver.NamedValue
}

func (c *postgresCatalogConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return postgresCatalogConn{c}, nil
}
func (c *postgresCatalogConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type postgresCatalogConn struct{ c *postgresCatalogConnector }

func (postgresCatalogConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (postgresCatalogConn) Close() error { return nil }
func (postgresCatalogConn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("writes are forbidden")
}
func (c postgresCatalogConn) QueryContext(_ context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	c.c.queries = append(c.c.queries, q)
	c.c.arguments = append(c.c.arguments, args)
	return c.c.query(q, args)
}

type postgresCatalogRows struct {
	columns []string
	values  [][]sqldriver.Value
	offset  int
}

func (r *postgresCatalogRows) Columns() []string { return r.columns }
func (r *postgresCatalogRows) Close() error      { return nil }
func (r *postgresCatalogRows) Next(dest []sqldriver.Value) error {
	if r.offset == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.offset])
	r.offset++
	return nil
}

func TestPostgresCatalogPermissionsAndCron(t *testing.T) {
	denied := &pgconn.PgError{Code: "42501", Message: "permission denied"}
	for _, tc := range []struct {
		name                                                           string
		installed, accessible, denyAccounts, denyMemberships, denyCron bool
		wantCron                                                       bool
		warning                                                        string
	}{
		{name: "not installed"},
		{name: "installed inaccessible", installed: true, warning: "pg_cron is installed"},
		{name: "available", installed: true, accessible: true, wantCron: true},
		{name: "cron permission race", installed: true, accessible: true, denyCron: true, warning: "pg_cron jobs are unavailable"},
		{name: "accounts denied", denyAccounts: true, warning: "Accounts metadata is unavailable"},
		{name: "memberships denied", denyMemberships: true, warning: "Role memberships are unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &postgresCatalogConnector{query: func(q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if strings.HasPrefix(q, "SELECT EXISTS(SELECT 1 FROM pg_extension") {
					return &postgresCatalogRows{columns: []string{"installed", "accessible"}, values: [][]sqldriver.Value{{tc.installed, tc.accessible}}}, nil
				}
				if strings.Contains(q, "FROM pg_roles ORDER") {
					if tc.denyAccounts {
						return nil, denied
					}
					return &postgresCatalogRows{columns: []string{"__name", "__type", "Login allowed"}, values: [][]sqldriver.Value{{`reader"name`, "user", true}, {"reader_group", "role", false}}}, nil
				}
				if strings.Contains(q, "FROM pg_auth_members") {
					if tc.denyMemberships {
						return nil, denied
					}
					return &postgresCatalogRows{columns: []string{"Account", "Role", "Admin option"}, values: [][]sqldriver.Value{{`reader"name`, "reader_group", true}}}, nil
				}
				if strings.Contains(q, "FROM cron.job") {
					if tc.denyCron {
						return nil, denied
					}
					return &postgresCatalogRows{columns: []string{"__name", "__type", "Schedule"}, values: [][]sqldriver.Value{{"1", "cron", "* * * * *"}}}, nil
				}
				return &postgresCatalogRows{}, nil
			}}
			db := sql.OpenDB(connector)
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			catalog, err := PostgresCatalog(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			supported := map[string]bool{}
			for _, kind := range catalog.SupportedObjectTypes {
				supported[kind] = true
			}
			if supported["event"] || supported["cron"] != tc.wantCron || !supported["materialized_view"] || !supported["role"] {
				t.Fatalf("incorrect capabilities: %v", catalog.SupportedObjectTypes)
			}
			if tc.warning != "" && !strings.Contains(strings.Join(catalog.Warnings, "\n"), tc.warning) {
				t.Fatalf("missing warning: %v", catalog.Warnings)
			}
			for _, object := range catalog.Objects {
				if object.Type == "user" {
					if object.Definition != "" {
						t.Fatal("authentication definition returned")
					}
					if !tc.denyMemberships && object.Details["Member of"] != `"reader_group" (admin option)` {
						t.Fatalf("membership not exposed: %+v", object)
					}
				}
				if object.Type == "role" && object.Definition != `CREATE ROLE "reader_group" NOLOGIN;` {
					t.Fatal("incorrect role reconstruction")
				}
				if object.Type == "cron" && (object.Definition != "" || object.Details["Definition status"] == "") {
					t.Fatal("cron command must be withheld")
				}
			}
			for _, query := range connector.queries {
				lower := strings.ToLower(query)
				for _, secretSource := range []string{"rolpassword", "pg_authid", "command"} {
					if strings.Contains(lower, secretSource) {
						t.Fatalf("secret source queried: %s", secretSource)
					}
				}
				if !strings.HasPrefix(query, "SELECT ") {
					t.Fatal("catalog must be read-only")
				}
				if strings.Contains(query, "pg_get_functiondef(") {
					t.Fatal("definition fetched eagerly")
				}
			}
		})
	}
}

func TestPostgresRoutineDefinitionUsesIdentityAndParameters(t *testing.T) {
	for _, kind := range []string{"function", "procedure"} {
		t.Run(kind, func(t *testing.T) {
			names := []string{`"odd.schema".echo(value integer)`, `"odd.schema".echo(value text)`, `bad'); SELECT 1; --`}
			connector := &postgresCatalogConnector{query: func(q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if !strings.Contains(q, pgRoutineName+"=$1") || !strings.Contains(q, "pg_get_functiondef(p.oid)") || len(args) != 2 {
					t.Fatalf("identity lookup is not parameterized: %s", q)
				}
				if strings.Contains(q, args[0].Value.(string)) {
					t.Fatal("object name interpolated in SQL")
				}
				return &postgresCatalogRows{columns: []string{"definition"}, values: [][]sqldriver.Value{{"CREATE " + strings.ToUpper(kind) + " " + args[0].Value.(string)}}}, nil
			}}
			db := sql.OpenDB(connector)
			defer db.Close()
			for _, name := range names {
				object, err := PostgresObjectDefinition(context.Background(), db, kind, name)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(object.Definition, name) {
					t.Fatal("overload identity was lost")
				}
			}
			if connector.arguments[0][0].Value == connector.arguments[1][0].Value {
				t.Fatal("overloads collided")
			}
		})
	}
}

func TestPostgresDefinitionAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		value      sqldriver.Value
		err        error
		wantError  bool
	}{
		{name: "permission", kind: "function", err: &pgconn.PgError{Code: "42501"}},
		{name: "missing", kind: "function", err: sql.ErrNoRows},
		{name: "not reconstructible type", kind: "type"},
		{name: "server failure", kind: "index", err: &pgconn.PgError{Code: "08006"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &postgresCatalogConnector{query: func(string, []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return &postgresCatalogRows{columns: []string{"definition"}, values: [][]sqldriver.Value{{tc.value}}}, nil
			}}
			db := sql.OpenDB(connector)
			defer db.Close()
			object, err := PostgresObjectDefinition(context.Background(), db, tc.kind, "identity")
			if (err != nil) != tc.wantError {
				t.Fatal(err)
			}
			if !tc.wantError && (object.Definition != "" || object.Details["Definition status"] == "") {
				t.Fatalf("missing availability status: %+v", object)
			}
		})
	}
	for _, kind := range []string{"user", "role", "event", "table", "FUNCTION; SELECT 1"} {
		object, err := PostgresObjectDefinition(context.Background(), nil, kind, "name")
		if err == nil || object.Definition != "" {
			t.Fatalf("unsupported type %s accepted", kind)
		}
	}
}

func TestPostgresCategoryLimit(t *testing.T) {
	values := make([][]sqldriver.Value, 1001)
	for i := range values {
		values[i] = []sqldriver.Value{"role", "role"}
	}
	db := sql.OpenDB(&postgresCatalogConnector{query: func(string, []sqldriver.NamedValue) (sqldriver.Rows, error) {
		return &postgresCatalogRows{columns: []string{"__name", "__type"}, values: values}, nil
	}})
	defer db.Close()
	objects, truncated, err := postgresCategory(context.Background(), db, "SELECT explicit_columns FROM catalog LIMIT 1001")
	if err != nil || len(objects) != 1000 || !truncated {
		t.Fatalf("unbounded category: %d %v %v", len(objects), truncated, err)
	}
}
