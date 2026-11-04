package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func SQLiteQuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// OpenSQLite accepts a file path, never a caller-controlled URI or creation request.
func OpenSQLite(ctx context.Context, path string, readonly bool) (*sql.DB, int64, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, 0, errors.New("SQLite requires an absolute file path")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, errors.New("SQLite database file is unavailable")
	}
	mode := "rw"
	if readonly {
		mode = "ro"
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {mode}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)"}, "_txlock": {"immediate"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, 0, errors.New("Could not configure SQLite")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	var pages, size int64
	if err = db.PingContext(ctx); err == nil {
		err = db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages)
	}
	if err == nil {
		err = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size)
	}
	if err != nil {
		db.Close()
		return nil, 0, errors.New("Could not open SQLite database")
	}
	return db, pages * size, nil
}

func sqliteRelation(ctx context.Context, db *sql.DB, name string, view bool) (string, error) {
	kind := "table"
	if view {
		kind = "view"
	}
	var definition sql.NullString
	err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name=? AND type=? AND name NOT GLOB 'sqlite_*' LIMIT 1`, name, kind).Scan(&definition)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRowsObjectNotFound
	}
	return definition.String, err
}

func SQLiteColumns(ctx context.Context, db *sql.DB, name string, view bool) ([]RowColumn, error) {
	if _, err := sqliteRelation(ctx, db, name, view); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT name,type,"notnull",pk FROM pragma_table_xinfo(?) WHERE hidden<>1 ORDER BY cid LIMIT 1001`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []RowColumn{}
	for rows.Next() {
		var c RowColumn
		var nonnull, pk int
		if err := rows.Scan(&c.Name, &c.Type, &nonnull, &pk); err != nil {
			return nil, err
		}
		if len(columns) == 1000 {
			return nil, errors.New("row sources with more than 1000 columns are unsupported")
		}
		c.Nullable = nonnull == 0
		c.PrimaryKey = pk > 0 && !view
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var backed int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_index_list(?) WHERE origin='pk'`, name).Scan(&backed); err != nil {
		return nil, err
	}
	if !view && backed == 0 {
		for i := range columns {
			if columns[i].PrimaryKey && strings.EqualFold(columns[i].Type, "INTEGER") {
				columns[i].Nullable = false
			}
		}
	}
	return columns, nil
}

type SQLiteCatalogResult struct {
	Tables               []PostgresTable
	Objects              []Object
	Warnings             []string
	SupportedObjectTypes []string
}

func SQLiteCatalog(ctx context.Context, db *sql.DB) (SQLiteCatalogResult, error) {
	result := SQLiteCatalogResult{Tables: []PostgresTable{}, Objects: []Object{}, Warnings: []string{}, SupportedObjectTypes: []string{"table", "view", "trigger", "index"}}
	rows, err := db.QueryContext(ctx, `SELECT name,type,coalesce(sql,'') FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*' AND type IN ('table','view','trigger','index') ORDER BY type,name LIMIT 1001`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var o Object
		o.Details = map[string]string{}
		if err := rows.Scan(&o.Name, &o.Type, &o.Definition); err != nil {
			rows.Close()
			return result, err
		}
		result.Objects = append(result.Objects, o)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(result.Objects) > 1000 {
		result.Objects = result.Objects[:1000]
		result.Warnings = append(result.Warnings, "Schema metadata is limited to 1000 objects.")
	}
	sizes, err := sqliteTableSizes(ctx, db)
	if err != nil {
		return result, fmt.Errorf("read SQLite table sizes: %w", err)
	}
	objects := []Object{}
	for _, o := range result.Objects {
		if o.Type != "table" {
			objects = append(objects, o)
			continue
		}
		cols, err := SQLiteColumns(ctx, db, o.Name, false)
		if err != nil {
			return result, err
		}
		result.Tables = append(result.Tables, PostgresTable{Name: o.Name, Columns: cols, DataBytes: sizes[o.Name].data, IndexBytes: sizes[o.Name].indexes})
	}
	result.Objects = objects
	return result, nil
}

// dbstat aggregate mode reports allocated B-tree bytes, including overflow pages.
// Join all schema indexes so UNIQUE autoindexes are attributed to their table.
func sqliteTableSizes(ctx context.Context, db *sql.DB) (map[string]struct{ data, indexes int64 }, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.tbl_name,
 SUM(CASE WHEN s.type='table' THEN d.pgsize ELSE 0 END),
 SUM(CASE WHEN s.type='index' THEN d.pgsize ELSE 0 END)
 FROM dbstat('main',1) d JOIN main.sqlite_schema s ON s.name=d.name
 WHERE s.type IN ('table','index') AND s.tbl_name NOT GLOB 'sqlite_*'
 GROUP BY s.tbl_name ORDER BY s.tbl_name LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sizes := map[string]struct{ data, indexes int64 }{}
	for rows.Next() {
		var name string
		var size struct{ data, indexes int64 }
		if err := rows.Scan(&name, &size.data, &size.indexes); err != nil {
			return nil, err
		}
		sizes[name] = size
	}
	return sizes, rows.Err()
}

func SQLiteObjectDefinition(ctx context.Context, db *sql.DB, kind, name string) (Object, error) {
	o := Object{Name: name, Type: kind, Details: map[string]string{}}
	if kind != "view" && kind != "trigger" && kind != "index" {
		return o, ErrRowsObjectNotFound
	}
	err := db.QueryRowContext(ctx, `SELECT coalesce(sql,'') FROM sqlite_schema WHERE name=? AND type=? AND name NOT GLOB 'sqlite_*' LIMIT 1`, name, kind).Scan(&o.Definition)
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrRowsObjectNotFound
	}
	return o, err
}
func SQLiteMetadata(ctx context.Context, db *sql.DB, name string, view bool) (TableMetadata, error) {
	m := TableMetadata{Name: name, Details: map[string]string{"Name": name, "Engine": "SQLite"}, Columns: []map[string]string{}, Indexes: []map[string]string{}, ForeignKeys: []map[string]string{}, Triggers: []map[string]string{}, Warnings: []string{}}
	definition, err := sqliteRelation(ctx, db, name, view)
	if errors.Is(err, ErrRowsObjectNotFound) {
		if view {
			return m, ErrViewNotFound
		}
		return m, ErrTableNotFound
	}
	if err != nil {
		return m, err
	}
	m.Definition = definition
	categories := []struct {
		label, query string
		dest         *[]map[string]string
	}{
		{"Columns", `SELECT cid+1 AS Position,name AS Name,type AS Type,CASE WHEN pk>0 AND upper(type)='INTEGER' AND NOT EXISTS(SELECT 1 FROM pragma_index_list(?) WHERE origin='pk') THEN 0 ELSE "notnull"=0 END AS Nullable,dflt_value AS "Default",pk AS "Primary key position",hidden AS Hidden FROM pragma_table_xinfo(?) ORDER BY cid LIMIT 1001`, &m.Columns},
		{"Indexes", `SELECT i.name AS Name,CASE WHEN i.origin='pk' THEN 'Primary key' WHEN i."unique"=1 THEN 'Unique' ELSE 'Index' END AS Kind,x.seqno+1 AS Position,CASE WHEN x.cid=-2 THEN '(expression)' WHEN x.cid=-1 THEN '(rowid)' ELSE x.name END AS "Column",CASE WHEN x.desc THEN 'DESC' ELSE 'ASC' END AS Ordering,x.coll AS Collation,i.partial AS Partial,'B-tree' AS Type FROM pragma_index_list(?) i JOIN pragma_index_xinfo(i.name) x WHERE x.key=1 ORDER BY i.seq,x.seqno LIMIT 1001`, &m.Indexes},
		{"Foreign keys", `SELECT id AS Name,seq+1 AS Position,"from" AS "Column","table" AS "Referenced table","to" AS "Referenced column",on_update AS "On update",on_delete AS "On delete" FROM pragma_foreign_key_list(?) ORDER BY id,seq LIMIT 1001`, &m.ForeignKeys},
		{"Triggers", `SELECT name AS Name,sql AS Definition FROM sqlite_schema WHERE type='trigger' AND tbl_name=? ORDER BY name LIMIT 1001`, &m.Triggers},
	}
	for _, c := range categories {
		args := []any{name}
		if c.label == "Columns" {
			args = append(args, name)
		}
		entries, truncated, err := metadataRows(ctx, db, c.query, args...)
		if err != nil {
			return m, err
		}
		*c.dest = entries
		if truncated {
			m.Warnings = append(m.Warnings, c.label+" metadata is limited to 1000 entries.")
		}
	}
	return m, nil
}

func SQLiteEditMetadata(ctx context.Context, db *sql.DB, table string) (EditMetadata, error) {
	m := EditMetadata{Columns: map[string]EditColumn{}, Key: []string{}}
	definition, err := sqliteRelation(ctx, db, table, false)
	if err != nil {
		return m, err
	}
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(definition)), "CREATE VIRTUAL TABLE") {
		return m, ErrReadOnlyTable
	}
	rows, err := db.QueryContext(ctx, `SELECT name,type,"notnull",pk,hidden FROM pragma_table_xinfo(?) ORDER BY pk,cid LIMIT 1001`, table)
	if err != nil {
		return m, err
	}
	primary := []string{}
	for rows.Next() {
		var name, typ string
		var nn, pk, hidden int
		if err := rows.Scan(&name, &typ, &nn, &pk, &hidden); err != nil {
			rows.Close()
			return m, err
		}
		nullable := nn == 0

		m.Columns[name] = EditColumn{Nullable: nullable, Generated: hidden != 0, PrimaryKey: pk > 0, Type: typ}
		if pk > 0 {
			primary = append(primary, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if len(m.Columns) > 1000 {
		return m, ErrReadOnlyTable
	}
	var keyErr error
	validKey := func(key []string) bool {
		if len(key) == 0 {
			return false
		}
		for _, name := range key {
			c, ok := m.Columns[name]
			if !ok || c.Nullable || c.Generated || !sqliteSafeKeyType(c.Type) {
				return false
			}
		}
		unsafe := []string{}
		for _, name := range key {
			unsafe = append(unsafe, "typeof("+SQLiteQuoteIdent(name)+") NOT IN ('integer','text')")
		}
		var found int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM "+SQLiteQuoteIdent(table)+" WHERE "+strings.Join(unsafe, " OR ")+" LIMIT 1").Scan(&found)
		if err == nil {
			return false
		}
		if !errors.Is(err, sql.ErrNoRows) {
			keyErr = err
			return false
		}
		return true
	}
	// Rowid aliases have no backing primary-key index; DESC and composite keys do.
	indexes, _, err := metadataRows(ctx, db, `SELECT name,origin FROM pragma_index_list(?) WHERE "unique"=1 AND partial=0 ORDER BY origin='pk' DESC,name LIMIT 1001`, table)
	if err != nil {
		return m, err
	}
	hasPKIndex := false
	for _, idx := range indexes {
		if idx["origin"] == "pk" {
			hasPKIndex = true
		}
	}
	if !hasPKIndex && len(primary) == 1 {
		name := primary[0]
		c := m.Columns[name]
		if strings.EqualFold(c.Type, "INTEGER") {
			c.Nullable = false
			m.Columns[name] = c
		}
	}

	if validKey(primary) {
		m.Key = primary
		return m, nil
	}
	for _, idx := range indexes {
		entries, truncated, err := metadataRows(ctx, db, `SELECT name FROM pragma_index_info(?) ORDER BY seqno LIMIT 1001`, idx["name"])
		if err != nil {
			return m, err
		}
		if truncated {
			continue
		}
		key := []string{}
		for _, e := range entries {
			key = append(key, e["name"])
		}
		if validKey(key) {
			m.Key = key
			return m, nil
		}
	}
	if keyErr != nil {
		return m, keyErr
	}
	return m, ErrReadOnlyTable
}
func sqliteSafeKeyType(typ string) bool {
	t := strings.ToUpper(typ)
	return t != "" && !strings.Contains(t, "BLOB") && !strings.Contains(t, "REAL") && !strings.Contains(t, "FLOA") && !strings.Contains(t, "DOUB")
}

func SQLiteApplyUpdates(ctx context.Context, db *sql.DB, statements []UpdateStatement) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, s := range statements {
		result, err := tx.ExecContext(ctx, s.SQL, s.Parameters...)
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
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(statements), nil
}
