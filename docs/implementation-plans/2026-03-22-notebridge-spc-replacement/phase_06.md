# NoteBridge Phase 6: CalDAV + Task Store

**Goal:** CalDAV clients can access tasks synced from the tablet.

**Architecture:** Transfer CalDAV backend and VTODO conversion unchanged from UltraBridge. Rewrite taskstore against syncdb's schedule_tasks table (same interface, new implementation). Wire CalDAV writes → event bus → Socket.IO push for tablet sync.

**Tech Stack:** Go 1.24, emersion/go-webdav (CalDAV), iCalendar/VTODO

**Scope:** Phase 6 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC7: CalDAV
- **AC7.1 Success:** Tasks synced from tablet appear as VTODOs via CalDAV
- **AC7.2 Success:** VTODO created via CalDAV client syncs to tablet on next sync
- **AC7.3 Success:** Task completion status round-trips: tablet ↔ CalDAV

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: TaskStore Against SyncDB

<!-- START_TASK_1 -->
### Task 1: Rewrite taskstore against syncdb

**Files:**
- Create: `/home/sysop/src/notebridge/internal/taskstore/store.go`
- Create: `/home/sysop/src/notebridge/internal/taskstore/model.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/taskstore/mapping.go` (from UB, unchanged)

**Implementation:**

**model.go** — Copy from UltraBridge unchanged. Task struct with all fields (TaskID, Title, Detail, Status, Importance, DueTime, CompletedTime, LastModified, Recurrence, IsReminderOn, Links, IsDeleted, TaskListID, UserID). Uses `sql.NullString` and `sql.NullInt64` for nullable fields.

**mapping.go** — Copy from UltraBridge unchanged. Helper functions: GenerateTaskID, ComputeETag, ComputeCTag, NullStr, SqlStr, MsToTime, TimeToMs, CalDAVStatus, SupernoteStatus, CompletionTime.

**store.go** — Rewrite implementation against syncdb's `schedule_tasks` table.

Constructor: `New(db *sql.DB, userID int64) *Store`

The interface the CalDAV backend depends on:
```go
type TaskStore interface {
    List(ctx context.Context) ([]Task, error)
    Get(ctx context.Context, taskID string) (*Task, error)
    Create(ctx context.Context, t *Task) error
    Update(ctx context.Context, t *Task) error
    Delete(ctx context.Context, taskID string) error
    MaxLastModified(ctx context.Context) (int64, error)
}
```

Key differences from UltraBridge's MariaDB implementation:
- Table name: `schedule_tasks` (not `t_schedule_task`)
- Column names match opennotecloud's schema: `task_id`, `user_id`, `task_list_id`, `title`, `detail`, `last_modified`, `recurrence`, `is_reminder_on`, `status`, `importance`, `due_time`, `completed_time`, `links`
- **Soft delete (definitive decision):** Use `is_deleted TEXT NOT NULL DEFAULT 'N'` column (added to Phase 1 Task 5 schema). Follow UltraBridge's proven pattern: `Delete()` sets `is_deleted='Y'`, `List()`/`Get()` filter `WHERE is_deleted='N'`. This is required for CalDAV sync correctness — CalDAV clients rely on CTag/ETag changes to detect deletions, and tombstones allow proper sync propagation. The device sync protocol handles its own deletion separately via `/api/file/schedule/task/{id}`.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: rewrite taskstore against syncdb schedule_tasks`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Port CalDAV backend and VTODO conversion

**Files:**
- Create: `/home/sysop/src/notebridge/internal/caldav/backend.go` (from UB, minimal changes)
- Create: `/home/sysop/src/notebridge/internal/caldav/vtodo.go` (from UB, unchanged)

**Implementation:**

**vtodo.go** — Copy from UltraBridge unchanged. Pure conversion functions:
- `TaskToVTODO(t *taskstore.Task, dueTimeMode string) *ical.Calendar`
- `VTODOToTask(cal *ical.Calendar, dueTimeMode string) (*taskstore.Task, error)`

**backend.go** — Copy from UltraBridge. Change module imports from `ultrabridge` to `notebridge`. The Backend struct implements `gocaldav.Backend`:

Backend fields:
- `store TaskStore` (the interface, not a concrete type)
- `prefix string` (CalDAV URL prefix, e.g., "/caldav")
- `collectionName string`
- `dueTimeMode string` ("preserve" or "date_only")
- `notifier SyncNotifier` (can be nil)

Key behavior:
- Single fixed collection at `{prefix}/user/calendars/tasks/`
- Object paths: `{prefix}/tasks/{task_id}.ics`
- `PutCalendarObject` calls store.Create or store.Update, then notifier.Notify
- `DeleteCalendarObject` calls store.Delete, then notifier.Notify
- Notifier errors logged but swallowed (best-effort)

For NoteBridge, the SyncNotifier implementation should publish events via the event bus rather than directly pushing Engine.IO frames. Create a simple adapter:

```go
type eventBusNotifier struct {
    bus    *events.EventBus
    userID int64
}

func (n *eventBusNotifier) Notify(ctx context.Context) error {
    n.bus.Publish(ctx, events.Event{
        Type:   events.FileModified, // triggers ServerMessage to connected tablets
        UserID: n.userID,
    })
    return nil
}
```

Add `emersion/go-webdav` dependency:
```bash
go get github.com/emersion/go-webdav
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port CalDAV backend and VTODO conversion`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Wire CalDAV to main.go

**Files:**
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go` — add CalDAV server on web port

**Implementation:**

In main.go, after creating the syncdb store and event bus:

1. Create taskstore: `taskStore := taskstore.New(db, userID)`
2. Create notifier adapter: `caldavNotifier := &eventBusNotifier{bus: eventBus, userID: userID}`
3. Create CalDAV backend:
   ```go
   caldavBackend := caldav.NewBackend(taskStore, "/caldav",
       cfg.CalDAVCollectionName, cfg.DueTimeMode, caldavNotifier)
   ```
4. Create CalDAV handler:
   ```go
   caldavHandler := &gocaldav.Handler{Backend: caldavBackend, Prefix: "/caldav"}
   ```
5. Mount on web server (port 8443):
   ```go
   webMux := http.NewServeMux()
   webMux.Handle("/caldav/", caldavHandler) // CalDAV doesn't need auth (uses its own)
   webMux.HandleFunc("GET /health", handleHealth)
   ```
6. Start web server alongside sync server:
   ```go
   go func() {
       if err := http.ListenAndServe(cfg.WebListenAddr, webMux); err != nil {
           logger.Error("web server failed", "error", err)
       }
   }()
   ```

Add config fields:
- `CalDAVCollectionName` (NB_CALDAV_COLLECTION_NAME, default: "Supernote Tasks")
- `DueTimeMode` (NB_DUE_TIME_MODE, default: "preserve")

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire CalDAV backend to web server`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-5) -->
## Subcomponent B: Tests

<!-- START_TASK_4 -->
### Task 4: Port taskstore and CalDAV tests

**Files:**
- Create: `/home/sysop/src/notebridge/internal/taskstore/store_test.go` (adapted from UB)
- Create: `/home/sysop/src/notebridge/internal/caldav/vtodo_test.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/caldav/backend_test.go` (adapted from UB)

**Implementation:**

**vtodo_test.go** — Copy from UltraBridge. Pure conversion tests, no DB dependency. Table-driven tests covering:
- Task → VTODO → Task round-trip preserves all fields
- Status mapping: "needsAction" → "NEEDS-ACTION", "completed" → "COMPLETED"
- DueTime modes: preserve vs date_only
- Empty/nil fields handled correctly
- CompletionTime quirk: extracted from LastModified, not CompletedTime

**store_test.go** — Adapted from UltraBridge for SQLite:
- Use syncdb.Open(":memory:") instead of MariaDB test connection
- Create test user in syncdb, get userID
- Test all 6 TaskStore methods: List, Get, Create, Update, Delete, MaxLastModified
- Verify soft-delete or hard-delete behavior matches design
- Verify ETag changes when task fields change
- Verify CTag changes when any task changes

**backend_test.go** — Adapted from UltraBridge:
- Use in-memory SQLite
- Test CalDAV object CRUD through the backend interface
- Verify notifier called on Put and Delete

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/taskstore/ ./internal/caldav/
```

**Commit:** `test: port taskstore and CalDAV tests`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: CalDAV integration tests

**Verifies:** AC7.1 (tablet tasks → CalDAV VTODOs), AC7.2 (CalDAV VTODO → tablet sync), AC7.3 (completion status round-trip)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/caldav/integration_test.go`

**Testing:**

End-to-end tests that exercise the complete flow: device sync API → syncdb → CalDAV backend → VTODO responses. Uses httptest servers for both sync API (port 19071) and CalDAV (port 8443).

**Test cases:**

- AC7.1 tablet tasks appear in CalDAV:
  1. Create a task via sync API (POST /api/file/schedule/task)
  2. List CalDAV objects (PROPFIND on /caldav/user/calendars/tasks/)
  3. Get specific task (GET /caldav/tasks/{task_id}.ics)
  4. Verify VTODO SUMMARY matches task title
  5. Verify VTODO STATUS matches task status
  6. Verify VTODO DUE matches task due_time

- AC7.2 CalDAV VTODO syncs to tablet:
  1. Create a VTODO via CalDAV (PUT /caldav/tasks/{new_id}.ics with VTODO payload)
  2. List tasks via sync API (POST /api/file/schedule/task/all)
  3. Verify new task appears with correct title, status, due time
  4. Verify event bus received notification (Socket.IO would push to tablet)

- AC7.3 completion status round-trip:
  1. Create task via sync API with status "needsAction"
  2. Read via CalDAV → STATUS is NEEDS-ACTION
  3. Update via CalDAV: set STATUS to COMPLETED
  4. Read via sync API → status is "completed"
  5. Update via sync API: set status back to "needsAction"
  6. Read via CalDAV → STATUS is NEEDS-ACTION

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/caldav/ -run TestIntegration
```

Expected: All tests pass.

**Commit:** `test: add CalDAV integration tests`
<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_B -->
