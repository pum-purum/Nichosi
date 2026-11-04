package driver

import (
	"context"
	"database/sql"
	"encoding/hex"
	"strings"
)

func sqlitePlaceholder(int) string { return "?" }
func SQLiteFilteredRows(ctx context.Context, db *sql.DB, name string, view bool, limit, offset int, filters []RowFilter, orders ...RowOrder) (RowPage, error) {
	page := RowPage{Columns: []RowColumn{}, Rows: [][]any{}}
	_, err := sqliteRelation(ctx, db, name, view)
	source := SQLiteQuoteIdent(name)
	if err != nil {
		return page, err
	}
	page.Columns, err = SQLiteColumns(ctx, db, name, view)
	if err != nil {
		return page, &RowError{"metadata_failed", err}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	known := map[string]bool{}
	names := []string{}
	for _, c := range page.Columns {
		known[c.Name] = true
		names = append(names, SQLiteQuoteIdent(c.Name))
	}
	ordering := []string{}
	seen := map[string]bool{}
	for _, o := range orders {
		if !known[o.Column] || seen[o.Column] {
			return page, ErrInvalidOrder
		}
		direction := "ASC"
		switch o.Direction {
		case "", "asc":
		case "desc":
			direction = "DESC"
		default:
			return page, ErrInvalidOrder
		}
		seen[o.Column] = true
		ordering = append(ordering, SQLiteQuoteIdent(o.Column)+" "+direction)
	}
	for _, c := range page.Columns {
		if c.PrimaryKey && !seen[c.Name] {
			ordering = append(ordering, SQLiteQuoteIdent(c.Name)+" ASC")
		}
	}
	orderSQL := ""
	if len(ordering) > 0 {
		orderSQL = " ORDER BY " + strings.Join(ordering, ",")
	}
	where, args, err := rowFilters(page.Columns, filters, SQLiteQuoteIdent, sqlitePlaceholder)
	if err != nil {
		return page, err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+source+where, args...).Scan(&page.TotalRows); err != nil {
		return page, &RowError{"count_failed", err}
	}
	if len(names) == 0 {
		return page, nil
	}
	query := "SELECT " + strings.Join(names, ",") + " FROM " + source + where + orderSQL + " LIMIT " + sqlitePlaceholder(len(args)+1) + " OFFSET " + sqlitePlaceholder(len(args)+2)
	args = append(args, limit, offset)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return page, &RowError{"query_failed", err}
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return page, err
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = map[string]any{"type": "blob", "hex": hex.EncodeToString(b)}
			} else {
				values[i] = JSONRowValue(v)
			}
		}
		page.Rows = append(page.Rows, values)
	}
	return page, rows.Err()
}
