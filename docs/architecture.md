# Architecture

## Delivery
1. Docker image (primary): Go binary with the built frontend embedded (`embed`), served over HTTP.
2. Desktop binary for Ubuntu (later): Wails shell around the same core and frontend.

Nichosi runs locally without application authentication or admin tokens. The backend
defaults to `127.0.0.1:8080`; Docker listens on `0.0.0.0` inside the container and
publishes the port only on the host loopback address.

## Layers
UI (Svelte) -> JSON API (`api/openapi.yaml`) -> services -> `Driver` interface -> MySQL / Postgres / SQLite.
Local store (SQLite): connection profiles, query history, manual relations, cached stats.

## Driver interface (the only place dialect SQL lives)
```go
type Driver interface {
    Connect(ctx context.Context, cfg ConnConfig) error
    Databases(ctx context.Context) ([]string, error)
    Tables(ctx context.Context, db string) ([]Table, error)
    Columns(ctx context.Context, db, table string) ([]Column, error)
    Indexes(ctx context.Context, db, table string) ([]Index, error)
    ForeignKeys(ctx context.Context, db string) ([]FK, error)
    Page(ctx context.Context, q PageQuery) (Rows, error) // keyset pagination
    Exec(ctx context.Context, tx Tx, stmt Stmt) (Result, error)
    Stats(ctx context.Context, db string) (DBStats, error)
    QuoteIdent(s string) string
}
```

## Features
- Browser title: `<database> · Nichosi` for the selected database; `Databases · Nichosi` on the connections screen.
- Connections: profiles, explicit readonly boolean (true by default), SSH tunnel, TLS, cancel query.
- Editing: virtualized grid, keyset pagination, ChangeSet (preview SQL, one transaction), primary or non-null unique key editing.
- Relations: real FKs, inferred links by naming plus data check (orphan ratio), manual links; graph depth 1-3.
- Statistics: table sizes, column profile (sampled), unused/duplicate indexes, processlist, slow queries.

## Security
Loopback bind by default; admin auth before any other bind; secrets encrypted at rest with a key from env;
parameterized SQL only; row limit and timeout on every UI query; audit log of write operations.

## Milestones
1. MVP: connect, schema tree, read-only grid with filters and pagination.
2. Editing with ChangeSet.
3. Relations graph and inference.
4. Statistics.
5. Polish: SSH tunnel, PostgreSQL, Docker image on Docker Hub and ghcr.io, desktop build.

## PostgreSQL connections

PostgreSQL profiles use `driver: "postgres"`. Local PostgreSQL connections currently
use TLS disabled and support schema/data browsing. PostgreSQL grid ChangeSets
are not yet available; the SQL console supports scripts on writable profiles. Keep this setup on a trusted local network.
Tables and views are listed across accessible user schemas with qualified names
such as `public.events`. Materialized views have their own category. The PostgreSQL
object browser also lists PostgreSQL accounts together under Users: LOGIN roles
and NOLOGIN roles. NOLOGIN roles receive a Built-in display badge, which describes
this grouping rather than proving server-provided origin. Account details retain
the Login allowed attribute. The browser also lists triggers, procedures,
functions, sequences, types, extensions and indexes. PostgreSQL schema-scoped
objects appear in expandable schema groups within each category; there is no
separate Schemas navigation section. Empty accessible schemas remain available.
Groups use raw `schemaName` metadata and short `displayName` labels; API requests
continue using the full opaque `name`, including routine identity arguments.
Users, extensions and cron jobs remain ungrouped.

Each schema group offers Schema properties with the owner, effective USAGE and
CREATE permissions for the current database account, and bounded ACL metadata.
Opening properties preserves the current category and selected object. Group
expansion state persists across categories until switching connections. Search
reveals matching children in collapsed groups and matches both short and qualified
names; clearing search restores expansion state.

Overloaded routines
include their identity arguments. Role metadata never includes authentication data.

The API reports `supportedObjectTypes`; the navigation displays those categories
even when empty and hides unsupported categories. PostgreSQL has no MySQL Events
category. Cron jobs are shown only when the optional pg_cron extension is installed
and accessible in the connected database. Nichosi does not install extensions or
create scheduled jobs. Permission failures appear as metadata warnings.

Object details are read-only. SQL definitions are fetched on demand where available;
reconstructed statements and unavailable definitions are identified in metadata.
Table metadata includes columns, indexes and foreign keys; a complete CREATE TABLE
statement is not reconstructed.

Use `passwordEnv` in `connections.json` to name an environment variable containing
the password. This takes precedence over the legacy `password` field. With Docker
Compose, set `NICHOSI_POSTGRES_PASSWORD` in the ignored root `.env` file; Compose
passes it to Nichosi. Direct backend runs require exporting the variable explicitly.
Restart Nichosi after changing profiles or environment variables.

For databases published by another Docker project, use `host.docker.internal` and
the published host port, for example 5432 for a primary and 5433 for a replica.

## Record editing

The pencil and trash buttons show the record’s primary key in their tooltips and accessible labels (or the editing unique key when no primary key exists). Composite keys include every key column.
The row pencil opens a record editor. Changes are accumulated in a Svelte ChangeSet
store; restoring a field to its original value removes it from the update. SQL NULL
is an explicit choice distinct from an empty string. Numeric text retains precision. SQL preview parameters for integer columns use numbers within JavaScript’s safe integer range; larger integers remain strings to preserve every digit.
The original primary key (or a complete non-null unique key) identifies the row throughout editing. Primary key columns are read-only in the form and rejected
by the backend when included in update values. Tables without a usable key and read-only connections
cannot be edited; views always remain read-only. MySQL updates require InnoDB so the transaction can roll back; SQLite updates
use SQLite transactions. Generated and binary fields are shown read-only.
Keys containing binary or temporal columns, prefix indexes, and functional indexes
are excluded; another usable key is selected when available.

The editor requests a debounced preview from the backend and displays parameterized
SQL together with the bound values. Only modified columns appear in SET; unchanged
key columns may still appear in WHERE to identify the record. Apply is available
only after the current changes have a successful preview. The backend validates
metadata again and applies the ChangeSet in one transaction with a ten-second
timeout. Any failure rolls back the entire set. This flow supports updates and deletes; inserts are not implemented.

The row trash button opens a modal confirmation showing every column of the original
record and a parameterized SQL preview. Cancel is initially focused; Escape cancels
before applying. Delete is enabled only after a successful preview. Deletion uses
the original complete row key and the same transaction and read-only checks as updates.
The modal remains open on failure; successful deletion refreshes the current data page.

## SQLite connections

SQLite uses `modernc.org/sqlite` without cgo. The `database` profile field is an
absolute path to an existing backend-local file; host, port, user and password are
unused. Docker deployments mount the file directory into Nichosi. Open mode is
`ro` by default and `rw` only with explicit `readonly: false`; missing databases
are never created by a connection request. Foreign key enforcement is enabled.

The object browser exposes tables, views, indexes and triggers. Stored definitions
retain SQLite syntax. Pages use bounded parameterized filters, ordering and a
request timeout. Tables with a usable primary or non-null unique key support
ChangeSet preview and transactional updates/deletes; keyless tables and views
remain read-only. The default Compose configuration mounts an existing SQLite demo volume.
Demo data was populated once; startup does not seed or recreate the database.

SQLite uses dynamic value types. BLOB cells retain their binary identity in JSON
as `{"type":"blob","hex":"..."}` and display as hexadecimal read-only values.
Candidate edit keys containing runtime values other than integers or text are
rejected. Binary key objects are also rejected during change validation, preserving
row identity if another process changes the database between page load and Apply.

SQLite table sizes come from `dbstat` in aggregate mode: table B-tree pages are
reported as data bytes, and explicit plus automatic index pages as index bytes.
These allocated sizes include overflow and unused space within occupied pages;
free database pages and file-level overhead are not attributed to a table.

## SQL console

Open SQL console from the connected workspace navigation rail. Write a script in
the CodeMirror editor, then select Execute or press
Ctrl/Cmd+Enter. The server validates and executes the current SQL directly;
there is no separate preview request or token. Unfinished
drafts remain in memory while navigating between tables and connections.

The console always sends `atomic: true`; there is no execution-mode selector.
Restoring history or drafts does not restore an earlier execution mode.
SQLite and PostgreSQL support transactional queries and changes. MySQL/MariaDB
transactional execution supports read-only queries because arbitrary DDL and
writes cannot guarantee rollback across engines and routines. Session settings,
transaction commands and nontransactional operations are unavailable in this mode.
A single SQL statement does not itself guarantee transactional DDL.

The API retains its explicit writable-profile-only non-atomic option for existing
clients. SQL console execution has no preview step; generated grid edits still
require preview. Read-only profiles remain read-only at the database level.
MySQL DELIMITER commands, SQLite trigger bodies and PostgreSQL dollar quotes are
supported. Executable comments/optimizer hints and backslash-escaped literals in
MySQL/PostgreSQL are rejected; use doubled quotes or dollar quoting.

Results show columns, rows, affected counts and truncation. Duration appears in history. Limits are
256 KiB of SQL, exactly one statement, 30 seconds, 200 rows per result without an outer LIMIT (up to
1000 with an explicit outer LIMIT), 1000 rows total and 4 MiB of serialized result column/row data. Large queries
should include explicit SQL limits. Connection loss during commit is reported as
an unknown outcome; check database state before repeating.
SQL errors show the database driver message directly, without generic application
advice or a duplicate rollback caption. Connection credentials and DSNs are
redacted before errors are returned or stored in history.

The SQL console replaces the object tree with script history in the left sidebar.
History lists the most recent 100 executions per connection for this server
session. Each entry shows SQL, timestamp and duration, with green/red outcome highlighting
and no status icons; selecting it loads
the script into the editor. Refresh and Clear history are available in the sidebar. Selecting an entry restores SQL, then requires explicit
execution. Clear history removes retained source. Server
restart clears all history; results are not stored. SQL is neither logged nor
persisted in browser storage or on disk. See
[the SQL console decision](decisions/001-sql-script-console.md) for constraints and
future encrypted persistence.

The console accepts exactly one SQL statement per execution. Multiple statements
show a warning and disable Execute (including its keyboard shortcut); the API
also rejects them with HTTP 400 before executing anything. Quoted semicolons,
comments and supported routine bodies do not separate statements.

The SQL console omits the API rowLimit option and has no limit control.
Only top-level LIMIT tokens count; comments, quoted values and nested query limits
do not override the default cap. API clients may still supply an explicit rowLimit.
Result headers use an opaque sticky background so scrolling data remains below them.
