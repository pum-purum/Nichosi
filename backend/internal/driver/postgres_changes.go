package driver

import (
	"context"
	"database/sql"
	"strings"
)

// PostgresEditMetadata resolves opaque API relation names through the catalog.
// Only complete, immediate, non-null unique keys can identify editable rows.
func PostgresEditMetadata(ctx context.Context, db *sql.DB, name string) (EditMetadata, error) {
	m := EditMetadata{Columns: map[string]EditColumn{}, Key: []string{}}
	oid, source, err := pgRelation(ctx, db, name, false)
	if err != nil {
		return m, err
	}
	m.source = source
	// A parent's unique index does not cover ordinary inheritance children.
	// Partitioned tables, in contrast, require globally usable partition keys.
	rows, err := db.QueryContext(ctx, `SELECT a.attname,t.typname,NOT a.attnotnull,
a.attgenerated<>'' OR a.attidentity='a',a.attidentity<>'',pg_get_expr(d.adbin,d.adrelid),
EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=a.attrelid AND i.indisprimary AND a.attnum=ANY(i.indkey))
FROM pg_attribute a JOIN pg_type t ON t.oid=a.atttypid JOIN pg_class c ON c.oid=a.attrelid
LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum
WHERE a.attrelid=$1 AND a.attnum>0 AND NOT a.attisdropped
AND (c.relkind='p' OR NOT EXISTS(SELECT 1 FROM pg_inherits h WHERE h.inhparent=c.oid))
ORDER BY a.attnum LIMIT 1001`, oid)
	if err != nil {
		return m, err
	}
	for rows.Next() {
		var name string
		var c EditColumn
		if err = rows.Scan(&name, &c.Type, &c.Nullable, &c.Generated, &c.AutoIncrement, &c.DefaultValue, &c.PrimaryKey); err != nil {
			rows.Close()
			return m, err
		}
		// Serial columns expose their sequence through a server-evaluated default.
		c.AutoIncrement = c.AutoIncrement || (c.DefaultValue != nil && strings.HasPrefix(*c.DefaultValue, "nextval("))
		// Identity ALWAYS accepts explicit inserts via OVERRIDING SYSTEM VALUE.
		if c.AutoIncrement {
			c.Generated = false
		}
		m.Columns[name] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return m, err
	}
	if len(m.Columns) == 0 || len(m.Columns) > 1000 {
		return m, ErrReadOnlyTable
	}
	for name, column := range m.Columns {
		if column.PrimaryKey && column.AutoIncrement {
			column.NextAutoIncrementValue, _ = postgresNextIdentity(ctx, db, oid, name)
			m.Columns[name] = column
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT i.indexrelid::bigint,a.attname
FROM pg_index i CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum,position)
LEFT JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.attnum
WHERE i.indrelid=$1 AND i.indisunique AND i.indisvalid AND i.indisready AND i.indimmediate
AND i.indpred IS NULL AND i.indexprs IS NULL AND k.position<=i.indnkeyatts
ORDER BY i.indisprimary DESC,i.indexrelid,k.position LIMIT 1001`, oid)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	var current int64
	candidate := []string{}
	valid, count := true, 0
	choose := func() {
		if valid && len(candidate) > 0 && len(m.Key) == 0 {
			m.Key = append([]string{}, candidate...)
		}
	}
	for rows.Next() {
		count++
		var index int64
		var name sql.NullString
		if err = rows.Scan(&index, &name); err != nil {
			return m, err
		}
		if index != current {
			choose()
			current, candidate, valid = index, nil, true
		}
		column, ok := m.Columns[name.String]
		if !name.Valid || !ok || column.Nullable || !safePostgresEditKeyType(column.Type) {
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

// These types have lossless scalar transport through JSONRowValue and the editor.
// Float, binary, temporal, composite, array and user-defined keys are conservative
// exclusions until their identity-preserving transport is implemented.
func safePostgresEditKeyType(name string) bool {
	switch name {
	case "int2", "int4", "int8", "text", "varchar", "bpchar", "uuid", "bool", "numeric":
		return true
	}
	return false
}

func PostgresApplyUpdates(ctx context.Context, db *sql.DB, statements []UpdateStatement) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Sessions default to read-only; elevate only this already-authorized ChangeSet.
	// PostgreSQL itself rejects this on a physical standby.
	if _, err = tx.ExecContext(ctx, "SET TRANSACTION READ WRITE"); err != nil {
		return 0, err
	}
	for _, statement := range statements {
		result, err := tx.ExecContext(ctx, statement.SQL, statement.Parameters...)
		if err != nil {
			return 0, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		// PostgreSQL counts matched rows even when UPDATE leaves values unchanged.
		if n != 1 {
			return 0, ErrRowConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(statements), nil
}
