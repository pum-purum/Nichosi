# SQL script console

The SQL console accepts user-authored SQL as source, rather than interpolating
application-generated values into SQL. Generated grid queries and ChangeSets
continue binding values as parameters. SQL console changes form a script
ChangeSet: the server validates and previews the exact source, then issues a
single-use preview token bound to the connection, source, execution mode and row limit for five
minutes. Execution revalidates the script and requires that token.

Every script runs on a dedicated connection, discarded afterward so session state
cannot leak into browsing. The default atomic mode uses one transaction, rejects
session/transaction commands and operations outside transaction guarantees, and
uses database-enforced read-only execution for read-only profiles, including
SQLite query_only. PostgreSQL and SQLite support transactional DML and a
conservative subset of DDL. MySQL/MariaDB atomic mode uses read-only transactions:
arbitrary DDL implicitly commits and writes can reach nontransactional engines
through routines or triggers.

The maintainer approved an explicit exception to the single-transaction rule for
the SQL console: Allow partial execution. This mode is available only when the
connection has explicit readonly:false. The preview and UI warn that statements
execute without a shared transaction and earlier changes may remain committed on
failure. This permits DML, DDL, routines, and explicit session/transaction commands
under the database account's privileges. Errors retain completed statement
results and never claim rollback. Repeating history restores the mode but still
requires another preview and explicit execution. Changing the mode invalidates
the preview. Record-key requirements remain specific to grid editing.

MySQL DELIMITER directives are handled locally for routine definitions. SQLite
trigger bodies and PostgreSQL dollar-quoted routines remain intact when splitting
statements. Executable comments and optimizer hints are rejected instead of
silently executing hidden SQL. Backslash-escaped literals are unsupported for
PostgreSQL and MySQL; use doubled quotes or PostgreSQL dollar quoting.

The first implementation retains the last 100 accepted executions per connection
in server memory, including failures, timestamps and duration. History is lost on
server restart and can be cleared. SQL may include sensitive literals: it is never
logged or persisted to browser storage or disk. Results are never retained in
history. Durable history requires an encrypted store and key management, rather
than plaintext local storage. Repeating history loads source into the editor and
requires a fresh preview and explicit execution.

Limits: 256 KiB of SQL, 100 statements, a 30-second execution deadline, 200 rows per
result by default (maximum 1000), 1000 rows across all results and a 4 MiB result
value budget. Truncated results are marked. Database errors are described without
echoing SQL, credentials or database values.

Transaction guarantees cover transactional database changes. Database privileges
remain authoritative: the console is not a sandbox for privileged routines, file
access, external effects, or sequence increments. A commit connection failure is
reported as an unknown outcome and must be checked before retrying.
