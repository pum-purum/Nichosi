package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrInvalidOrder = errors.New("invalid row ordering")

type RowOrder struct {
	Column, Direction string
}

var ErrRowsObjectNotFound = errors.New("row source not found")

type RowColumn struct {
	Name, Type           string
	Nullable, PrimaryKey bool
}
type RowPage struct {
	Columns   []RowColumn
	Rows      [][]any
	TotalRows int64
}
type RowError struct {
	Stage string
	Err   error
}

func (e *RowError) Error() string { return e.Err.Error() }
func (e *RowError) Unwrap() error { return e.Err }

// MySQLRows reads a bounded page from exactly the requested object kind.
func MySQLRows(ctx context.Context, db *sql.DB, database, name string, view bool, limit, offset int, orderBy ...RowOrder) (RowPage, error) {
	page := RowPage{Columns: []RowColumn{}, Rows: [][]any{}}
	kind := "BASE TABLE"
	if view {
		kind = "VIEW"
	}
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = ?", database, name, kind).Scan(&exists); err != nil {
		return page, &RowError{"metadata_failed", err}
	}
	if exists == 0 {
		return page, ErrRowsObjectNotFound
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
	cols, err := db.QueryContext(ctx, "SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY FROM information_schema.columns WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION LIMIT 1001", database, name)
	if err != nil {
		return page, &RowError{"metadata_failed", err}
	}
	defer cols.Close()
	names := []string{}
	for cols.Next() {
		if len(names) == 1000 {
			return page, &RowError{"metadata_failed", fmt.Errorf("row sources with more than 1000 columns are unsupported")}
		}
		var c RowColumn
		var nullable, key string
		if err := cols.Scan(&c.Name, &c.Type, &nullable, &key); err != nil {
			return page, &RowError{"metadata_failed", err}
		}
		c.Nullable = nullable == "YES"
		c.PrimaryKey = key == "PRI" && !view
		page.Columns = append(page.Columns, c)
		names = append(names, QuoteIdent(c.Name))
	}
	if err := cols.Err(); err != nil {
		return page, &RowError{"metadata_failed", err}
	}
	if err := cols.Close(); err != nil {
		return page, &RowError{"metadata_failed", err}
	}
	ordering := []string{}
	ordered := map[string]bool{}
	for _, order := range orderBy {
		found := false
		for _, column := range page.Columns {
			if column.Name == order.Column {
				found = true
				break
			}
		}
		direction := "ASC"
		switch order.Direction {
		case "", "asc":
		case "desc":
			direction = "DESC"
		default:
			return page, ErrInvalidOrder
		}
		if !found || ordered[order.Column] {
			return page, ErrInvalidOrder
		}
		ordered[order.Column] = true
		ordering = append(ordering, QuoteIdent(order.Column)+" "+direction)
	}
	// Primary keys break ties so sorted pages remain stable.
	if len(ordering) > 0 {
		for _, column := range page.Columns {
			if column.PrimaryKey && !ordered[column.Name] {
				ordering = append(ordering, QuoteIdent(column.Name)+" ASC")
			}
		}
	}
	orderSQL := ""
	if len(ordering) > 0 {
		orderSQL = " ORDER BY " + strings.Join(ordering, ",")
	}

	source := QuoteIdent(database) + "." + QuoteIdent(name)
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+source).Scan(&page.TotalRows); err != nil {
		return page, &RowError{"count_failed", err}
	}
	if len(names) == 0 {
		return page, nil
	}
	rows, err := db.QueryContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM "+source+orderSQL+" LIMIT ? OFFSET ?", limit, offset)
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
			return page, &RowError{"query_failed", err}
		}
		for i, v := range values {
			values[i] = JSONRowValue(v)
		}
		page.Rows = append(page.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return page, &RowError{"query_failed", err}
	}
	if err := rows.Close(); err != nil {
		return page, &RowError{"query_failed", err}
	}
	return page, nil
}

// JSONRowValue preserves integers that JavaScript cannot represent exactly.
func JSONRowValue(value any) any {
	switch v := value.(type) {
	case []byte:
		return string(v)
	case int64:
		if v > 9007199254740991 || v < -9007199254740991 {
			return strconv.FormatInt(v, 10)
		}
	case uint64:
		if v > 9007199254740991 {
			return strconv.FormatUint(v, 10)
		}
	}
	return value
}
