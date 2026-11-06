package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pum-purum/nichosi/internal/driver"
)

// openConnection selects a database-specific creator. Each creator owns cleanup
// until it can hand a successfully opened pool to the profile factory.
func (a *app) openConnection(ctx context.Context, input ConnectionInput) (*profile, error) {
	switch input.Driver {
	case Sqlite:
		return a.openSQLiteConnection(ctx, input)
	case Postgres:
		return a.openPostgresConnection(ctx, input)
	default:
		return a.openMySQLConnection(ctx, input)
	}
}

// newConnectedProfile keeps public connection metadata and reconnect settings in sync.
func newConnectedProfile(input ConnectionInput, pool *sql.DB, size int64, engine *ConnectionEngine) *profile {
	now := time.Now().UTC()
	readonly := connectionReadonly(input.Readonly)
	input.Readonly = &readonly
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = input.Database
		if input.Driver == Sqlite {
			name = filepath.Base(input.Database)
		}
	}
	return &profile{
		Connection: Connection{
			Id: fmt.Sprintf("conn-%d", now.UnixNano()), Name: name,
			Driver: ConnectionDriver(input.Driver), Engine: engine,
			Host: input.Host, Port: input.Port, User: input.User, Database: input.Database,
			Readonly: readonly, Connected: true, DatabaseSizeBytes: &size, LastConnectedAt: &now,
		},
		pool:  pool,
		input: input,
	}
}

func (a *app) openSQLiteConnection(ctx context.Context, input ConnectionInput) (*profile, error) {
	pool, size, err := driver.OpenSQLite(ctx, input.Database, connectionReadonly(input.Readonly))
	if err != nil {
		return nil, err
	}
	engine := ConnectionEngineSqlite
	return newConnectedProfile(input, pool, size, &engine), nil
}

func (a *app) openPostgresConnection(ctx context.Context, input ConnectionInput) (*profile, error) {
	pool, size, err := driver.OpenPostgres(ctx, input.Host, input.Port, input.User, *input.Password, input.Database)
	if err != nil {
		return nil, err
	}
	// Grid editing is not supported for PostgreSQL profiles yet.
	readonly := true
	input.Readonly = &readonly
	engine := ConnectionEnginePostgres
	return newConnectedProfile(input, pool, size, &engine), nil
}

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
	var engine *ConnectionEngine
	if product, err := driver.MySQLEngine(ctx, pool); err == nil {
		value := ConnectionEngine(product)
		engine = &value
	}
	return newConnectedProfile(input, pool, size, engine), nil
}
