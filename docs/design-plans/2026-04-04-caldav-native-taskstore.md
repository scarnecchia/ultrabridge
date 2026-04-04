# CalDAV-Native Task Store Design

## Summary

UltraBridge currently reads and writes tasks directly against Supernote's MariaDB database, which limits CalDAV fidelity to the fields Supernote's schema exposes and tightly couples the service to a single vendor's storage layout. This design makes UltraBridge the authoritative owner of task data by migrating to a local SQLite database that stores tasks with full RFC 5545 VTODO fidelity, including round-tripping arbitrary iCalendar properties that Supernote has no concept of. The CalDAV backend and web UI continue to use the same `TaskStore` interface they consume today — only the backing implementation changes.

With UltraBridge owning the store, a new sync engine reconciles the local database against external devices through pluggable adapters. The first adapter targets the Supernote Private Cloud REST API, replacing direct DB writes with API calls and handling Supernote-specific quirks (challenge-response JWT auth, MD5 task IDs, the inverted completedTime/lastModified field meanings) in one isolated package. The engine applies a UltraBridge-wins conflict policy and is designed so future adapters for other e-ink devices can be registered without touching the core task store or CalDAV layer. An initial migration imports existing Supernote tasks into the new store on first run.

## Definition of Done

1. **UltraBridge owns task storage** — tasks live in a new SQLite database with full RFC 5545 VTODO support. CalDAV clients get perfect round-trip fidelity for all VTODO properties (Tier 1, 2, and 3). The current direct-MariaDB `TaskStore` is replaced entirely.

2. **Supernote sync adapter** — a poll-and-write adapter syncs tasks between UltraBridge's SQLite and SPC via the REST API (`/api/file/schedule/task/*`). Configurable sync interval. UltraBridge is authoritative on conflict. No regression in Supernote device capability.

3. **Web UI sync control** — a "Sync Now" button triggers an immediate Supernote sync cycle. Sync status is visible.

4. **Clean migration** — on first run with an existing SPC installation, tasks are imported from Supernote into the new store. No dual-mode configuration.

5. **Adapter-ready architecture** — the sync adapter interface is designed so future vendor adapters (Boox, Viwoods, etc.) can be added without changes to the core task store or CalDAV backend.

## Acceptance Criteria

### caldav-native-taskstore.AC1: UltraBridge owns task storage
- **caldav-native-taskstore.AC1.1 Success:** CalDAV client creates task via PUT, task persists in SQLite and is retrievable via GET
- **caldav-native-taskstore.AC1.2 Success:** CalDAV client updates task (title, status, due date), changes persist and ETag updates
- **caldav-native-taskstore.AC1.3 Success:** CalDAV client deletes task, task is soft-deleted (not returned in LIST, still in DB)
- **caldav-native-taskstore.AC1.4 Success:** CalDAV client sets Tier 3 properties (RRULE, VALARM, CATEGORIES), they round-trip perfectly on next GET
- **caldav-native-taskstore.AC1.5 Success:** Task created on Supernote (no ical_blob) renders as valid VTODO with correct Tier 1/2 fields
- **caldav-native-taskstore.AC1.6 Edge:** CTag changes when any task is created, modified, or deleted
- **caldav-native-taskstore.AC1.7 Edge:** MaxLastModified returns 0 for empty store

### caldav-native-taskstore.AC2: Supernote sync adapter
- **caldav-native-taskstore.AC2.1 Success:** Task created in UltraBridge appears on Supernote device after sync cycle + STARTSYNC push
- **caldav-native-taskstore.AC2.2 Success:** Task completed in UltraBridge sets status=completed and updates lastModified on SPC side
- **caldav-native-taskstore.AC2.3 Success:** Task created on Supernote device appears in UltraBridge after sync cycle
- **caldav-native-taskstore.AC2.4 Success:** Task edited on Supernote device (title change) reflected in UltraBridge after sync
- **caldav-native-taskstore.AC2.5 Success:** Conflict (both sides edited) resolves with UltraBridge version winning and pushing back to SPC
- **caldav-native-taskstore.AC2.6 Success:** Adapter authenticates via SPC challenge-response, re-authenticates on 401
- **caldav-native-taskstore.AC2.7 Failure:** SPC unreachable — sync cycle logs warning, retries next interval, task store continues working
- **caldav-native-taskstore.AC2.8 Failure:** SPC auth failure (wrong password) — logged as error, sync disabled until next restart or config change
- **caldav-native-taskstore.AC2.9 Edge:** Task deleted on SPC side (hard delete) detected and soft-deleted locally

### caldav-native-taskstore.AC3: Web UI sync control
- **caldav-native-taskstore.AC3.1 Success:** Tasks tab shows sync status: last sync time, next scheduled sync, adapter state
- **caldav-native-taskstore.AC3.2 Success:** "Sync Now" button triggers immediate sync cycle, status updates on completion
- **caldav-native-taskstore.AC3.3 Failure:** Sync in progress — button disabled or shows in-progress indicator, no double-trigger

### caldav-native-taskstore.AC4: Clean migration
- **caldav-native-taskstore.AC4.1 Success:** First run with empty task DB and reachable SPC imports all non-deleted tasks, creates sync map entries
- **caldav-native-taskstore.AC4.2 Success:** Subsequent starts with populated task DB skip import
- **caldav-native-taskstore.AC4.3 Success:** First run without SPC (standalone mode) starts with empty store, no error

### caldav-native-taskstore.AC5: Adapter-ready architecture
- **caldav-native-taskstore.AC5.1 Success:** Sync engine accepts mock adapter implementing DeviceAdapter interface, runs full sync cycle
- **caldav-native-taskstore.AC5.2 Success:** Registering/unregistering an adapter requires no changes to task store, CalDAV backend, or web handler
- **caldav-native-taskstore.AC5.3 Success:** Disabling Supernote adapter (UB_SN_SYNC_ENABLED=false) leaves task store and CalDAV fully functional

## Glossary

- **RFC 5545**: The IETF standard defining the iCalendar data format. Specifies the VCALENDAR and VTODO object structures used to represent calendar tasks.
- **VTODO**: The iCalendar component type representing a task or to-do item. Defined in RFC 5545.
- **VCALENDAR**: The iCalendar container object that wraps one or more components such as VTODO or VEVENT.
- **iCal blob (`ical_blob`)**: The full serialized VCALENDAR text stored verbatim in SQLite, enabling round-trip fidelity for properties that have no corresponding structured column.
- **CalDAV**: A calendar access protocol (RFC 4791) built on WebDAV, used by clients such as Apple Reminders and Thunderbird to sync tasks and events over HTTP.
- **ETag**: An HTTP header value used to detect whether a resource has changed since it was last fetched. CalDAV clients use ETags to detect conflicts before overwriting.
- **CTag**: A CalDAV collection tag — a server-side opaque string that changes whenever any task in the collection is created, modified, or deleted. Clients poll it to decide whether to re-sync.
- **STARTSYNC**: A proprietary Engine.IO event pushed by UltraBridge to the Supernote device to tell it to initiate a sync cycle. Already implemented in the existing `sync.Notifier`.
- **Engine.IO**: A transport protocol (used here at v3) that underlies Socket.IO. UltraBridge uses it to push real-time notifications to the Supernote device.
- **SPC (Supernote Private Cloud)**: The self-hosted server application that Supernote devices sync to. Exposes a REST API used by this design's adapter.
- **WAL mode**: Write-Ahead Logging, a SQLite journal mode that improves concurrent read performance and crash resilience. Used by UltraBridge for both its notes and task SQLite databases.
- **Challenge-response JWT auth**: The SPC login flow where the client fetches a server-generated random code, SHA-256 hashes the password with that code, and submits the result to receive a JWT for subsequent requests.
- **JWT (JSON Web Token)**: A signed token returned by SPC after login, passed as the `x-access-token` header on all subsequent API requests.
- **task_sync_map**: The SQLite join table that records the mapping between a local task ID and its counterpart remote ID on a given adapter, along with the remote ETag seen at last sync. Used to detect which side changed since the last cycle.
- **sync_state**: The SQLite table that stores per-adapter sync metadata, specifically the last sync token and timestamp returned by the remote.
- **DeviceAdapter**: The Go interface defined in this design that all device sync adapters must implement. Provides `Pull` (fetch remote changes) and `Push` (apply local changes to remote).
- **RemoteTask**: The adapter-neutral struct used to pass task data across the adapter boundary, decoupling the sync engine from any vendor's wire format.
- **Tier 1/2/3 properties**: An internal classification of VTODO fields by how faithfully Supernote's schema can represent them. Tier 1 fields (title, status, due) map cleanly; Tier 3 fields (RRULE, VALARM, CATEGORIES) have no Supernote equivalent and require blob storage.
- **RRULE**: An iCalendar recurrence rule property specifying how a task repeats (e.g., daily, weekly). A Tier 3 property with no Supernote counterpart.
- **VALARM**: An iCalendar component that defines an alarm or reminder attached to a VTODO. A Tier 3 property.
- **Soft delete**: Marking a record as deleted (`is_deleted = 'Y'`) rather than removing the row, preserving history and enabling sync tombstones.
- **Idempotent migrations**: Schema migration statements written as `CREATE TABLE IF NOT EXISTS` so they can be re-run on every startup without error or data loss.
- **UB-wins conflict policy**: The conflict resolution rule applied by the sync engine: when both UltraBridge and a remote device have modified the same task since the last sync, UltraBridge's version is kept and pushed back to the device.
- **fsnotify**: A Go library for watching filesystem events (file creation, modification, deletion). Mentioned as the mechanism for a future passive adapter mode.
- **bcrypt**: A password hashing function used for UltraBridge's own Basic Auth credential (`UB_PASSWORD_HASH`). Cannot be reversed, which is why a separate plaintext `UB_SN_PASSWORD` is needed for SPC's challenge-response flow.

## Architecture

UltraBridge becomes the authority for task storage. Tasks live in a new SQLite database with full RFC 5545 VTODO fidelity. A sync engine reconciles UltraBridge's local store with vendor devices via pluggable adapters. This design ships the Supernote adapter (outbound, REST API); the architecture accommodates future inbound adapters (e.g., a WebDAV server receiving Boox files) and passive adapters (filesystem watchers) without changes to the core.

### Data Flow

```
CalDAV clients ←→ CalDAV Backend ←→ SQLite Task Store ←→ Sync Engine ←→ Supernote Adapter ←→ SPC REST API
                                                                      ←→ [Future Adapter]
Web UI ←→ caldav.TaskStore interface ←→ SQLite Task Store
          (unchanged)
```

**CalDAV read path:** Load task from SQLite. If `ical_blob` exists, deserialize it and overlay DB-authoritative fields (title, status, due, completed, last_modified). If no blob (Supernote-originated task), build VTODO from structured fields as today. Return full VTODO with all Tier 1/2/3 properties.

**CalDAV write path:** Parse incoming VTODO. Extract Tier 1/2 fields into structured columns. Store full VCALENDAR as `ical_blob`. Trigger STARTSYNC via Engine.IO notifier (unchanged).

**Sync path (Supernote adapter):** On interval or manual trigger, sync engine runs a reconciliation cycle. Pull remote tasks from SPC REST API, diff against local state via `task_sync_map`, resolve conflicts (UB authoritative), apply changes bidirectionally.

### Core Components

**SQLite Task Store** (`internal/taskdb/`) — new package, follows `notedb` pattern. Implements `caldav.TaskStore` interface (6 methods: List, Get, Create, Update, Delete, MaxLastModified). SQLite in WAL mode, MaxOpenConns=1, idempotent migrations.

**Schema:**

```sql
CREATE TABLE tasks (
    task_id     TEXT PRIMARY KEY,
    title       TEXT,
    detail      TEXT,
    status      TEXT NOT NULL DEFAULT 'needsAction',
    importance  TEXT,
    due_time    INTEGER NOT NULL DEFAULT 0,
    completed_time INTEGER NOT NULL DEFAULT 0,
    last_modified  INTEGER NOT NULL DEFAULT 0,
    recurrence  TEXT,
    is_reminder_on TEXT NOT NULL DEFAULT 'N',
    links       TEXT,
    is_deleted  TEXT NOT NULL DEFAULT 'N',
    ical_blob   TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE sync_state (
    adapter_id      TEXT PRIMARY KEY,
    last_sync_token TEXT,
    last_sync_at    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE task_sync_map (
    task_id     TEXT NOT NULL REFERENCES tasks(task_id),
    adapter_id  TEXT NOT NULL,
    remote_id   TEXT NOT NULL,
    remote_etag TEXT,
    last_pushed_at  INTEGER NOT NULL DEFAULT 0,
    last_pulled_at  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (task_id, adapter_id)
);
CREATE INDEX idx_task_sync_map_remote ON task_sync_map(adapter_id, remote_id);
```

**Sync Engine** (`internal/tasksync/`) — adapter-agnostic reconciliation. Manages registered adapters, runs sync cycles on interval, exposes manual trigger and status.

**DeviceAdapter interface:**

```go
type DeviceAdapter interface {
    ID() string
    Start(ctx context.Context) error
    Stop() error
    Pull(ctx context.Context, since string) ([]RemoteTask, string, error)
    Push(ctx context.Context, changes []Change) error
}

type RemoteTask struct {
    RemoteID string
    Title    string
    Detail   string
    Status   string
    DueTime  int64
    // ... all mapped fields
    ETag     string // opaque hash for change detection
}

type Change struct {
    Type     ChangeType // Create, Update, Delete
    TaskID   string
    Remote   RemoteTask
}
```

The interface supports three adapter modes without encoding them:
- **Outbound** (Supernote): `Start` authenticates, `Pull` calls REST API, `Push` calls REST API
- **Inbound** (future): `Start` registers HTTP routes, `Pull` drains received-file queue, `Push` writes to synced folder
- **Passive** (fallback): `Start` sets up fsnotify, `Pull` returns detected files, `Push` writes to directory

**Supernote Adapter** (`internal/tasksync/supernote/`) — outbound adapter using SPC REST API (`/api/file/schedule/task/*`). Handles JWT auth (challenge-response login), task field mapping (Supernote quirks isolated here), and STARTSYNC push via the existing `sync.Notifier`.

### Adapter Authentication

SPC uses challenge-response JWT auth:
1. `POST /api/official/user/query/random/code` → `randomCode` + `timestamp`
2. SHA-256 hash password with challenge
3. `POST /api/official/user/account/login/equipment` → JWT (never expires for device login)
4. All subsequent requests: `x-access-token: {jwt}` header

Re-authenticate on 401.

### Conflict Resolution

UltraBridge is authoritative. During sync:
1. Pull remote tasks, compare against `task_sync_map` etags
2. Remote creates → import to local store
3. Remote updates → if local hasn't changed since last sync, apply remote update; if local has changed, local wins (push local version back)
4. Remote deletes → soft-delete locally
5. Local creates/updates/deletes → push to adapter

## Existing Patterns

This design follows established patterns from the UltraBridge codebase:

**SQLite database pattern** (`internal/notedb/`): `notedb.Open()` at `internal/notedb/db.go:13-30` opens SQLite with WAL mode, foreign keys, MaxOpenConns=1. Schema migrations in `internal/notedb/schema.go:10-90` use idempotent `CREATE TABLE IF NOT EXISTS`. The new `taskdb` package follows this exactly.

**Interface-based dependency injection**: `caldav.TaskStore` (6 methods at `internal/caldav/backend.go:16-23`) and `caldav.SyncNotifier` (`backend.go:26-28`) are interfaces consumed by the CalDAV backend and web handler. The new SQLite task store implements `TaskStore` with zero changes to consumers.

**Main.go wiring** (`cmd/ultrabridge/main.go`): Components are created and wired at startup. Currently `store := taskstore.New(database, userID)` at line 77. This changes to the new SQLite-backed store. `NewBackend` (line 124) and `web.NewHandler` (line 153) receive the store via the same interface.

**Config via env vars** (`internal/config/config.go:11-60`): All config uses `UB_` prefixed env vars loaded in `config.Load()`. New Supernote sync vars follow the same pattern.

**Install script prompts** (`install.sh:160-221`): Prompts for configuration values and generates `.ultrabridge.env`. New adapter config gets its own section in the prompt flow.

**Divergence: REST API client.** No existing code calls SPC REST endpoints. The Supernote adapter introduces this pattern. The closest analog is `processor.OCRClient` (`internal/processor/ocrclient.go:28-50`) which is an HTTP client with auth headers — similar shape but different target.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: SQLite Task Store
**Goal:** Replace MariaDB-backed task store with SQLite, implementing the existing `caldav.TaskStore` interface.

**Components:**
- `internal/taskdb/` — new package: `db.go` (Open, WAL mode, migrations), `schema.go` (tasks table DDL), `store.go` (TaskStore implementation)
- `internal/taskdb/store.go` — implements `caldav.TaskStore` interface (List, Get, Create, Update, Delete, MaxLastModified) against SQLite
- `internal/config/config.go` — add `UB_TASK_DB_PATH` env var
- `cmd/ultrabridge/main.go` — replace `taskstore.New(database, userID)` with `taskdb.Open()` + new store

**Dependencies:** None (first phase)

**Covers:** `caldav-native-taskstore.AC1.1`, `caldav-native-taskstore.AC1.2`, `caldav-native-taskstore.AC1.3`

**Done when:** CalDAV clients can create, read, update, and delete tasks via the new SQLite store. Web UI task operations work unchanged. All existing CalDAV tests pass against the new store.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: iCal Blob Round-Trip
**Goal:** Full RFC 5545 VTODO fidelity via iCal blob storage and overlay reads.

**Components:**
- `internal/taskdb/store.go` — add `ical_blob` column handling: store full VCALENDAR on write, overlay DB fields on read
- `internal/caldav/vtodo.go` — refactor read path: if blob exists, deserialize and overlay; if null, build from fields (backward-compatible for imported tasks)

**Dependencies:** Phase 1 (SQLite task store)

**Covers:** `caldav-native-taskstore.AC1.4`, `caldav-native-taskstore.AC1.5`

**Done when:** CalDAV client can set Tier 3 properties (RRULE, VALARM, CATEGORIES, etc.), and they round-trip perfectly. Tasks without blobs (Supernote-originated) still render correctly.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: Sync Engine
**Goal:** Adapter-agnostic reconciliation engine with configurable sync interval and manual trigger.

**Components:**
- `internal/tasksync/engine.go` — SyncEngine: adapter registration, sync loop (interval + manual trigger), reconciliation logic (pull/diff/resolve/push), status reporting
- `internal/tasksync/types.go` — DeviceAdapter interface, RemoteTask, Change, ChangeType, SyncStatus types
- `internal/taskdb/schema.go` — add `sync_state` and `task_sync_map` tables

**Dependencies:** Phase 1 (task store for local CRUD)

**Covers:** `caldav-native-taskstore.AC5.1`, `caldav-native-taskstore.AC5.2`

**Done when:** Sync engine can register an adapter, run reconciliation cycles, detect local/remote changes via sync map, and resolve conflicts with UB-wins policy. Tested with a mock adapter.
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: Supernote Adapter
**Goal:** Outbound adapter that syncs tasks with SPC via REST API.

**Components:**
- `internal/tasksync/supernote/adapter.go` — implements DeviceAdapter: JWT auth (challenge-response login), Pull (fetch all tasks, diff), Push (create/bulk-update/delete)
- `internal/tasksync/supernote/client.go` — HTTP client for SPC task endpoints: login, fetch groups, fetch tasks, create task, bulk update, delete
- `internal/tasksync/supernote/mapping.go` — field mapping between UB task model and SPC wire format (Supernote quirks: completedTime=creation, MD5 IDs, base64 links)
- `internal/config/config.go` — add `UB_SN_SYNC_ENABLED`, `UB_SN_SYNC_INTERVAL`, `UB_SN_API_URL`, `UB_SN_PASSWORD`

**Dependencies:** Phase 3 (sync engine)

**Covers:** `caldav-native-taskstore.AC2.1`, `caldav-native-taskstore.AC2.2`, `caldav-native-taskstore.AC2.3`, `caldav-native-taskstore.AC2.4`, `caldav-native-taskstore.AC2.5`, `caldav-native-taskstore.AC2.6`

**Done when:** Tasks created/modified/deleted in UltraBridge appear on Supernote device after sync. Tasks created/modified/deleted on device appear in UltraBridge after sync. JWT auth works. STARTSYNC triggers device pickup.
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: Migration
**Goal:** Auto-import existing Supernote tasks on first run.

**Components:**
- `internal/tasksync/supernote/migration.go` — reads all tasks from SPC REST API (or falls back to existing `taskstore.Store.List()` via MariaDB), inserts into SQLite, creates sync map entries
- `cmd/ultrabridge/main.go` — detect empty task DB + reachable SPC, run migration before starting sync engine

**Dependencies:** Phase 1 (task store), Phase 4 (Supernote adapter for REST-based import)

**Covers:** `caldav-native-taskstore.AC4.1`, `caldav-native-taskstore.AC4.2`, `caldav-native-taskstore.AC4.3`

**Done when:** Starting UltraBridge with an empty task DB and existing SPC installation imports all tasks. Subsequent starts skip import. Starting without SPC begins with empty store.
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: Web UI Sync Control
**Goal:** Sync status display and manual sync trigger in the web UI.

**Components:**
- `internal/web/handler.go` — add sync engine dependency to `NewHandler`, add `GET /sync/status` and `POST /sync/trigger` endpoints
- `internal/web/` templates — sync status panel on Tasks tab: last sync time, next scheduled sync, in-progress indicator, error display, "Sync Now" button

**Dependencies:** Phase 3 (sync engine status API), Phase 4 (Supernote adapter registered)

**Covers:** `caldav-native-taskstore.AC3.1`, `caldav-native-taskstore.AC3.2`

**Done when:** Web UI shows current sync status. "Sync Now" button triggers immediate cycle. Status updates after sync completes.
<!-- END_PHASE_6 -->

<!-- START_PHASE_7 -->
### Phase 7: Configuration & Deployment
**Goal:** Install script updates, documentation, adapter enable/disable workflow.

**Components:**
- `install.sh` — add Supernote sync section: SPC password prompt, sync interval, enable/disable. Support re-running to change adapter config post-deployment.
- `rebuild.sh` — no changes expected (already handles rebuild)
- `.ultrabridge.env.example` — add new env vars with documentation
- `README.md` — update Configuration section with new env vars, add adapter management documentation
- `cmd/ultrabridge/main.go` — MariaDB connection failure is non-fatal when `UB_SN_SYNC_ENABLED=false`

**Dependencies:** Phase 4 (adapter config), Phase 6 (web UI)

**Covers:** `caldav-native-taskstore.AC4.3`, `caldav-native-taskstore.AC5.3`

**Done when:** `install.sh` handles Supernote sync configuration. Re-running install can enable/disable sync. UltraBridge starts successfully without SPC when sync is disabled. README documents the configuration.
<!-- END_PHASE_7 -->

## Additional Considerations

**Future adapter modes:** The DeviceAdapter interface supports outbound (Supernote, this design), inbound (future WebDAV server receiving Boox files), and passive (filesystem watcher fallback) adapters without modification. Inbound adapters would need access to the HTTP mux to register routes — this can be provided via `Start` context when needed. Not built now.

**SPC endpoint migration direction:** This design introduces the first SPC REST API client in UltraBridge. The directional preference is to favor SPC REST endpoints over direct DB access for consistency. The notes pipeline's SPC catalog sync (`internal/processor/catalog.go`) is a candidate for future migration to REST, but is out of scope here.

**Credential separation:** SPC challenge-response auth requires the plaintext password (SHA-256 hashed with a server-provided challenge). UltraBridge's existing `UB_PASSWORD_HASH` is bcrypt and cannot be reversed. The Supernote adapter therefore needs a separate `UB_SN_PASSWORD` credential. If the user sets the same password for both UB auth and SPC, they are stored differently (bcrypt hash vs plaintext for challenge-response).
