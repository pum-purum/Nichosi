# Nichosi

A database workspace in your browser. Explore tables and views, build precise filters, inspect schema details, and turn SQL into answers — in a dark, IDE-style interface.

Nichosi connects to **MySQL, MariaDB, PostgreSQL, and SQLite**. It runs as a self-hosted web app in Docker, with a Go backend and a Svelte + TypeScript frontend. Connections default to read-only; editing requires an explicit writable profile and driver support.

![SQL console running a PostgreSQL analysis with CTEs, joins and a window function, alongside execution history](docs/screenshots/sql-analysis.jpg)

## Explore, query, revisit

- **Browse your database.** Navigate schema-grouped tables and views, search database objects, and inspect column types and keys beside the data.
- **Narrow down the rows.** Combine conditions in a visual filter builder or type an expression. Sort on the server and move through pages without losing your applied filters.
- **Write expressive SQL.** Use joins, CTEs, aggregates, subqueries, and window functions supported by your database. Execute with Ctrl/Cmd+Enter and inspect the result directly below the editor.
- **Keep the useful work.** Per-connection history records SQL source, execution status, timestamps, and duration. Restore an earlier query and run it again explicitly.
- **Understand the structure.** Inspect columns, keys, indexes, foreign keys, triggers, and SQL definitions where the driver exposes them.
- **Review generated edits.** Grid changes go through a ChangeSet SQL preview before transactional application. Views and tables without a usable row key stay read-only.

[Run from Docker Hub](#run-from-docker-hub) · [Build from source](#start-the-app) · [SQL and history](#sql-scripts-and-history) · [Configuration](#backend-configuration) · [Architecture](docs/architecture.md)

## A closer look

These are actual application screenshots at 1920 × 1080, captured against a PostgreSQL demo database. The `culture` dataset connects countries, cities, people, music, playlists, landmarks, and travel diaries through 11 tables and two views. People and activity are fictional; cultural references and numeric values are illustrative. Demo data is not installed automatically when you start Nichosi.

### Start with your connections

The start screen brings your saved database profiles together, with their database engine, connection state, read-only status, cached size, and last successful connection time. Choose a profile to enter its workspace. Nichosi runs locally without a separate application login.

![Nichosi start screen showing saved MySQL, PostgreSQL and SQLite connections](docs/screenshots/connections.jpg)

### Filters that express more than a search box

Find tracks between three and seven minutes, restrict their album position, or include a title match. The builder and expression field show the same filter; this example returns 24 matching tracks.

![Applied song filter combining BETWEEN, AND, OR and LIKE, with matching PostgreSQL rows](docs/screenshots/filters.jpg)

Supported operators include comparisons, `BETWEEN`, `LIKE`, `IN`, `IS NULL`, and `IS NOT NULL`. `AND` takes precedence over `OR`; grouping parentheses are currently unsupported. Use the SQL console for queries that need more elaborate logic.

### Real views, real pagination

Browse a seven-table playlist view with readable song titles, artists, albums, and countries. This capture shows **page two: rows 26–50 of 128**, with 25 rows per page and sorting by curator. Page sizes of 25, 50, and 100 are available; applied sorting and filters persist while paging.

![Playlist view sorted by curator on page two, showing rows 26–50 of 128](docs/screenshots/views-pagination.jpg)

### See how a view is built

Switch from data to **View info** to inspect column metadata and a formatted, copyable SQL definition. The playlist view makes its joins visible without leaving the workspace.

![View metadata and formatted SQL definition joining playlists, people, songs, albums, artists and countries](docs/screenshots/view-definition.jpg)

### Complex SQL with a result you can read

The opening screenshot ranks artists within each country using two CTEs over the seven-table playlist view, distinct listener counts, and `DENSE_RANK()`. Ties remain visible in the result. The console executes **one SQL statement per run**; a statement can contain multiple CTEs and nested queries. Multi-statement batches are currently rejected.

### A history you can return to

Move from a country ranking to album runtimes, travel journals, or playlist diversity. Each execution creates a history entry with its status, timestamp, and duration. Selecting an entry restores its source without executing it; the example below was restored from history and explicitly run again.

![SQL history containing travel and music analyses, with a restored country ranking query and its results](docs/screenshots/script-history.jpg)

History retains the latest 100 executions per connection. The bundled Docker setup persists it in the host `data/` directory; local backend runs need `NICHOSI_HISTORY_DIR` configured. Results are not stored in history.


## Run from Docker Hub

Use the published [pumphonia/nichosi image](https://hub.docker.com/r/pumphonia/nichosi)
without cloning the repository or installing Go and Node.js. You need Docker with
the Compose plugin and a database reachable from the container. Published images
currently target `linux/amd64`.

Create a directory for Nichosi and place your existing `connections.json` in it.
Save the `compose.yaml` below alongside it. No `.env` file is required.

**`connections.json`** contains your connection profiles, including database
passwords. If you already have this file, keep your profiles. For a new setup,
adjust this example:

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
      "password": "your-database-password",
      "readonly": true
    }
  ]
}
```

MariaDB also uses `"driver": "mysql"`; optionally add `"engine": "mariadb"`.
For PostgreSQL, use `"driver": "postgres"` and the appropriate port (usually `5432`).
Connections default to read-only. Set `"readonly": false` explicitly to request
editing where the driver supports it.

**`compose.yaml`** — comments explain the settings:

```yaml
services:
  nichosi:
    # Download the published release instead of building from source.
    image: pumphonia/nichosi:latest
    restart: unless-stopped
    environment:
      # Listen on the container network; the host port below stays local.
      NICHOSI_ADDR: 0.0.0.0:8080
      NICHOSI_CONNECTIONS_FILE: /config/connections.json
      NICHOSI_METADATA_FILE: /data/connection-metadata.json
      NICHOSI_HISTORY_DIR: /data/script-history
    ports:
      # Nichosi has no application login. Expose it only on localhost.
      - "127.0.0.1:8080:8080"
    extra_hosts:
      # Allow access to a database on the Docker host, including Linux.
      - "host.docker.internal:host-gateway"
    volumes:
      # Create connections.json before starting; the container only reads it.
      - type: bind
        source: ./connections.json
        target: /config/connections.json
        read_only: true
        bind:
          create_host_path: false
      # Keep SQL history and cached metadata across container recreation.
      - nichosi-data:/data

volumes:
  nichosi-data:
```

Keep `connections.json` private and out of version control: it contains database passwords. For a database in another Compose
project, attach Nichosi to that database's Docker network and use its service name
as `host`. Inside the Nichosi container, `localhost` refers to Nichosi itself.
For SQLite, mount your existing database file separately and use its container
path as `database`; see [SQLite files](#sqlite-files) for permissions and profiles.

Run these commands from the directory containing `compose.yaml`:

```sh
docker compose pull
docker compose up -d
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) and select your connection.
Use `docker compose logs nichosi` to inspect logs and `docker compose restart nichosi`
to reload changes to `connections.json`. After changing `compose.yaml`,
run `docker compose up -d` to recreate the container with the new settings.

To update to the latest published image, run `docker compose pull` and
`docker compose up -d` again. Stop the app with
`docker compose down`. The named data volume survives this command;
`docker compose down -v` deletes it, including stored SQL history and metadata.

## What you need

The following instructions build Nichosi from source. For a published image, use
[Run from Docker Hub](#run-from-docker-hub).

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

## Publish to Docker Hub

The `Publish Docker image` GitHub Actions workflow builds `deploy/Dockerfile`
and publishes a Linux AMD64 image whenever a Git tag is pushed. The image tag
matches the Git tag exactly, including a leading `v`; no `latest` alias is added.
The build uses the source at that tag.

Before the first release:

1. Create a Docker Hub repository, for example `your-account/nichosi`.
2. In GitHub, open **Settings → Secrets and variables → Actions** and add
   repository variables `DOCKERHUB_USERNAME` (your Docker Hub login) and
   `DOCKERHUB_IMAGE` (the full `namespace/repository`, such as `your-account/nichosi`).
3. Create a Docker Hub access token with write access to that repository and save
   it as the GitHub Actions repository secret `DOCKERHUB_TOKEN`.
4. Push the workflow to GitHub before creating a release tag. The tagged commit
   must contain the workflow.

For example:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Follow the run in the repository's **Actions** tab. After it succeeds:

```sh
docker pull your-account/nichosi:v1.0.0
```

Tags must also be valid Docker tags: 1–128 ASCII letters, digits, underscores,
periods, or hyphens, starting with a letter, digit, or underscore. Tags containing
slashes or `+` fail validation instead of being silently renamed. Existing tags
are not published retroactively; rerun their workflow if a publication fails.
To use a published image with the Compose examples above, replace the `build`
section with `image: your-account/nichosi:v1.0.0` and run `docker compose up -d`.
