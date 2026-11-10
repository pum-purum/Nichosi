package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Object contains safe metadata only; account authentication data is never returned.
type Object struct {
	Name, Type, Definition  string
	SchemaName, DisplayName string
	Details                 map[string]string
}

// MySQLObjects reads schema objects and server-wide accounts with a bounded result size.
// Account metadata uses an administrator-provided view exposing non-secret fields only.
func MySQLObjects(ctx context.Context, db *sql.DB, database string) ([]Object, []string, error) {
	result := []Object{}
	warnings := []string{}
	queries := []struct {
		query  string
		args   []any
		kind   string
		labels []string
	}{
		{`SELECT TABLE_NAME, '', SECURITY_TYPE, CHECK_OPTION, DEFINER, CHARACTER_SET_CLIENT, COLLATION_CONNECTION FROM information_schema.views WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME LIMIT 1001`, []any{database}, "view", []string{"Security", "Check option", "Definer", "Character set client", "Collation connection"}},
		{`SELECT TRIGGER_NAME, '', EVENT_MANIPULATION, EVENT_OBJECT_TABLE, ACTION_TIMING, DEFINER, SQL_MODE, CAST(CREATED AS CHAR) FROM information_schema.triggers WHERE TRIGGER_SCHEMA = ? ORDER BY TRIGGER_NAME LIMIT 1001`, []any{database}, "trigger", []string{"Event", "Table", "Timing", "Definer", "SQL mode", "Created"}},
		{`SELECT ROUTINE_NAME, '', ROUTINE_TYPE, DTD_IDENTIFIER, IS_DETERMINISTIC, SECURITY_TYPE, DEFINER, SQL_DATA_ACCESS, ROUTINE_COMMENT, SQL_MODE, CAST(CREATED AS CHAR), CAST(LAST_ALTERED AS CHAR) FROM information_schema.routines WHERE ROUTINE_SCHEMA = ? ORDER BY ROUTINE_NAME LIMIT 1001`, []any{database}, "routine", []string{"Routine type", "Returns", "Deterministic", "Security", "Definer", "SQL data access", "Comment", "SQL mode", "Created", "Last altered"}},
		{`SELECT EVENT_NAME, '', STATUS, EVENT_TYPE, INTERVAL_VALUE, INTERVAL_FIELD, CAST(EXECUTE_AT AS CHAR), CAST(STARTS AS CHAR), CAST(ENDS AS CHAR), TIME_ZONE, DEFINER, ON_COMPLETION, EVENT_COMMENT, SQL_MODE, CAST(CREATED AS CHAR), CAST(LAST_ALTERED AS CHAR), CAST(LAST_EXECUTED AS CHAR) FROM information_schema.events WHERE EVENT_SCHEMA = ? ORDER BY EVENT_NAME LIMIT 1001`, []any{database}, "event", []string{"Status", "Schedule", "Interval", "Unit", "Execute at", "Starts", "Ends", "Time zone", "Definer", "On completion", "Comment", "SQL mode", "Created", "Last altered", "Last executed"}},
		{`SELECT CONCAT(QUOTE(account_name), '@', QUOTE(account_host)), '', account_type, account_locked, account_name, account_host FROM ` + QuoteIdent(database) + `.nichosi_account_metadata ORDER BY account_name, account_host LIMIT 1001`, nil, "account", []string{"Account type", "Locked", "Account name", "Host"}},
	}
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.query, q.args...)
		if err != nil && q.kind == "account" {
			var server *mysql.MySQLError
			if errors.As(err, &server) && server.Number == 1146 {
				warnings = append(warnings, "Users and roles are unavailable because the optional nichosi_account_metadata view is not installed. Other database objects are unaffected.")
			} else {
				warnings = append(warnings, err.Error())
			}
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %s metadata: %w", q.kind, err)
		}
		count := 0
		for rows.Next() {
			count++
			if count > 1000 {
				warnings = append(warnings, fmt.Sprintf("%s metadata is limited to 1000 objects.", q.kind))
				break
			}
			values := make([]sql.NullString, 2+len(q.labels))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, nil, err
			}
			object := Object{Name: values[0].String, Type: q.kind, Definition: values[1].String, Details: map[string]string{}}
			for i, label := range q.labels {
				if values[i+2].Valid {
					object.Details[label] = values[i+2].String
				}
			}
			if q.kind == "routine" {
				if values[2].String == "FUNCTION" {
					object.Type = "function"
				} else {
					object.Type = "procedure"
				}
			}
			if q.kind == "account" {
				object.Type = values[2].String
				object.Details["Account category"] = mysqlAccountCategory(object.Details["Account name"], object.Details["Host"])
				object.Details["Classification"] = "Locked accounts without credentials are shown as roles"
			}
			result = append(result, object)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	// Keep listings bounded and cheap; full schema DDL is fetched on demand.
	for i := range result {
		object := &result[i]
		if object.Type == "role" {
			object.Definition = "CREATE ROLE " + QuoteIdent(object.Details["Account name"]) + "@" + QuoteIdent(object.Details["Host"]) + ";"
			object.Details["Definition source"] = "Reconstructed CREATE ROLE; grants and original creation history are not included"
			continue
		}
		if object.Type == "user" {
			object.Details["Definition status"] = "User creation SQL is hidden to protect authentication data"
			continue
		}
	}
	return result, warnings, nil
}

// MySQLObjectDefinition retrieves one schema object's current server-reported
// CREATE statement. Account types are deliberately excluded to protect secrets.
func MySQLObjectDefinition(ctx context.Context, db *sql.DB, database, kind, name string) (Object, error) {
	object := Object{Name: name, Type: kind, Details: map[string]string{}}
	switch kind {
	case "view", "trigger", "procedure", "function", "event":
	default:
		return object, fmt.Errorf("unsupported object type")
	}
	if name == "" {
		return object, fmt.Errorf("object name is required")
	}
	if err := mysqlObjectDefinition(ctx, db, database, &object); err != nil {
		if ctx.Err() != nil {
			return object, err
		}
		object.Definition = ""
		object.Details["Definition status"] = err.Error()
	}
	return object, nil
}

// QuoteIdent quotes a MySQL identifier.
func QuoteIdent(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

var errDefinitionUnavailable = errors.New("CREATE statement is unavailable")

// mysqlObjectDefinition reads columns by name because SHOW CREATE result layouts
// differ across object kinds and MySQL/MariaDB versions.
func mysqlObjectDefinition(ctx context.Context, db *sql.DB, database string, object *Object) error {
	kind, column := "", ""
	switch object.Type {
	case "table":
		kind, column = "TABLE", "Create Table"
	case "view":
		kind, column = "VIEW", "Create View"
	case "trigger":
		kind, column = "TRIGGER", "SQL Original Statement"
	case "procedure":
		kind, column = "PROCEDURE", "Create Procedure"
	case "function":
		kind, column = "FUNCTION", "Create Function"
	case "event":
		kind, column = "EVENT", "Create Event"
	default:
		return fmt.Errorf("unsupported object type")
	}
	rows, err := db.QueryContext(ctx, "SHOW CREATE "+kind+" "+QuoteIdent(database)+"."+QuoteIdent(object.Name))
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("missing CREATE statement: %w", errDefinitionUnavailable)
	}
	values := make([]sql.NullString, len(columns))
	dest := make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return err
	}
	for i, name := range columns {
		if strings.EqualFold(name, column) || object.Type == "trigger" && strings.EqualFold(name, "Create Trigger") {
			if !values[i].Valid || values[i].String == "" {
				return fmt.Errorf("CREATE statement is hidden: %w", errDefinitionUnavailable)
			}
			object.Definition = values[i].String
			object.Details["Definition source"] = "SHOW CREATE " + kind
		}
	}
	if object.Definition == "" {
		return fmt.Errorf("missing CREATE statement column: %w", errDefinitionUnavailable)
	}
	return rows.Err()
}

// mysqlAccountCategory recognizes documented account identities, not their history
// or effective privileges. Match both name and host without prefix heuristics.
func mysqlAccountCategory(name, host string) string {
	if host != "localhost" {
		return "other"
	}
	switch name {
	case "mysql.infoschema", "mysql.session", "mysql.sys":
		return "internal"
	case "root":
		return "default-admin"
	default:
		return "other"
	}
}
