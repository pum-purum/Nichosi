package main

import (
	"context"
	"fmt"
	"github.com/pum-purum/nichosi/internal/driver"
	"path/filepath"
	"strings"
	"time"
)

func validConnectionInput(input ConnectionInput) bool {
	if input.Driver == Sqlite {
		return filepath.IsAbs(input.Database) && !strings.ContainsRune(input.Database, 0) && input.Host == "" && input.Port == 0 && input.User == "" && (input.Password == nil || *input.Password == "")
	}
	return (input.Driver == Mysql || input.Driver == Postgres) && input.Host != "" && input.Port > 0 && input.Port <= 65535 && input.User != "" && input.Password != nil && *input.Password != "" && input.Database != ""
}
func (a *app) openSQLiteConnection(ctx context.Context, input ConnectionInput) (*profile, error) {
	readonly := connectionReadonly(input.Readonly)
	pool, size, err := driver.OpenSQLite(ctx, input.Database, readonly)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = filepath.Base(input.Database)
	}
	input.Readonly = &readonly
	engine := ConnectionEngineSqlite
	return &profile{Connection: Connection{Id: fmt.Sprintf("conn-%d", now.UnixNano()), Name: name, Driver: ConnectionDriverSqlite, Engine: &engine, Database: input.Database, Readonly: readonly, Connected: true, DatabaseSizeBytes: &size, LastConnectedAt: &now}, pool: pool, input: input}, nil
}
func editMetadata(ctx context.Context, item *profile, table string) (driver.EditMetadata, error) {
	if item.Driver == ConnectionDriverSqlite {
		return driver.SQLiteEditMetadata(ctx, item.pool, table)
	}
	return driver.MySQLEditMetadata(ctx, item.pool, item.Database, table)
}
