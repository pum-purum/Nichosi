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

	catalog, err := PostgresCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range catalog.SupportedObjectTypes {
		if kind == "event" {
			t.Fatal("MySQL events must not be advertised by PostgreSQL")
		}
	}
	found := map[string]int{}
	for _, o := range objects {
		found[o.Type]++
		if o.Type == "user" {
			if o.Definition != "" {
				t.Fatal("account authentication definition returned")
			}
			continue
		}
		if o.Type == "role" {
			if !strings.HasPrefix(o.Definition, "CREATE ROLE ") {
				t.Fatal("role reconstruction unavailable")
			}
			continue
		}
		definition, err := PostgresObjectDefinition(ctx, db, o.Type, o.Name)
		if err != nil {
			t.Fatalf("%s %s: %v", o.Type, o.Name, err)
		}
		if definition.Definition == "" && definition.Details["Definition status"] == "" {
			t.Fatalf("missing definition availability for %s", o.Name)
		}
		if o.Type == "view" || o.Type == "materialized_view" {
			metadata, err := PostgresMetadata(ctx, db, o.Name, true)
			if err != nil {
				t.Fatal(err)
			}
			if metadata.Definition != definition.Definition || metadata.Details["Definition source"] == "" {
				t.Fatal("view metadata lost definition or source")
			}
			if len(metadata.Indexes) != 0 || len(metadata.ForeignKeys) != 0 || len(metadata.Triggers) != 0 {
				t.Fatal("view metadata violated relation envelope")
			}
		}
	}
	fixture := false
	for _, table := range tables {
		if table.Name == `"odd.schema"."odd.table"` {
			fixture = true
		}
	}
	if fixture {
		for _, kind := range []string{"user", "role", "trigger", "procedure", "function", "schema", "sequence", "type", "extension", "index", "view", "materialized_view"} {
			if found[kind] == 0 {
				t.Errorf("missing fixture category %s", kind)
			}
		}
		overloads := map[string]string{}
		for _, o := range objects {
			if o.Type == "function" && strings.HasPrefix(o.Name, `"odd.schema".echo(`) {
				definition, err := PostgresObjectDefinition(ctx, db, o.Type, o.Name)
				if err != nil {
					t.Fatal(err)
				}
				overloads[o.Name] = definition.Definition
			}
		}
		if len(overloads) != 2 {
			t.Fatalf("overloaded routines collided: %v", overloads)
		}
		for name, definition := range overloads {
			if strings.Contains(name, "integer") && !strings.Contains(definition, "RETURNS integer") {
				t.Fatal("wrong integer overload")
			}
			if strings.Contains(name, "text") && !strings.Contains(definition, "RETURNS text") {
				t.Fatal("wrong text overload")
			}
		}
	}
}
