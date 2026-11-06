# MySQL database objects

The object browser lists views, triggers, procedures, functions, scheduled events,
roles and users. Category badges and list headings display the object count
whenever it is at least one; empty categories have no count badge. Select a category and an object to inspect its full CREATE statement.
The detail panel starts with the object name and SQL, without a decorative icon,
repeated category label or separate metadata grid. The read-only code viewer includes line numbers, horizontal scrolling
and an icon-only Copy SQL action in the top-right corner of the code panel.
The copy button appears on hover or keyboard focus, and stays visible on touch
devices. Highlight.js supplies local SQL syntax highlighting for
keywords, strings, numbers, comments and built-in types/functions, using the
core and SQL grammar only. The library is bundled with the application; no CDN
or runtime network request is used. Copy SQL copies plain executable SQL, compacted to a single line by collapsing
whitespace between tokens and removing ordinary comments. Executable MySQL/MariaDB
comments and optimizer hints are retained. Whitespace inside quoted values and
identifiers remains unchanged, including literal newlines; ambiguous quoting
falls back to the exact server text to avoid changing SQL semantics. SQL keywords and operators use a fixed blue accent. SQL is displayed with local whitespace formatting by
default: DDL clauses, event schedules and statement bodies use separate lines,
and related clauses are indented by two spaces per level. Schedule details nest
under ON SCHEDULE, event statements nest under DO, query expressions nest under
SELECT/FROM/WHERE/SET, and nested BEGIN blocks and subqueries gain their own level. Quoted strings, identifiers and comments are
protected from keyword formatting. The viewer keeps its readable formatting while copying uses the compact
server definition; there is no original-text toggle. Incomplete quoted text
or comments fall back to the original definition. Backslash-containing strings
also use the original text when the object SQL mode is unknown. Formatting does not execute SQL. Accounts are server-wide, identified by both user name and host. Account labels
and filtering use the plain account name and host (for example, root@localhost),
without SQL quoting. Object identity and SQL definitions retain their original quoting.
Definitions for views, triggers, procedures, functions and events come from
`SHOW CREATE`, preserving the server-reported declaration, parameters, definer,
schedule, options and body rather than displaying only the body. For view display and copying, the default `ALGORITHM=UNDEFINED` clause is
omitted from the CREATE header; explicit MERGE/TEMPTABLE algorithms are retained.
The API still returns the unmodified server definition. This is the
server's current definition, not a record of the original SQL text or subsequent
ALTER statements. When access is denied, an
object disappears, or a definition is hidden, the object remains listed with an
availability explanation; partial bodies are not presented as full definitions.
The API retains object metadata for formatting and availability handling;
the detail panel omits fields already represented by the CREATE statement.
MySQL/MariaDB events are the supported scheduled jobs. PostgreSQL, `pg_cron` and
external operating-system cron jobs are not currently supported.

Definitions are fetched on demand when an object is selected, through
`GET /api/connections/{connId}/object-definition?type=…&name=…`, with a separate
ten-second timeout. Schema listings do not fetch DDL, so large schemas do not
issue one SHOW CREATE query per object. Definition failures leave the object
metadata available; failed requests can be retried from the detail view. Each metadata category is limited to 1,000 objects; schema
requests have a ten-second timeout.

Account badges distinguish documented MySQL account identities in both lists
and detail headings. `mysql.infoschema@localhost`, `mysql.session@localhost` and
`mysql.sys@localhost` receive a Built-in badge; `root@localhost` receives Default
admin. Matching is exact for both user and host, without `mysql.*` prefix matching.
Other accounts remain unmarked: their creation by a person, installer or service
cannot be established from these metadata. Renamed or third-party service
accounts are not automatically recognized. These badges do not inspect effective
privileges and do not represent MySQL's `SYSTEM_USER` account category, which is
based on privileges and may also apply to manually created accounts.

User detail panels show the raw account name and host pattern, account lock
state, recognized account category and server-wide scope. Not locked does not
guarantee login: authentication and privilege checks still apply. Unknown lock
state is displayed explicitly. Category labels do not establish account creation
history or effective privileges. Role assignments are not available in the
current safe metadata view and are not inferred. No additional database queries
or authentication fields are needed for these panels.

A MySQL role is a named collection of privileges that can be granted to user
accounts or other roles. Roles and login users share account infrastructure;
being a role and being an internal/reserved account are separate concepts.

MySQL 8 does not distinguish roles from users in a dedicated account-type field.
The optional administrator-installed `nichosi_account_metadata` view classifies
locked accounts without credentials as roles (including unassigned roles).
A locked passwordless user is consequently also shown as a role. The UI states
this classification. Roles show a reconstructed `CREATE ROLE` using only account name and host;
a short note marks it as reconstructed, without grants or creation history.
It is not a complete recreation script for the account's privileges. User creation
SQL is intentionally unavailable: `SHOW CREATE USER` is never requested because
it can expose authentication data. Authentication strings are never returned to Nichosi.
Without this view the remaining objects still load and the UI displays a warning
that users and roles are unavailable.

The optional SQL SECURITY DEFINER metadata view must be installed separately by
an administrator. It exposes only account name, host, lock status and inferred
type; it grants no direct access to `mysql.user`. The account creating the view
must retain permission to read that system table. Install this view in the database
used by the connection and grant the connection user SELECT on the view:

```sql
CREATE OR REPLACE SQL SECURITY DEFINER VIEW nichosi_account_metadata AS
SELECT User AS account_name, Host AS account_host, account_locked,
       CASE WHEN account_locked = 'Y' AND authentication_string = ''
            THEN 'role' ELSE 'user' END AS account_type
FROM mysql.user;
```

Nichosi does not install demo objects or accounts.

The table view omits the unfinished row-filter and column-visibility controls,
read-only banner and toolbar divider. Edit and Delete row buttons are disabled
when the connection readonly flag is true or the RowsPage editable flag is false. Their
tooltips explain the restriction, including the server-provided readOnlyReason
when available. Object-name filtering in the sidebar remains available.

Table-loading failures show a centered error panel with Reconnect and Connections
actions, and the reported database or driver error text immediately visible
in a monospace block. Database failures retain their source error message instead
of a generic replacement; line breaks are preserved and HTML is displayed as text. Reconnect reloads the connection profiles
first to recover IDs changed by a server restart. It reconnects only to an exact
profile match (name, driver, host, port, user and database); missing or ambiguous
profiles require choosing a connection again. Pagination is hidden while an error
is shown. Returning to Connections cancels pending UI updates.

A failed table-existence query returns HTTP 502 (metadata_failed), including when
the database server is stopped or metadata permissions are missing. Table not
found (HTTP 404, table_not_found) is returned only after a successful metadata
query reports zero matching tables. The reported database/driver error text is returned in the error message.
