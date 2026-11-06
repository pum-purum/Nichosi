# Nichosi

Nichosi is a web app for browsing database tables. It connects to MySQL, MariaDB, PostgreSQL (read-only), and SQLite.

## What you need

- Docker with the Docker Compose plugin.
- A running MySQL or MariaDB server that the Nichosi container can reach.
- The database name, host, port, user name, and password.

## Start the app

Open a terminal and download the project:

```sh
git clone https://github.com/pum-purum/Nichosi.git
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
| `NICHOSI_CONNECTIONS_FILE` | JSON connection profile file loaded at startup | No file loaded |
| `NICHOSI_METADATA_FILE` | File for cached database sizes and last successful connection times | Persistence disabled |
| `NICHOSI_HISTORY_DIR` | Directory for retained SQL script history | Persistence disabled |

Nichosi is a local application and does not require an authentication token.
The Compose configuration binds the published port to `127.0.0.1` and mounts the
connection file. To run the backend directly with saved profiles, use
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

Database sizes and last successful connection times are stored in the host `data/` directory mounted at `/data`. They survive container recreation, `make down`, and `docker compose down -v`. The metadata file contains no passwords or DSNs. Configured connections start disconnected and display their cached observations until the next successful connection. For local runs, set `NICHOSI_METADATA_FILE` to a writable file path and `NICHOSI_HISTORY_DIR` to a writable directory to enable persistence.

## SQLite files

SQLite is an embedded database: Nichosi opens an existing file, with no database
server, host, port, user, or password. Paths are absolute paths inside the backend
container. Missing files are rejected. Read-only profiles open the file in SQLite
read-only mode; writable profiles use a transaction for each ChangeSet. Tables
without a usable primary or non-null unique key and all views remain read-only in
the grid. The SQL console can execute scripts on writable profiles.

Add this profile to `connections.json`:

```json
{"name":"SQLite database","driver":"sqlite","database":"/sqlite/database.sqlite","readonly":false}
```

The Compose configuration mounts the existing `sqlite-demo` volume at `/sqlite`.
No seed scripts or initializer containers run at startup. Populate the volume with
your existing database file before connecting; Nichosi never creates a missing
database. The example connection file includes MySQL and SQLite profiles. Remove profiles
you do not need; add PostgreSQL profiles explicitly when needed.

Start or rebuild Nichosi with `make re`; stop it with `make down`. The named volume
preserves the existing database and edits across restarts.

To use a file on your computer, replace `sqlite-demo:/sqlite` in
`deploy/docker-compose.yml` with `../sqlite:/sqlite`. Create the `sqlite/` directory
in the repository root and place your existing file there as `database.sqlite`.
The example profile then opens `/sqlite/database.sqlite` inside the container.
Give UID 65532 access to the directory and file, including write permissions when
editing is needed (SQLite also creates journal files). Run `make re` to apply the
mount change. No separate SQLite server is needed.

## SQL scripts and history

Open SQL console from a connected database's navigation rail. Write SQL, then select **Execute** or press Ctrl/Cmd+Enter. The server
validates and executes the script directly; there is no separate preview step.
Results include row counts and affected rows; execution duration appears in history.

The console always requests transactional execution; there is no mode selector.
SQLite and PostgreSQL support transactional queries and changes. MySQL/MariaDB
currently support read queries only in this mode. Database DDL may have implicit
commit semantics; a single statement does not guarantee rollback support.

History retains the latest 100 executions per connection in `/data/script-history`
and survives container restarts and recreation. History appears in the left sidebar while the SQL console is open.
Select an entry to restore its source, then execute it again
explicitly. **Clear history** removes retained SQL from memory and disk. SQL source and execution
metadata are stored on disk; query results are not retained. Treat the data directory
as sensitive because SQL source may contain confidential values. Unfinished drafts survive workspace navigation
in memory. Generated row edits still require their ChangeSet SQL preview.

The SQL console accepts one SQL statement at a time. Multiple statements show a
warning and disable Execute; the server rejects them before executing anything.

SQL results default to at most 200 rows when the outer query has no LIMIT.
Specify LIMIT in SQL to choose a different count, subject to the safety cap of
1000 rows and 4 MiB per execution. The console has no row-limit control.


## Store container data on the host

The image declares `/data` as a Docker volume. The bundled Compose file binds the host `data/` directory for connection
observations and SQL history, and uses the named `sqlite-demo` volume for SQLite
databases. To let Docker manage application data instead, replace `../data:/data`
with `nichosi-data:/data` and declare `nichosi-data` under top-level `volumes`. Connection profiles come from the read-only `connections.json`
bind mount. Unexecuted editor drafts remain in browser memory.

For host storage, save this example as `compose.yaml` in the repository root:

```yaml
name: nichosi
services:
  nichosi:
    build:
      context: .
      dockerfile: deploy/Dockerfile
    environment:
      NICHOSI_ADDR: 0.0.0.0:8080
      NICHOSI_CONNECTIONS_FILE: /config/connections.json
      NICHOSI_METADATA_FILE: /data/connection-metadata.json
      NICHOSI_HISTORY_DIR: /data/script-history
    env_file:
      - path: .env
        required: false
    ports:
      - "127.0.0.1:8080:8080"
    volumes:
      - ./data:/data
      - ./sqlite:/sqlite
      - ./connections.json:/config/connections.json:ro
    restart: unless-stopped
```

Create the host directories before starting the non-root container:

```sh
mkdir -p data sqlite
sudo chown -R 65532:65532 data sqlite
sudo chmod 700 data sqlite
test -f connections.json || cp connections.example.json connections.json
docker compose up -d --build
```

Existing named-volume contents are not migrated automatically. Copy them into the
host directories before switching mounts if you want to keep existing observations,
history, or SQLite data. Place existing SQLite files in `./sqlite` and use their
`/sqlite/...` paths in connection profiles. Back up `data`, `sqlite`, and
`connections.json` together, along with any external environment secrets.
