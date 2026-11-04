package driver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgresCatalogResult describes driver capabilities even when a category is empty.
type PostgresCatalogResult struct {
	Tables               []PostgresTable
	Objects              []Object
	Warnings             []string
	SupportedObjectTypes []string
}

func postgresBaseObjectTypes() []string {
	return []string{"table", "view", "materialized_view", "trigger", "procedure", "function", "role", "user", "schema", "sequence", "type", "extension", "index"}
}

func postgresPermissionError(err error) bool {
	var server *pgconn.PgError
	return errors.As(err, &server) && server.Code == "42501"
}

const pgRoutineName = `quote_ident(n.nspname)||'.'||quote_ident(p.proname)||'('||pg_get_function_identity_arguments(p.oid)||')'`
const pgTriggerName = `quote_ident(n.nspname)||'.'||quote_ident(c.relname)||'.'||quote_ident(t.tgname)`

// All category queries select explicit safe fields; account authentication catalogs
// and cron commands are deliberately excluded. Definitions are fetched on demand.
var postgresObjectCategories = []struct{ label, query string }{
	{"Accounts", `SELECT rolname AS "__name",CASE WHEN rolcanlogin THEN 'user' ELSE 'role' END AS "__type",rolname AS "Account name",rolcanlogin AS "Login allowed",rolsuper AS "Superuser",rolinherit AS "Inherit privileges",rolcreaterole AS "Create roles",rolcreatedb AS "Create databases",rolreplication AS "Replication",rolbypassrls AS "Bypass row security",rolconnlimit AS "Connection limit",rolvaliduntil AS "Valid until" FROM pg_roles ORDER BY rolname LIMIT 1001`},
	{"Triggers", `SELECT ` + pgTriggerName + ` AS "__name",'trigger' AS "__type",n.nspname AS "__schema",quote_ident(c.relname)||'.'||quote_ident(t.tgname) AS "__display",t.tgname AS "Name",` + pgRelationName + ` AS "Table",t.tgenabled AS "Enabled",pg_get_userbyid(c.relowner) AS "Table owner" FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal AND ` + pgUserNamespace + ` AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') ORDER BY n.nspname,c.relname,t.tgname LIMIT 1001`},
	{"Routines", `SELECT ` + pgRoutineName + ` AS "__name",CASE WHEN p.prokind='p' THEN 'procedure' ELSE 'function' END AS "__type",n.nspname AS "__schema",p.proname||'('||pg_get_function_identity_arguments(p.oid)||')' AS "__display",n.nspname AS "Schema",p.proname AS "Name",pg_get_function_identity_arguments(p.oid) AS "Identity arguments",pg_get_function_result(p.oid) AS "Returns",l.lanname AS "Language",pg_get_userbyid(p.proowner) AS "Owner",p.prosecdef AS "Security definer" FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE ` + pgUserNamespace + ` AND p.prokind IN ('f','p','w') AND has_schema_privilege(n.oid,'USAGE') AND has_function_privilege(p.oid,'EXECUTE') ORDER BY n.nspname,p.proname,pg_get_function_identity_arguments(p.oid) LIMIT 1001`},
	{"Schemas", `SELECT quote_ident(n.nspname) AS "__name",'schema' AS "__type",n.nspname AS "__schema",n.nspname AS "__display",current_user AS "Current account",has_schema_privilege(n.oid,'USAGE') AS "Current account USAGE",has_schema_privilege(n.oid,'CREATE') AS "Current account CREATE",n.nspname AS "Name",pg_get_userbyid(n.nspowner) AS "Owner",obj_description(n.oid,'pg_namespace') AS "Comment" FROM pg_namespace n WHERE ` + pgUserNamespace + ` AND has_schema_privilege(n.oid,'USAGE') ORDER BY n.nspname LIMIT 1001`},
	{"Sequences", `SELECT ` + pgRelationName + ` AS "__name",'sequence' AS "__type",n.nspname AS "__schema",c.relname AS "__display",n.nspname AS "Schema",c.relname AS "Name",pg_get_userbyid(c.relowner) AS "Owner",format_type(s.seqtypid,NULL) AS "Type",s.seqstart AS "Start",s.seqincrement AS "Increment",s.seqmin AS "Minimum",s.seqmax AS "Maximum",s.seqcache AS "Cache",s.seqcycle AS "Cycle" FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ` + pgUserNamespace + ` AND has_schema_privilege(n.oid,'USAGE') AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(c.oid,'USAGE,SELECT') ELSE false END ORDER BY n.nspname,c.relname LIMIT 1001`},
	{"Types", `SELECT quote_ident(n.nspname)||'.'||quote_ident(t.typname) AS "__name",'type' AS "__type",n.nspname AS "__schema",t.typname AS "__display",n.nspname AS "Schema",t.typname AS "Name",t.typtype AS "Kind",pg_get_userbyid(t.typowner) AS "Owner",obj_description(t.oid,'pg_type') AS "Comment" FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace LEFT JOIN pg_class c ON c.oid=t.typrelid WHERE ` + pgUserNamespace + ` AND t.typelem=0 AND t.typisdefined AND (t.typtype<>'c' OR c.relkind='c') AND has_schema_privilege(n.oid,'USAGE') AND has_type_privilege(t.oid,'USAGE') ORDER BY n.nspname,t.typname LIMIT 1001`},
	{"Extensions", `SELECT quote_ident(e.extname) AS "__name",'extension' AS "__type",e.extname AS "Name",e.extversion AS "Version",n.nspname AS "Schema",pg_get_userbyid(e.extowner) AS "Owner",e.extrelocatable AS "Relocatable" FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace ORDER BY e.extname LIMIT 1001`},
	{"Indexes", `SELECT quote_ident(n.nspname)||'.'||quote_ident(ci.relname) AS "__name",'index' AS "__type",n.nspname AS "__schema",ci.relname AS "__display",n.nspname AS "Schema",ci.relname AS "Name",` + pgRelationName + ` AS "Table",i.indisprimary AS "Primary key",i.indisunique AS "Unique",i.indisvalid AS "Valid",pg_relation_size(ci.oid) AS "Bytes" FROM pg_index i JOIN pg_class ci ON ci.oid=i.indexrelid JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ` + pgUserNamespace + ` AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') ORDER BY n.nspname,ci.relname LIMIT 1001`},
}

const postgresCronQuery = `SELECT jobid::text AS "__name",'cron' AS "__type",jobid AS "Job ID",schedule AS "Schedule",database AS "Database",username AS "User",active AS "Active",nodename AS "Host",nodeport AS "Port" FROM cron.job ORDER BY jobid LIMIT 1001`

func PostgresCatalog(ctx context.Context, db *sql.DB) (PostgresCatalogResult, error) {
	result := PostgresCatalogResult{Tables: []PostgresTable{}, Objects: []Object{}, Warnings: []string{}, SupportedObjectTypes: postgresBaseObjectTypes()}
	if err := postgresRelations(ctx, db, &result); err != nil {
		return result, err
	}
	for _, category := range postgresObjectCategories {
		objects, truncated, err := postgresCategory(ctx, db, category.query)
		if err != nil {
			if postgresPermissionError(err) {
				result.Warnings = append(result.Warnings, category.label+" metadata is unavailable to this account.")
				continue
			}
			return result, fmt.Errorf("read PostgreSQL %s: %w", category.label, err)
		}
		result.Objects = append(result.Objects, objects...)
		if category.label == "Accounts" {
			memberships, limited, membershipErr := metadataRows(ctx, db, `SELECT member.rolname AS "Account",parent.rolname AS "Role",am.admin_option AS "Admin option" FROM pg_auth_members am JOIN pg_roles member ON member.oid=am.member JOIN pg_roles parent ON parent.oid=am.roleid ORDER BY member.rolname,parent.rolname LIMIT 1001`)
			if membershipErr != nil {
				if postgresPermissionError(membershipErr) {
					result.Warnings = append(result.Warnings, "Role memberships are unavailable to this account.")
				} else {
					return result, fmt.Errorf("read PostgreSQL role memberships: %w", membershipErr)
				}
			} else {
				membershipNames := map[string][]string{}
				for _, entry := range memberships {
					label := PostgresQuoteIdent(entry["Role"])
					if entry["Admin option"] == "true" {
						label += " (admin option)"
					}
					membershipNames[entry["Account"]] = append(membershipNames[entry["Account"]], label)
				}
				for _, object := range objects {
					object.Details["Member of"] = strings.Join(membershipNames[object.Name], ", ")
				}
				if limited {
					result.Warnings = append(result.Warnings, "Role memberships are limited to 1000 entries.")
				}
			}
		}
		if category.label == "Schemas" {
			if err := postgresSchemaACL(ctx, db, objects, &result.Warnings); err != nil {
				return result, err
			}
		}
		if truncated {
			result.Warnings = append(result.Warnings, category.label+" metadata is limited to 1000 entries.")
		}
	}
	installed, accessible, err := postgresCronAccess(ctx, db)
	if err != nil {
		if postgresPermissionError(err) {
			result.Warnings = append(result.Warnings, "pg_cron availability is unavailable to this account.")
			return result, nil
		}
		return result, err
	}
	if installed && !accessible {
		result.Warnings = append(result.Warnings, "pg_cron is installed, but cron jobs are unavailable to this account.")
	}
	if accessible {
		objects, truncated, err := postgresCategory(ctx, db, postgresCronQuery)
		if err != nil {
			var server *pgconn.PgError
			if postgresPermissionError(err) || (errors.As(err, &server) && server.Code == "42P01") {
				result.Warnings = append(result.Warnings, "pg_cron jobs are unavailable to this account.")
				return result, nil
			}
			return result, fmt.Errorf("read pg_cron jobs: %w", err)
		}
		result.Objects = append(result.Objects, objects...)
		result.SupportedObjectTypes = append(result.SupportedObjectTypes, "cron")
		if truncated {
			result.Warnings = append(result.Warnings, "pg_cron jobs are limited to 1000 entries.")
		}
	}
	return result, nil
}

func postgresRelations(ctx context.Context, db *sql.DB, result *PostgresCatalogResult) error {
	// Each relation category has its own limit so a large table catalog cannot hide views.
	for _, category := range []struct{ kind, label string }{{"r,p", "Tables"}, {"v", "Views"}, {"m", "Materialized views"}} {
		rows, err := db.QueryContext(ctx, `SELECT `+pgRelationName+`,n.nspname,c.relname,GREATEST(c.reltuples,0)::bigint,pg_table_size(c.oid),pg_indexes_size(c.oid),c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE `+pgUserNamespace+` AND c.relkind::text=ANY(string_to_array($1,',')) AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') ORDER BY n.nspname,c.relname LIMIT 1001`, category.kind)
		if err != nil {
			if postgresPermissionError(err) {
				result.Warnings = append(result.Warnings, category.label+" metadata is unavailable to this account.")
				continue
			}
			return err
		}
		count := 0
		for rows.Next() {
			if count == 1000 {
				result.Warnings = append(result.Warnings, category.label+" metadata is limited to 1000 entries.")
				break
			}
			count++
			var table PostgresTable
			var kind string
			if err := rows.Scan(&table.Name, &table.SchemaName, &table.DisplayName, &table.RowsEstimate, &table.DataBytes, &table.IndexBytes, &kind); err != nil {
				rows.Close()
				return err
			}
			if kind == "v" || kind == "m" {
				objectType := "view"
				if kind == "m" {
					objectType = "materialized_view"
				}
				result.Objects = append(result.Objects, Object{Name: table.Name, SchemaName: table.SchemaName, DisplayName: table.DisplayName, Type: objectType, Details: map[string]string{"Relation kind": kind}})
			} else {
				table.Columns = []RowColumn{}
				result.Tables = append(result.Tables, table)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			if postgresPermissionError(err) {
				result.Warnings = append(result.Warnings, category.label+" metadata is unavailable to this account.")
				continue
			}
			return err
		}
		rows.Close()
	}
	for i := range result.Tables {
		columns, err := PostgresColumns(ctx, db, result.Tables[i].Name, false)
		if err != nil {
			if postgresPermissionError(err) || errors.Is(err, ErrRowsObjectNotFound) {
				result.Warnings = append(result.Warnings, "Columns for "+result.Tables[i].Name+" are unavailable.")
				continue
			}
			return err
		}
		result.Tables[i].Columns = columns
	}
	return nil
}

func postgresCategory(ctx context.Context, db *sql.DB, query string) ([]Object, bool, error) {
	entries, truncated, err := metadataRows(ctx, db, query)
	if err != nil {
		return nil, false, err
	}
	objects := make([]Object, 0, len(entries))
	for _, details := range entries {
		name, kind := details["__name"], details["__type"]
		delete(details, "__name")
		delete(details, "__type")
		object := Object{Name: name, Type: kind, SchemaName: details["__schema"], DisplayName: details["__display"], Details: details}
		delete(details, "__schema")
		delete(details, "__display")
		switch kind {
		case "role":
			object.Definition = "CREATE ROLE " + PostgresQuoteIdent(name) + " NOLOGIN;"
			details["Definition source"] = "Reconstructed CREATE ROLE; attributes, memberships and grants are not included."
		case "user":
			details["Definition status"] = "Account authentication definitions are not requested or returned."
		case "cron":
			details["Definition status"] = "Job SQL is withheld because commands may contain credentials; scheduling calls are not reconstructed."
		}
		objects = append(objects, object)
	}
	return objects, truncated, nil
}

func postgresCronAccess(ctx context.Context, db *sql.DB) (bool, bool, error) {
	var installed, accessible bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_cron'),EXISTS(SELECT 1 FROM pg_extension e JOIN pg_namespace n ON n.nspname='cron' JOIN pg_class c ON c.relnamespace=n.oid AND c.relname='job' WHERE e.extname='pg_cron' AND c.relkind IN ('r','p') AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT'))`).Scan(&installed, &accessible)
	return installed, installed && accessible, err
}

func PostgresObjectDefinition(ctx context.Context, db *sql.DB, kind, name string) (Object, error) {
	result := Object{Name: name, Type: kind, Details: map[string]string{}}
	switch kind {
	case "view", "materialized_view":
		return postgresViewDefinition(ctx, db, kind, name)
	case "function", "procedure", "trigger", "schema", "sequence", "type", "extension", "index", "cron":
	default:
		return result, errors.New("unsupported PostgreSQL object type")
	}
	if kind == "cron" {
		_, accessible, err := postgresCronAccess(ctx, db)
		if err != nil {
			return postgresUnavailable(result, err)
		}
		if !accessible {
			result.Details["Definition status"] = "pg_cron is not installed or its jobs are unavailable to this account."
			return result, nil
		}
		result.Details["Definition status"] = "Job SQL is withheld because commands may contain credentials; scheduling calls are not reconstructed."
		return result, nil
	}
	query, args := postgresDefinitionQuery(kind, name)
	var definition sql.NullString
	err := db.QueryRowContext(ctx, query, args...).Scan(&definition)
	if err != nil {
		return postgresUnavailable(result, err)
	}
	if !definition.Valid || definition.String == "" {
		result.Details["Definition status"] = "A complete CREATE statement cannot be reconstructed safely for this PostgreSQL object."
		return result, nil
	}
	result.Definition = definition.String
	switch kind {
	case "function", "procedure":
		result.Details["Definition source"] = "pg_get_functiondef"
	case "trigger":
		result.Details["Definition source"] = "pg_get_triggerdef"
	case "index":
		result.Details["Definition source"] = "pg_get_indexdef"
	default:
		result.Details["Definition source"] = "Reconstructed from PostgreSQL catalogs; ownership, grants and current state are not included."
	}
	return result, nil
}

func postgresUnavailable(result Object, err error) (Object, error) {
	if errors.Is(err, sql.ErrNoRows) {
		result.Details["Definition status"] = "Object was not found or is not visible to this account."
		return result, nil
	}
	if postgresPermissionError(err) {
		result.Details["Definition status"] = "Definition is unavailable to this account."
		return result, nil
	}
	return result, err
}

func postgresDefinitionQuery(kind, name string) (string, []any) {
	args := []any{name}
	switch kind {
	case "function", "procedure":
		prokind := "f,w"
		if kind == "procedure" {
			prokind = "p"
		}
		args = append(args, prokind)
		return `SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE ` + pgUserNamespace + ` AND ` + pgRoutineName + `=$1 AND p.prokind::text=ANY(string_to_array($2,',')) AND has_schema_privilege(n.oid,'USAGE') AND has_function_privilege(p.oid,'EXECUTE') LIMIT 1`, args
	case "trigger":
		return `SELECT pg_get_triggerdef(t.oid,true)||';' FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ` + pgUserNamespace + ` AND ` + pgTriggerName + `=$1 AND NOT t.tgisinternal AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') LIMIT 1`, args
	case "index":
		return `SELECT pg_get_indexdef(i.indexrelid)||';' FROM pg_index i JOIN pg_class ci ON ci.oid=i.indexrelid JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ` + pgUserNamespace + ` AND quote_ident(n.nspname)||'.'||quote_ident(ci.relname)=$1 AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') LIMIT 1`, args
	case "schema":
		return `SELECT 'CREATE SCHEMA '||quote_ident(n.nspname)||';' FROM pg_namespace n WHERE ` + pgUserNamespace + ` AND quote_ident(n.nspname)=$1 AND has_schema_privilege(n.oid,'USAGE') LIMIT 1`, args
	case "sequence":
		return `SELECT 'CREATE SEQUENCE '||` + pgRelationName + `||' AS '||format_type(s.seqtypid,NULL)||' INCREMENT BY '||s.seqincrement||' MINVALUE '||s.seqmin||' MAXVALUE '||s.seqmax||' START WITH '||s.seqstart||' CACHE '||s.seqcache||CASE WHEN s.seqcycle THEN ' CYCLE;' ELSE ' NO CYCLE;' END FROM pg_sequence s JOIN pg_class c ON c.oid=s.seqrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE ` + pgUserNamespace + ` AND ` + pgRelationName + `=$1 AND has_schema_privilege(n.oid,'USAGE') AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(c.oid,'USAGE,SELECT') ELSE false END LIMIT 1`, args
	case "type":
		return `SELECT CASE WHEN t.typtype='e' THEN 'CREATE TYPE '||quote_ident(n.nspname)||'.'||quote_ident(t.typname)||' AS ENUM ('||(SELECT string_agg(quote_literal(e.enumlabel),', ' ORDER BY e.enumsortorder) FROM pg_enum e WHERE e.enumtypid=t.oid)||');' ELSE NULL END FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace LEFT JOIN pg_class c ON c.oid=t.typrelid WHERE ` + pgUserNamespace + ` AND quote_ident(n.nspname)||'.'||quote_ident(t.typname)=$1 AND t.typelem=0 AND t.typisdefined AND (t.typtype<>'c' OR c.relkind='c') AND has_schema_privilege(n.oid,'USAGE') AND has_type_privilege(t.oid,'USAGE') LIMIT 1`, args
	case "extension":
		return `SELECT 'CREATE EXTENSION '||quote_ident(e.extname)||' WITH SCHEMA '||quote_ident(n.nspname)||' VERSION '||quote_literal(e.extversion)||';' FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE quote_ident(e.extname)=$1 LIMIT 1`, args
	}
	return "", nil
}

func postgresViewDefinition(ctx context.Context, db *sql.DB, kind, name string) (Object, error) {
	result := Object{Name: name, Type: kind, Details: map[string]string{}}
	kinds := "v,m"
	if kind == "view" {
		kinds = "v"
	}
	if kind == "materialized_view" {
		kinds = "m"
	}
	var source, definition, relkind string
	var options sql.NullString
	var populated bool
	err := db.QueryRowContext(ctx, `SELECT `+pgRelationName+`,pg_get_viewdef(c.oid,true),c.relkind,(SELECT string_agg(quote_ident(split_part(opt,'=',1))||'='||quote_literal(substring(opt from position('=' in opt)+1)),', ') FROM unnest(c.reloptions) opt),c.relispopulated FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE `+pgUserNamespace+` AND `+pgRelationName+`=$1 AND c.relkind::text=ANY(string_to_array($2,',')) AND has_schema_privilege(n.oid,'USAGE') AND has_table_privilege(c.oid,'SELECT') LIMIT 1`, name, kinds).Scan(&source, &definition, &relkind, &options, &populated)
	if err != nil {
		return postgresUnavailable(result, err)
	}
	prefix := "CREATE VIEW "
	result.Type = "view"
	if relkind == "m" {
		prefix = "CREATE MATERIALIZED VIEW "
		result.Type = "materialized_view"
	}
	result.Definition = prefix + source
	if options.Valid && options.String != "" {
		result.Definition += " WITH (" + options.String + ")"
	}
	result.Definition += " AS\n" + strings.TrimSuffix(strings.TrimSpace(definition), ";")
	if relkind == "m" && !populated {
		result.Definition += "\nWITH NO DATA"
	}
	result.Definition += ";"
	result.Details["Definition source"] = "Reconstructed CREATE statement using pg_get_viewdef and relation options; ownership and grants are not included."
	return result, nil
}

// Schema ACLs are loaded once alongside schema listings, never per selected
// object. Each schema has at most 1000 displayed direct grants; effective access
// remains a separate server-computed result that includes memberships and PUBLIC.
const postgresSchemaACLQuery = `SELECT n.nspname AS "Schema",n.nspacl IS NULL AS "Default ACL",COALESCE(grants.entries,'') AS "ACL entries",COALESCE(grants.truncated,false) AS "ACL truncated" FROM pg_namespace n LEFT JOIN LATERAL (SELECT string_agg(CASE WHEN acl.grantee=0 THEN 'PUBLIC' ELSE quote_ident(pg_get_userbyid(acl.grantee)) END||': '||acl.privilege_type||CASE WHEN acl.is_grantable THEN ' WITH GRANT OPTION' ELSE '' END||' (grantor '||quote_ident(pg_get_userbyid(acl.grantor))||')',E'\n' ORDER BY acl.grantee,acl.privilege_type,acl.grantor) FILTER (WHERE acl.entry_no<=1000) AS entries,COUNT(*)>1000 AS truncated FROM (SELECT expanded.*,row_number() OVER (ORDER BY expanded.grantee,expanded.privilege_type,expanded.grantor) AS entry_no FROM aclexplode(COALESCE(n.nspacl,acldefault('n',n.nspowner))) expanded ORDER BY expanded.grantee,expanded.privilege_type,expanded.grantor LIMIT 1001) acl) grants ON true WHERE ` + pgUserNamespace + ` AND has_schema_privilege(n.oid,'USAGE') ORDER BY n.nspname LIMIT 1001`

func postgresSchemaACL(ctx context.Context, db *sql.DB, schemas []Object, warnings *[]string) error {
	if len(schemas) == 0 {
		return nil
	}
	entries, truncated, err := metadataRows(ctx, db, postgresSchemaACLQuery)
	if err != nil {
		if postgresPermissionError(err) {
			*warnings = append(*warnings, "Schema ACL metadata is unavailable to this account.")
			for _, schema := range schemas {
				schema.Details["ACL status"] = "Schema ACL metadata is unavailable to this account; effective permissions above remain available."
			}
			return nil
		}
		return fmt.Errorf("read PostgreSQL schema ACLs: %w", err)
	}
	byName := map[string]map[string]string{}
	for _, entry := range entries {
		byName[entry["Schema"]] = entry
	}
	for _, schema := range schemas {
		schema.Details["Permission interpretation"] = "USAGE and CREATE are effective permissions for the current account, including role memberships, PUBLIC grants and superuser rights. Owner identifies the schema owner, who has implicit grant options."
		schema.Details["ACL interpretation"] = "PUBLIC means every role. Entries show direct schema grants and their grantors; they do not enumerate inherited grants or ALTER DEFAULT PRIVILEGES rules for future objects."
		entry, ok := byName[schema.SchemaName]
		if !ok {
			schema.Details["ACL status"] = "Schema ACL metadata was unavailable in this listing."
			continue
		}
		if entry["Default ACL"] == "true" {
			schema.Details["ACL source"] = "Default schema ACL (nspacl is NULL): PostgreSQL built-in owner grants, not ALTER DEFAULT PRIVILEGES rules."
		} else {
			schema.Details["ACL source"] = "Explicit schema ACL (nspacl); an empty list contains no direct grants."
		}
		schema.Details["ACL entries"] = entry["ACL entries"]
		if schema.Details["ACL entries"] == "" {
			schema.Details["ACL entries"] = "No direct grants."
		}
		if entry["ACL truncated"] == "true" {
			schema.Details["ACL status"] = "Limited to 1000 direct grants."
			*warnings = append(*warnings, "Schema ACL for "+schema.SchemaName+" is limited to 1000 direct grants.")
		} else {
			schema.Details["ACL status"] = "Available."
		}
	}
	if truncated {
		*warnings = append(*warnings, "Schema ACL metadata is limited to 1000 schemas.")
	}
	return nil
}
