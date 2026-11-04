package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func PostgresQuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func pgPlaceholder(n int) string         { return "$" + strconv.Itoa(n) }

// OpenPostgres uses structured connection fields and read-only server sessions.
// TLS is disabled for the local database workflow until TLS profiles are supported.
func OpenPostgres(ctx context.Context, host string, port int, user, password, database string) (*sql.DB, int64, error) {
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, 0, errors.New("configure PostgreSQL connection")
	}
	cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database = host, uint16(port), user, password, database
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"default_transaction_read_only": "on", "statement_timeout": "10000", "application_name": "Nichosi"}
	pool := stdlib.OpenDB(*cfg)
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(2)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, 0, safePostgresError(err, password)
	}
	var size int64
	if err := pool.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&size); err != nil {
		pool.Close()
		return nil, 0, safePostgresError(err, password)
	}
	return pool, size, nil
}
func safePostgresError(err error, password string) error {
	message := err.Error()
	if password != "" {
		message = strings.ReplaceAll(message, password, "[redacted]")
	}
	return errors.New(message)
}

type PostgresTable struct {
	Name                                string
	RowsEstimate, DataBytes, IndexBytes int64
	Columns                             []RowColumn
}

const pgRelationName = `quote_ident(n.nspname)||'.'||quote_ident(c.relname)`
const pgUserNamespace = `n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%'`

// PostgresSchema preserves the schema reader used by integration callers.
func PostgresSchema(ctx context.Context, db *sql.DB) ([]PostgresTable, []Object, []string, error) {
	catalog, err := PostgresCatalog(ctx, db)
	return catalog.Tables, catalog.Objects, catalog.Warnings, err
}

func pgRelation(ctx context.Context, db *sql.DB, name string, view bool) (int64, string, error) {
	kinds := "r,p"
	if view {
		kinds = "v,m"
	}
	var oid int64
	var source string
	err := db.QueryRowContext(ctx, `SELECT c.oid::bigint, `+pgRelationName+` FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE `+pgUserNamespace+` AND `+pgRelationName+`=$1 AND c.relkind::text=ANY(string_to_array($2,',')) AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') LIMIT 1`, name, kinds).Scan(&oid, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrRowsObjectNotFound
	}
	return oid, source, err
}

func PostgresColumns(ctx context.Context, db *sql.DB, name string, view bool) ([]RowColumn, error) {
	oid, _, err := pgRelation(ctx, db, name, view)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT a.attname,format_type(a.atttypid,a.atttypmod),NOT a.attnotnull,EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=a.attrelid AND i.indisprimary AND a.attnum=ANY(i.indkey)) FROM pg_attribute a WHERE a.attrelid=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 1001`, oid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []RowColumn{}
	for rows.Next() {
		if len(columns) == 1000 {
			return nil, errors.New("row sources with more than 1000 columns are unsupported")
		}
		var c RowColumn
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &c.PrimaryKey); err != nil {
			return nil, err
		}
		if view {
			c.PrimaryKey = false
		}
		columns = append(columns, c)
	}
	return columns, rows.Err()
}

func PostgresFilteredRows(ctx context.Context, db *sql.DB, name string, view bool, limit, offset int, filters []RowFilter, orders ...RowOrder) (RowPage, error) {
	page := RowPage{Columns: []RowColumn{}, Rows: [][]any{}}
	_, source, err := pgRelation(ctx, db, name, view)
	if err != nil {
		return page, err
	}
	page.Columns, err = PostgresColumns(ctx, db, name, view)
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
		names = append(names, PostgresQuoteIdent(c.Name))
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
		ordering = append(ordering, PostgresQuoteIdent(o.Column)+" "+direction)
	}
	for _, c := range page.Columns {
		if c.PrimaryKey && !seen[c.Name] {
			ordering = append(ordering, PostgresQuoteIdent(c.Name)+" ASC")
		}
	}
	orderSQL := ""
	if len(ordering) > 0 {
		orderSQL = " ORDER BY " + strings.Join(ordering, ",")
	}
	where, args, err := rowFilters(page.Columns, filters, PostgresQuoteIdent, pgPlaceholder)
	if err != nil {
		return page, err
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+source+where, args...).Scan(&page.TotalRows); err != nil {
		return page, &RowError{"count_failed", err}
	}
	if len(names) == 0 {
		return page, nil
	}
	query := "SELECT " + strings.Join(names, ",") + " FROM " + source + where + orderSQL + " LIMIT " + pgPlaceholder(len(args)+1) + " OFFSET " + pgPlaceholder(len(args)+2)
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
			values[i] = JSONRowValue(v)
		}
		page.Rows = append(page.Rows, values)
	}
	return page, rows.Err()
}

func PostgresMetadata(ctx context.Context, db *sql.DB, name string, view bool) (TableMetadata, error) {
	result := TableMetadata{Name: name, Details: map[string]string{}, Columns: []map[string]string{}, Indexes: []map[string]string{}, ForeignKeys: []map[string]string{}, Triggers: []map[string]string{}, Warnings: []string{}}
	oid, _, err := pgRelation(ctx, db, name, view)
	if errors.Is(err, ErrRowsObjectNotFound) {
		if view {
			return result, ErrViewNotFound
		}
		return result, ErrTableNotFound
	}
	if err != nil {
		return result, err
	}
	details, _, err := metadataRows(ctx, db, `SELECT n.nspname AS "Schema",c.relname AS "Name",pg_get_userbyid(c.relowner) AS "Owner",obj_description(c.oid,'pg_class') AS "Comment" FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=$1 LIMIT 1`, oid)
	if err != nil {
		return result, err
	}
	if len(details) == 0 {
		if view {
			return result, ErrViewNotFound
		}
		return result, ErrTableNotFound
	}
	result.Details = details[0]
	categories := []struct {
		label, query string
		dest         *[]map[string]string
	}{
		{"Columns", `SELECT a.attnum AS "Position",a.attname AS "Name",format_type(a.atttypid,a.atttypmod) AS "Type",NOT a.attnotnull AS "Nullable",pg_get_expr(d.adbin,d.adrelid) AS "Default",col_description(a.attrelid,a.attnum) AS "Comment" FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT 1001`, &result.Columns},
		{"Indexes", `SELECT ci.relname AS "Name",i.indisprimary AS "Primary key",i.indisunique AS "Unique",pg_get_indexdef(i.indexrelid) AS "Definition" FROM pg_index i JOIN pg_class ci ON ci.oid=i.indexrelid WHERE i.indrelid=$1 ORDER BY ci.relname LIMIT 1001`, &result.Indexes},
		{"Foreign keys", `SELECT conname AS "Name",pg_get_constraintdef(oid,true) AS "Definition" FROM pg_constraint WHERE conrelid=$1 AND contype='f' ORDER BY conname LIMIT 1001`, &result.ForeignKeys},
		{"Triggers", `SELECT tgname AS "Name",pg_get_triggerdef(oid,true) AS "Definition" FROM pg_trigger WHERE tgrelid=$1 AND NOT tgisinternal ORDER BY tgname LIMIT 1001`, &result.Triggers},
	}
	for _, c := range categories {
		if view && c.label != "Columns" {
			continue
		}
		entries, truncated, err := metadataRows(ctx, db, c.query, oid)
		if err != nil {
			return result, fmt.Errorf("read PostgreSQL %s: %w", c.label, err)
		}
		*c.dest = entries
		if truncated {
			result.Warnings = append(result.Warnings, c.label+" metadata is limited to 1000 entries.")
		}
	}
	if view {
		object, err := postgresViewDefinition(ctx, db, "", name)
		if err != nil {
			return result, err
		}
		result.Definition = object.Definition
		for key, value := range object.Details {
			result.Details[key] = value
		}
	} else {
		result.Warnings = append(result.Warnings, "PostgreSQL does not expose a complete CREATE TABLE statement; columns, indexes and constraints are shown separately.")
	}
	return result, nil
}
