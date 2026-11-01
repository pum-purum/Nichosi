package driver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var ErrInvalidChange = errors.New("invalid change set")
var ErrReadOnlyTable = errors.New("table has no usable key or transactional storage engine")
var ErrRowConflict = errors.New("row no longer exists or key is not unique")

type EditColumn struct {
	Nullable, Generated, PrimaryKey bool
	Type                            string
}
type EditMetadata struct {
	Columns map[string]EditColumn
	Key     []string
}
type RowChange struct {
	Op          string
	Key, Values map[string]any
}
type UpdateStatement struct {
	SQL           string
	Parameters    []any
	where         string
	keyParameters []any
}

// MySQLEditMetadata admits only base tables with transactional storage and a complete non-null unique key.
func MySQLEditMetadata(ctx context.Context, db *sql.DB, database, table string) (EditMetadata, error) {
	m := EditMetadata{Columns: map[string]EditColumn{}, Key: []string{}}
	var engine string
	err := db.QueryRowContext(ctx, "SELECT COALESCE(ENGINE, '') FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE'", database, table).Scan(&engine)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrRowsObjectNotFound
	}
	if err != nil {
		return m, err
	}
	if !strings.EqualFold(engine, "InnoDB") {
		return m, ErrReadOnlyTable
	}
	rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME, IS_NULLABLE, EXTRA, DATA_TYPE FROM information_schema.columns WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION LIMIT 1001", database, table)
	if err != nil {
		return m, err
	}
	for rows.Next() {
		var name, nullable, extra, dataType string
		if err = rows.Scan(&name, &nullable, &extra, &dataType); err != nil {
			rows.Close()
			return m, err
		}
		m.Columns[name] = EditColumn{Nullable: nullable == "YES", Generated: strings.Contains(strings.ToUpper(extra), "VIRTUAL GENERATED") || strings.Contains(strings.ToUpper(extra), "STORED GENERATED") || strings.Contains(strings.ToUpper(extra), "PERSISTENT GENERATED"), Type: dataType}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if len(m.Columns) > 1000 {
		return m, ErrReadOnlyTable
	}
	rows, err = db.QueryContext(ctx, "SELECT INDEX_NAME, COLUMN_NAME, SUB_PART FROM information_schema.statistics WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND NON_UNIQUE = 0 ORDER BY (INDEX_NAME = 'PRIMARY') DESC, INDEX_NAME, SEQ_IN_INDEX LIMIT 1001", database, table)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	current := ""
	candidate := []string{}
	valid := true
	count := 0
	choose := func() {
		if current != "" && valid && len(candidate) > 0 && len(m.Key) == 0 {
			m.Key = append([]string{}, candidate...)
		}
	}
	for rows.Next() {
		count++
		var index string
		var name sql.NullString
		var prefix sql.NullInt64
		if err = rows.Scan(&index, &name, &prefix); err != nil {
			return m, err
		}
		if index != current {
			choose()
			current = index
			candidate = nil
			valid = true
		}
		c, ok := m.Columns[name.String]
		if index == "PRIMARY" && name.Valid && ok {
			c.PrimaryKey = true
			m.Columns[name.String] = c
		}
		if !name.Valid || !ok || c.Nullable || prefix.Valid || !safeEditKeyType(c.Type) {
			valid = false
		}
		candidate = append(candidate, name.String)
	}
	if err = rows.Err(); err != nil {
		return m, err
	}
	choose()
	if count > 1000 || len(m.Key) == 0 {
		return m, ErrReadOnlyTable
	}
	return m, nil
}

func changeValue(value any) (any, error) {
	switch v := value.(type) {
	case nil, string, bool, float64, int64:
		return v, nil
	case json.Number:
		return string(v), nil
	default:
		return nil, ErrInvalidChange
	}
}

// BuildUpdates uses only submitted value columns, with identifiers validated against metadata.
func BuildUpdates(database, table string, metadata EditMetadata, changes []RowChange) ([]UpdateStatement, error) {
	if len(changes) == 0 || len(changes) > 100 || len(metadata.Key) == 0 {
		return nil, ErrInvalidChange
	}
	statements := make([]UpdateStatement, 0, len(changes))
	for _, change := range changes {
		if change.Op != "update" || len(change.Values) == 0 || len(change.Key) != len(metadata.Key) {
			return nil, ErrInvalidChange
		}
		names := make([]string, 0, len(change.Values))
		for name := range change.Values {
			names = append(names, name)
		}
		sort.Strings(names)
		s := UpdateStatement{Parameters: []any{}, keyParameters: []any{}}
		assignments := []string{}
		predicates := []string{}
		for _, name := range names {
			column, ok := metadata.Columns[name]
			if !ok || column.Generated || column.PrimaryKey || (change.Values[name] == nil && !column.Nullable) {
				return nil, ErrInvalidChange
			}
			value, err := changeValue(change.Values[name])
			if err != nil {
				return nil, err
			}
			assignments = append(assignments, QuoteIdent(name)+" = ?")
			s.Parameters = append(s.Parameters, value)
		}
		for _, name := range metadata.Key {
			value, ok := change.Key[name]
			if !ok || value == nil {
				return nil, ErrInvalidChange
			}
			value, err := changeValue(value)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, QuoteIdent(name)+" = ?")
			s.keyParameters = append(s.keyParameters, value)
		}
		s.where = strings.Join(predicates, " AND ")
		s.Parameters = append(s.Parameters, s.keyParameters...)
		s.SQL = "UPDATE " + QuoteIdent(database) + "." + QuoteIdent(table) + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.where + ";"
		statements = append(statements, s)
	}
	return statements, nil
}

func MySQLApplyUpdates(ctx context.Context, db *sql.DB, database, table string, statements []UpdateStatement) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		// Lock and verify the original key first: RowsAffected may be zero for a no-op update.
		rows, err := tx.QueryContext(ctx, "SELECT 1 FROM "+QuoteIdent(database)+"."+QuoteIdent(table)+" WHERE "+statement.where+" LIMIT 2 FOR UPDATE", statement.keyParameters...)
		if err != nil {
			return 0, err
		}
		count := 0
		for rows.Next() {
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if count != 1 {
			return 0, ErrRowConflict
		}
		result, err := tx.ExecContext(ctx, statement.SQL, statement.Parameters...)
		if err != nil {
			return 0, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		if affected > 1 {
			return 0, fmt.Errorf("%w: affected multiple rows", ErrRowConflict)
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(statements), nil
}

// Binary keys require explicit binary transport; temporal keys require timezone-safe binding.
func safeEditKeyType(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob", "bit", "date", "datetime", "timestamp", "time":
		return false
	}
	return true
}
