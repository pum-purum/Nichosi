package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"strings"
	"testing"
)

func TestPostgresNextIdentity(t *testing.T) {
	for _, tc := range []struct {
		last, step, min, max, want string
		called, cycle              bool
	}{
		{"7", "3", "1", "100", "7", false, false},
		{"9007199254740993", "2", "1", "9223372036854775807", "9007199254740995", true, false},
		{"-4", "-2", "-10", "-1", "-6", true, false},
		{"10", "1", "1", "10", "1", true, true},
		{"10", "1", "1", "10", "", true, false},
	} {
		t.Run(tc.last+tc.want, func(t *testing.T) {
			db := sql.OpenDB(&postgresCatalogConnector{query: func(q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if strings.Contains(q, "JOIN pg_sequence") {
					if len(args) != 2 || args[0].Value != int64(42) || args[1].Value != "id" {
						t.Fatalf("unbound lookup: %v", args)
					}
					return &editRows{columns: []string{"schema", "name", "step", "min", "max", "cycle"}, values: [][]sqldriver.Value{{"odd.schema", `seq"name`, tc.step, tc.min, tc.max, tc.cycle}}}, nil
				}
				if q != `SELECT last_value::text,is_called FROM "odd.schema"."seq""name"` {
					t.Fatalf("unexpected SQL: %s", q)
				}
				return &editRows{columns: []string{"last", "called"}, values: [][]sqldriver.Value{{tc.last, tc.called}}}, nil
			}})
			defer db.Close()
			got, err := postgresNextIdentity(context.Background(), db, 42, "id")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal(*got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}
