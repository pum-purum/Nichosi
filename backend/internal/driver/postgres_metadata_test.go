package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func TestPostgresMetadataSequences(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			queried := false
			db := sql.OpenDB(&postgresCatalogConnector{query: func(q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
				if len(args) == 2 {
					return &editRows{columns: []string{"oid", "source"}, values: [][]sqldriver.Value{{int64(42), "public.items"}}}, nil
				}
				if len(args) != 1 || args[0].Value != int64(42) {
					t.Fatalf("metadata must use resolved OID: %+v", args)
				}
				if strings.Contains(q, `AS "Owner"`) {
					return &editRows{columns: []string{"Name"}, values: [][]sqldriver.Value{{"items"}}}, nil
				}
				if q == postgresTableSequencesQuery {
					queried = true
					values := [][]sqldriver.Value{}
					if present {
						values = append(values, []sqldriver.Value{"public", "items_id_seq", "id", "bigint", "1"})
					}
					return &editRows{columns: []string{"Schema", "Name", "Column", "Type", "Increment"}, values: values}, nil
				}
				return &editRows{columns: []string{"Name"}}, nil
			}})
			defer db.Close()
			metadata, err := PostgresMetadata(context.Background(), db, "public.items", false)
			if err != nil {
				t.Fatal(err)
			}
			if !queried || metadata.Sequences == nil {
				t.Fatal("missing sequence metadata query")
			}
			want := 0
			if present {
				want = 1
			}
			if len(*metadata.Sequences) != want {
				t.Fatalf("sequences: %+v", metadata.Sequences)
			}
			if present && (*metadata.Sequences)[0]["Column"] != "id" {
				t.Fatal("lost sequence column link")
			}
		})
	}
}
