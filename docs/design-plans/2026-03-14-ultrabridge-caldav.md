# UltraBridge CalDAV Task Sync Design

## Summary

UltraBridge is a Go service that adds CalDAV task synchronization to a self-hosted Supernote Private Cloud installation. Because Ratta's private cloud service exposes tasks only to the Supernote device, UltraBridge sits alongside the existing Docker stack, reads and writes the `t_schedule_task` table directly, and presents those tasks as a standard CalDAV VTODO collection. This allows any CalDAV-capable client — such as DAVx5 with OpenTasks on Android, 2Do on iOS, or GNOME Evolution on Linux — to subscribe to, create, and complete Supernote tasks without any modification to Ratta's software.

The sync is bidirectional. When a CalDAV client writes a task, UltraBridge inserts or updates the database row and then sends a `STARTSYNC` push message to Ratta's socket.io service, causing connected Supernote devices to sync within seconds. When the device creates or completes a task, UltraBridge surfaces it on the next CalDAV client poll by reading directly from the database. Field mapping bridges the gap between iCalendar semantics and Supernote's schema, including several non-obvious quirks in how the device records completion timestamps. A minimal web UI and structured logging are included for operational verification; authentication is a single shared credential pair, separate from Supernote's own auth system.

## Definition of Done

1. **A Go service ("UltraBridge")** running as a Docker container alongside the existing Supernote Private Cloud stack
2. **CalDAV server** exposing a VTODO-only collection that CalDAV clients (OpenTasks/DAVx5, 2Do, GNOME Evolution) can discover, subscribe to, and sync tasks with
3. **Bidirectional task sync**: tasks created/modified on the Supernote device appear in CalDAV clients; tasks created/modified in CalDAV clients appear on the device (via direct DB writes + socket.io push)
4. **Minimal web UI**: a simple page to list, create, and complete tasks — for verification, not a full app
5. **Simple auth**: username/password from config, no integration with Supernote's auth system

## Acceptance Criteria

### ultrabridge-caldav.AC1: Go service running as Docker container
- **ultrabridge-caldav.AC1.1 Success:** Container starts, connects to MariaDB, auto-discovers user ID, and responds to health check
- **ultrabridge-caldav.AC1.2 Failure:** Container exits with clear error message if MariaDB is unreachable or `.dbenv` is missing/malformed
- **ultrabridge-caldav.AC1.3 Success:** All operations produce structured log entries with request IDs, configurable level/format/targets

### ultrabridge-caldav.AC2: CalDAV server with VTODO collection
- **ultrabridge-caldav.AC2.1 Success:** `/.well-known/caldav` returns redirect to CalDAV prefix
- **ultrabridge-caldav.AC2.2 Success:** PROPFIND on collection returns `supported-calendar-component-set` containing `VTODO`
- **ultrabridge-caldav.AC2.3 Success:** Collection display name matches `UB_CALDAV_COLLECTION_NAME` config value
- **ultrabridge-caldav.AC2.4 Success:** CTag changes when any task is created, modified, or deleted
- **ultrabridge-caldav.AC2.5 Failure:** PUT with a VEVENT (not VTODO) is rejected

### ultrabridge-caldav.AC3: Bidirectional task sync
- **ultrabridge-caldav.AC3.1 Success:** Task created on Supernote device appears as VTODO in CalDAV client on next sync
- **ultrabridge-caldav.AC3.2 Success:** Task completed on Supernote device shows STATUS=COMPLETED in CalDAV with correct completion timestamp (from `last_modified`, not `completed_time`)
- **ultrabridge-caldav.AC3.3 Success:** Task created via CalDAV client (PUT with VTODO) appears in `t_schedule_task` with correct field mapping, MD5 task ID, and `user_id`
- **ultrabridge-caldav.AC3.4 Success:** Task completed via CalDAV client (STATUS=COMPLETED) writes `status=completed` and updates `last_modified`
- **ultrabridge-caldav.AC3.5 Success:** After CalDAV write, socket.io STARTSYNC message is sent and device syncs within seconds
- **ultrabridge-caldav.AC3.6 Failure:** If socket.io is unreachable, DB write still succeeds and warning is logged (graceful degradation)
- **ultrabridge-caldav.AC3.7 Success:** Full round-trip: create task on CalDAV → appears on device → modify on device → CalDAV client sees update on next sync
- **ultrabridge-caldav.AC3.8 Success:** Tier 2 fields (DESCRIPTION, PRIORITY) are stored in DB and round-trip through CalDAV
- **ultrabridge-caldav.AC3.9 Edge:** Task with no due date (`due_time=0`) renders as VTODO with no DUE property
- **ultrabridge-caldav.AC3.10 Edge:** `UB_DUE_TIME_MODE=date_only` strips time component from DUE on both read and write
- **ultrabridge-caldav.AC3.11 Edge:** `UB_DUE_TIME_MODE=preserve` round-trips full timestamp including time component

### ultrabridge-caldav.AC4: Minimal web UI
- **ultrabridge-caldav.AC4.1 Success:** Web browser can view a list of all non-deleted tasks
- **ultrabridge-caldav.AC4.2 Success:** Web browser can create a new task (title, optional due date) and it appears in the list
- **ultrabridge-caldav.AC4.3 Success:** Web browser can mark a task complete and status updates in the list
- **ultrabridge-caldav.AC4.4 Success:** Logs tab shows live log entries via websocket with level filtering

### ultrabridge-caldav.AC5: Simple auth
- **ultrabridge-caldav.AC5.1 Success:** Valid Basic Auth credentials grant access to CalDAV and web UI
- **ultrabridge-caldav.AC5.2 Failure:** Missing or invalid credentials return 401 with generic message (no credential hints)

## Glossary

- **CalDAV**: A standard protocol (RFC 4791) for sharing calendar and task data over HTTP. Extends WebDAV. Used by calendar and task-manager apps to subscribe to remote collections.
- **VTODO**: The iCalendar component type representing a task or to-do item, as defined in RFC 5545. Distinct from VEVENT (calendar event) and VJOURNAL (journal entry).
- **VEVENT**: An iCalendar component type representing a calendar event (not a task). UltraBridge rejects PUT requests containing VEVENTs.
- **iCalendar**: A text-based data format (`.ics`) for representing calendar objects, defined in RFC 5545. Contains one or more components such as VTODO or VEVENT.
- **PROPFIND**: An HTTP method defined by WebDAV used to retrieve properties of a resource or collection. CalDAV clients use it to discover collection metadata and list objects.
- **REPORT calendar-multiget**: A WebDAV REPORT request type used by CalDAV clients to fetch multiple calendar objects by UID in a single request.
- **CTag (collection tag)**: A server-side token representing the current state of an entire CalDAV collection. When the CTag changes, clients know to re-fetch the collection. UltraBridge derives it from `MAX(last_modified)` across all tasks.
- **ETag (entity tag)**: A per-object token representing the current version of a single calendar object. CalDAV clients use ETags to detect changes and avoid unnecessary re-fetches.
- **`/.well-known/caldav`**: A standardized URL path (RFC 6764) that CalDAV clients probe first to discover the actual CalDAV endpoint on a server.
- **`emersion/go-webdav`**: An open-source Go library implementing WebDAV and CalDAV server and client logic. UltraBridge implements its `caldav.Backend` interface to provide CalDAV functionality.
- **`caldav.Backend`**: The interface defined by `emersion/go-webdav` that a CalDAV server implementation must satisfy. Covers operations like listing, fetching, creating, and deleting calendar objects.
- **socket.io / Engine.IO**: A real-time event messaging library. Ratta's private cloud uses it to push sync notifications to connected Supernote devices. Engine.IO v3 is the underlying transport layer. UltraBridge connects as a client to send `STARTSYNC` messages.
- **STARTSYNC**: A proprietary Ratta message type sent over socket.io that instructs a connected Supernote device to immediately pull updated task data from the server.
- **`t_schedule_task`**: The MariaDB table in the Supernote Private Cloud database that stores task records. This is the data source UltraBridge reads from and writes to.
- **`u_user`**: The MariaDB table storing Supernote user accounts. UltraBridge queries it at startup to auto-discover the single user's ID.
- **MD5 task ID**: The convention used by Supernote devices to generate `task_id` values: an MD5 hash of the task title concatenated with the creation timestamp in milliseconds. UltraBridge replicates this convention for tasks created via CalDAV.
- **Snowflake ID**: A class of 64-bit integer IDs that encode a timestamp and node identifier, designed for distributed systems. Used for some ID fields in the Supernote schema (not task IDs, which are MD5).
- **Basic Auth**: HTTP authentication scheme (RFC 7617) where credentials are base64-encoded and sent in the `Authorization` request header. UltraBridge uses this for both CalDAV and web UI access.
- **bcrypt**: A password hashing algorithm. UltraBridge stores only a bcrypt hash of the configured password, never the plaintext.
- **`lumberjack`**: A Go library for log file rotation. Handles size-based rotation and retention of old log files.
- **`slog`**: Go's standard library structured logging package (`log/slog`), introduced in Go 1.21. Produces key-value structured log entries in JSON or text format.
- **Syslog / Graylog**: Log aggregation targets. UltraBridge can forward structured log entries to a syslog-compatible endpoint (including Graylog's GELF-over-UDP input) via `UB_LOG_SYSLOG_ADDR`.
- **DAVx5**: An open-source Android app that synchronizes CalDAV (and CardDAV) accounts with Android's contacts and calendar providers. A primary target CalDAV client for UltraBridge.
- **OpenTasks**: An open-source Android task manager app that integrates with DAVx5 to surface CalDAV VTODO collections.
- **NPM (Nginx Proxy Manager)**: The reverse proxy used in the Supernote Private Cloud deployment to handle TLS termination and external routing.
- **Tier 2 / Tier 3 fields**: UltraBridge's internal classification of VTODO properties. Tier 2 fields (DESCRIPTION, PRIORITY) are stored in existing DB columns and round-trip through CalDAV, even though the device UI ignores them. Tier 3 fields (categories, attachments, etc.) are silently dropped in v1.
- **`UB_DUE_TIME_MODE`**: A UltraBridge configuration option controlling how task due dates are handled. `preserve` stores full timestamps; `date_only` strips the time component to match what the Supernote device UI displays.

## Architecture

UltraBridge is a single Go binary with four internal components, deployed as a Docker container on the same network as the Supernote Private Cloud stack.

```
                    ┌─────────────────────────────────────┐
                    │           UltraBridge                │
                    │                                      │
  CalDAV clients ──>│  CalDAV Handler (go-webdav)          │
                    │    │                                 │
  Web browser ─────>│  Web UI Handler (net/http)           │
                    │    │         │                       │
                    │    │     Log Viewer (websocket tail)  │
                    │    │                                 │
                    │    └──> Task Store (DB layer)         │──> MariaDB (t_schedule_task)
                    │           │                          │
                    │           └──> Sync Notifier          │──> Socket.io (port 18072)
                    │                                      │
                    │  Auth Middleware (Basic Auth, bcrypt)  │
                    │  Structured Logging (slog + lumberjack)│
                    └─────────────────────────────────────┘
```

**CalDAV Handler:** `emersion/go-webdav` CalDAV server with a custom `caldav.Backend` implementation. Exposes a single VTODO-only collection at `/caldav/tasks/` with `/.well-known/caldav` discovery. CalDAV clients discover, subscribe, and sync tasks through standard RFC 4791 protocol.

**Web UI Handler:** Minimal `net/http` handler serving a simple HTML/JS page for task list, create, and complete operations. Includes a Logs tab that tails recent log entries via websocket with level filtering.

**Task Store:** Database access layer that reads/writes `t_schedule_task` in the existing MariaDB. Handles all field mapping between VTODO and Supernote's schema, generates MD5 task IDs for new tasks, and manages the quirks (e.g., `lastModified` is the actual completion time, `completedTime` is misleadingly the creation time).

**Sync Notifier:** Socket.io client (Engine.IO v3) that connects to Ratta's socket.io service on port 18072 and sends `STARTSYNC` messages after any write operation. This triggers connected Supernote devices to sync immediately.

**Auth Middleware:** HTTP middleware wrapping both CalDAV and Web UI handlers. Validates Basic Auth credentials against a bcrypt hash from configuration.

**Structured Logging:** `log/slog` with JSON output, request ID tracing, and multi-target output (stdout, rotating file via `lumberjack`, syslog/Graylog). All operations log structured events with source component, operation, and relevant IDs.

### Data Flow: CalDAV Write (client → device)

1. CalDAV client sends `PUT /caldav/tasks/{uid}.ics` with a VTODO
2. Auth middleware validates Basic Auth
3. CalDAV Handler parses iCalendar, calls `Backend.PutCalendarObject()`
4. Task Store maps VTODO fields → `t_schedule_task` columns (see Field Mapping below)
5. `INSERT` or `UPDATE` into `t_schedule_task`
6. Sync Notifier sends `42["ServerMessage","{...STARTSYNC...}"]` via socket.io
7. Device receives push, calls `POST /api/file/schedule/task/all`, sees the change

### Data Flow: CalDAV Read (device → client)

1. CalDAV client sends `PROPFIND` or `REPORT calendar-multiget`
2. Auth middleware validates Basic Auth
3. CalDAV Handler calls `Backend.ListCalendarObjects()` or `Backend.QueryCalendarObjects()`
4. Task Store queries `SELECT * FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'`
5. Each row is mapped to a VTODO and returned with an ETag

Reads are stateless — no caching, no background polling. The CalDAV client's sync interval (typically 5-15 minutes) determines how quickly device changes appear.

### Field Mapping: VTODO ↔ t_schedule_task

| VTODO Property | DB Column | Direction | Notes |
|---|---|---|---|
| `UID` | `task_id` | Bidirectional | Used directly as CalDAV UID. MD5 hash format. |
| `SUMMARY` | `title` | Bidirectional | |
| `STATUS` | `status` | Bidirectional | `NEEDS-ACTION` ↔ `needsAction`, `COMPLETED` ↔ `completed` |
| `DUE` | `due_time` | Bidirectional | ms UTC timestamp. Configurable: preserve time or date-only (see below). 0 = no due date. |
| `COMPLETED` | `last_modified` | Read | Only when `status = completed`. Do NOT use `completed_time`. |
| `LAST-MODIFIED` | `last_modified` | Read | ms UTC timestamp / 1000. |
| `DESCRIPTION` | `detail` | Bidirectional | Tier 2: stored in DB, device ignores. |
| `PRIORITY` | `importance` | Bidirectional | Tier 2: stored in DB, device ignores. |
| `URL` | `links` | Read-only | Exposed as `supernote://note/{fileId}/page/{page}`. Tasks created via CalDAV have no links. |

**Tier 2 fields** (description, priority, recurrence): Written to existing DB columns even though the Supernote device UI ignores them. CalDAV clients see them on round-trip.

**Tier 3 fields** (categories, attachments, percent-complete, alarms, geo-location): Silently dropped in v1. v2 should add an `ultrabridge_task_extra` sidecar table storing the full iCalendar blob alongside each `task_id` for perfect round-trip fidelity.

**DUE time handling:** Configurable via `UB_DUE_TIME_MODE`:
- `preserve` (default): Store and return the full timestamp. Lossless.
- `date_only`: Strip time component on writes, expose as `DATE` (not `DATE-TIME`) on reads. Matches what the Supernote device UI displays.

**New task ID generation:** `MD5(title + creation_timestamp_ms)` — matches the convention used by Supernote devices.

**ETag generation:** MD5 of concatenated `task_id + title + status + due_time + last_modified`. Changes when any synced field changes.

**CTag (collection tag):** `MAX(last_modified)` from `t_schedule_task`. Changes when any task changes, signaling CalDAV clients to do a full sync.

### Socket.io Push Protocol

After any write to `t_schedule_task`, the Sync Notifier sends:

```json
42["ServerMessage","{\"code\":\"200\",\"timestamp\":<now_ms>,\"msgType\":\"FILE-SYN\",\"data\":[{\"messageType\":\"STARTSYNC\",\"equipmentNo\":\"ultrabridge\",\"timestamp\":<now_ms>}]}"]
```

The device acknowledges with `42["ClientMessage","Received"]` and initiates a task sync.

Connection parameters: Engine.IO v3, WebSocket transport, connecting to `ws://supernote-service:8080/socket.io/`.

### Configuration

Auto-discovered at startup (not user-configured):
- `user_id` — `SELECT id FROM u_user LIMIT 1`
- DB credentials — read from Supernote's existing `.dbenv` file via `UB_SUPERNOTE_DBENV_PATH`
- Socket.io URL — defaults to `ws://supernote-service:8080/socket.io/`

User-configured (`.ultrabridge.env`):
- `UB_USERNAME` / `UB_PASSWORD_HASH` — CalDAV/web auth credentials (bcrypt)
- `UB_CALDAV_COLLECTION_NAME` — display name (default: "Supernote Tasks")
- `UB_DUE_TIME_MODE` — `preserve` or `date_only` (default: `preserve`)
- `UB_LOG_LEVEL` — `debug`, `info`, `warn`, `error` (default: `info`)
- `UB_LOG_FORMAT` — `json` or `text` (default: `json`)
- `UB_LOG_FILE` — path for file logging (optional)
- `UB_LOG_FILE_MAX_MB` — rotation size (default: 50)
- `UB_LOG_FILE_MAX_AGE_DAYS` — retention (default: 30)
- `UB_LOG_FILE_MAX_BACKUPS` — rotated files kept (default: 5)
- `UB_LOG_SYSLOG_ADDR` — syslog/Graylog target (optional, e.g., `udp://graylog:1514`)
- `UB_LISTEN_ADDR` — listen address (default: `:8443`)
- `UB_WEB_ENABLED` — enable web UI (default: `true`)

### Deployment

Docker container joining the existing Supernote Private Cloud compose network:

```yaml
ultrabridge:
  image: ultrabridge:latest
  container_name: ultrabridge
  ports:
    - "8443:8443"
  env_file:
    - .ultrabridge.env
  volumes:
    - ./sndata/logs/ultrabridge:/var/log/ultrabridge
    - ./.dbenv:/run/secrets/dbenv:ro
  depends_on:
    - mariadb
  restart: unless-stopped
```

External access via reverse proxy (e.g., NPM) or direct port mapping. CalDAV clients connect to `https://<host>/caldav/`.

## Existing Patterns

This is a greenfield Go project — there is no existing codebase to follow patterns from.

The design follows patterns from the Supernote Private Cloud reference documentation (`/mnt/supernote/PRIVATE_CLOUD_REFERENCE.md`):
- Task field schema and mapping conventions from Section 5
- Socket.io FILE-SYN message format from Section 7
- MD5 task ID generation convention observed in device traffic
- `completedTime` vs `lastModified` quirk documented in task completion protocol

The `emersion/go-webdav` library defines the `caldav.Backend` interface pattern — all CalDAV operations are implemented by satisfying this interface.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Project Scaffolding
**Goal:** Initialize Go project with module, dependencies, directory structure, Dockerfile, and a health-check endpoint.

**Components:**
- `go.mod` with module path and dependencies (`go-webdav`, `go-sql-driver/mysql`, `lumberjack`, `zishang520/socket.io`)
- `cmd/ultrabridge/main.go` — entry point, config loading, HTTP server startup
- `internal/config/` — configuration struct, env parsing, `.dbenv` reader
- `Dockerfile` — multi-stage build
- `docker-compose.override.yml` — adds ultrabridge to existing stack

**Dependencies:** None (first phase)

**Done when:** `go build` succeeds, Docker image builds, container starts and responds to `GET /health` with 200
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Database Layer
**Goal:** Connect to MariaDB, read/write tasks, auto-discover user ID.

**Components:**
- `internal/db/` — connection pool setup, auto-discovery (`SELECT id FROM u_user LIMIT 1`)
- `internal/taskstore/` — CRUD operations on `t_schedule_task`: list, get, create, update, delete
- `internal/taskstore/mapping.go` — field mapping logic, MD5 ID generation, ETag computation, CTag computation

**Dependencies:** Phase 1

**Covers:** `ultrabridge-caldav.AC3.1`, `ultrabridge-caldav.AC3.2`, `ultrabridge-caldav.AC3.3`, `ultrabridge-caldav.AC3.4`

**Done when:** Task store can list, create, update, and delete tasks in MariaDB. Auto-discovery of user ID works. Unit tests verify field mapping including the `completedTime`/`lastModified` quirk and MD5 ID generation.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: CalDAV Server
**Goal:** Expose tasks as VTODO objects via CalDAV protocol.

**Components:**
- `internal/caldav/` — `caldav.Backend` implementation wrapping Task Store
- `internal/caldav/vtodo.go` — VTODO ↔ task row conversion (uses field mapping from Phase 2)
- CalDAV collection configuration: VTODO-only, configurable display name, CTag
- `/.well-known/caldav` redirect
- HTTP route mounting at configured prefix

**Dependencies:** Phase 2

**Covers:** `ultrabridge-caldav.AC2.1`, `ultrabridge-caldav.AC2.2`, `ultrabridge-caldav.AC2.3`, `ultrabridge-caldav.AC2.4`, `ultrabridge-caldav.AC2.5`

**Done when:** CalDAV client (e.g., `curl` with PROPFIND/PUT/DELETE) can discover the collection, list tasks, create a task, update it, and delete it. Tasks round-trip correctly through VTODO ↔ DB mapping.
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: Auth Middleware
**Goal:** Protect CalDAV and Web UI endpoints with Basic Auth.

**Components:**
- `internal/auth/` — middleware that validates Basic Auth against bcrypt hash from config
- Applied to CalDAV handler and Web UI handler
- `/.well-known/caldav` redirect is auth-protected (CalDAV clients send credentials on discovery)

**Dependencies:** Phase 3

**Covers:** `ultrabridge-caldav.AC5.1`, `ultrabridge-caldav.AC5.2`

**Done when:** Unauthenticated requests return 401. Valid credentials pass through. Invalid credentials are rejected. Tests verify both paths.
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: Socket.io Sync Notifier
**Goal:** Push STARTSYNC to connected Supernote devices after task writes.

**Components:**
- `internal/sync/` — Engine.IO v3 client connecting to `ws://supernote-service:8080/socket.io/`
- Sends `42["ServerMessage","{...STARTSYNC...}"]` after create/update/delete
- Handles connection lifecycle: connect, reconnect on failure, keepalive (respond to `ratta_ping`)
- Graceful degradation: if socket.io is unreachable, log warning and continue (DB write still succeeds)

**Dependencies:** Phase 2 (triggered after task store writes)

**Covers:** `ultrabridge-caldav.AC3.5`, `ultrabridge-caldav.AC3.6`

**Done when:** Creating a task via CalDAV triggers a STARTSYNC message visible in a tcpdump capture. Connection resilience tested (service restarts, temporary disconnects).
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: Structured Logging
**Goal:** Comprehensive, multi-target structured logging with request tracing.

**Components:**
- `internal/logging/` — slog handler configuration, multi-target writer (stdout, file, syslog)
- `lumberjack` integration for file rotation
- Request ID middleware (UUID per request, carried through all log entries)
- Logging integrated into all components: auth, CalDAV handler, task store, sync notifier

**Dependencies:** Phase 1 (can be wired incrementally from Phase 2 onward, but fully configured here)

**Covers:** `ultrabridge-caldav.AC1.3`

**Done when:** All operations produce structured log entries with request IDs. File rotation works. Syslog target is configurable. Log level filtering works.
<!-- END_PHASE_6 -->

<!-- START_PHASE_7 -->
### Phase 7: Web UI
**Goal:** Minimal web interface for task verification and log viewing.

**Components:**
- `internal/web/` — HTTP handlers for task list page, create/complete actions
- `internal/web/static/` — HTML/CSS/JS (can be embedded via `embed` package)
- Logs tab: websocket endpoint streaming recent log entries with level filtering
- Routes: `GET /` (task list), `POST /tasks` (create), `POST /tasks/{id}/complete`, `GET /ws/logs` (log stream)

**Dependencies:** Phase 2 (task store), Phase 4 (auth), Phase 6 (logging)

**Covers:** `ultrabridge-caldav.AC4.1`, `ultrabridge-caldav.AC4.2`, `ultrabridge-caldav.AC4.3`

**Done when:** Web browser can list tasks, create a task, complete a task, and view live logs. Auth required.
<!-- END_PHASE_7 -->

<!-- START_PHASE_8 -->
### Phase 8: Integration Testing & Deployment
**Goal:** End-to-end verification with real CalDAV clients and Supernote device.

**Components:**
- Integration test suite: create task via CalDAV → verify in DB → verify device syncs → complete on device → verify CalDAV client sees completion
- Docker Compose configuration finalized
- Documentation: README with setup instructions, CalDAV client configuration guides

**Dependencies:** All previous phases

**Covers:** `ultrabridge-caldav.AC3.7`, `ultrabridge-caldav.AC1.1`, `ultrabridge-caldav.AC1.2`

**Done when:** Full bidirectional sync verified with at least one CalDAV client and the Supernote Nomad. Docker deployment documented and reproducible.
<!-- END_PHASE_8 -->

## Additional Considerations

**v2: Full iCalendar fidelity.** Add an `ultrabridge_task_extra` table that stores the complete iCalendar blob alongside each `task_id`. This enables perfect round-trip for CalDAV clients that set fields beyond what Supernote supports (categories, attachments, multiple alarms, percent-complete, etc.). The Task Store would merge DB fields with the stored blob on read, and extract/store the blob on write. This preserves all client data without polluting the Supernote schema.

**Fallback write path.** If direct DB writes cause issues (e.g., Ratta's server caches task state and doesn't see external changes), the Task Store can be refactored to call Ratta's REST API instead (`POST /api/file/schedule/task`, `PUT /api/file/schedule/task/list`, `DELETE /api/file/schedule/task/{id}`). This requires maintaining a valid JWT via the device login flow. The Task Store interface isolates this decision — callers don't need to change.

**Error handling.** Socket.io disconnects are non-fatal — DB writes succeed regardless, and the device will pick up changes on its next poll. DB connection failures return 503 to CalDAV clients with structured error logging. Auth failures return 401 with generic messages (no credential hints).

**Task groups.** The `task_list_id` field exists in the schema but has been empty in all observed traffic. UltraBridge v1 ignores it. If task groups become relevant, they could map to multiple CalDAV collections (one per group).
