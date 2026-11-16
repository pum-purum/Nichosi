package driver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

var ErrInvalidChange = errors.New("invalid change set")
var ErrReadOnlyTable = errors.New("table has no usable key or transactional storage engine")
var ErrRowConflict = errors.New("row no longer exists or key is not unique")

type EditColumn struct {
	Nullable, Generated, PrimaryKey bool
	Type                            string
	DefaultValue                    *string
	AutoIncrement                   bool
	NextAutoIncrementValue          *string
}
type EditMetadata struct {
	Columns map[string]EditColumn
	Key     []string
	// source is a PostgreSQL relation quoted by the catalog, never request text.
	source string
}
type RowChange struct {
	Op          string
	Key, Values map[string]any
}
type UpdateStatement struct {
	SQL           string
	Parameters    []any
	insert        bool
	where         string
	keyParameters []any
}

// MySQLEditMetadata admits only base tables with transactional storage and a complete non-null unique key.
func MySQLEditMetadata(ctx context.Context, db *sql.DB, database, table string) (EditMetadata, error) {
	m := EditMetadata{Columns: map[string]EditColumn{}, Key: []string{}}
	var engine string
	err := db.QueryRowContext(ctx, "SELECT COALESCE(ENGINE, '') FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE' LIMIT 1", database, table).Scan(&engine)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrRowsObjectNotFound
	}
	if err != nil {
		return m, err
	}
	if !strings.EqualFold(engine, "InnoDB") {
		return m, ErrReadOnlyTable
	}
	rows, err := db.QueryContext(ctx, "SELECT COLUMN_NAME, IS_NULLABLE, EXTRA, DATA_TYPE, COLUMN_DEFAULT, VERSION() FROM information_schema.columns WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION LIMIT 1001", database, table)
	if err != nil {
		return m, err
	}
	for rows.Next() {
		var name, nullable, extra, dataType, version string
		var defaultValue *string
		if err = rows.Scan(&name, &nullable, &extra, &dataType, &defaultValue, &version); err != nil {
			rows.Close()
			return m, err
		}
		m.Columns[name] = EditColumn{Nullable: nullable == "YES", Generated: strings.Contains(strings.ToUpper(extra), "VIRTUAL GENERATED") || strings.Contains(strings.ToUpper(extra), "STORED GENERATED") || strings.Contains(strings.ToUpper(extra), "PERSISTENT GENERATED"), Type: dataType, DefaultValue: mysqlDefaultSQL(defaultValue, dataType, extra, version), AutoIncrement: strings.Contains(strings.ToLower(extra), "auto_increment")}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if len(m.Columns) > 1000 {
		return m, ErrReadOnlyTable
	}
	for name, column := range m.Columns {
		if column.AutoIncrement {
			// A missing hint must never make an otherwise editable table read-only.
			column.NextAutoIncrementValue, _ = mysqlNextAutoIncrement(ctx, db, database, table)
			m.Columns[name] = column
		}
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

func changeValue(value any, column EditColumn) (any, error) {
	// Preserve exact transport for integers outside JavaScript's safe range.
	switch strings.ToLower(column.Type) {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year", "int2", "int4", "int8":
		var text string
		switch v := value.(type) {
		case string:
			text = v
		case json.Number:
			text = string(v)
		}
		if n, err := strconv.ParseInt(text, 10, 64); err == nil && n >= -9007199254740991 && n <= 9007199254740991 {
			return n, nil
		}
	}

	switch v := value.(type) {
	case nil, string, bool, float64, int64:
		return v, nil
	case json.Number:
		return string(v), nil
	default:
		return nil, ErrInvalidChange
	}
}

// BuildUpdates builds MySQL changes with identifiers validated against metadata.
func BuildUpdates(database, table string, metadata EditMetadata, changes []RowChange) ([]UpdateStatement, error) {
	return BuildChanges(database, table, metadata, changes, "mysql")
}

func BuildChanges(database, table string, metadata EditMetadata, changes []RowChange, dialect string) ([]UpdateStatement, error) {
	if len(changes) == 0 || len(changes) > 100 || len(metadata.Key) == 0 {
		return nil, ErrInvalidChange
	}
	quote, placeholder := QuoteIdent, func(int) string { return "?" }
	target := quote(database) + "." + quote(table)
	if dialect == "postgres" {
		if metadata.source == "" {
			return nil, ErrInvalidChange
		}
		quote, placeholder = PostgresQuoteIdent, pgPlaceholder
		target = metadata.source
	}
	statements := make([]UpdateStatement, 0, len(changes))
	for _, change := range changes {
		if change.Op == "insert" {
			if len(change.Key) != 0 {
				return nil, ErrInvalidChange
			}
			names := make([]string, 0, len(change.Values))
			for name := range change.Values {
				names = append(names, name)
			}
			sort.Strings(names)
			statement := UpdateStatement{insert: true, Parameters: []any{}}
			quoted, placeholders := []string{}, []string{}
			for _, name := range names {
				column, ok := metadata.Columns[name]
				if !ok || column.Generated || (change.Values[name] == nil && !column.Nullable) {
					return nil, ErrInvalidChange
				}
				value, err := changeValue(change.Values[name], column)
				if err != nil {
					return nil, err
				}
				quoted = append(quoted, quote(name))
				placeholders = append(placeholders, placeholder(len(statement.Parameters)+1))
				statement.Parameters = append(statement.Parameters, value)
			}
			if len(names) == 0 && (dialect == "sqlite" || dialect == "postgres") {
				statement.SQL = "INSERT INTO " + target + " DEFAULT VALUES;"
			} else {
				statement.SQL = "INSERT INTO " + target + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ");"
			}
			statements = append(statements, statement)
			continue
		}
		if (change.Op != "update" && change.Op != "delete") ||
			(change.Op == "update" && len(change.Values) == 0) ||
			(change.Op == "delete" && len(change.Values) != 0) || len(change.Key) != len(metadata.Key) {
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
			value, err := changeValue(change.Values[name], column)
			if err != nil {
				return nil, err
			}
			assignments = append(assignments, quote(name)+" = "+placeholder(len(s.Parameters)+1))
			s.Parameters = append(s.Parameters, value)
		}
		for _, name := range metadata.Key {
			value, ok := change.Key[name]
			if !ok || value == nil {
				return nil, ErrInvalidChange
			}
			value, err := changeValue(value, metadata.Columns[name])
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, quote(name)+" = "+placeholder(len(s.Parameters)+len(s.keyParameters)+1))
			s.keyParameters = append(s.keyParameters, value)
		}
		s.where = strings.Join(predicates, " AND ")
		s.Parameters = append(s.Parameters, s.keyParameters...)
		if change.Op == "delete" {
			s.SQL = "DELETE FROM " + target + " WHERE " + s.where + ";"
		} else {
			s.SQL = "UPDATE " + target + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.where + ";"
		}
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
		if statement.insert {
			result, err := tx.ExecContext(ctx, statement.SQL, statement.Parameters...)
			if err != nil {
				return 0, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return 0, err
			}
			if n != 1 {
				return 0, ErrRowConflict
			}
			continue
		}
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

// mysqlDefaultSQL preserves expressions and quotes MySQL's unquoted string literals.
// MariaDB already reports defaults as SQL, including quotes around literals.
func mysqlDefaultSQL(value *string, dataType, extra, version string) *string {
	if value == nil || strings.Contains(strings.ToLower(version), "mariadb") || strings.Contains(strings.ToUpper(extra), "DEFAULT_GENERATED") {
		return value
	}
	switch strings.ToLower(dataType) {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "decimal", "numeric", "float", "double", "real", "bit", "year":
		return value
	case "timestamp", "datetime":
		if strings.HasPrefix(strings.ToUpper(*value), "CURRENT_TIMESTAMP") {
			return value
		}
	}
	quoted := "'" + strings.ReplaceAll(strings.ReplaceAll(*value, `\`, strings.Repeat(`\`, 2)), "'", "''") + "'"
	return &quoted
}
