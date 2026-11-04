package driver

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresFilters(t *testing.T) {
	columns := []RowColumn{{Name: `odd?"name`}, {Name: "id"}}
	for _, tc := range []struct {
		name    string
		filters []RowFilter
		want    string
		count   int
	}{
		{"quoted comparison", []RowFilter{{Column: `odd?"name`, Op: "eq", Value: "secret?"}}, ` WHERE ("odd?""name" = $1)`, 1},
		{"between and in", []RowFilter{{Column: "id", Op: "between", Value: []any{float64(1), float64(2)}}, {Column: "id", Op: "in", Connector: "or", Value: []any{float64(3)}}}, ` WHERE ("id" BETWEEN $1 AND $2) OR ("id" IN ($3))`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := rowFilters(columns, tc.filters, PostgresQuoteIdent, pgPlaceholder)
			if err != nil || got != tc.want || len(args) != tc.count {
				t.Fatalf("got %q, %d arguments, %v", got, len(args), err)
			}
		})
	}
}

// TestPostgresIntegration never writes to the developer's database.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("NICHOSI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NICHOSI_TEST_POSTGRES_DSN is unset")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skip("local PostgreSQL test database unavailable")
	}
	tables, objects, _, err := PostgresSchema(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		page, err := PostgresFilteredRows(ctx, db, table.Name, false, 1, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Rows) > 1 {
			t.Fatal("page exceeded limit")
		}
		metadata, err := PostgresMetadata(ctx, db, table.Name, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(metadata.Columns) != len(page.Columns) {
			t.Fatal("inconsistent column metadata")
		}
		break
	}
	for _, o := range objects {
		definition, err := PostgresObjectDefinition(ctx, db, "view", o.Name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(definition.Definition, "CREATE ") {
			t.Fatal("missing view definition")
		}
		break
	}
}
