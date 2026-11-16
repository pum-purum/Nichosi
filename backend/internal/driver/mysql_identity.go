package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

// mysqlNextAutoIncrement bypasses MySQL 8's persisted statistics cache.
// All session changes and the read must use the same physical connection.
func mysqlNextAutoIncrement(ctx context.Context, db *sql.DB, database, table string) (*string, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var expiry uint64
	err = conn.QueryRowContext(ctx, "SELECT @@SESSION.information_schema_stats_expiry").Scan(&expiry)
	var serverError *mysql.MySQLError
	if err != nil && !(errors.As(err, &serverError) && serverError.Number == 1193) {
		return nil, err
	}
	// MariaDB and older MySQL do not have this cache or session variable.
	if err == nil && expiry != 0 {
		// Even a canceled SET may have reached the server, so always restore.
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, restoreErr := conn.ExecContext(cleanup, "SET SESSION information_schema_stats_expiry = ?", expiry); restoreErr != nil {
				// Never return a connection with unknown session state to the pool.
				_ = conn.Raw(func(any) error { return sqldriver.ErrBadConn })
			}
		}()
		if _, err = conn.ExecContext(ctx, "SET SESSION information_schema_stats_expiry = ?", 0); err != nil {
			return nil, err
		}
	}
	var value *string
	err = conn.QueryRowContext(ctx, "SELECT CAST(AUTO_INCREMENT AS CHAR) FROM information_schema.tables WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE' LIMIT 1", database, table).Scan(&value)
	return value, err
}
