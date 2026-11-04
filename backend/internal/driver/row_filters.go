package driver

import (
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalidFilter = errors.New("invalid row filter")

type RowFilter struct {
	Column, Op, Connector string
	Value                 any
}

func filterScalar(value any) (any, bool) {
	switch v := value.(type) {
	case string, bool, float64:
		return v, true
	case json.Number:
		// Preserve decimal and integer precision all the way to the database.
		return string(v), true
	default:
		return nil, false
	}
}

func mysqlRowFilters(columns []RowColumn, filters []RowFilter) (string, []any, error) {
	return rowFilters(columns, filters, QuoteIdent, func(int) string { return "?" })
}

func rowFilters(columns []RowColumn, filters []RowFilter, quote func(string) string, placeholder func(int) string) (string, []any, error) {
	if len(filters) > 100 {
		return "", nil, ErrInvalidFilter
	}
	if len(filters) == 0 {
		return "", nil, nil
	}
	known := map[string]bool{}
	for _, column := range columns {
		known[column.Name] = true
	}
	parts := []string{}
	args := []any{}
	operators := map[string]string{"eq": "=", "ne": "<>", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=", "like": "LIKE"}
	for i, filter := range filters {
		if !known[filter.Column] {
			return "", nil, ErrInvalidFilter
		}
		if filter.Connector != "" && filter.Connector != "and" && filter.Connector != "or" {
			return "", nil, ErrInvalidFilter
		}
		if i > 0 {
			join := "AND"
			if filter.Connector == "or" {
				join = "OR"
			}
			parts = append(parts, join)
		}
		column := quote(filter.Column)
		var predicate string
		switch filter.Op {
		case "isNull", "notNull":
			if filter.Value != nil {
				return "", nil, ErrInvalidFilter
			}
			predicate = column + " IS NULL"
			if filter.Op == "notNull" {
				predicate = column + " IS NOT NULL"
			}
		case "in", "between":
			values, ok := filter.Value.([]any)
			if !ok || len(values) == 0 || len(values) > 1000 || (filter.Op == "between" && len(values) != 2) {
				return "", nil, ErrInvalidFilter
			}
			placeholders := make([]string, len(values))
			for j, value := range values {
				scalar, ok := filterScalar(value)
				if !ok {
					return "", nil, ErrInvalidFilter
				}
				args = append(args, scalar)
				placeholders[j] = placeholder(len(args))
			}
			predicate = column + " IN (" + strings.Join(placeholders, ",") + ")"
			if filter.Op == "between" {
				predicate = column + " BETWEEN " + placeholders[0] + " AND " + placeholders[1]
			}
		default:
			operator, ok := operators[filter.Op]
			if !ok {
				return "", nil, ErrInvalidFilter
			}
			scalar, ok := filterScalar(filter.Value)
			if !ok {
				return "", nil, ErrInvalidFilter
			}
			if filter.Op == "like" {
				if _, ok := scalar.(string); !ok {
					return "", nil, ErrInvalidFilter
				}
			}
			args = append(args, scalar)
			predicate = column + " " + operator + " " + placeholder(len(args))
		}
		parts = append(parts, "("+predicate+")")
	}
	return " WHERE " + strings.Join(parts, " "), args, nil
}
