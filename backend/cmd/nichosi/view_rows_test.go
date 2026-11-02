package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type viewRowsConnector struct {
	deadline      time.Duration
	limit, offset int64
	err           error
}

func (c *viewRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return &viewRowsConn{c}, nil
}
func (*viewRowsConnector) Driver() driver.Driver { return tableCheckDriver{} }

type viewRowsConn struct{ c *viewRowsConnector }

func (*viewRowsConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (*viewRowsConn) Close() error                        { return nil }
func (*viewRowsConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }
func (c *viewRowsConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if deadline, ok := ctx.Deadline(); ok {
		c.c.deadline = time.Until(deadline)
	}
	if c.c.err != nil {
		return nil, c.c.err
	}
	switch {
	case strings.Contains(q, "information_schema.tables"):
		if args[2].Value != "VIEW" {
			return nil, errors.New("wrong object kind")
		}
		return &viewTestRows{columns: []string{"COUNT(*)"}, values: []driver.Value{int64(1)}}, nil
	case strings.Contains(q, "information_schema.columns"):
		return &viewTestRows{columns: []string{"COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE", "COLUMN_KEY"}, values: []driver.Value{"id", "int", "NO", "PRI"}}, nil
	case strings.HasPrefix(q, "SELECT COUNT(*)"):
		return &viewTestRows{columns: []string{"COUNT(*)"}, values: []driver.Value{int64(3)}}, nil
	default:
		c.c.limit = args[len(args)-2].Value.(int64)
		c.c.offset = args[len(args)-1].Value.(int64)
		return &viewTestRows{columns: []string{"id"}, values: []driver.Value{int64(2)}}, nil
	}
}

type viewTestRows struct {
	columns []string
	values  []driver.Value
	read    bool
}

func (r *viewTestRows) Columns() []string { return r.columns }
func (*viewTestRows) Close() error        { return nil }
func (r *viewTestRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.values)
	return nil
}

func TestViewRowsReadOnlyAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		limit, offset int64
	}{{"default", "{}", 200, 0}, {"bounded", `{"limit":9000,"offset":-3}`, 1000, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			connector := &viewRowsConnector{}
			pool := sql.OpenDB(connector)
			defer pool.Close()
			application := &app{connections: []*profile{{Connection: Connection{Id: "test", Database: "demo", Readonly: false}, pool: pool}}}
			request := httptest.NewRequest(http.MethodPost, "/api/connections/test/views/v/rows", strings.NewReader(tc.body))
			request.SetPathValue("connId", "test")
			request.SetPathValue("view", "v")
			response := httptest.NewRecorder()
			application.queryViewRows(response, request)
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			var page RowsPage
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Editable || page.ReadOnlyReason == nil || *page.ReadOnlyReason != "Views are read-only" || page.TotalRows != 3 || len(page.Rows) != 1 {
				t.Fatalf("invalid view page: %+v", page)
			}
			if connector.limit != tc.limit || connector.offset != tc.offset {
				t.Fatalf("bounds: %d %d", connector.limit, connector.offset)
			}
			if connector.deadline <= 0 || connector.deadline > 10*time.Second {
				t.Fatalf("timeout: %v", connector.deadline)
			}
		})
	}
}
func TestViewRowsErrorDoesNotLeakDetails(t *testing.T) {
	connector := &viewRowsConnector{err: errors.New("mysql://user:secret@private-host/db")}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	application := &app{connections: []*profile{{Connection: Connection{Id: "test", Database: "demo"}, pool: pool}}}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	request.SetPathValue("connId", "test")
	request.SetPathValue("view", "v")
	response := httptest.NewRecorder()
	application.queryViewRows(response, request)
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "private-host") {
		t.Fatalf("unsafe error: %s", response.Body.String())
	}
}
