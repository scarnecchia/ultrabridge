# UltraBridge

UltraBridge is a sidecar service for [Supernote Private Cloud](https://support.supernote.com/article/75/set-up-supernote-partner-cloud), adding two capabilities to the self-hosted Supernote stack:

1. **CalDAV task sync** — synchronise Supernote tasks with any CalDAV client (DAVx5, GNOME Evolution, 2Do, etc.)
2. **Notes pipeline** — automatically discover `.note` files, extract handwritten text, index it for full-text search, and optionally run vision-API OCR

## Prerequisites

- **Supernote Private Cloud** running with Docker Compose (at `/mnt/supernote/`)
- **Docker** and **Docker Compose**
- For CalDAV sync: a CalDAV client on your device
- For OCR: an API key for Anthropic or OpenRouter

## Quick Start

The interactive installer handles configuration, password hashing, Docker image build, and startup:

```bash
./install.sh
```

It prompts for username, password, port, and collection name, then builds and starts the container. Safe to re-run to change configuration.

After code changes, rebuild and restart without reconfiguring:

```bash
./rebuild.sh
```

Both scripts auto-detect the Supernote stack at `/mnt/supernote/`. Pass a different path if needed: `./install.sh /path/to/supernote`.

### Manual setup

1. Copy `.ultrabridge.env.example` to `/mnt/supernote/.ultrabridge.env` and set `UB_USERNAME` and `UB_PASSWORD_HASH` (generate with `docker run --rm ultrabridge:dev hash-password "yourpassword"`)
2. Create a `docker-compose.override.yml` in `/mnt/supernote/` (see `.ultrabridge.env.example` for the template)
3. `sudo docker compose up -d ultrabridge`

Verify: `curl -u admin:yourpassword http://localhost:8443/health` should return `{"status":"ok"}`

## Web UI

Navigate to `http://<host>:<port>/` after starting the service.

| Tab | What it does |
|-----|-------------|
| **Tasks** | View, create, and complete Supernote tasks |
| **Files** | Browse `.note` files; queue/skip/force individual files; start/stop the OCR processor |
| **Search** | Full-text keyword search over indexed note content |
| **Logs** | Live log stream (SSE) |

## CalDAV Client Setup

UltraBridge exposes a single CalDAV collection at `https://your-host:8443/caldav/tasks/`.

### DAVx5 (Android)
1. **Add account** → DAVx5 settings → Add account → CalDAV
2. **Server URL:** `https://your-host:8443/.well-known/caldav`
3. **Login:** Username and password from `.ultrabridge.env`
4. **Accept SSL:** If using a self-signed certificate, enable "SSL / TLS: Custom CA"
5. **Sync:** Select "Supernote Tasks" collection

### GNOME Evolution (Linux)
1. **Calendar** → **New Calendar** → Remote
2. **Type:** CalDAV, **URL:** `https://your-host:8443/caldav/tasks/`
3. **Login:** Username and password from `.ultrabridge.env`

### OpenTasks / 2Do
Use `https://your-host:8443/.well-known/caldav` as the server URL with your credentials.

## Configuration

Copy `.ultrabridge.env.example` to `.ultrabridge.env` and edit. All variables are optional except `UB_USERNAME` and `UB_PASSWORD_HASH`.

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_USERNAME` | (required) | Basic Auth username |
| `UB_PASSWORD_HASH` | (required) | bcrypt hash — generate with `hash-password` |
| `UB_LISTEN_ADDR` | `:8443` | Listen address (port inside container) |
| `UB_WEB_ENABLED` | `true` | Enable web UI |
| `UB_CALDAV_COLLECTION_NAME` | `Supernote Tasks` | Name shown in CalDAV clients |
| `UB_DUE_TIME_MODE` | `preserve` | `preserve` or `date_only` |

### Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `UB_LOG_FORMAT` | `json` | `json` or `text` |
| `UB_LOG_FILE` | (empty) | Optional file path |
| `UB_LOG_FILE_MAX_MB` | `50` | Max size before rotation |
| `UB_LOG_FILE_MAX_AGE_DAYS` | `30` | Days to keep |
| `UB_LOG_FILE_MAX_BACKUPS` | `5` | Number of backup files |
| `UB_LOG_SYSLOG_ADDR` | (empty) | e.g. `udp://graylog:1514` |

### Notes Pipeline

All pipeline variables are optional. Omitting `UB_NOTES_PATH` disables the pipeline entirely (Files and Search tabs show "not configured").

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_NOTES_PATH` | (empty) | Root directory of `.note` files |
| `UB_DB_PATH` | `/data/ultrabridge.db` | SQLite database for the pipeline |
| `UB_BACKUP_PATH` | (empty) | Copy originals here before any OCR write |
| `UB_OCR_ENABLED` | `false` | Enable vision-API OCR |
| `UB_OCR_API_URL` | (empty) | API base URL — Anthropic or OpenRouter |
| `UB_OCR_API_KEY` | (empty) | API key |
| `UB_OCR_MODEL` | (empty) | Model name (e.g. `anthropic/claude-opus-4-6`) |
| `UB_OCR_CONCURRENCY` | `1` | Parallel OCR workers |
| `UB_OCR_MAX_FILE_MB` | `0` | Skip files larger than N MB (0 = no limit) |

### Infrastructure (rarely need to change)

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_DB_HOST` | `mariadb` | MariaDB hostname |
| `UB_DB_PORT` | `3306` | MariaDB port |
| `UB_SUPERNOTE_DBENV_PATH` | `/run/secrets/dbenv` | Path to `.dbenv` credentials file |
| `UB_SOCKETIO_URL` | `ws://supernote-service:8080/socket.io/` | Engine.IO URL for device notifications |
| `UB_USER_ID` | (auto-discover) | Explicit user ID if multiple users exist |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ Supernote Private Cloud Stack                                   │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────┐ │
│  │  nginx       │  │  MariaDB     │  │  .note file store     │ │
│  │  (proxy)     │  │  (tasks)     │  │  (NFS / volume)       │ │
│  └──────────────┘  └──────────────┘  └───────────────────────┘ │
│         ▲                 ▲                       ▲             │
│         │                 │                       │             │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │  UltraBridge                                             │  │
│  │                                                          │  │
│  │  ┌─────────────────────────┐  ┌──────────────────────┐  │  │
│  │  │  CalDAV subsystem       │  │  Notes pipeline      │  │  │
│  │  │  CalDAV ← TaskStore     │  │  Pipeline            │  │  │
│  │  │         → MariaDB       │  │   ↓ fsnotify watcher │  │  │
│  │  │         → Engine.IO     │  │   ↓ reconciler       │  │  │
│  │  └─────────────────────────┘  │  NoteStore → SQLite  │  │  │
│  │                               │  Processor (OCR jobs)│  │  │
│  │  ┌─────────────────────────┐  │  SearchIndex (FTS5)  │  │  │
│  │  │  Web UI                 │  └──────────────────────┘  │  │
│  │  │  Tasks / Files / Search │                             │  │
│  │  └─────────────────────────┘                             │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### Notes pipeline flow

```
.note file written/changed
         │
         ▼
   fsnotify watcher  ──(2s debounce)──▶  Processor queue
         +
   reconciler (15 min)
         │
         ▼
   Worker picks up job
         │
         ├─ backup original (if UB_BACKUP_PATH set)
         ├─ extract existing MyScript RECOGNTEXT → index as "myScript"
         ├─ if OCR enabled:
         │    render page → JPEG → vision API → inject RECOGNTEXT
         │    index as "api"
         └─ job marked done
                  │
                  ▼
           FTS5 search index
```

## Development

### Build

```bash
go build -C /home/sysop/src/ultrabridge ./cmd/ultrabridge/
```

### Test

```bash
go test -C /home/sysop/src/ultrabridge ./...
```

Integration tests require a running MariaDB:

```bash
TEST_DBENV_PATH=/mnt/supernote/.dbenv go test -tags integration ./tests/ -v
```

### Rebuild Docker image

```bash
./rebuild.sh
```

Or manually:

```bash
docker build -t ultrabridge:dev .
```

### Generate a password hash

```bash
docker run --rm ultrabridge:dev hash-password "yourpassword"
```

## Known Limitations

1. **CalDAV — tier 3 fields dropped:** Only title, description, priority, due date, and status are synchronised. Recurrence, reminders, and other advanced fields are not mapped.

2. **Single-user by default:** UltraBridge auto-discovers the user from `u_user`. If multiple users exist, it refuses to start — set `UB_USER_ID` to resolve this.

3. **No TLS termination:** The service listens on plain HTTP. Use a reverse proxy (nginx, Caddy) for HTTPS in production.

4. **Engine.IO listener is a stub:** The pipeline detects files via fsnotify and periodic reconciliation. Parsing Supernote Engine.IO push events for instant detection is not yet implemented (`extractNotePaths` in `internal/pipeline/engineio.go`).

5. **TITLE block extraction is a stub:** `extractNoteTitle` returns an empty string. Heading text is captured indirectly when RECOGNTEXT is indexed. Full heading extraction is out of scope for the current release.

## Troubleshooting

### "database connection failed"

Check that `.dbenv` is readable and MariaDB is running:

```bash
cat /mnt/supernote/.dbenv
docker ps | grep mariadb
```

### "user resolution failed"

- **"no users found"** — No users in the database yet. Sync your Supernote device first.
- **"multiple users found"** — Set `UB_USER_ID` in `.ultrabridge.env`.

### Files tab shows "UB_NOTES_PATH is not configured"

Set `UB_NOTES_PATH` in `.ultrabridge.env` to the directory containing your `.note` files.

### OCR jobs stuck in "in_progress"

The watchdog reclaims stuck jobs after 10 minutes. If jobs consistently get stuck, check `UB_OCR_API_URL` and `UB_OCR_API_KEY` are correct and the API is reachable from inside the container.

### CalDAV client shows empty collection

1. `curl -u admin:password http://localhost:8443/health` — should return `{"status":"ok"}`
2. Verify credentials in the CalDAV client
3. Check logs: `docker logs ultrabridge`

## License

[Apache 2.0](https://www.apache.org/licenses/LICENSE-2.0.txt)
