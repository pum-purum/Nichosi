package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

type identityConnector struct {
	expiry                                      int64
	unsupported, failRead, failSet, failRestore bool
	sets                                        []int64
	closed                                      int
}

func (c *identityConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return &identityConn{c}, nil
}
func (*identityConnector) Driver() sqldriver.Driver { return definitionDriver{} }

type identityConn struct{ c *identityConnector }

func (*identityConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unsupported") }
func (c *identityConn) Close() error                         { c.c.closed++; return nil }
func (*identityConn) Begin() (sqldriver.Tx, error)           { return nil, errors.New("unsupported") }
func (c *identityConn) ExecContext(_ context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	if q != "SET SESSION information_schema_stats_expiry = ?" || len(args) != 1 {
		return nil, errors.New("unexpected SET")
	}
	value := args[0].Value.(int64)
	c.c.sets = append(c.c.sets, value)
	if value == 0 && c.c.failSet || value != 0 && c.c.failRestore {
		return nil, errors.New("SET failed")
	}
	c.c.expiry = value
	return sqldriver.RowsAffected(0), nil
}
func (c *identityConn) QueryContext(_ context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	if q == "SELECT @@SESSION.information_schema_stats_expiry" {
		if c.c.unsupported {
			return nil, &mysql.MySQLError{Number: 1193, Message: "Unknown system variable"}
		}
		return &editRows{columns: []string{"expiry"}, values: [][]sqldriver.Value{{c.c.expiry}}}, nil
	}
	if !strings.Contains(q, "CAST(AUTO_INCREMENT AS CHAR)") || !strings.HasSuffix(q, "LIMIT 1") || len(args) != 2 || args[0].Value != "db" || args[1].Value != "table" {
		return nil, errors.New("unsafe counter query")
	}
	if c.c.failRead {
		return nil, errors.New("read failed")
	}
	// Simulate two existing rows and a stale persisted counter of 1.
	value := "3"
	if !c.c.unsupported && c.c.expiry != 0 {
		value = "1"
	}
	return &editRows{columns: []string{"AUTO_INCREMENT"}, values: [][]sqldriver.Value{{value}}}, nil
}
func TestMySQLIdentityCacheBypass(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connector identityConnector
		wantSets  []int64
		wantError bool
		discard   bool
	}{
		{name: "stale cache", connector: identityConnector{expiry: 86400}, wantSets: []int64{0, 86400}},
		{name: "cache already disabled"},
		{name: "MariaDB or old MySQL", connector: identityConnector{unsupported: true}},
		{name: "failed read restores session", connector: identityConnector{expiry: 86400, failRead: true}, wantSets: []int64{0, 86400}, wantError: true},
		{name: "failed SET restores session", connector: identityConnector{expiry: 86400, failSet: true}, wantSets: []int64{0, 86400}, wantError: true},
		{name: "failed restore discards connection", connector: identityConnector{expiry: 86400, failRestore: true}, wantSets: []int64{0, 86400}, discard: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &tc.connector
			original := c.expiry
			db := sql.OpenDB(c)
			defer db.Close()
			db.SetMaxOpenConns(1)
			value, err := mysqlNextAutoIncrement(context.Background(), db, "db", "table")
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && (value == nil || *value != "3") {
				t.Fatalf("expected fresh counter 3, got %v", value)
			}
			if !reflect.DeepEqual(c.sets, tc.wantSets) {
				t.Fatalf("SET values: %v", c.sets)
			}
			if tc.discard {
				if c.closed != 1 {
					t.Fatal("dirty connection was not discarded")
				}
			} else if c.expiry != original {
				t.Fatalf("session expiry changed: %d", c.expiry)
			}
		})
	}
}
