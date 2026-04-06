<p align="center">
  <img src="https://github.com/jdkruzr/ultrabridge/blob/main/docs/erbwidesmall.png" alt="minimalistic depiction of Einstein-Rosen bridge"/>
</p>

<p align="center">
 <h1>UltraBridge</h1>
</p>

UltraBridge is a data management application for e-ink tablets including Onyx Boox and Supernote (via a sidecar service for [Supernote Private Cloud](https://support.supernote.com/article/75/set-up-supernote-partner-cloud) ), providing four capabilities:

1. **CalDAV task sync** — synchronise Supernote tasks with any CalDAV client (DAVx5, GNOME Evolution, 2Do, etc.)
2. **Supernote notes pipeline** — automatically discover `.note` files, extract handwritten text, index it for full-text search, and optionally run vision-API OCR
3. **Boox notes pipeline** — accept Boox `.note` file uploads via WebDAV, parse the ZIP/protobuf format, render pages, OCR, extract TODOs via color coding, and index for unified search alongside Supernote notes
4. **Unified search** — full-text search across both Supernote and Boox notes with source indicators

**This software was developed using Claude Code, trained on open source software, and will therefore always be open-source software.**

## Prerequisites

- **Docker** and **Docker Compose v2**
- For Supernote features: **Supernote Private Cloud** running with Docker Compose
- For Boox integration: a Boox device with WebDAV export support (Tab Ultra C Pro, NoteAir, Note Air5C, etc.)
- For CalDAV sync: a CalDAV client on your device
- For OCR: an API key for Anthropic or OpenRouter, or an API endpoint from a local inference API server like vLLM

> **Boox-only users:** UltraBridge works without Supernote Private Cloud. The installer auto-detects whether SPC is present and adjusts accordingly. CalDAV tasks, Boox WebDAV uploads, OCR, and search all work in standalone mode.

## Quick Start

### Before you run the installer

Have the following ready:

- **Username and password** for CalDAV/web access (you choose these)
- **For Supernote pipeline:** full path to your `.note` files (usually `/mnt/supernote/note/your@email.com`)
- **For Supernote pipeline:** full path for backups *(recommended)* — originals are copied here before OCR writes
- **For Boox pipeline:** a directory for Boox note uploads (the WebDAV root)
- **API credentials** *(optional, for OCR)* — an [OpenRouter](https://openrouter.ai) key, a direct Anthropic key, or the base URL of a local vLLM instance

You can skip any feature during install and enable it later by re-running `install.sh`. The installer auto-detects Supernote Private Cloud and only shows relevant prompts.

### Run the installer

```bash
./install.sh
```

It prompts for username, password, port, collection name, optional notes pipeline settings, and optional Boox WebDAV integration, then builds and starts the container. Safe to re-run to change configuration.

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
| **Tasks** | View, create, and complete tasks; bulk actions; purge all completed tasks |
| **Files** | Browse `.note` files from both Supernote and Boox with source badges; view rendered pages, OCR text, and version history; queue/skip/force processing; scan now |
| **Search** | Full-text keyword search across all indexed notes with source badges and folder filter |
| **Logs** | Live WebSocket log stream with level filtering |
| **Settings** | Per-pipeline OCR prompts (Supernote / Boox); red ink to-do extraction toggle and prompt |

### Red Ink To-Do Extraction

When enabled in Settings > Boox, a second OCR pass scans each Boox page for **red handwriting**. Any red text found is automatically created as a CalDAV task — visible in the Tasks tab, synced to your CalDAV client, and pushed to your Supernote device.

This lets you use red ink on your Boox device as a "to-do" marker: write in red, and UltraBridge picks it up. Duplicate detection prevents the same task from being created twice (checks both incomplete and completed tasks).

This feature is exposed in the Settings tab of the application and can be configured as needed (for example, changing the prompt from "red" to "blue.")

## CalDAV Client Setup

**(Note for all URLs: 8443 is the internally exposed port; you are expected to run a reverse proxy for TLS termination like Caddy or Nginx Proxy Manager.)**

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
| `UB_OCR_FORMAT` | `anthropic` | `anthropic` (Anthropic/OpenRouter) or `openai` (vLLM/Ollama) |
| `UB_OCR_API_URL` | (empty) | API base URL (e.g. `https://openrouter.ai/api` or `http://localhost:8000`) |
| `UB_OCR_API_KEY` | (empty) | API key — leave blank for unauthenticated local endpoints |
| `UB_OCR_MODEL` | (empty) | Model name (e.g. `anthropic/claude-opus-4-6` or `Qwen3-VL-8B-Instruct`) |
| `UB_OCR_CONCURRENCY` | `1` | Parallel OCR workers |
| `UB_OCR_MAX_FILE_MB` | `0` | Skip files larger than N MB (0 = no limit) |

### Boox Notes Pipeline

When enabled, UltraBridge runs a WebDAV server at `/webdav/` that Boox devices can sync to. Uploaded `.note` files are parsed (ZIP + protobuf), rendered to page images, OCR'd, and indexed alongside Supernote notes.

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_BOOX_ENABLED` | `false` | Enable Boox WebDAV uploads and processing |
| `UB_BOOX_NOTES_PATH` | (empty) | Root directory for Boox note uploads (WebDAV root) |

On the Boox device, configure WebDAV sync under Settings > Cloud Storage with `http://<host>:<port>/webdav/` as the server URL and your UltraBridge credentials. Uploaded notes appear in the Files tab with a "B" badge.

Re-uploaded notes are versioned automatically — the previous version is archived under `.versions/` before the new file is written.

### Task Store

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_TASK_DB_PATH` | `/data/ultrabridge-tasks.db` | Path to SQLite database for task storage |

### Supernote Sync

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_SN_SYNC_ENABLED` | `false` | Enable task sync with Supernote device via SPC |
| `UB_SN_ACCOUNT` | _(none)_ | Supernote account email (used for SPC auth) |
| `UB_SN_SYNC_INTERVAL` | `300` | Sync interval in seconds |
| `UB_SN_API_URL` | `http://supernote-service:8080` | SPC REST API URL |
| `UB_SN_PASSWORD` | _(none)_ | SPC password for challenge-response auth |

When sync is disabled, UltraBridge runs in standalone mode with tasks stored locally in SQLite. CalDAV and the web UI work normally. MariaDB connection failure is non-fatal in this mode.

When sync is enabled, tasks are bidirectionally synced between UltraBridge and the Supernote device. UltraBridge is authoritative on conflicts.

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
┌──────────────────────────────────────────────────────────────────────┐
│ Supernote Private Cloud Stack                                        │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────────────────┐ │
│  │  nginx       │  │  MariaDB     │  │  .note file store          │ │
│  │  (proxy)     │  │  (tasks)     │  │  (NFS / volume)            │ │
│  └──────────────┘  └──────────────┘  └────────────────────────────┘ │
│         ▲                 ▲                       ▲                  │
│         │                 │                       │                  │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  UltraBridge                                                  │  │
│  │                                                               │  │
│  │  ┌──────────────────────┐  ┌───────────────────────────────┐  │  │
│  │  │  CalDAV subsystem    │  │  Supernote notes pipeline     │  │  │
│  │  │  CalDAV ← TaskStore  │  │   ↓ fsnotify watcher          │  │  │
│  │  │        → MariaDB     │  │   ↓ reconciler                │  │  │
│  │  │        → Engine.IO   │  │  NoteStore → SQLite           │  │  │
│  │  └──────────────────────┘  │  Processor (OCR jobs)         │  │  │
│  │                            └───────────────────────────────┘  │  │
│  │  ┌──────────────────────┐  ┌───────────────────────────────┐  │  │
│  │  │  Boox notes pipeline │  │  Shared services              │  │  │
│  │  │  WebDAV server ←─────│──│  SearchIndex (FTS5)           │  │  │
│  │  │   ↓ .note parser    │  │  Web UI (Tasks/Files/Search)  │  │  │
│  │  │   ↓ page renderer   │  │  Auth middleware               │  │  │
│  │  │   ↓ OCR + indexer ──│─▶│                                │  │  │
│  │  │  Version archive     │  │                                │  │  │
│  │  └──────────────────────┘  └───────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

### Supernote notes pipeline flow

```
.note file written/changed on device
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

### Boox notes pipeline flow

```
Boox device syncs via WebDAV
         │
         ▼
   WebDAV PUT /webdav/onyx/{model}/{type}/{folder}/{name}.note
         │
         ├─ version-on-overwrite (old file → .versions/)
         ├─ parent directories auto-created
         └─ upload callback → enqueue job
                  │
                  ▼
   Boox processor picks up job (5s poll)
         │
         ├─ parse ZIP (protobuf metadata, shapes, point files)
         ├─ extract title, device model, page count
         ├─ render each page → JPEG cache
         ├─ if OCR enabled: vision API → text
         ├─ index page text → FTS5
         └─ job marked done
                  │
                  ▼
   Unified FTS5 search index (shared with Supernote)
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

## Appendix: Standalone OCR Injection

The [go-sn](https://github.com/jdkruzr/go-sn) library includes `sninject`, a standalone tool for processing `.note` files outside of UltraBridge's pipeline. It renders each page, sends it to a vision API, injects JIIX RECOGNTEXT, and optionally zeros RECOGNFILE to prevent device re-recognition. No database, sync, or watcher involved.

```bash
go install github.com/jdkruzr/go-sn/cmd/sninject@latest

# Process a note using the same vLLM endpoint as UltraBridge
sninject -in original.note -out processed.note \
  -api-url http://192.168.9.199:8000 \
  -model Qwen/Qwen3-VL-8B-Instruct

# Dry run — see OCR results without modifying the file
sninject -in original.note -out /dev/null -dry-run
```

This is useful for debugging injection issues, testing OCR quality on specific files, or one-off processing without starting the full pipeline. See the [go-sn README](https://github.com/jdkruzr/go-sn#sninject) for full usage.

### Why `-zero-recognfile`?

When UltraBridge (or `sninject`) injects RECOGNTEXT into an RTR note, the device's MyScript engine detects a mismatch between the injected text and its own RECOGNFILE (iink recognition data). On the next sync, the device re-runs recognition from RECOGNFILE and overwrites the injected text — often with lower quality results (especially for math, symbols, and measurements).

Zeroing RECOGNFILE removes the data the device uses to re-derive RECOGNTEXT, preventing this clobbering. The trade-off: if you later add new strokes to that page on the device, it may need to do a full recognition pass instead of an incremental update.

## Known Limitations

1. **CalDAV — tier 3 fields dropped:** Only title, description, priority, due date, and status are synchronised. Recurrence, reminders, and other advanced fields are not mapped.

2. **TITLE block extraction is a stub:** `extractNoteTitle` returns an empty string. Heading text is captured indirectly when RECOGNTEXT is indexed. Full heading extraction is out of scope for the current release.

## Troubleshooting

### "database connection failed"

This is expected in standalone mode (no Supernote Private Cloud). The warning is non-fatal — UltraBridge continues with SQLite-only storage.

If you have SPC installed, check that `.dbenv` is readable and MariaDB is running:

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

### Boox WebDAV sync fails

1. Verify `UB_BOOX_ENABLED=true` in `.ultrabridge.env`
2. Check the WebDAV URL is `http://<host>:<port>/webdav/` (trailing slash required for some Boox firmware)
3. Confirm credentials match your UltraBridge username/password
4. Check container logs: `docker logs ultrabridge | grep boox`

### Boox notes not appearing in Files tab

1. Verify `UB_BOOX_NOTES_PATH` is set and the directory exists inside the container
2. Check the Docker volume mount includes the Boox notes path in `docker-compose.override.yml`
3. Uploaded files should appear at `{UB_BOOX_NOTES_PATH}/onyx/{model}/...`

### CalDAV client shows empty collection

1. `curl -u admin:password http://localhost:8443/health` — should return `{"status":"ok"}`
2. Verify credentials in the CalDAV client
3. Check logs: `docker logs ultrabridge`

## License

[Apache 2.0](https://www.apache.org/licenses/LICENSE-2.0.txt)
