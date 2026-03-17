# UltraBridge CalDAV

UltraBridge is a CalDAV/WebDAV bridge for the Supernote Private Cloud, enabling synchronization of tasks between Supernote tablets and standard CalDAV clients (GNOME Evolution, DAVx5, 2Do, etc.). It sits alongside the Supernote stack as a companion microservice, reading from and writing to the same MariaDB database.

## Prerequisites

- **Supernote Private Cloud** running with Docker Compose (at `/mnt/supernote/`)
- **Docker** and **Docker Compose**
- A CalDAV client on your calendar/task app (DAVx5, GNOME Evolution, 2Do, etc.)

## Quick Start

The interactive installer handles everything — configuration, password hashing, Docker image build, and startup:

```bash
./install.sh
```

It will prompt for username, password, port, and collection name, then build and start the container. Safe to re-run to change configuration.

After making code changes, rebuild and restart without reconfiguring:

```bash
./rebuild.sh
```

Both scripts auto-detect the Supernote stack at `/mnt/supernote/`. Pass a different path as an argument if needed: `./install.sh /path/to/supernote`.

### Manual setup

If you prefer to configure manually instead of using the installer:

1. Copy `.ultrabridge.env.example` to `/mnt/supernote/.ultrabridge.env` and set `UB_USERNAME` and `UB_PASSWORD_HASH` (generate with `docker run --rm ultrabridge:dev hash-password "yourpassword"`)
2. Create a `docker-compose.override.yml` in `/mnt/supernote/` (see `.ultrabridge.env.example` for the template)
3. `sudo docker compose up -d ultrabridge`

Verify: `curl -u admin:yourpassword http://localhost:8443/health` should return `{"status":"ok"}`

## CalDAV Client Setup

UltraBridge exposes a single CalDAV collection at `https://your-host:8443/caldav/tasks/`.

### DAVx5 (Android)

1. **Add account** → DAVx5 settings → Add account → CalDAV
2. **Server URL:** `https://your-host:8443/.well-known/caldav`
3. **Login:** Username and password from `.ultrabridge.env`
4. **Accept SSL:** If using self-signed certificate, enable "SSL / TLS: Custom CA"
5. **Sync:** Select "Supernote Tasks" collection

### GNOME Evolution (Linux)

1. **Calendar** → **New Calendar** → Remote
2. **Type:** CalDAV
3. **Server URL:** `https://your-host:8443/caldav/tasks/`
4. **Login:** Username and password from `.ultrabridge.env`
5. **Color:** Choose a color
6. **Create** → Evolution syncs automatically

### OpenTasks (Android)

1. **Settings** → **Caldav Sync**
2. **Add account** → CalDAV
3. **Server URL:** `https://your-host:8443/.well-known/caldav`
4. **Login:** Username and password from `.ultrabridge.env`
5. **OK** → Select "Supernote Tasks"

### 2Do (iOS/Mac)

1. **Settings** → **Sync** → **Add Account** → CalDAV
2. **Server URL:** `https://your-host:8443/caldav/tasks/`
3. **Login:** Username and password from `.ultrabridge.env`
4. **Save**

## Configuration

All configuration is via environment variables in `.ultrabridge.env`:

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_USERNAME` | (required) | Basic Auth username |
| `UB_PASSWORD_HASH` | (required) | bcrypt hash of password |
| `UB_CALDAV_COLLECTION_NAME` | `Supernote Tasks` | Name shown in CalDAV clients |
| `UB_DUE_TIME_MODE` | `preserve` | `preserve` or `date_only` (strip time from due dates) |
| `UB_LISTEN_ADDR` | `:8443` | Listen address (port inside container) |
| `UB_WEB_ENABLED` | `true` | Enable web UI at `/` |
| `UB_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `UB_LOG_FORMAT` | `json` | `json` or `text` |
| `UB_LOG_FILE` | (empty) | Optional file path for logging |
| `UB_LOG_FILE_MAX_MB` | `50` | Max size before rotation |
| `UB_LOG_FILE_MAX_AGE_DAYS` | `30` | Keep logs for N days |
| `UB_LOG_FILE_MAX_BACKUPS` | `5` | Keep N backup files |
| `UB_LOG_SYSLOG_ADDR` | (empty) | Optional syslog address (e.g., `udp://graylog:1514`) |
| `UB_DB_HOST` | `mariadb` | Database hostname (auto-detected in Docker) |
| `UB_DB_PORT` | `3306` | Database port |
| `UB_SUPERNOTE_DBENV_PATH` | `/run/secrets/dbenv` | Path to `.dbenv` file |
| `UB_SOCKETIO_URL` | `ws://supernote-service:8080/socket.io/` | Socket.io URL for device notifications |

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Supernote Private Cloud Stack                               │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │  nginx       │  │  MariaDB     │  │  Redis       │       │
│  │  (reverse    │  │  (t_schedule │  │  (sessions)  │       │
│  │   proxy)     │  │   _task etc) │  │              │       │
│  └──────────────┘  └──────────────┘  └──────────────┘       │
│         ▲                 ▲                                   │
│         │                 │                                   │
│  ┌──────────────────────────────────────────────────────┐   │
│  │  UltraBridge CalDAV Microservice                    │   │
│  │  ┌────────────────────────────────────────────────┐ │   │
│  │  │ CalDAV Handler ← Tasks Store → MariaDB         │ │   │
│  │  │                                                 │ │   │
│  │  │ GET /caldav/tasks/task-001.ics                 │ │   │
│  │  │ PUT /caldav/tasks/task-002.ics (VTODO)         │ │   │
│  │  │ DELETE /caldav/tasks/task-003.ics              │ │   │
│  │  │ PROPFIND /caldav/tasks/ (list)                 │ │   │
│  │  │                                                 │ │   │
│  │  │ Notifies device via Socket.io on write         │ │   │
│  │  └────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────┘   │
│                         ▲                                     │
└─────────────────────────┼─────────────────────────────────────┘
                          │
              CalDAV clients (DAVx5, Evolution, etc.)
```

## Development

### Build the service

```bash
cd /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
go build -o ultrabridge ./cmd/ultrabridge/
```

### Run unit tests

```bash
go test ./...
```

### Run integration tests

Integration tests require a running Supernote Private Cloud MariaDB:

```bash
TEST_DBENV_PATH=/mnt/supernote/.dbenv go test -tags integration ./tests/ -v
```

Expected output: All tests pass.

### Build and restart

```bash
./rebuild.sh
```

Or manually: `docker build -t ultrabridge:dev .`

### Generate a password hash

```bash
docker run --rm ultrabridge:dev hash-password "yourpassword"
```

## Known Limitations

1. **Tier 3 fields are dropped:** Only title, description, priority, due date, and status are synchronized. Recurrence, reminders, and other advanced fields are not mapped.

2. **Single-user only:** UltraBridge discovers the single user from `u_user` and does not support multiple users. This matches the Supernote Private Cloud design.

3. **No TLS termination:** The service listens on plain HTTP. Use a reverse proxy (nginx, Caddy) for HTTPS in production.

4. **Device sync notifications:** UltraBridge notifies the device via Socket.io when tasks are modified via CalDAV. If the device is offline, it will pick up changes on next sync.

## Troubleshooting

### "database connection failed"

Check that `.dbenv` is readable and MariaDB is running:

```bash
cat /mnt/supernote/.dbenv
docker ps | grep mariadb
```

### "user discovery failed"

No users exist in the Supernote Private Cloud database. Create a user via the Supernote web UI first.

### CalDAV client shows empty collection

1. Verify UltraBridge is running: `curl -u admin:password http://localhost:8443/health`
2. Verify credentials are correct in the CalDAV client
3. Check logs: `docker logs ultrabridge`

### SSL certificate errors

Use a self-signed certificate and configure your CalDAV client to trust it, or use a reverse proxy with a valid certificate.

## License

Same as UltraBridge project.
