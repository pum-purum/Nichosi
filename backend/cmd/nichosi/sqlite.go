package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/pum-purum/nichosi/internal/driver"
)

func validConnectionInput(input ConnectionInput) bool {
	if input.Driver == Sqlite {
		return filepath.IsAbs(input.Database) && !strings.ContainsRune(input.Database, 0) && input.Host == "" && input.Port == 0 && input.User == "" && (input.Password == nil || *input.Password == "")
	}
	return (input.Driver == Mysql || input.Driver == Postgres) && input.Host != "" && input.Port > 0 && input.Port <= 65535 && input.User != "" && input.Password != nil && *input.Password != "" && input.Database != ""
}
func editMetadata(ctx context.Context, item *profile, table string) (driver.EditMetadata, error) {
	if item.Driver == ConnectionDriverSqlite {
		return driver.SQLiteEditMetadata(ctx, item.pool, table)
	}
	return driver.MySQLEditMetadata(ctx, item.pool, item.Database, table)
}
