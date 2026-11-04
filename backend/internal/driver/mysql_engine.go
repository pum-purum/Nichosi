package driver

import (
	"context"
	"database/sql"
	"strings"
)

// MySQLEngine identifies the product behind a MySQL-compatible connection.
func MySQLEngine(ctx context.Context, pool *sql.DB) (string, error) {
	var version string
	if err := pool.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return "", err
	}
	return mysqlEngineFromVersion(version), nil
}

func mysqlEngineFromVersion(version string) string {
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return "mariadb"
	}
	return "mysql"
}
