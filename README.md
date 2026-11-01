# Nichosi

Nichosi is a web app for browsing database tables. It currently connects to MySQL and MariaDB. PostgreSQL and SQLite are not available yet.

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
   make app-up
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
| `NICHOSI_LOCAL_PREVIEW` | Set to `true` to allow non-loopback binding without an admin token for local Docker preview | Disabled |
| `NICHOSI_TEST_DSN` | MySQL DSN used only by optional integration tests | Tests skipped |

The Compose configuration enables local preview, binds the published port to
`127.0.0.1`, and mounts the connection file. Keep local preview restricted to your
own computer. To run the backend directly with saved profiles, use
`NICHOSI_CONNECTIONS_FILE="$PWD/connections.json" make be-run`.

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
make app-down
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
