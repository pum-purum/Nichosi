# Nichosi

Nichosi is a web app for browsing database tables. It connects to MySQL, MariaDB, PostgreSQL (read-only), and SQLite.

## What you need

- Docker with the Docker Compose plugin.
- A running MySQL or MariaDB server that the Nichosi container can reach.
- The database name, host, port, user name, and password.

## Start the app

Open a terminal and download the project:

```sh
git clone https://github.com/Pum-purum/Nichosi.git
cd Nichosi
```

Run the following commands from the `Nichosi` folder.

1. Create your local connection file:

   ```sh
   cp connections.example.json connections.json
   ```

2. Open `connections.json` and replace the example values with your database details. The file contains your password, so keep it private. It is ignored by Git and is not copied into the Docker image.

   MariaDB uses `"driver": "mysql"`. Add `"engine": "mariadb"` to its profile to show the MariaDB logo before connecting. The optional `engine` defaults to the driver; MySQL-compatible servers are identified after connecting. The engine changes only the displayed database product, not the connection protocol.

   ```json
   {
     "connections": [
       {
         "name": "My database",
         "driver": "mysql",
         "host": "host.docker.internal",
         "port": 3306,
         "user": "database_user",
         "database": "database_name",
         "readonly": false,
         "password": "your-password"
       }
     ]
   }
   ```

   `host` must be an address the Docker container can reach. If your database runs in another container, use that container's service name and make sure both containers share a Docker network. If it runs on your computer, `host.docker.internal` usually works with Docker Desktop; on Linux, configure a host address that is reachable from Docker.

   You can add more connection profiles to the `connections` array. Set `"readonly": true`
   to restrict a connection to read-only operations, or `"readonly": false` to opt out of
   that restriction. The value must be a JSON boolean, not a string. If omitted, it
   defaults to `true`. The old `environment` field is no longer used; replace it with
   an explicit `readonly` value (`true` for former `prod` profiles). The API uses the
   same `readonly` name in connection inputs and responses.

3. Build and start Nichosi:

   ```sh
   make re
   ```

   This builds the app's Docker image and starts the container in the background. The first build can take a few minutes.

4. Open <http://127.0.0.1:8080> in your browser. Choose your connection to see its tables and table data.

If the app started before you finished editing `connections.json`, restart it to reload the file:

```sh
docker compose -f deploy/docker-compose.yml restart
```

## Backend configuration

The backend reads these variables from the process environment; it does not load
`.env` files. Database connection details come from `connections.json` or the API,
not `MYSQL_*` environment variables.

| Variable | Purpose | Default |
| --- | --- | --- |
| `NICHOSI_ADDR` | HTTP listen address | `127.0.0.1:8080` |
| `NICHOSI_ADMIN_TOKEN` | Bearer token for API authentication; at least 24 characters required for non-loopback binding unless local preview is enabled | Unset |
| `NICHOSI_CONNECTIONS_FILE` | JSON connection profile file loaded at startup | No file loaded |
| `NICHOSI_METADATA_FILE` | File for cached database sizes and last successful connection times | Persistence disabled |
| `NICHOSI_LOCAL_PREVIEW` | Set to `true` to allow non-loopback binding without an admin token for local Docker preview | Disabled |
| `NICHOSI_TEST_DSN` | MySQL DSN used only by optional integration tests | Tests skipped |

The Compose configuration enables local preview, binds the published port to
`127.0.0.1`, and mounts the connection file. Keep local preview restricted to your
own computer. To run the backend directly with saved profiles, use
`NICHOSI_CONNECTIONS_FILE="$PWD/connections.json" make run`.

Use **Refresh** above the data grid to reload the current table or view without
reloading the browser page. The selected connection, current page, applied filters,
and sorting are preserved.

## Table information

Use **Table info** at the bottom of the table workspace to inspect storage details,
columns, keys and indexes, foreign keys, triggers, and the read-only `CREATE TABLE`
statement. Metadata loads when you open the tab; reload the browser page to refresh it.
Sizes and row counts are estimates. Unavailable or truncated categories show warnings.
Switch back to **Data** to return to the rows and current page.

## Views

Choose **Views** in the far-left rail and select a view to browse its rows using the
same data grid, page sizes, and pagination as tables. View rows are always read-only,
even on writable connections. The **View info** tab shows properties, column metadata,
and the formatted `CREATE VIEW` statement with a copy control. Reload the browser page to refresh metadata. Views have
no separate table storage; table-only indexes, foreign keys and triggers are not shown.

## Stop the app

```sh
make down
```

The web app is available only from your own computer by default. Docker mounts `connections.json` as read-only. Do not commit this file or share it: it contains database passwords.

## Troubleshooting

- **No connections appear:** Check that `connections.json` contains valid details and valid JSON, then restart the app.
- **The database cannot be reached:** Check the host and port from inside Docker. A database address that works on your computer may not work from a container.
- **The page does not open:** Check that Docker is running and port `8080` is not already in use. View container logs with `docker compose -f deploy/docker-compose.yml logs`.

## Project information

- [Architecture](docs/architecture.md)
- [Design guidance](docs/design/README.md)
- [Contributor instructions](AGENTS.md)

Click a column heading in the Data grid to sort all rows on the server. Click again to
reverse the direction. Sorting returns to page one and persists across pagination
and page-size changes; selecting another table or view resets it. MySQL determines
NULL ordering and text collation. Primary keys break ties when available.

Database sizes and last successful connection times are stored in the `nichosi-data` Docker volume. They survive container recreation and `make down`; `docker compose down -v` deletes them. The metadata file contains no passwords or DSNs. Configured connections start disconnected and display their cached observations until the next successful connection. For local runs, set `NICHOSI_METADATA_FILE` to a writable file path to enable persistence.

## SQLite demo

SQLite is an embedded database: Nichosi opens an existing file, with no database
server, host, port, user, or password. Paths are absolute paths inside the backend
container. Missing files are rejected. Read-only profiles open the file in SQLite
read-only mode; writable profiles use a transaction for each ChangeSet. Tables
without a usable primary or non-null unique key and all views remain read-only in
the grid. The SQL console can execute scripts on writable profiles.

Add this profile to `connections.json`:

```json
{"name":"SQLite demo","driver":"sqlite","database":"/sqlite/demo.sqlite","readonly":false}
```

The Compose configuration mounts the existing `sqlite-demo` volume at `/sqlite`.
The local demo was populated once with 80 customers, 500 orders, a view, indexes,
a foreign key, an audit trigger, a unique-key table and a keyless audit table.
No seed scripts or initializer containers run at startup. An empty volume must be
populated separately before connecting; Nichosi never creates a missing database.

Start or rebuild Nichosi with `make re`; stop it with `make down`. The named volume
preserves the existing database and edits across restarts.

For your own file, mount its directory
with write permissions for UID 65532 when editing is needed (including SQLite
journal files), then use its container path in the profile.

## SQL scripts and history

Open SQL console from a connected database's navigation rail. Write SQL, choose
the result row limit, then select **Execute** or press Ctrl/Cmd+Enter. The server
validates and executes the script directly; there is no separate preview step.
Results include row counts and affected rows; execution duration appears in history.

The console always requests transactional execution; there is no mode selector.
SQLite and PostgreSQL support transactional queries and changes. MySQL/MariaDB
currently support read queries only in this mode. Database DDL may have implicit
commit semantics; a single statement does not guarantee rollback support.

History retains the latest 100 executions per connection until the server
restarts. History appears in the left sidebar while the SQL console is open.
Select an entry to restore its source and row limit, then execute it again
explicitly. **Clear history** removes retained SQL. Neither SQL nor results are
written to browser storage or disk. Unfinished drafts survive workspace navigation
in memory. Generated row edits still require their ChangeSet SQL preview.

The SQL console accepts one SQL statement at a time. Multiple statements show a
warning and disable Execute; the server rejects them before executing anything.
