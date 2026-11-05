package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ScriptStatement retains exact user-authored SQL and an unquoted token stream for validation.
type ScriptStatement struct {
	SQL   string
	Words []string
}
type ScriptOutput struct {
	Statement    int
	Columns      []string
	Rows         [][]any
	Truncated    bool
	RowsAffected int64
}

// ScriptStatementError locates a failure without exposing SQL or database diagnostics.
type ScriptStatementError struct {
	Statement int
	Err       error
}

func (e *ScriptStatementError) Error() string { return fmt.Sprintf("statement %d failed", e.Statement) }
func (e *ScriptStatementError) Unwrap() error { return e.Err }

var ErrScript = errors.New("unsupported SQL script")
var ErrScriptOutcomeUnknown = errors.New("transaction outcome unknown")

// SplitScript handles SQL literals, identifiers, comments and PostgreSQL dollar quoting.
// MySQL DELIMITER directives are consumed locally, never sent to the database.
func SplitScript(source, dialect string) ([]ScriptStatement, error) {
	if len(source) == 0 || len(source) > 262144 {
		return nil, fmt.Errorf("%w: SQL must contain 1–262144 bytes", ErrScript)
	}
	result := []ScriptStatement{}
	words := []string{}
	start := 0
	delimiter := ";"
	blockStack := []string{}
	trigger := false
	flush := func(end int) {
		if len(words) > 0 {
			result = append(result, ScriptStatement{strings.TrimSpace(source[start:end]), words})
			words = []string{}
		}
		start = end + len(delimiter)
		trigger = false
		blockStack = nil
	}
	for i := 0; i < len(source); {
		c := source[i]
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}
		if dialect == "mysql" && len(words) == 0 && (i == 0 || strings.TrimSpace(source[strings.LastIndexAny(source[:i], "\r\n")+1:i]) == "") {
			end := strings.IndexAny(source[i:], "\r\n")
			if end < 0 {
				end = len(source) - i
			}
			fields := strings.Fields(source[i : i+end])
			if len(fields) > 0 && strings.EqualFold(fields[0], "DELIMITER") {
				if len(fields) != 2 || len(fields[1]) > 16 || strings.ContainsAny(fields[1], "'\"`\\") {
					return nil, fmt.Errorf("%w: invalid DELIMITER directive", ErrScript)
				}
				delimiter = fields[1]
				i += end
				if i < len(source) {
					i++
				}
				start = i
				continue
			}
		}
		if strings.HasPrefix(source[i:], delimiter) && len(blockStack) == 0 {
			flush(i)
			i += len(delimiter)
			continue
		}
		if c == '-' && i+1 < len(source) && source[i+1] == '-' && (dialect != "mysql" || i+2 == len(source) || unicode.IsSpace(rune(source[i+2]))) || c == '#' && dialect == "mysql" {
			for i < len(source) && source[i] != '\r' && source[i] != '\n' {
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(source) && source[i+1] == '*' {
			if i+2 < len(source) && (source[i+2] == '!' || source[i+2] == '+' || i+3 < len(source) && (source[i+2] == 'M' || source[i+2] == 'm') && source[i+3] == '!') {
				return nil, fmt.Errorf("%w: executable comments and optimizer hints are not supported", ErrScript)
			}
			depth := 1
			i += 2
			for i < len(source) && depth > 0 {
				if i+1 < len(source) && source[i:i+2] == "/*" {
					if dialect != "postgres" {
						return nil, fmt.Errorf("%w: nested comments are unsupported", ErrScript)
					}
					depth++
					i += 2
				} else if i+1 < len(source) && source[i:i+2] == "*/" {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				return nil, fmt.Errorf("%w: unfinished comment", ErrScript)
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' && dialect == "sqlite" {
			quote := c
			if c == '[' {
				quote = ']'
			}
			i++
			closed := false
			for i < len(source) {
				if source[i] == '\\' && dialect != "sqlite" {
					return nil, fmt.Errorf("%w: use doubled quotes instead of backslash escapes", ErrScript)
				}
				if source[i] == quote {
					i++
					if i < len(source) && source[i] == quote {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unfinished quoted value", ErrScript)
			}
			words = append(words, "<QUOTED>")
			continue
		}
		if c == '$' && dialect == "postgres" {
			j := i + 1
			for j < len(source) && (source[j] == '_' || source[j] >= 'a' && source[j] <= 'z' || source[j] >= 'A' && source[j] <= 'Z' || j > i+1 && source[j] >= '0' && source[j] <= '9') {
				j++
			}
			if j < len(source) && source[j] == '$' {
				tag := source[i : j+1]
				end := strings.Index(source[j+1:], tag)
				if end < 0 {
					return nil, fmt.Errorf("%w: unfinished dollar quote", ErrScript)
				}
				i = j + 1 + end + len(tag)
				words = append(words, "<QUOTED>")
				continue
			}
		}
		if c == '\\' {
			return nil, fmt.Errorf("%w: client commands and escape strings are unsupported", ErrScript)
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			j := i + 1
			for j < len(source) && (source[j] == '_' || source[j] == '$' || source[j] >= 'a' && source[j] <= 'z' || source[j] >= 'A' && source[j] <= 'Z' || source[j] >= '0' && source[j] <= '9') {
				if delimiter != ";" && strings.HasPrefix(source[j:], delimiter) {
					break
				}
				j++
			}
			word := strings.ToUpper(source[i:j])
			words = append(words, word)
			if dialect == "sqlite" && len(words) > 1 && words[0] == "CREATE" {
				if word == "TRIGGER" {
					trigger = true
				}
				if trigger {
					if word == "BEGIN" || word == "CASE" {
						blockStack = append(blockStack, word)
					}
					if word == "END" && len(blockStack) > 0 {
						blockStack = blockStack[:len(blockStack)-1]
					}
				}
			}
			i = j
			continue
		}
		words = append(words, string(c))
		i++
	}
	flush(len(source))
	if len(result) == 0 || len(result) > 100 {
		return nil, fmt.Errorf("%w: provide between 1 and 100 statements", ErrScript)
	}
	return result, nil
}

// ValidateScriptMode reserves unrestricted execution for explicitly writable connections.
func ValidateScriptMode(statements []ScriptStatement, dialect string, readonly, atomic bool) error {
	if !atomic {
		if readonly {
			return fmt.Errorf("%w: non-atomic execution requires a writable connection", ErrScript)
		}
		return nil
	}
	return ValidateScript(statements, dialect, readonly)
}

func ValidateScript(statements []ScriptStatement, dialect string, readonly bool) error {
	for _, s := range statements {
		first := s.Words[0]
		allowed := " SELECT WITH VALUES EXPLAIN "
		if !readonly && dialect != "mysql" {
			allowed += " INSERT UPDATE DELETE CREATE ALTER DROP REPLACE "
		}
		if !strings.Contains(allowed, " "+first+" ") {
			return fmt.Errorf("%w: %s is unavailable in this transactional console", ErrScript, first)
		}
		for _, word := range s.Words {
			switch word {
			case "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "BEGIN", "START", "TRANSACTION", "ATTACH", "DETACH", "PRAGMA", "VACUUM", "COPY", "CALL", "DO", "DELIMITER", "OUTFILE", "DUMPFILE", "LOAD_FILE", "INTO", "LOCK", "UNLOCK", "SET", "RESET", "USE", "DATABASE", "TABLESPACE", "CONCURRENTLY", "TEMP", "TEMPORARY", "UNLOGGED", "SYSTEM", "EXTENSION", "SERVER", "FOREIGN", "FUNCTION", "PROCEDURE", "TRIGGER", "EVENT", "ROLE", "USER", "POLICY", "RULE":
				// INTO and SET are needed for ordinary transactional DML.
				if (word == "INTO" || word == "SET") && !readonly && dialect != "mysql" && first != "SELECT" && first != "EXPLAIN" {
					continue
				}
				return fmt.Errorf("%w: %s is unavailable in this transactional console", ErrScript, word)
			}
		}
		if dialect == "mysql" && first != "SELECT" && first != "WITH" && first != "EXPLAIN" {
			return fmt.Errorf("%w: MySQL console currently supports read-only queries", ErrScript)
		}
	}
	return nil
}

// ExecuteScript uses an isolated connection; no script session state returns to the pool.
// MySQL is always read-only: arbitrary DDL and nontransactional engines cannot roll back.
func ExecuteScript(ctx context.Context, pool *sql.DB, dialect string, readonly bool, statements []ScriptStatement, limit int) ([]ScriptOutput, error) {
	return ExecuteScriptMode(ctx, pool, dialect, readonly, statements, limit, true)
}

type scriptRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func ExecuteScriptMode(ctx context.Context, pool *sql.DB, dialect string, readonly bool, statements []ScriptStatement, limit int, atomic bool) (outputs []ScriptOutput, executionErr error) {
	if err := ValidateScriptMode(statements, dialect, readonly, atomic); err != nil {
		return outputs, err
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: invalid row limit", ErrScript)
	}
	conn, err := pool.Conn(ctx)
	if err != nil {
		return outputs, err
	}
	// Discard the isolated session even after user-authored transaction/session commands.
	defer func() { _ = conn.Raw(func(any) error { return sqldriver.ErrBadConn }); _ = conn.Close() }()
	if dialect == "sqlite" {
		if _, err = sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, 4<<20); err != nil {
			return outputs, err
		}
		if readonly {
			if _, err = conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
				return outputs, err
			}
		}
	}
	var runner scriptRunner = conn
	var tx *sql.Tx
	if atomic {
		tx, err = conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readonly || dialect == "mysql"})
		if err != nil {
			return outputs, err
		}
		runner = tx
		defer func() {
			rollbackErr := tx.Rollback()
			if executionErr != nil && rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				executionErr = fmt.Errorf("%w: %v", ErrScriptOutcomeUnknown, executionErr)
			}
		}()
	}
	// Atomic failure discards outputs because their accompanying writes have rolled back.
	// Non-atomic failure retains outputs from completed statements.
	defer func() {
		if executionErr != nil && atomic {
			outputs = nil
		}
	}()

	currentStatement := 0
	defer func() {
		if executionErr != nil && currentStatement > 0 && !errors.Is(executionErr, ErrScriptOutcomeUnknown) {
			executionErr = &ScriptStatementError{Statement: currentStatement, Err: executionErr}
		}
	}()
	outputs = []ScriptOutput{}
	bytesLeft := 4 << 20
	rowsLeft := 1000
	for statementIndex, statement := range statements {
		currentStatement = statementIndex + 1
		if len(outputs) >= 1000 {
			return outputs, errors.New("too many result sets")
		}
		output := ScriptOutput{Statement: statementIndex + 1, Columns: []string{}, Rows: [][]any{}}
		first := statement.Words[0]
		query := first == "SELECT" || first == "VALUES" || first == "EXPLAIN" || first == "SHOW" || first == "DESC" || first == "DESCRIBE" || first == "TABLE" || first == "PRAGMA" || first == "CALL" || first == "FETCH" || first == "EXECUTE"
		commandFound := first != "WITH"
		depth := 0
		for _, word := range statement.Words {
			if word == "(" {
				depth++
				continue
			}
			if word == ")" {
				depth--
				continue
			}
			if depth == 0 && !commandFound && (word == "SELECT" || word == "INSERT" || word == "UPDATE" || word == "DELETE" || word == "VALUES" || word == "TABLE") {
				query = word == "SELECT" || word == "VALUES" || word == "TABLE"
				commandFound = true
			}
			if depth == 0 && word == "RETURNING" {
				query = true
			}
		}
		if query {
			rows, e := runner.QueryContext(ctx, statement.SQL)
			if e != nil {
				return outputs, e
			}
			for {
				output = ScriptOutput{Statement: statementIndex + 1, Columns: []string{}, Rows: [][]any{}}
				output.Columns, e = rows.Columns()
				if e != nil {
					rows.Close()
					return outputs, e
				}
				if len(output.Columns) > 1000 {
					rows.Close()
					return outputs, errors.New("result has too many columns")
				}
				columnTypes, e := rows.ColumnTypes()
				if e != nil {
					rows.Close()
					return outputs, e
				}
				encodedColumns, e := json.Marshal(output.Columns)
				if e != nil {
					rows.Close()
					return outputs, e
				}
				bytesLeft -= len(encodedColumns)
				if bytesLeft < 0 {
					rows.Close()
					return outputs, errors.New("result exceeds 4 MiB")
				}
				for rows.Next() {
					if len(output.Rows) >= limit || rowsLeft <= 0 {
						output.Truncated = true
						break
					}
					values := make([]any, len(output.Columns))
					pointers := make([]any, len(values))
					for i := range values {
						pointers[i] = &values[i]
					}
					if e = rows.Scan(pointers...); e != nil {
						rows.Close()
						return outputs, e
					}
					for i, v := range values {
						switch v := v.(type) {
						case []byte:
							kind := strings.ToUpper(columnTypes[i].DatabaseTypeName())
							if dialect == "sqlite" || kind == "BYTEA" || strings.Contains(kind, "BLOB") || kind == "BINARY" || kind == "VARBINARY" || kind == "BIT" || kind == "GEOMETRY" || !utf8.Valid(v) {
								values[i] = map[string]any{"type": "blob", "hex": hex.EncodeToString(v)}
							} else {
								values[i] = string(v)
							}
						case float64:
							if math.IsNaN(v) || math.IsInf(v, 0) {
								values[i] = fmt.Sprint(v)
							}
						case int64:
							values[i] = fmt.Sprint(v)
						}
					}
					encoded, encodeErr := json.Marshal(values)
					if encodeErr != nil {
						rows.Close()
						return outputs, encodeErr
					}
					bytesLeft -= len(encoded) + 1
					if bytesLeft < 0 {
						rows.Close()
						return outputs, errors.New("result exceeds 4 MiB; reduce the query result")
					}
					output.Rows = append(output.Rows, values)
					rowsLeft--
				}
				if e = rows.Err(); e != nil {
					rows.Close()
					return outputs, e
				}
				outputs = append(outputs, output)
				if len(outputs) > 1000 {
					rows.Close()
					return outputs[:1000], errors.New("too many result sets")
				}
				if !rows.NextResultSet() {
					break
				}
			}
			e = rows.Err()
			closeErr := rows.Close()
			if e != nil {
				return outputs, e
			}
			if closeErr != nil {
				return outputs, closeErr
			}
			continue
		} else {
			res, e := runner.ExecContext(ctx, statement.SQL)
			if e != nil {
				return outputs, e
			}
			output.RowsAffected, _ = res.RowsAffected()
		}
		outputs = append(outputs, output)
	}
	currentStatement = 0
	if atomic {
		if err = tx.Commit(); err != nil {
			return outputs, fmt.Errorf("%w: %v", ErrScriptOutcomeUnknown, err)
		}
	}
	return outputs, nil
}

// ScriptErrorMessage preserves database diagnostics; the HTTP boundary redacts connection secrets.
func ScriptErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrScriptOutcomeUnknown) {
		return "Could not confirm the transaction outcome. Changes may have committed; check database state before repeating the script."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Execution timed out."
	}
	if errors.Is(err, context.Canceled) {
		return "Execution cancelled."
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) {
		return mysqlError.Error()
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		return postgresError.Error()
	}
	var sqliteError *sqlite.Error
	if errors.As(err, &sqliteError) {
		return sqliteError.Error()
	}
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err.Error()
}
