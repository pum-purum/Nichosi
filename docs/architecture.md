# Architecture

## Delivery
1. Docker image (primary): Go binary with the built frontend embedded (`embed`), served over HTTP.
2. Desktop binary for Ubuntu (later): Wails shell around the same core and frontend.

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

## Record editing

The row pencil opens a record editor. Changes are accumulated in a Svelte ChangeSet
store; restoring a field to its original value removes it from the update. SQL NULL
is an explicit choice distinct from an empty string. Numeric text retains precision.
The original primary key (or a complete non-null unique key) identifies the row throughout editing. Primary key columns are read-only in the form and rejected
by the backend when included in update values. Tables without a usable key and read-only connections
cannot be edited; views always remain read-only. Updates currently require InnoDB
so the transaction can roll back. Generated and binary fields are shown read-only.
Keys containing binary or temporal columns, prefix indexes, and functional indexes
are excluded; another usable key is selected when available.

The editor requests a debounced preview from the backend and displays parameterized
SQL together with the bound values. Only modified columns appear in SET; unchanged
key columns may still appear in WHERE to identify the record. Apply is available
only after the current changes have a successful preview. The backend validates
metadata again and applies the ChangeSet in one transaction with a ten-second
timeout. Any failure rolls back the entire set. This flow currently supports updates;
inserts and deletes are not implemented.
