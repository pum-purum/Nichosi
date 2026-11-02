package driver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestMySQLRowFilters(t *testing.T) {
	columns := []RowColumn{{Name: "col`name"}}
	for _, tc := range []struct {
		name, op, want string
		value          any
		invalid        bool
	}{
		{"equals", "eq", "= ?", "x' OR 1=1", false},
		{"not equals", "ne", "<> ?", true, false},
		{"less", "lt", "< ?", json.Number("9007199254740993"), false},
		{"less equal", "lte", "<= ?", "10", false},
		{"greater", "gt", "> ?", "10", false},
		{"greater equal", "gte", ">= ?", "10", false},
		{"pattern", "like", "LIKE ?", "%value%", false},
		{"membership", "in", "IN (?,?)", []any{"a", "b"}, false},
		{"range", "between", "BETWEEN ? AND ?", []any{"a", "z"}, false},
		{"null", "isNull", "IS NULL", nil, false},
		{"not null", "notNull", "IS NOT NULL", nil, false},
		{"unknown op", "raw SQL", "", "x", true},
		{"missing scalar", "eq", "", nil, true},
		{"object scalar", "eq", "", map[string]any{"x": 1}, true},
		{"empty in", "in", "", []any{}, true},
		{"scalar in", "in", "", "a,b", true},
		{"short range", "between", "", []any{"a"}, true},
		{"long range", "between", "", []any{"a", "b", "c"}, true},
		{"nested in", "in", "", []any{[]any{"a"}}, true},
		{"null with value", "isNull", "", "a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, args, err := mysqlRowFilters(columns, []RowFilter{{Column: "col`name", Op: tc.op, Value: tc.value}})
			if tc.invalid {
				if !errors.Is(err, ErrInvalidFilter) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil || got != " WHERE (`col``name` "+tc.want+")" {
				t.Fatalf("%s %v %v", got, args, err)
			}
			if tc.name == "equals" && (strings.Contains(got, "1=1") || len(args) != 1 || args[0] != tc.value) {
				t.Fatal("value must be bound")
			}
			if tc.op == "lt" && args[0] != "9007199254740993" {
				t.Fatal("lost integer precision")
			}
		})
	}
	for _, filters := range [][]RowFilter{
		{{Column: "missing", Op: "eq", Value: "x"}},
		{{Column: "col`name", Op: "eq", Value: "x", Connector: "OR 1=1"}},
		make([]RowFilter, 101),
	} {
		if _, _, err := mysqlRowFilters(columns, filters); !errors.Is(err, ErrInvalidFilter) {
			t.Fatalf("got %v", err)
		}
	}
}

func TestMySQLFilteredRowsCountAndPage(t *testing.T) {
	connector := &rowSourceConnector{sourceKind: "BASE TABLE"}
	db := sql.OpenDB(connector)
	defer db.Close()
	filters := []RowFilter{
		{Column: "col`name", Op: "eq", Value: "x"},
		{Column: "col`name", Op: "between", Value: []any{"1", "9"}, Connector: "or"},
		{Column: "col`name", Op: "notNull", Connector: "and"},
	}
	_, err := MySQLFilteredRows(context.Background(), db, "demo", "t", false, 25, 50, filters)
	if err != nil {
		t.Fatal(err)
	}
	where := " WHERE (`col``name` = ?) OR (`col``name` BETWEEN ? AND ?) AND (`col``name` IS NOT NULL)"
	if !strings.HasSuffix(connector.queries[2], where) || !strings.Contains(connector.queries[3], where+" LIMIT ? OFFSET ?") {
		t.Fatal(connector.queries)
	}
	countArgs, pageArgs := connector.arguments[2], connector.arguments[3]
	if len(countArgs) != 3 || len(pageArgs) != 5 || !reflect.DeepEqual(countArgs, pageArgs[:3]) {
		t.Fatalf("different predicates: %v %v", countArgs, pageArgs)
	}
}
