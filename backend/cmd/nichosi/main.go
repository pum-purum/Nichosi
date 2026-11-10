package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pum-purum/nichosi/internal/driver"
)

type profile struct {
	scripts scriptState
	Connection
	mu    sync.RWMutex
	pool  *sql.DB
	input ConnectionInput
}

type connectionsConfig struct {
	Connections []json.RawMessage `json:"connections"`
}

type connectionConfig struct {
	Name        string `json:"name"`
	Driver      string `json:"driver"`
	Engine      string `json:"engine"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Database    string `json:"database"`
	Readonly    *bool  `json:"readonly"`
	Password    string `json:"password"`
	PasswordEnv string `json:"passwordEnv"`
}

type app struct {
	mu          sync.RWMutex
	connections []*profile
	metadata    connectionMetadataStore
	historyDir  string
	historyMu   sync.Mutex
	static      http.Handler
}

func main() {
	addr := env("NICHOSI_ADDR", "127.0.0.1:8080")
	static, err := fs.Sub(webFiles, "static")
	if err != nil {
		slog.Error("frontend files unavailable", "error", err)
		os.Exit(1)
	}
	application := &app{static: http.FileServer(http.FS(static))}
	if err := application.metadata.load(os.Getenv("NICHOSI_METADATA_FILE")); err != nil {
		slog.Warn("could not load connection metadata; starting with empty metadata", "error", err)
	}
	application.historyDir = os.Getenv("NICHOSI_HISTORY_DIR")
	application.loadConfiguredConnections()
	for _, item := range application.connections {
		application.metadata.restore(&item.Connection)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/connections", application.listConnections)
	mux.HandleFunc("POST /api/connections", application.createConnection)
	mux.HandleFunc("POST /api/connections/{connId}/connect", application.connectConnection)
	mux.HandleFunc("POST /api/connections/{connId}/disconnect", application.disconnectConnection)
	mux.HandleFunc("GET /api/connections/{connId}/schema", application.getSchema)
	mux.HandleFunc("GET /api/connections/{connId}/object-definition", application.getObjectDefinition)
	mux.HandleFunc("GET /api/connections/{connId}/tables/{table}/metadata", application.getTableMetadata)
	mux.HandleFunc("POST /api/connections/{connId}/views/{view}/rows", application.queryViewRows)
	mux.HandleFunc("GET /api/connections/{connId}/views/{view}/metadata", application.getViewMetadata)
	mux.HandleFunc("POST /api/connections/{connId}/tables/{table}/rows", application.queryRows)
	mux.HandleFunc("POST /api/connections/{connId}/tables/{table}/changes/preview", application.previewChanges)
	mux.HandleFunc("POST /api/connections/{connId}/tables/{table}/changes/apply", application.applyChanges)
	mux.HandleFunc("POST /api/connections/{connId}/scripts/execute", application.executeScript)
	mux.HandleFunc("GET /api/connections/{connId}/scripts/history", application.listScriptHistory)
	mux.HandleFunc("DELETE /api/connections/{connId}/scripts/history", application.clearScriptHistory)
	mux.Handle("/", application.static)

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("Nichosi listening", "address", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func (a *app) listConnections(w http.ResponseWriter, _ *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]Connection, 0, len(a.connections))
	for _, item := range a.connections {
		item.mu.RLock()
		result = append(result, item.Connection)
		item.mu.RUnlock()
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) createConnection(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var input ConnectionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid connection details")
		return
	}
	if !validConnectionInput(input) {
		writeError(w, http.StatusBadRequest, "invalid_connection", "Provide valid database connection details")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	item, err := a.openConnection(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	a.mu.Lock()
	a.connections = append(a.connections, item)
	a.mu.Unlock()
	a.persistMetadata(item.Connection)
	writeJSON(w, http.StatusCreated, item)
}

func (a *app) connection(id string) *profile {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, item := range a.connections {
		if item.Id == id {
			return item
		}
	}
	return nil
}

func (a *app) connectConnection(w http.ResponseWriter, r *http.Request) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.ConfigurationErrors != nil && len(*item.ConfigurationErrors) > 0 {
		writeError(w, http.StatusBadRequest, "invalid_configuration", strings.Join(*item.ConfigurationErrors, "; "))
		return
	}
	if item.pool == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		opened, err := a.openConnection(ctx, item.input)
		if err != nil {
			writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
			return
		}
		item.pool = opened.pool
		if opened.Engine != nil {
			item.Engine = opened.Engine
		}
		item.DatabaseSizeBytes = opened.DatabaseSizeBytes
		item.LastConnectedAt = opened.LastConnectedAt
		item.Readonly = opened.Readonly
		item.input.Readonly = opened.input.Readonly
	}
	item.Connected = true
	a.persistMetadata(item.Connection)
	writeJSON(w, http.StatusOK, item.Connection)
}

func (a *app) disconnectConnection(w http.ResponseWriter, r *http.Request) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}
	item.mu.Lock()
	defer item.mu.Unlock()
	if item.pool != nil {
		if err := item.pool.Close(); err != nil {
			writeError(w, http.StatusBadGateway, "disconnect_failed", err.Error())
			return
		}
		item.pool = nil
	}
	item.Connected = false
	writeJSON(w, http.StatusOK, item.Connection)
}

func (a *app) getSchema(w http.ResponseWriter, r *http.Request) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, 404, "not_found", "Connection not found")
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, http.StatusConflict, "disconnected", "Connection is closed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if item.Driver == ConnectionDriverPostgres || item.Driver == ConnectionDriverSqlite {
		catalog, err := driver.PostgresCatalogResult{}, error(nil)
		if item.Driver == ConnectionDriverSqlite {
			c, e := driver.SQLiteCatalog(ctx, item.pool)
			err = e
			catalog.Tables = c.Tables
			catalog.Objects = c.Objects
			catalog.Warnings = c.Warnings
			catalog.SupportedObjectTypes = c.SupportedObjectTypes
		} else {
			catalog, err = driver.PostgresCatalog(ctx, item.pool)
		}
		tables, objects, warnings := catalog.Tables, catalog.Objects, catalog.Warnings
		if err != nil {
			writeError(w, 502, "metadata_failed", err.Error())
			return
		}
		schema := Schema{Tables: []TableInfo{}, Objects: []DatabaseObject{}, Warnings: &warnings, SupportedObjectTypes: []ObjectType{}}
		for _, kind := range catalog.SupportedObjectTypes {
			schema.SupportedObjectTypes = append(schema.SupportedObjectTypes, ObjectType(kind))
		}
		for _, t := range tables {
			table := TableInfo{Name: t.Name, SchemaName: optionalString(t.SchemaName), DisplayName: optionalString(t.DisplayName), RowsEstimate: t.RowsEstimate, DataBytes: t.DataBytes, IndexBytes: t.IndexBytes, Columns: []Column{}}
			for _, c := range t.Columns {
				table.Columns = append(table.Columns, Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable, PrimaryKey: c.PrimaryKey})
			}
			schema.Tables = append(schema.Tables, table)
		}
		for _, o := range objects {
			details := o.Details
			schema.Objects = append(schema.Objects, DatabaseObject{Name: o.Name, SchemaName: optionalString(o.SchemaName), DisplayName: optionalString(o.DisplayName), Type: ObjectType(o.Type), Definition: &o.Definition, Details: &details})
		}
		writeJSON(w, 200, schema)
		return
	}
	rows, err := item.pool.QueryContext(ctx, "SELECT TABLE_NAME, COALESCE(TABLE_ROWS,0), COALESCE(DATA_LENGTH,0), COALESCE(INDEX_LENGTH,0) FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME", item.Database)
	if err != nil {
		writeError(w, 502, "metadata_failed", err.Error())
		return
	}
	defer rows.Close()
	schema := Schema{Tables: []TableInfo{}, Objects: []DatabaseObject{}, SupportedObjectTypes: []ObjectType{ObjectTypeTable, ObjectTypeView, ObjectTypeTrigger, ObjectTypeProcedure, ObjectTypeFunction, ObjectTypeEvent, ObjectTypeRole, ObjectTypeUser}}
	for rows.Next() {
		var table TableInfo
		if err := rows.Scan(&table.Name, &table.RowsEstimate, &table.DataBytes, &table.IndexBytes); err != nil {
			writeError(w, 502, "metadata_failed", err.Error())
			return
		}
		table.Columns = []Column{}
		schema.Tables = append(schema.Tables, table)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 502, "metadata_failed", err.Error())
		return
	}
	for i := range schema.Tables {
		columns, err := item.pool.QueryContext(ctx, "SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY FROM information_schema.columns WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION", item.Database, schema.Tables[i].Name)
		if err != nil {
			writeError(w, 502, "metadata_failed", err.Error())
			return
		}
		for columns.Next() {
			var c Column
			var nullable, key string
			if err := columns.Scan(&c.Name, &c.Type, &nullable, &key); err != nil {
				columns.Close()
				writeError(w, http.StatusBadGateway, "metadata_failed", err.Error())
				return
			}
			c.Nullable = nullable == "YES"
			c.PrimaryKey = key == "PRI"
			schema.Tables[i].Columns = append(schema.Tables[i].Columns, c)
		}
		if err := columns.Err(); err != nil {
			columns.Close()
			writeError(w, http.StatusBadGateway, "metadata_failed", err.Error())
			return
		}
		if err := columns.Close(); err != nil {
			writeError(w, http.StatusBadGateway, "metadata_failed", err.Error())
			return
		}
	}
	objects, warnings, err := driver.MySQLObjects(ctx, item.pool, item.Database)
	if err != nil {
		writeError(w, 502, "metadata_failed", err.Error())
		return
	}
	schema.Warnings = &warnings
	for _, object := range objects {
		definition := object.Definition
		details := object.Details
		schema.Objects = append(schema.Objects, DatabaseObject{Name: object.Name, Type: ObjectType(object.Type), Definition: &definition, Details: &details})
	}
	writeJSON(w, http.StatusOK, schema)
}

func (a *app) getObjectDefinition(w http.ResponseWriter, r *http.Request) {
	kind, name := r.URL.Query().Get("type"), r.URL.Query().Get("name")
	switch kind {
	case "view", "materialized_view", "trigger", "procedure", "function", "event", "schema", "sequence", "type", "extension", "index", "cron":
	default:
		writeError(w, http.StatusBadRequest, "invalid_object_type", "A schema object type is required")
		return
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_object_name", "An object name is required")
		return
	}
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, http.StatusNotFound, "not_found", "Connection not found")
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, http.StatusConflict, "disconnected", "Connection is closed")
		return
	}
	if item.Driver == ConnectionDriverSqlite && kind != "view" && kind != "trigger" && kind != "index" || item.Driver == ConnectionDriverPostgres && kind == "event" || item.Driver == ConnectionDriverMysql && kind != "view" && kind != "trigger" && kind != "procedure" && kind != "function" && kind != "event" {
		writeError(w, http.StatusBadRequest, "invalid_object_type", "Object type is not supported by this connection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var object driver.Object
	var err error
	if item.Driver == ConnectionDriverPostgres {
		object, err = driver.PostgresObjectDefinition(ctx, item.pool, kind, name)
	} else if item.Driver == ConnectionDriverSqlite {
		object, err = driver.SQLiteObjectDefinition(ctx, item.pool, kind, name)
	} else {
		object, err = driver.MySQLObjectDefinition(ctx, item.pool, item.Database, kind, name)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "metadata_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, DatabaseObject{Name: object.Name, Type: ObjectType(object.Type), Definition: &object.Definition, Details: &object.Details})
}

func (a *app) queryRows(w http.ResponseWriter, r *http.Request)     { a.queryObjectRows(w, r, false) }
func (a *app) queryViewRows(w http.ResponseWriter, r *http.Request) { a.queryObjectRows(w, r, true) }
func (a *app) queryObjectRows(w http.ResponseWriter, r *http.Request, view bool) {
	item := a.connection(r.PathValue("connId"))
	if item == nil {
		writeError(w, 404, "not_found", "Connection not found")
		return
	}
	item.mu.RLock()
	defer item.mu.RUnlock()
	if item.pool == nil {
		writeError(w, http.StatusConflict, "disconnected", "Connection is closed")
		return
	}
	var query RowsQuery
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&query); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, 400, "invalid_request", "Invalid row query")
		return
	}
	limit := 200
	if query.Limit != nil {
		limit = *query.Limit
	}
	offset := 0
	if query.Offset != nil {
		offset = *query.Offset
	}
	name := r.PathValue("table")
	kind := "table"
	if view {
		name = r.PathValue("view")
		kind = "view"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ordering := []driver.RowOrder{}
	if query.OrderBy != nil {
		for _, order := range *query.OrderBy {
			direction := ""
			if order.Direction != nil {
				direction = string(*order.Direction)
			}
			ordering = append(ordering, driver.RowOrder{Column: order.Column, Direction: direction})
		}
	}
	filters := []driver.RowFilter{}
	if query.Filters != nil {
		for _, filter := range *query.Filters {
			connector := ""
			if filter.Connector != nil {
				connector = string(*filter.Connector)
			}
			var value any
			if filter.Value != nil {
				value = *filter.Value
			}
			filters = append(filters, driver.RowFilter{Column: filter.Column, Op: string(filter.Op), Connector: connector, Value: value})
		}
	}
	var result driver.RowPage
	var err error
	if item.Driver == ConnectionDriverPostgres {
		result, err = driver.PostgresFilteredRows(ctx, item.pool, name, view, limit, offset, filters, ordering...)
	} else if item.Driver == ConnectionDriverSqlite {
		result, err = driver.SQLiteFilteredRows(ctx, item.pool, name, view, limit, offset, filters, ordering...)
	} else {
		result, err = driver.MySQLFilteredRows(ctx, item.pool, item.Database, name, view, limit, offset, filters, ordering...)
	}
	if errors.Is(err, driver.ErrInvalidFilter) {
		writeError(w, http.StatusBadRequest, "invalid_filter", "Invalid filter column, operator, connector or value")
		return
	}
	if errors.Is(err, driver.ErrInvalidOrder) {
		writeError(w, http.StatusBadRequest, "invalid_order", "Invalid sort column or direction")
		return
	}
	if errors.Is(err, driver.ErrRowsObjectNotFound) {
		writeError(w, 404, kind+"_not_found", "Requested "+kind+" not found")
		return
	}
	if err != nil {
		code := "query_failed"
		var stage *driver.RowError
		if errors.As(err, &stage) {
			code = stage.Stage
		}
		message := err.Error()
		if view {
			message = "Could not read view rows"
		} else if len(filters) > 0 {
			message = "Could not read filtered table rows"
		}
		writeError(w, 502, code, message)
		return
	}
	page := RowsPage{Columns: []Column{}, Rows: result.Rows, TotalRows: result.TotalRows, Editable: false}
	for _, c := range result.Columns {
		page.Columns = append(page.Columns, Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable, PrimaryKey: c.PrimaryKey})
	}
	reason := "Read-only connection"
	if !view && item.Driver != ConnectionDriverPostgres && !item.Readonly && !connectionReadonly(item.input.Readonly) {
		metadata, editErr := editMetadata(ctx, item, name)
		if editErr == nil {
			page.Editable = true
			page.EditKey = &metadata.Key
			for i := range page.Columns {
				generated := metadata.Columns[page.Columns[i].Name].Generated
				page.Columns[i].Generated = &generated
			}
			reason = ""
		} else if errors.Is(editErr, driver.ErrReadOnlyTable) {
			reason = "Editing requires a transactional table with a primary or non-null unique key"
		} else {
			reason = "Could not verify table editing metadata"
		}
	}
	if item.Driver == ConnectionDriverPostgres {
		reason = "PostgreSQL editing is not supported yet"
	}
	if view {
		reason = "Views are read-only"
	}
	page.ReadOnlyReason = ptrString(reason)
	writeJSON(w, 200, page)
}

func ptrString(value string) *string { return &value }
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (a *app) loadConfiguredConnections() {
	configPath := os.Getenv("NICHOSI_CONNECTIONS_FILE")
	if configPath == "" {
		return
	}
	file, err := os.Open(configPath)
	if err != nil {
		slog.Warn("could not open connections config", "path", configPath)
		return
	}
	defer file.Close()
	var config connectionsConfig
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&config); err != nil {
		slog.Warn("could not parse connections config", "path", configPath)
		return
	}
	for index, raw := range config.Connections {
		saved, problems := decodeConfiguredConnection(raw)
		engine, valid := configuredEngine(saved.Driver, saved.Engine)
		if !valid && (saved.Driver == "mysql" || saved.Driver == "postgres" || saved.Driver == "sqlite") {
			problems = append(problems, "engine: must be compatible with driver")
		}
		readonly := saved.Driver == "postgres" || connectionReadonly(saved.Readonly)
		input := ConnectionInput{Name: saved.Name, Driver: ConnectionInputDriver(saved.Driver), Host: saved.Host,
			Port: saved.Port, User: saved.User, Password: &saved.Password, Database: saved.Database, Readonly: &readonly}
		name := strings.TrimSpace(saved.Name)
		if name == "" {
			name = fmt.Sprintf("Connection #%d", index+1)
		}
		kind := ConnectionDriver(saved.Driver)
		if kind != ConnectionDriverMysql && kind != ConnectionDriverPostgres && kind != ConnectionDriverSqlite {
			kind = ConnectionDriverUnknown
		}
		item := &profile{Connection: Connection{Id: fmt.Sprintf("conn-%d", time.Now().UTC().UnixNano()),
			Name: name, Driver: kind, Host: saved.Host, Port: saved.Port, User: saved.User,
			Database: saved.Database, Readonly: readonly, Connected: false}, input: input}
		if valid {
			item.Engine = &engine
		}
		if len(problems) > 0 {
			item.ConfigurationErrors = &problems
		}
		a.connections = append(a.connections, item)
	}
}

// connectionReadonly defaults both omitted and null settings to read-only.
func connectionReadonly(value *bool) bool { return value == nil || *value }

func configuredEngine(driver, engine string) (ConnectionEngine, bool) {
	if engine == "" {
		engine = driver
	}
	valid := (driver == "mysql" && (engine == "mysql" || engine == "mariadb")) ||
		(driver == "postgres" && engine == "postgres") || (driver == "sqlite" && engine == "sqlite")
	return ConnectionEngine(engine), valid
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, Error{Code: code, Message: message})
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
