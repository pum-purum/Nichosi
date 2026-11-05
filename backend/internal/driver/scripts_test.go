package driver

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSplitScript(t *testing.T) {
	for _, tc := range []struct {
		dialect, sql string
		count        int
		bad          bool
	}{
		{"sqlite", `SELECT ';', "a;b", [c;d]; -- semi;\n`, 1, false},
		{"mysql", "SELECT 'a'';b'; # ;\nSELECT 2;", 2, false},
		{"postgres", `SELECT $$a;b$$; SELECT $tag$';'$tag$;`, 2, false},
		{"postgres", "SELECT 1 -- comment\r; COMMIT; SELECT 2;", 3, false},
		{"postgres", `/* nested /* ; */ */ SELECT 1;`, 1, false},
		{"mysql", `SELECT 1; /*! COMMIT */`, 0, true},
		{"mysql", `SELECT 1 /*M! INTO OUTFILE '/tmp/x' */`, 0, true},
		{"sqlite", `SELECT 'unfinished`, 0, true},
		{"sqlite", `/*unfinished`, 0, true},
		{"sqlite", `-- only a comment`, 0, true},
		{"postgres", `SELECT E'\'; COMMIT; --'`, 0, true},
		{"sqlite", strings.Repeat("SELECT 1;", 101), 0, true},
	} {
		t.Run(tc.dialect+tc.sql, func(t *testing.T) {
			s, err := SplitScript(tc.sql, tc.dialect)
			if (err != nil) != tc.bad || !tc.bad && len(s) != tc.count {
				t.Fatalf("%#v %v", s, err)
			}
		})
	}
}
func TestValidateScript(t *testing.T) {
	for _, tc := range []struct {
		dialect, sql string
		ro, bad      bool
	}{
		{"sqlite", "SELECT 1", true, false},
		{"sqlite", "DELETE FROM parent", true, true},
		{"sqlite", "WITH x AS (SELECT 1) DELETE FROM parent", true, false}, // Database read-only enforcement is authoritative.
		{"sqlite", "PRAGMA query_only=OFF", true, true},
		{"sqlite", "ATTACH 'x' AS y", false, true},
		{"postgres", "COMMIT", false, true},
		{"postgres", "CREATE DATABASE x", false, true},
		{"postgres", "CREATE TABLE x(id integer)", false, false},
		{"mysql", "DELETE FROM x", false, true},
		{"mysql", "SELECT * FROM x INTO OUTFILE 'x'", false, true},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			s, err := SplitScript(tc.sql, tc.dialect)
			if err == nil {
				err = ValidateScript(s, tc.dialect, tc.ro)
			}
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
		})
	}
}
func TestSQLiteScriptTransaction(t *testing.T) {
	db, _ := sqliteFixture(t)
	run := func(source string, ro bool, limit int) ([]ScriptOutput, error) {
		s, e := SplitScript(source, "sqlite")
		if e != nil {
			return nil, e
		}
		if e = ValidateScript(s, "sqlite", ro); e != nil {
			return nil, e
		}
		return ExecuteScript(context.Background(), db, "sqlite", ro, s, limit)
	}
	out, err := run("SELECT name FROM entries ORDER BY id; SELECT 9007199254740993;", false, 1)
	if err != nil || len(out) != 2 || !out[0].Truncated || out[1].Rows[0][0] != "9007199254740993" {
		t.Fatalf("%#v %v", out, err)
	}
	_, err = run("UPDATE parent SET name='changed' WHERE id=1; INSERT INTO missing VALUES(1);", false, 200)
	if err == nil {
		t.Fatal("expected failure")
	}
	var name string
	db.QueryRow("SELECT name FROM parent WHERE id=1").Scan(&name)
	if name != "root" {
		t.Fatal("failed script was not rolled back")
	}
	_, err = run("WITH x AS (SELECT 1) DELETE FROM parent;", true, 200)
	if err == nil {
		t.Fatal("read-only CTE write succeeded")
	}
	_, err = run("UPDATE parent SET name='committed' WHERE id=1;", false, 200)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run("CREATE TABLE script_created(id INTEGER); INSERT INTO missing VALUES(1);", false, 200)
	if err == nil {
		t.Fatal("expected DDL rollback")
	}
	var count int
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='script_created'").Scan(&count)
	if count != 0 {
		t.Fatal("DDL not rolled back")
	}
	out, err = run("SELECT 1e999, X'00ff', 9223372036854775807", false, 200)
	if err != nil || out[0].Rows[0][0] != "+Inf" || out[0].Rows[0][1].(map[string]any)["hex"] != "00ff" || out[0].Rows[0][2] != "9223372036854775807" {
		t.Fatalf("special values: %#v %v", out, err)
	}
	out, err = run("WITH x AS (SELECT 1) UPDATE parent SET name='cte'; INSERT INTO no_key SELECT 'test';", false, 200)
	if err != nil || out[0].RowsAffected != 1 || out[1].RowsAffected != 1 {
		t.Fatalf("CTE counts: %#v %v", out, err)
	}
	_, err = run("SELECT replace(hex(zeroblob(400000)), '0', char(0))", false, 200)
	if err == nil {
		t.Fatal("JSON escaping byte budget not enforced")
	}
	_, err = run("SELECT randomblob(5000000)", false, 200)
	if err == nil {
		t.Fatal("byte budget not enforced")
	}
}

func init() { sql.Register("script-commit-failure", commitFailureDriver{}) }
func TestScriptCommitUnknown(t *testing.T) {
	db, err := sql.Open("script-commit-failure", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = ExecuteScript(context.Background(), db, "postgres", false, nil, 1)
	if !errors.Is(err, ErrScriptOutcomeUnknown) || !strings.Contains(ScriptErrorMessage(err), "may have committed") {
		t.Fatalf("commit failure must be unknown: %v", err)
	}
}

type commitFailureDriver struct{}

func (commitFailureDriver) Open(string) (sqldriver.Conn, error) { return commitFailureConn{}, nil }

type commitFailureConn struct{}

func (commitFailureConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unused") }
func (commitFailureConn) Close() error                           { return nil }
func (commitFailureConn) Begin() (sqldriver.Tx, error)           { return commitFailureTx{}, nil }

type commitFailureTx struct{}

func (commitFailureTx) Commit() error   { return errors.New("connection lost during commit") }
func (commitFailureTx) Rollback() error { return nil }

func TestScriptClientDelimitersAndTriggers(t *testing.T) {
	for _, tc := range []struct {
		dialect, source string
		count           int
	}{
		{"mysql", "  DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT ';'; SELECT 2; END$$\n DELIMITER ;\nCALL p();", 2},
		{"mysql", "DELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; END//\nDELIMITER ;\nSELECT 2;", 2},
		{"mysql", "  DELIMITER $$\rCREATE PROCEDURE p() BEGIN SELECT 1; END$$\rDELIMITER ;\rSELECT 2;", 2},
		{"sqlite", "CREATE TRIGGER tr AFTER INSERT ON parent BEGIN INSERT INTO no_key VALUES(CASE WHEN new.id=1 THEN ';' ELSE 'x' END); INSERT INTO no_key VALUES('y'); END; SELECT 1;", 2},
		{"postgres", "CREATE FUNCTION p() RETURNS void LANGUAGE plpgsql AS $$ BEGIN PERFORM 1; END; $$; SELECT 1;", 2},
	} {
		s, err := SplitScript(tc.source, tc.dialect)
		if err != nil || len(s) != tc.count {
			t.Fatalf("%s: %#v %v", tc.dialect, s, err)
		}
		if err = ValidateScriptMode(s, tc.dialect, false, false); err != nil {
			t.Fatal(err)
		}
		if err = ValidateScriptMode(s, tc.dialect, true, false); err == nil {
			t.Fatal("read-only unrestricted mode accepted")
		}
	}
	db, _ := sqliteFixture(t)
	source := "CREATE TRIGGER script_tr AFTER INSERT ON parent BEGIN INSERT INTO no_key VALUES(CASE WHEN new.id=2 THEN 'ok' ELSE 'bad' END); INSERT INTO no_key VALUES('second'); END; INSERT INTO parent(id,name) VALUES(2,'trigger'); SELECT * FROM no_key;"
	statements, err := SplitScript(source, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	out, err := ExecuteScriptMode(context.Background(), db, "sqlite", false, statements, 200, false)
	if err != nil || len(out) != 3 || len(out[2].Rows) != 2 {
		t.Fatalf("trigger execution %#v %v", out, err)
	}
}

func TestMySQLScriptModes(t *testing.T) {
	dsn := os.Getenv("NICHOSI_TEST_DSN")
	if dsn == "" {
		t.Skip("local MySQL DSN is not configured")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("configure local MySQL")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Skip("local MySQL unavailable")
	}
	table := QuoteIdent(fmt.Sprintf("nichosi_script_%d", time.Now().UnixNano()))
	proc := QuoteIdent(fmt.Sprintf("nichosi_proc_%d", time.Now().UnixNano()))
	defer db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	defer db.ExecContext(context.Background(), "DROP PROCEDURE IF EXISTS "+proc)
	run := func(source string, ro, atomic bool) ([]ScriptOutput, error) {
		s, e := SplitScript(source, "mysql")
		if e != nil {
			return nil, e
		}
		return ExecuteScriptMode(ctx, db, "mysql", ro, s, 200, atomic)
	}
	out, err := run("CREATE TABLE "+table+"(id INT PRIMARY KEY,name VARCHAR(30)) ENGINE=InnoDB; INSERT INTO "+table+" VALUES(1,'first'); ALTER TABLE "+table+" ADD flag INT DEFAULT 0; UPDATE "+table+" SET flag=1; SELECT * FROM "+table+";", false, false)
	if err != nil || len(out) != 5 || out[1].RowsAffected != 1 || out[3].RowsAffected != 1 || len(out[4].Rows) != 1 {
		t.Fatalf("DDL and DML: %#v %v", out, err)
	}
	out, err = run("INSERT INTO "+table+"(id,name) VALUES(2,'saved'); SELECT * FROM nichosi_missing_script_table;", false, false)
	if err == nil || len(out) != 1 {
		t.Fatal("partial error must retain completed results")
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 2 {
		t.Fatal("partial changes were not saved")
	}
	if _, err = run("DELETE FROM "+table, false, true); err == nil {
		t.Fatal("atomic MySQL write accepted")
	}
	if _, err = run("SELECT * FROM "+table, true, false); err == nil {
		t.Fatal("read-only partial mode accepted")
	}
	out, err = run("SELECT * FROM "+table, true, true)
	if err != nil || len(out[0].Rows) != 2 {
		t.Fatal("read-only MySQL execution failed")
	}
	source := "DELIMITER $$\nCREATE PROCEDURE " + proc + "() BEGIN SELECT 1 AS a; SELECT 2 AS b; END$$\nDELIMITER ;\nCALL " + proc + "();"
	out, err = run(source, false, false)
	if err != nil || len(out) < 3 || out[1].Statement != 2 || out[2].Statement != 2 {
		t.Fatalf("routine result sets: %#v %v", out, err)
	}
	out, err = run("SET @nichosi_script_value = 42; SELECT @nichosi_script_value;", false, false)
	if err != nil || out[1].Rows[0][0] != "42" {
		t.Fatalf("session state: %#v %v", out, err)
	}
	out, err = run("SELECT @nichosi_script_value", false, false)
	if err != nil || out[0].Rows[0][0] != nil {
		t.Fatal("script session state leaked")
	}
}

func TestPostgresScriptModes(t *testing.T) {
	dsn := os.Getenv("NICHOSI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("local PostgreSQL DSN is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("configure local PostgreSQL")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		t.Skip("local PostgreSQL unavailable")
	}
	run := func(source string, ro, atomic bool) ([]ScriptOutput, error) {
		s, e := SplitScript(source, "postgres")
		if e != nil {
			return nil, e
		}
		return ExecuteScriptMode(ctx, db, "postgres", ro, s, 200, atomic)
	}
	out, err := run("CREATE TABLE script_rows(id bigint PRIMARY KEY); INSERT INTO script_rows VALUES(9007199254740993); SELECT * FROM script_rows;", false, true)
	if err != nil || len(out) != 3 || out[2].Rows[0][0] != "9007199254740993" {
		t.Fatalf("transactional DDL/DML: %#v %v", out, err)
	}
	out, err = run("INSERT INTO script_rows VALUES(2); SELECT * FROM missing_script_table;", false, true)
	if err == nil || len(out) != 0 {
		t.Fatal("atomic failure retained results")
	}
	var count int
	db.QueryRowContext(ctx, "SELECT count(*) FROM script_rows").Scan(&count)
	if count != 1 {
		t.Fatal("atomic changes not rolled back")
	}
	out, err = run("INSERT INTO script_rows VALUES(2); SELECT * FROM missing_script_table;", false, false)
	if err == nil || len(out) != 1 {
		t.Fatal("partial failure lost completed results")
	}
	db.QueryRowContext(ctx, "SELECT count(*) FROM script_rows").Scan(&count)
	if count != 2 {
		t.Fatal("non-atomic changes not retained")
	}
	if _, err = run("CREATE TABLE comment_escape(id int) -- hidden\r; COMMIT; INSERT INTO comment_escape VALUES(1);", false, true); err == nil {
		t.Fatal("CR comment escaped atomic validation")
	}
	var escaped int
	db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_name='comment_escape'").Scan(&escaped)
	if escaped != 0 {
		t.Fatal("CR comment script executed before validation")
	}
	if _, err = run("WITH x AS (SELECT 1) DELETE FROM script_rows", true, true); err == nil {
		t.Fatal("read-only CTE write accepted")
	}
	if _, err = run("SELECT set_config('transaction_read_only','off',true); WITH x AS (SELECT 1) DELETE FROM script_rows;", true, true); err == nil {
		t.Fatal("read-only transaction reconfiguration accepted")
	}
	if _, err = run("SELECT 1", true, false); err == nil {
		t.Fatal("read-only partial mode accepted")
	}
	out, err = run("CREATE FUNCTION script_answer() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN RETURN 42; END; $$; SELECT script_answer();", false, false)
	if err != nil || len(out) != 2 || out[1].Rows[0][0] != "42" {
		t.Fatalf("dollar quoted function: %#v %v", out, err)
	}
	out, err = run("SELECT set_config('application_name','nichosi-script-test',false); SELECT current_setting('application_name');", false, false)
	if err != nil || out[1].Rows[0][0] != "nichosi-script-test" {
		t.Fatal("session command failed")
	}
	out, err = run("SELECT current_setting('application_name')", false, false)
	if err != nil || out[0].Rows[0][0] == "nichosi-script-test" {
		t.Fatal("session state leaked")
	}
}

func TestScriptErrorMessagePreservesDatabaseError(t *testing.T) {
	mysqlError := &mysql.MySQLError{Number: 1064, SQLState: [5]byte{'4', '2', '0', '0', '0'}, Message: "You have an error in your SQL syntax near 'FRO entries' at line 1"}
	postgresError := &pgconn.PgError{Severity: "ERROR", Code: "42601", Message: `syntax error at or near "FRO"`}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, sqliteError := db.Exec("SELECT * FROM missing_error_table")
	if sqliteError == nil {
		t.Fatal("expected database error")
	}
	for _, databaseError := range []error{mysqlError, postgresError, sqliteError} {
		wrapped := &ScriptStatementError{Statement: 2, Err: fmt.Errorf("driver operation: %w", databaseError)}
		if got := ScriptErrorMessage(wrapped); got != databaseError.Error() {
			t.Fatalf("got %q; want %q", got, databaseError.Error())
		}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "Execution timed out."},
		{context.Canceled, "Execution cancelled."},
		{errors.New("result exceeds 4 MiB"), "result exceeds 4 MiB"},
	} {
		if got := ScriptErrorMessage(&ScriptStatementError{Statement: 1, Err: tc.err}); got != tc.want {
			t.Fatalf("got %q; want %q", got, tc.want)
		}
	}
}

func TestScriptResultLimit(t *testing.T) {
	for _, tc := range []struct {
		sql  string
		want int
	}{
		{"SELECT 1", 200},
		{"SELECT 1 LIMIT 500", 1000},
		{"SELECT 'LIMIT 500' /* LIMIT 500 */", 200},
		{"SELECT * FROM (SELECT 1 LIMIT 500) sub", 200},
		{"WITH sub AS (SELECT 1 LIMIT 500) SELECT * FROM sub", 200},
		{"WITH sub AS (SELECT 1) SELECT * FROM sub LIMIT 500", 1000},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			statements, err := SplitScript(tc.sql, "sqlite")
			if err != nil {
				t.Fatal(err)
			}
			if got := ScriptResultLimit(statements[0]); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
