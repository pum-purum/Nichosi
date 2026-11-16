package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

var ErrTableNotFound = errors.New("table not found")

type TableMetadata struct {
	Name                                    string
	Details                                 map[string]string
	Columns, Indexes, ForeignKeys, Triggers []map[string]string
	Sequences                               *[]map[string]string
	Definition                              string
	Warnings                                []string
}

// MySQLTableMetadata reads bounded metadata without fetching user table data.
func MySQLTableMetadata(ctx context.Context, db *sql.DB, database, table string) (TableMetadata, error) {
	result := TableMetadata{Name: table, Details: map[string]string{}, Columns: []map[string]string{}, Indexes: []map[string]string{}, ForeignKeys: []map[string]string{}, Triggers: []map[string]string{}, Warnings: []string{}}
	details, _, err := metadataRows(ctx, db, `SELECT ENGINE AS Engine, TABLE_COLLATION AS Collation, TABLE_ROWS AS "Estimated rows", DATA_LENGTH AS "Data bytes", INDEX_LENGTH AS "Index bytes", DATA_FREE AS "Free bytes", AVG_ROW_LENGTH AS "Average row bytes", AUTO_INCREMENT AS "Next auto increment", ROW_FORMAT AS "Row format", CREATE_TIME AS Created, UPDATE_TIME AS Updated, TABLE_COMMENT AS Comment, CREATE_OPTIONS AS Options FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE' LIMIT 1`, database, table)
	if err != nil {
		return result, fmt.Errorf("read table metadata: %w", err)
	}
	if len(details) == 0 {
		return result, ErrTableNotFound
	}
	result.Details = details[0]
	if strings.EqualFold(result.Details["Engine"], "FEDERATED") || connectionOption.MatchString(result.Details["Options"]) {
		result.Details["Options"] = "Withheld: may contain connection credentials"
	}
	categories := []struct {
		name, query string
		dest        *[]map[string]string
	}{
		{"Columns", `SELECT ORDINAL_POSITION AS Position, COLUMN_NAME AS Name, COLUMN_TYPE AS Type, IS_NULLABLE AS Nullable, COLUMN_DEFAULT AS "Default", COLUMN_KEY AS "Key", EXTRA AS Extra, CHARACTER_SET_NAME AS "Character set", COLLATION_NAME AS Collation, COLUMN_COMMENT AS Comment FROM information_schema.columns WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION LIMIT 1001`, &result.Columns},
		{"Indexes", `SELECT INDEX_NAME AS Name, CASE WHEN INDEX_NAME = 'PRIMARY' THEN 'Primary key' WHEN NON_UNIQUE = 0 THEN 'Unique' ELSE 'Index' END AS Kind, SEQ_IN_INDEX AS Position, COALESCE(COLUMN_NAME, '') AS "Column", COLLATION AS Ordering, SUB_PART AS "Prefix length", INDEX_TYPE AS Type, CARDINALITY AS "Estimated cardinality", INDEX_COMMENT AS Comment FROM information_schema.statistics WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX LIMIT 1001`, &result.Indexes},
		{"Foreign keys", `SELECT k.CONSTRAINT_NAME AS Name, k.ORDINAL_POSITION AS Position, k.COLUMN_NAME AS "Column", k.REFERENCED_TABLE_SCHEMA AS "Referenced database", k.REFERENCED_TABLE_NAME AS "Referenced table", k.REFERENCED_COLUMN_NAME AS "Referenced column", r.UPDATE_RULE AS "On update", r.DELETE_RULE AS "On delete" FROM information_schema.key_column_usage k LEFT JOIN information_schema.referential_constraints r ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION LIMIT 1001`, &result.ForeignKeys},
		{"Triggers", `SELECT TRIGGER_NAME AS Name, ACTION_TIMING AS Timing, EVENT_MANIPULATION AS Event, ACTION_STATEMENT AS Statement, DEFINER AS Definer, CREATED AS Created, SQL_MODE AS "SQL mode" FROM information_schema.triggers WHERE TRIGGER_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? ORDER BY TRIGGER_NAME LIMIT 1001`, &result.Triggers},
	}
	for _, category := range categories {
		entries, truncated, err := metadataRows(ctx, db, category.query, database, table)
		if err != nil {
			if metadataPermissionError(err) {
				result.Warnings = append(result.Warnings, category.name+" metadata is unavailable to this account.")
				continue
			}
			return result, fmt.Errorf("read %s metadata: %w", category.name, err)
		}
		*category.dest = entries
		if truncated {
			result.Warnings = append(result.Warnings, category.name+" metadata is limited to 1000 entries.")
		}
	}
	object := Object{Name: table, Type: "table", Details: map[string]string{}}
	if err := mysqlObjectDefinition(ctx, db, database, &object); err != nil {
		if metadataPermissionError(err) || errors.Is(err, errDefinitionUnavailable) {
			result.Warnings = append(result.Warnings, "CREATE TABLE is unavailable to this account.")
		} else {
			return result, fmt.Errorf("read table definition: %w", err)
		}
	} else {
		if connectionOption.MatchString(object.Definition) {
			result.Warnings = append(result.Warnings, "CREATE TABLE is withheld because connection options may contain credentials.")
		} else {
			result.Definition = object.Definition
		}
	}
	result.Warnings = append(result.Warnings, "Trigger metadata only includes triggers visible to this account; an empty list does not prove that no triggers exist.")
	return result, nil
}

var connectionOption = regexp.MustCompile(`(?i)\bCONNECTION\s*=`)

func metadataPermissionError(err error) bool {
	var server *mysql.MySQLError
	if !errors.As(err, &server) {
		return false
	}
	switch server.Number {
	case 1044, 1142, 1143, 1227, 1370:
		return true
	}
	return false
}

func metadataRows(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]string, bool, error) {
	result := []map[string]string{}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, false, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return result, false, err
	}
	for rows.Next() {
		if len(result) == 1000 {
			return result, true, nil
		}
		values := make([]sql.NullString, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return result, false, err
		}
		entry := map[string]string{}
		for i, column := range columns {
			if values[i].Valid {
				entry[column] = values[i].String
			} else {
				entry[column] = "NULL"
			}
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return result, false, err
	}
	if err := rows.Close(); err != nil {
		return result, false, err
	}
	return result, false, nil
}
