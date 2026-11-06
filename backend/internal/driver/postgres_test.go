package driver

import "testing"

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
