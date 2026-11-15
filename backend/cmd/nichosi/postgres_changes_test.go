package main

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPostgresGridEditingHTTP(t *testing.T) {
	writable, readonly := false, true
	for _, tc := range []struct {
		name                          string
		setting                       *bool
		publicReadonly, keyless, view bool
		editable                      bool
	}{
		{name: "writable", setting: &writable, editable: true},
		{name: "readonly default"},
		{name: "readonly explicit", setting: &readonly, publicReadonly: true},
		{name: "public readonly", setting: &writable, publicReadonly: true},
		{name: "keyless", setting: &writable, keyless: true},
		{name: "view", setting: &writable, view: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &postgresGridConnector{t: t, keyless: tc.keyless, view: tc.view}
			pool := sql.OpenDB(c)
			defer pool.Close()
			item := &profile{Connection: Connection{Id: "pg", Driver: ConnectionDriverPostgres, Readonly: tc.publicReadonly}, input: ConnectionInput{Readonly: tc.setting}, pool: pool}
			a := &app{connections: []*profile{item}}
			table := `"s.dot"."t""x"`
			request := httptest.NewRequest("POST", "/", strings.NewReader(`{}`))
			request.SetPathValue("connId", "pg")
			request.SetPathValue("table", table)
			request.SetPathValue("view", table)
			response := httptest.NewRecorder()
			a.queryObjectRows(response, request, tc.view)
			if response.Code != 200 {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			var page RowsPage
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Editable != tc.editable {
				t.Fatalf("editable=%v: %s", page.Editable, response.Body.String())
			}
			if tc.editable {
				if page.EditKey == nil || len(*page.EditKey) != 1 || (*page.EditKey)[0] != "id" || (page.ReadOnlyReason != nil && *page.ReadOnlyReason != "") {
					t.Fatal(page)
				}
				if page.Columns[0].AutoIncrement == nil || !*page.Columns[0].AutoIncrement || page.Columns[1].DefaultValue == nil || *page.Columns[1].DefaultValue != "'new'::text" {
					t.Fatal(page.Columns)
				}
			} else if page.ReadOnlyReason == nil || *page.ReadOnlyReason == "" {
				t.Fatal("missing reason")
			}
			for _, apply := range []bool{false, true} {
				request := httptest.NewRequest("POST", "/", strings.NewReader(`{"changes":[{"op":"insert","values":{"label":"O'Reilly"}}]}`))
				request.SetPathValue("connId", "pg")
				request.SetPathValue("table", table)
				response := httptest.NewRecorder()
				a.changeRows(response, request, apply)
				if !tc.editable {
					if response.Code != 403 && !(tc.view && response.Code == 404) {
						t.Fatalf("unsafe changes allowed: %d %s", response.Code, response.Body.String())
					}
					continue
				}
				if response.Code != 200 {
					t.Fatalf("%d %s", response.Code, response.Body.String())
				}
				if !apply {
					if c.committed || len(c.executed) > 0 {
						t.Fatal("preview wrote to database")
					}
					var preview ChangePreview
					if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
						t.Fatal(err)
					}
					if len(preview.Statements) != 1 || preview.Statements[0] != `INSERT INTO "s.dot"."t""x" ("label") VALUES ($1);` || preview.Parameters == nil || (*preview.Parameters)[0][0] != "O'Reilly" {
						t.Fatal(preview)
					}
				} else if !c.committed || len(c.executed) != 2 || c.executed[0] != "SET TRANSACTION READ WRITE" || len(c.arguments[1]) != 1 || c.arguments[1][0].Value != "O'Reilly" {
					t.Fatalf("%+v", c)
				}
			}
			if !tc.editable && (c.committed || len(c.executed) > 0) {
				t.Fatal("readonly source wrote to database")
			}
		})
	}
}

type postgresGridConnector struct {
	t             *testing.T
	keyless, view bool
	executed      []string
	arguments     [][]sqldriver.NamedValue
	committed     bool
}

func (c *postgresGridConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &postgresGridConn{c}, nil
}
func (*postgresGridConnector) Driver() sqldriver.Driver { return tableCheckDriver{} }

type postgresGridConn struct{ c *postgresGridConnector }

func (*postgresGridConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("unsupported")
}
func (*postgresGridConn) Close() error                   { return nil }
func (c *postgresGridConn) Begin() (sqldriver.Tx, error) { return c, nil }
func (c *postgresGridConn) Commit() error                { c.c.committed = true; return nil }
func (*postgresGridConn) Rollback() error                { return nil }
func (c *postgresGridConn) checkDeadline(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 10*time.Second {
		c.c.t.Fatal("missing bounded request timeout")
	}
}
func (c *postgresGridConn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	c.checkDeadline(ctx)
	c.c.executed = append(c.c.executed, q)
	c.c.arguments = append(c.c.arguments, args)
	return sqldriver.RowsAffected(1), nil
}
func (c *postgresGridConn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	c.checkDeadline(ctx)
	switch {
	case strings.Contains(q, "FROM pg_class c JOIN pg_namespace"):
		values := [][]sqldriver.Value{{int64(42), `"s.dot"."t""x"`}}
		if c.c.view && args[1].Value == "r,p" {
			values = nil
		}
		return &postgresGridRows{columns: []string{"oid", "source"}, values: values}, nil
	case strings.Contains(q, "FROM pg_attribute a JOIN pg_type"):
		return &postgresGridRows{columns: []string{"name", "type", "nullable", "generated", "identity", "default", "primary"}, values: [][]sqldriver.Value{{"id", "int8", false, true, true, nil, true}, {"label", "text", true, false, false, "'new'::text", false}}}, nil
	case strings.Contains(q, "FROM pg_index i CROSS JOIN LATERAL"):
		values := [][]sqldriver.Value{{int64(1), "id"}}
		if c.c.keyless {
			values = nil
		}
		return &postgresGridRows{columns: []string{"index", "column"}, values: values}, nil
	case strings.Contains(q, "format_type(a.atttypid,a.atttypmod)"):
		return &postgresGridRows{columns: []string{"name", "type", "nullable", "primary"}, values: [][]sqldriver.Value{{"id", "bigint", false, true}, {"label", "text", true, false}}}, nil
	case strings.HasPrefix(q, "SELECT COUNT(*) FROM"):
		return &postgresGridRows{columns: []string{"count"}, values: [][]sqldriver.Value{{int64(1)}}}, nil
	case strings.HasPrefix(q, `SELECT "id","label" FROM`):
		if len(args) != 2 || args[0].Value != int64(200) {
			c.c.t.Fatalf("missing default row limit: %+v", args)
		}
		return &postgresGridRows{columns: []string{"id", "label"}, values: [][]sqldriver.Value{{int64(1), "old"}}}, nil
	}
	return nil, errors.New("unexpected PostgreSQL query: " + q)
}

type postgresGridRows struct {
	columns []string
	values  [][]sqldriver.Value
}

func (r *postgresGridRows) Columns() []string { return r.columns }
func (*postgresGridRows) Close() error        { return nil }
func (r *postgresGridRows) Next(dest []sqldriver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dest, r.values[0])
	r.values = r.values[1:]
	return nil
}
