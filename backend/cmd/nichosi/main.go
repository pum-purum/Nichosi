package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Pum-purum/nichosi/internal/driver"
	"github.com/go-sql-driver/mysql"
)

type profile struct {
	Connection
	mu    sync.RWMutex
	pool  *sql.DB
	input ConnectionInput
}

type connectionsConfig struct {
	Connections []connectionConfig `json:"connections"`
}

type connectionConfig struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Database string `json:"database"`
	Readonly *bool  `json:"readonly"`
	Password string `json:"password"`
}

type app struct {
	mu          sync.RWMutex
	connections []*profile
	token       string
	static      http.Handler
}

func main() {
	addr := env("NICHOSI_ADDR", "127.0.0.1:8080")
	token := os.Getenv("NICHOSI_ADMIN_TOKEN")
	localPreview := os.Getenv("NICHOSI_LOCAL_PREVIEW") == "true"
	if !isLoopback(addr) && len(token) < 24 && !localPreview {
		slog.Error("NICHOSI_ADMIN_TOKEN with at least 24 characters is required for non-loopback binding")
		os.Exit(1)
	}

	static, err := fs.Sub(webFiles, "static")
	if err != nil {
		slog.Error("frontend files unavailable", "error", err)
		os.Exit(1)
	}
	application := &app{token: token, static: http.FileServer(http.FS(static))}
	application.loadConfiguredConnections()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/connections", application.authorize(http.HandlerFunc(application.listConnections)))
	mux.Handle("POST /api/connections", application.authorize(http.HandlerFunc(application.createConnection)))
	mux.Handle("POST /api/connections/{connId}/connect", application.authorize(http.HandlerFunc(application.connectConnection)))
	mux.Handle("POST /api/connections/{connId}/disconnect", application.authorize(http.HandlerFunc(application.disconnectConnection)))
	mux.Handle("GET /api/connections/{connId}/schema", application.authorize(http.HandlerFunc(application.getSchema)))
	mux.Handle("GET /api/connections/{connId}/object-definition", application.authorize(http.HandlerFunc(application.getObjectDefinition)))
	mux.Handle("GET /api/connections/{connId}/tables/{table}/metadata", application.authorize(http.HandlerFunc(application.getTableMetadata)))
	mux.Handle("POST /api/connections/{connId}/views/{view}/rows", application.authorize(http.HandlerFunc(application.queryViewRows)))
	mux.Handle("GET /api/connections/{connId}/views/{view}/metadata", application.authorize(http.HandlerFunc(application.getViewMetadata)))
	mux.Handle("POST /api/connections/{connId}/tables/{table}/rows", application.authorize(http.HandlerFunc(application.queryRows)))
	mux.Handle("/", application.static)

	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("Nichosi listening", "address", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func (a *app) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token != "" {
			provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(a.token)) != 1 {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Admin token is required")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
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
	if input.Driver != ConnectionInputDriver("mysql") || input.Host == "" || input.Port < 1 || input.Port > 65535 || input.User == "" || input.Password == nil || *input.Password == "" || input.Database == "" {
		writeError(w, http.StatusBadRequest, "invalid_connection", "Provide valid MySQL connection details")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	item, err := a.openMySQLConnection(ctx, input)
	if err != nil {
		writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
		return
	}
	a.mu.Lock()
	a.connections = append(a.connections, item)
	a.mu.Unlock()
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
	if item.pool == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		opened, err := a.openMySQLConnection(ctx, item.input)
		if err != nil {
			writeError(w, http.StatusBadGateway, "connection_failed", err.Error())
			return
		}
		item.pool = opened.pool
		item.DatabaseSizeBytes = opened.DatabaseSizeBytes
		item.LastConnectedAt = opened.LastConnectedAt
		item.Readonly = opened.Readonly
		item.input.Readonly = opened.input.Readonly
	}
	item.Connected = true
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
	rows, err := item.pool.QueryContext(ctx, "SELECT TABLE_NAME, COALESCE(TABLE_ROWS,0), COALESCE(DATA_LENGTH,0), COALESCE(INDEX_LENGTH,0) FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME", item.Database)
	if err != nil {
		writeError(w, 502, "metadata_failed", err.Error())
		return
	}
	defer rows.Close()
	schema := Schema{Tables: []TableInfo{}, Objects: []DatabaseObject{}}
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
		schema.Objects = append(schema.Objects, DatabaseObject{Name: object.Name, Type: DatabaseObjectType(object.Type), Definition: &definition, Details: &details})
	}
	writeJSON(w, http.StatusOK, schema)
}

func (a *app) getObjectDefinition(w http.ResponseWriter, r *http.Request) {
	kind, name := r.URL.Query().Get("type"), r.URL.Query().Get("name")
	switch kind {
	case "view", "trigger", "procedure", "function", "event":
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	object, err := driver.MySQLObjectDefinition(ctx, item.pool, item.Database, kind, name)
	if err != nil {
		writeError(w, http.StatusBadGateway, "metadata_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, DatabaseObject{Name: object.Name, Type: DatabaseObjectType(object.Type), Definition: &object.Definition, Details: &object.Details})
}

func quoteMySQLIdent(name string) string { return "`" + strings.ReplaceAll(name, "`", "``") + "`" }

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
	if err := json.NewDecoder(r.Body).Decode(&query); err != nil && !errors.Is(err, io.EOF) {
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
	result, err := driver.MySQLRows(ctx, item.pool, item.Database, name, view, limit, offset, ordering...)
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
		}
		writeError(w, 502, code, message)
		return
	}
	page := RowsPage{Columns: []Column{}, Rows: result.Rows, TotalRows: result.TotalRows, Editable: false}
	for _, c := range result.Columns {
		page.Columns = append(page.Columns, Column{Name: c.Name, Type: c.Type, Nullable: c.Nullable, PrimaryKey: c.PrimaryKey})
	}
	reason := "Editing is not available yet"
	if view {
		reason = "Views are read-only"
	}
	page.ReadOnlyReason = ptrString(reason)
	writeJSON(w, 200, page)
}

func ptrString(value string) *string { return &value }

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
	for _, saved := range config.Connections {
		if saved.Driver != "mysql" || saved.Name == "" || saved.Host == "" || saved.Port < 1 || saved.Port > 65535 || saved.User == "" || saved.Database == "" || saved.Password == "" {
			slog.Warn("skipping invalid connection profile", "name", saved.Name)
			continue
		}
		readonly := connectionReadonly(saved.Readonly)
		input := ConnectionInput{
			Name: saved.Name, Driver: ConnectionInputDriver(saved.Driver), Host: saved.Host,
			Port: saved.Port, User: saved.User, Password: &saved.Password,
			Database: saved.Database, Readonly: &readonly,
		}
		name := strings.TrimSpace(saved.Name)
		if name == "" {
			name = saved.Database
		}
		a.connections = append(a.connections, &profile{
			Connection: Connection{
				Id:        fmt.Sprintf("conn-%d", time.Now().UTC().UnixNano()),
				Name:      name,
				Driver:    ConnectionDriver("mysql"),
				Host:      saved.Host,
				Port:      saved.Port,
				User:      saved.User,
				Database:  saved.Database,
				Readonly:  readonly,
				Connected: false,
			},
			input: input,
		})
	}
}

// connectionReadonly defaults both omitted and null settings to read-only.
func connectionReadonly(value *bool) bool { return value == nil || *value }

func (a *app) openMySQLConnection(ctx context.Context, input ConnectionInput) (*profile, error) {
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd = input.User, *input.Password
	cfg.Net, cfg.Addr, cfg.DBName = "tcp", net.JoinHostPort(input.Host, strconv.Itoa(input.Port)), input.Database
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	cfg.ParseTime = true
	pool, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("configure MySQL connection: %w", err)
	}
	pool.SetMaxOpenConns(5)
	pool.SetMaxIdleConns(2)
	if err := pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping MySQL: %w", err)
	}
	var size int64
	if err := pool.QueryRowContext(ctx, "SELECT COALESCE(SUM(data_length + index_length), 0) FROM information_schema.tables WHERE table_schema = ?", input.Database).Scan(&size); err != nil {
		pool.Close()
		return nil, fmt.Errorf("read MySQL database size: %w", err)
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = input.Database
	}
	readonly := connectionReadonly(input.Readonly)
	input.Readonly = &readonly
	return &profile{Connection: Connection{Id: fmt.Sprintf("conn-%d", now.UnixNano()), Name: name, Driver: ConnectionDriver("mysql"), Host: input.Host, Port: input.Port, User: input.User, Database: input.Database, Readonly: readonly, Connected: true, DatabaseSizeBytes: &size, LastConnectedAt: &now}, pool: pool, input: input}, nil
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
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}
