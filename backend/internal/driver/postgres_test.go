package driver

import (
	"context"
	"database/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
		for _, kind := range []string{"user", "role", "trigger", "procedure", "function", "schema", "extension", "index", "view", "materialized_view"} {
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

// TestPostgresSchemaGroupingIntegration checks raw grouping names against the
// isolated developer fixture without parsing opaque qualified API identifiers.
func TestPostgresSchemaGroupingIntegration(t *testing.T) {
	dsn := os.Getenv("NICHOSI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("NICHOSI_TEST_POSTGRES_DSN is unset")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test configuration")
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Skip("local PostgreSQL test database unavailable")
	}
	catalog, err := PostgresCatalog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]PostgresTable{}
	for _, table := range catalog.Tables {
		tables[table.Name] = table
		if table.SchemaName == "" || table.DisplayName == "" {
			t.Fatalf("missing grouping fields for %s", table.Name)
		}
	}
	for _, object := range catalog.Objects {
		switch object.Type {
		case "role", "user", "extension", "cron":
			if object.SchemaName != "" {
				t.Fatalf("global object %s was grouped", object.Name)
			}
		default:
			if object.SchemaName == "" || object.DisplayName == "" {
				t.Fatalf("missing grouping fields for %s", object.Name)
			}
		}
	}
	odd, fixture := tables[`"odd.schema"."odd.table"`]
	if !fixture {
		t.Skip("quoted schema grouping fixture is absent")
	}
	if odd.SchemaName != "odd.schema" || odd.DisplayName != "odd.table" {
		t.Fatalf("quoted dotted identifiers were split: %+v", odd)
	}
	if public, ok := tables["public.events"]; ok {
		other, ok := tables[`"odd.schema".events`]
		if !ok || public.Name == other.Name || public.DisplayName != other.DisplayName || public.SchemaName == other.SchemaName {
			t.Fatal("same-name tables lost schema identity")
		}
	}
	schemas := map[string]Object{}
	overloads := map[string]bool{}
	triggerFound := false
	for _, object := range catalog.Objects {
		if object.Type == "schema" {
			schemas[object.SchemaName] = object
		}
		if object.Type == "function" && object.SchemaName == "odd.schema" && strings.HasPrefix(object.DisplayName, "echo(") {
			overloads[object.DisplayName] = true
		}
		if object.Type == "trigger" && object.SchemaName == "odd.schema" && object.DisplayName == `"odd.table".before_item` {
			triggerFound = true
		}
	}
	if len(overloads) != 2 || !overloads["echo(value integer)"] || !overloads["echo(value text)"] {
		t.Fatalf("overload display labels collided: %v", overloads)
	}
	if !triggerFound {
		t.Fatal("trigger display label lost table context")
	}
	oddSchema, ok := schemas["odd.schema"]
	if !ok || oddSchema.Name != `"odd.schema"` || oddSchema.DisplayName != "odd.schema" {
		t.Fatal("schema identifier or raw grouping name was changed")
	}
	if oddSchema.Details["Owner"] == "" || oddSchema.Details["Current account USAGE"] == "" || oddSchema.Details["Current account CREATE"] == "" || oddSchema.Details["ACL source"] == "" || oddSchema.Details["ACL interpretation"] == "" {
		t.Fatalf("schema permissions unavailable: %+v", oddSchema.Details)
	}
	if empty, ok := schemas["empty_schema"]; ok {
		if !strings.Contains(empty.Details["ACL entries"], "PUBLIC: USAGE") {
			t.Fatalf("PUBLIC ACL grant missing: %v", empty.Details)
		}
		// This fixture creates a restricted LOGIN role; use a separate connection so
		// effective permissions are verified without mutating any connection state.
		restricted := cfg.Copy()
		restricted.User = "catalog_reader"
		reader := stdlib.OpenDB(*restricted)
		defer reader.Close()
		if err := reader.PingContext(ctx); err != nil {
			t.Skip("fixture restricted LOGIN is unavailable")
		}
		restrictedCatalog, err := PostgresCatalog(ctx, reader)
		if err != nil {
			t.Fatal(err)
		}
		checked := false
		for _, schema := range restrictedCatalog.Objects {
			if schema.Type == "schema" && schema.SchemaName == "odd.schema" {
				checked = true
				if schema.Details["Current account"] != "catalog_reader" || schema.Details["Current account USAGE"] != "true" || schema.Details["Current account CREATE"] != "false" {
					t.Fatalf("effective account permissions incorrect: %v", schema.Details)
				}
			}
		}
		if !checked {
			t.Fatal("accessible schema missing for restricted account")
		}
	}
}
