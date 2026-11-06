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
	builder := newRowFilterBuilder(columns, quote, placeholder)
	for _, filter := range filters {
		if err := builder.add(filter); err != nil {
			return "", nil, err
		}
	}
	return " WHERE " + strings.Join(builder.parts, " "), builder.args, nil
}

// rowFilterBuilder owns placeholder numbering and bound values for one WHERE clause.
type rowFilterBuilder struct {
	known       map[string]bool
	quote       func(string) string
	placeholder func(int) string
	parts       []string
	args        []any
}

func newRowFilterBuilder(columns []RowColumn, quote func(string) string, placeholder func(int) string) *rowFilterBuilder {
	known := make(map[string]bool, len(columns))
	for _, column := range columns {
		known[column.Name] = true
	}
	return &rowFilterBuilder{known: known, quote: quote, placeholder: placeholder, parts: []string{}, args: []any{}}
}

func (b *rowFilterBuilder) bind(value any) (string, error) {
	scalar, ok := filterScalar(value)
	if !ok {
		return "", ErrInvalidFilter
	}
	b.args = append(b.args, scalar)
	return b.placeholder(len(b.args)), nil
}

func (b *rowFilterBuilder) add(filter RowFilter) error {
	if !b.known[filter.Column] || (filter.Connector != "" && filter.Connector != "and" && filter.Connector != "or") {
		return ErrInvalidFilter
	}
	predicate, err := b.predicate(filter)
	if err != nil {
		return err
	}
	if len(b.parts) > 0 {
		join := "AND"
		if filter.Connector == "or" {
			join = "OR"
		}
		b.parts = append(b.parts, join)
	}
	b.parts = append(b.parts, "("+predicate+")")
	return nil
}

func (b *rowFilterBuilder) predicate(filter RowFilter) (string, error) {
	column := b.quote(filter.Column)
	switch filter.Op {
	case "isNull", "notNull":
		if filter.Value != nil {
			return "", ErrInvalidFilter
		}
		if filter.Op == "notNull" {
			return column + " IS NOT NULL", nil
		}
		return column + " IS NULL", nil
	case "in", "between":
		values, ok := filter.Value.([]any)
		if !ok || len(values) == 0 || len(values) > 1000 || (filter.Op == "between" && len(values) != 2) {
			return "", ErrInvalidFilter
		}
		placeholders := make([]string, len(values))
		for i, value := range values {
			bound, err := b.bind(value)
			if err != nil {
				return "", err
			}
			placeholders[i] = bound
		}
		if filter.Op == "between" {
			return column + " BETWEEN " + placeholders[0] + " AND " + placeholders[1], nil
		}
		return column + " IN (" + strings.Join(placeholders, ",") + ")", nil
	default:
		operator, ok := rowFilterOperator(filter.Op)
		if !ok {
			return "", ErrInvalidFilter
		}
		if filter.Op == "like" {
			scalar, _ := filterScalar(filter.Value)
			if _, ok := scalar.(string); !ok {
				return "", ErrInvalidFilter
			}
		}
		bound, err := b.bind(filter.Value)
		if err != nil {
			return "", err
		}
		return column + " " + operator + " " + bound, nil
	}
}

func rowFilterOperator(op string) (string, bool) {
	switch op {
	case "eq":
		return "=", true
	case "ne":
		return "<>", true
	case "lt":
		return "<", true
	case "lte":
		return "<=", true
	case "gt":
		return ">", true
	case "gte":
		return ">=", true
	case "like":
		return "LIKE", true
	default:
		return "", false
	}
}
