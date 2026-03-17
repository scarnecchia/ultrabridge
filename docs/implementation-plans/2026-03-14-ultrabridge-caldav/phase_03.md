# UltraBridge CalDAV — Phase 3: CalDAV Server

**Goal:** Expose tasks as VTODO objects via CalDAV protocol using `emersion/go-webdav`.

**Architecture:** Implement `caldav.Backend` interface wrapping the Phase 2 task store. VTODO↔Task conversion in a dedicated file. Mount CalDAV handler at `/caldav/` prefix with `/.well-known/caldav` redirect. Single VTODO-only collection.

**Tech Stack:** Go 1.22, `github.com/emersion/go-webdav` (caldav package), `github.com/emersion/go-ical`

**Scope:** 8 phases from original design (phase 3 of 8)

**Codebase verified:** 2026-03-17 (go-webdav source inspected at commit 1916c2d)

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC2: CalDAV server with VTODO collection
- **ultrabridge-caldav.AC2.1 Success:** `/.well-known/caldav` returns redirect to CalDAV prefix
- **ultrabridge-caldav.AC2.2 Success:** PROPFIND on collection returns `supported-calendar-component-set` containing `VTODO`
- **ultrabridge-caldav.AC2.3 Success:** Collection display name matches `UB_CALDAV_COLLECTION_NAME` config value
- **ultrabridge-caldav.AC2.4 Success:** CTag changes when any task is created, modified, or deleted
- **ultrabridge-caldav.AC2.5 Failure:** PUT with a VEVENT (not VTODO) is rejected

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: VTODO ↔ Task conversion

**Files:**
- Create: `internal/caldav/vtodo.go`

**Implementation:**

Pure conversion functions between `taskstore.Task` and `ical.Calendar` (containing a VTODO component). These use the mapping functions from Phase 2.

The `go-ical` library uses:
- `ical.Calendar` — top-level object containing components
- `ical.Component` — a VEVENT, VTODO, etc. with `Name` field and `Props`
- Properties accessed via `component.Props.Get(propName)` or set via `component.Props.Set(prop)`
- Constants: `ical.CompToDo`, `ical.PropUID`, `ical.PropSummary`, `ical.PropStatus`, `ical.PropDue`, `ical.PropCompleted`, `ical.PropLastModified`, `ical.PropDescription`, `ical.PropPriority`

Key conversion details:
- `Task.TaskID` → VTODO `UID`
- `Task.Title` → VTODO `SUMMARY`
- `Task.Status` → VTODO `STATUS` (via `CalDAVStatus()`)
- `Task.DueTime` (ms UTC) → VTODO `DUE` (respecting `DueTimeMode` config: `preserve` = `DATE-TIME`, `date_only` = `DATE`)
- `Task.LastModified` (ms UTC) → VTODO `LAST-MODIFIED` (always seconds precision)
- When `status == "completed"`: `Task.LastModified` → VTODO `COMPLETED` (the quirk!)
- `Task.Detail` → VTODO `DESCRIPTION` (Tier 2)
- `Task.Importance` → VTODO `PRIORITY` (Tier 2)
- `Task.Links` → VTODO `URL` (read-only, `supernote://note/{fileId}/page/{page}`)
- Zero `DueTime` (0) → omit `DUE` property entirely

Reverse direction (VTODO → Task for PUT):
- VTODO `UID` → use as-is if present, else generate MD5
- VTODO `SUMMARY` → `Task.Title`
- VTODO `STATUS` → `Task.Status` (via `SupernoteStatus()`)
- VTODO `DUE` → `Task.DueTime` (convert to ms UTC; if `date_only` mode, strip time)
- VTODO `DESCRIPTION` → `Task.Detail`
- VTODO `PRIORITY` → `Task.Importance`
- Ignore: `COMPLETED` property on write (we set `last_modified` based on status change logic in store)

```go
package caldav

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

// TaskToVTODO converts a task store Task to an ical.Calendar containing a VTODO.
func TaskToVTODO(t *taskstore.Task, dueTimeMode string) *ical.Calendar {
	cal := ical.NewCalendar()
	cal.Props.SetText(ical.PropProductID, "-//UltraBridge//CalDAV//EN")
	cal.Props.SetText(ical.PropVersion, "2.0")

	todo := ical.NewComponent(ical.CompToDo)
	todo.Props.SetText(ical.PropUID, t.TaskID)

	if t.Title.Valid && t.Title.String != "" {
		todo.Props.SetText(ical.PropSummary, t.Title.String)
	}

	status := taskstore.CalDAVStatus(nullStr(t.Status))
	todo.Props.SetText(ical.PropStatus, status)

	if t.DueTime != 0 {
		dueTime := taskstore.MsToTime(t.DueTime)
		if dueTimeMode == "date_only" {
			prop := ical.NewProp(ical.PropDue)
			prop.SetDate(dueTime)
			todo.Props.Set(prop)
		} else {
			prop := ical.NewProp(ical.PropDue)
			prop.SetDateTime(dueTime)
			todo.Props.Set(prop)
		}
	}

	if t.LastModified.Valid {
		lm := taskstore.MsToTime(t.LastModified.Int64)
		prop := ical.NewProp(ical.PropLastModified)
		prop.SetDateTime(lm)
		todo.Props.Set(prop)
	}

	// Completion time: use last_modified (NOT completed_time) per Supernote quirk
	if ct, ok := taskstore.CompletionTime(t); ok {
		prop := ical.NewProp(ical.PropCompleted)
		prop.SetDateTime(ct)
		todo.Props.Set(prop)
	}

	// Tier 2 fields
	if t.Detail.Valid && t.Detail.String != "" {
		todo.Props.SetText(ical.PropDescription, t.Detail.String)
	}
	if t.Importance.Valid && t.Importance.String != "" {
		todo.Props.SetText(ical.PropPriority, t.Importance.String)
	}

	// Links (read-only, informational)
	if t.Links.Valid && t.Links.String != "" {
		todo.Props.SetText(ical.PropURL, t.Links.String)
	}

	cal.Children = append(cal.Children, todo.Component)
	return cal
}

// VTODOToTask extracts task fields from an ical.Calendar containing a VTODO.
// Returns the extracted task and the UID. Does not set user_id or task_id generation
// — caller handles those.
func VTODOToTask(cal *ical.Calendar, dueTimeMode string) (*taskstore.Task, error) {
	var todo *ical.Component
	for _, child := range cal.Children {
		if child.Name == ical.CompToDo {
			todo = child
			break
		}
	}
	if todo == nil {
		return nil, fmt.Errorf("no VTODO component found")
	}

	t := &taskstore.Task{}

	if uid := todo.Props.Get(ical.PropUID); uid != nil {
		t.TaskID = uid.Value
	}
	if summary := todo.Props.Get(ical.PropSummary); summary != nil {
		t.Title = sqlStr(summary.Value)
	}
	if status := todo.Props.Get(ical.PropStatus); status != nil {
		t.Status = sqlStr(taskstore.SupernoteStatus(status.Value))
	}
	if due := todo.Props.Get(ical.PropDue); due != nil {
		dueTime, err := due.DateTime(time.UTC)
		if err == nil {
			if dueTimeMode == "date_only" {
				// Strip time component
				dueTime = time.Date(dueTime.Year(), dueTime.Month(), dueTime.Day(),
					0, 0, 0, 0, time.UTC)
			}
			t.DueTime = taskstore.TimeToMs(dueTime)
		}
	}
	if desc := todo.Props.Get(ical.PropDescription); desc != nil {
		t.Detail = sqlStr(desc.Value)
	}
	if prio := todo.Props.Get(ical.PropPriority); prio != nil {
		t.Importance = sqlStr(prio.Value)
	}

	return t, nil
}

// FindVTODO returns the first VTODO component in the calendar, or error.
func FindVTODO(cal *ical.Calendar) (*ical.Component, error) {
	for _, child := range cal.Children {
		if child.Name == ical.CompToDo {
			return child, nil
		}
	}
	return nil, fmt.Errorf("no VTODO component found")
}

// HasVEvent returns true if the calendar contains a VEVENT component.
func HasVEvent(cal *ical.Calendar) bool {
	for _, child := range cal.Children {
		if child.Name == ical.CompEvent {
			return true
		}
	}
	return false
}
```

Use the exported helpers `taskstore.NullStr()` and `taskstore.SqlStr()` from Phase 2 (see below). Do NOT redefine `nullStr`/`sqlStr` locally — import from the `taskstore` package to avoid duplication.

Note: Phase 2 `internal/taskstore/mapping.go` must export these as `NullStr` and `SqlStr` (capitalized). Update the Phase 2 mapping code accordingly — rename the unexported `nullStr` to `NullStr` and add `SqlStr`.

**Verification:**

```bash
go build ./internal/caldav/
```

Expected: Compiles.

**Commit:** `feat: add VTODO to task bidirectional conversion`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: VTODO conversion tests

**Verifies:** ultrabridge-caldav.AC2.4, ultrabridge-caldav.AC2.5, ultrabridge-caldav.AC3.9, ultrabridge-caldav.AC3.10, ultrabridge-caldav.AC3.11

**Files:**
- Create: `internal/caldav/vtodo_test.go`

**Testing:**

Tests must verify:
- **ultrabridge-caldav.AC2.5:** `HasVEvent` returns true for calendar with VEVENT; `FindVTODO` returns error for calendar without VTODO
- **ultrabridge-caldav.AC3.9:** Task with `DueTime=0` → `TaskToVTODO` produces VTODO with no DUE property
- **ultrabridge-caldav.AC3.10:** With `dueTimeMode="date_only"`, DUE is rendered as DATE (no time component) and round-trips correctly
- **ultrabridge-caldav.AC3.11:** With `dueTimeMode="preserve"`, DUE round-trips full DATE-TIME

Additional tests:
- `TaskToVTODO` produces valid ical.Calendar with VTODO component
- UID matches TaskID
- SUMMARY matches Title
- STATUS maps correctly: `needsAction` → `NEEDS-ACTION`, `completed` → `COMPLETED`
- Completed task uses `LastModified` (not `CompletedTime`) for COMPLETED property
- Tier 2 fields (DESCRIPTION, PRIORITY) round-trip
- `VTODOToTask` extracts correct fields from a VTODO
- Round-trip: Task → VTODO → Task preserves all mapped fields

Follow Go standard testing patterns (table-driven tests).

**Verification:**

```bash
go test ./internal/caldav/ -v -run TestVTODO
```

Expected: All tests pass.

**Commit:** `test: add VTODO conversion tests`

<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-5) -->
<!-- START_TASK_3 -->
### Task 3: CalDAV Backend implementation

**Files:**
- Create: `internal/caldav/backend.go`

**Implementation:**

Implement `caldav.Backend` interface. The backend wraps a `*taskstore.Store` and serves a single VTODO-only collection.

The `caldav.Backend` interface requires these methods:
```go
CalendarHomeSetPath(ctx context.Context) (string, error)
CurrentUserPrincipal(ctx context.Context) (string, error)  // from webdav.UserPrincipalBackend
CreateCalendar(ctx context.Context, calendar *caldav.Calendar) error
ListCalendars(ctx context.Context) ([]caldav.Calendar, error)
GetCalendar(ctx context.Context, path string) (*caldav.Calendar, error)
GetCalendarObject(ctx context.Context, path string, req *caldav.CalendarCompRequest) (*caldav.CalendarObject, error)
ListCalendarObjects(ctx context.Context, path string, req *caldav.CalendarCompRequest) ([]caldav.CalendarObject, error)
QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) ([]caldav.CalendarObject, error)
PutCalendarObject(ctx context.Context, path string, calendar *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (*caldav.CalendarObject, error)
DeleteCalendarObject(ctx context.Context, path string) error
```

Key design decisions:
- Single collection at `{prefix}/tasks/`
- `CalendarHomeSetPath` returns `{prefix}/`
- `CurrentUserPrincipal` returns `/user/`
- `CreateCalendar` returns error (collection is pre-defined, not user-creatable)
- `ListCalendars` returns the single collection with `SupportedComponentSet: ["VTODO"]`
- `GetCalendar` returns the collection if path matches, 404 otherwise
- `GetCalendarObject` extracts task_id from path `{prefix}/tasks/{task_id}.ics`, calls store.Get, converts to VTODO
- `ListCalendarObjects` calls store.List, converts each to CalendarObject
- `QueryCalendarObjects` calls store.List then filters (simple filter — the library provides a `caldav.Filter()` helper but we can filter manually)
- `PutCalendarObject` parses VTODO, rejects VEVENTs, converts to Task, calls store.Create or store.Update
- `DeleteCalendarObject` extracts task_id, calls store.Delete
- CTag: call `taskstore.ComputeCTag` from listed tasks
- ETag: call `taskstore.ComputeETag` per task

First, define the `TaskStore` and `SyncNotifier` interfaces in `internal/caldav/backend.go`. These interfaces are shared — Phase 7 (web) will import from this package rather than redefining them.

```go
package caldav

import (
	"context"
	"fmt"
	"path"
	"strings"

	gocaldav "github.com/emersion/go-webdav/caldav"
	ical "github.com/emersion/go-ical"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

// TaskStore defines the task persistence operations needed by CalDAV and Web handlers.
// *taskstore.Store satisfies this interface.
type TaskStore interface {
	List(ctx context.Context) ([]taskstore.Task, error)
	Get(ctx context.Context, taskID string) (*taskstore.Task, error)
	Create(ctx context.Context, t *taskstore.Task) error
	Update(ctx context.Context, t *taskstore.Task) error
	Delete(ctx context.Context, taskID string) error
	MaxLastModified(ctx context.Context) (int64, error)
}

// SyncNotifier triggers device sync after task writes.
type SyncNotifier interface {
	Notify(ctx context.Context) error
}

type Backend struct {
	store          TaskStore
	notifier       SyncNotifier
	prefix         string
	collectionName string
	dueTimeMode    string
}

func NewBackend(store TaskStore, prefix, collectionName, dueTimeMode string, notifier SyncNotifier) *Backend {
	return &Backend{
		store:          store,
		notifier:       notifier,
		prefix:         strings.TrimSuffix(prefix, "/"),
		collectionName: collectionName,
		dueTimeMode:    dueTimeMode,
	}
}

func (b *Backend) CurrentUserPrincipal(ctx context.Context) (string, error) {
	return "/user/", nil
}

func (b *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	return b.prefix + "/", nil
}

func (b *Backend) CreateCalendar(ctx context.Context, calendar *gocaldav.Calendar) error {
	return fmt.Errorf("calendar creation not supported")
}

func (b *Backend) ListCalendars(ctx context.Context) ([]gocaldav.Calendar, error) {
	return []gocaldav.Calendar{b.collection()}, nil
}

func (b *Backend) GetCalendar(ctx context.Context, urlPath string) (*gocaldav.Calendar, error) {
	col := b.collection()
	if path.Clean(urlPath) != path.Clean(col.Path) {
		return nil, fmt.Errorf("calendar not found")
	}
	return &col, nil
}

func (b *Backend) GetCalendarObject(ctx context.Context, urlPath string, req *gocaldav.CalendarCompRequest) (*gocaldav.CalendarObject, error) {
	taskID := b.taskIDFromPath(urlPath)
	if taskID == "" {
		return nil, fmt.Errorf("invalid path")
	}
	task, err := b.store.Get(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return b.taskToCalendarObject(task), nil
}

func (b *Backend) ListCalendarObjects(ctx context.Context, urlPath string, req *gocaldav.CalendarCompRequest) ([]gocaldav.CalendarObject, error) {
	tasks, err := b.store.List(ctx)
	if err != nil {
		return nil, err
	}
	objects := make([]gocaldav.CalendarObject, len(tasks))
	for i := range tasks {
		objects[i] = *b.taskToCalendarObject(&tasks[i])
	}
	return objects, nil
}

func (b *Backend) QueryCalendarObjects(ctx context.Context, urlPath string, query *gocaldav.CalendarQuery) ([]gocaldav.CalendarObject, error) {
	// List all tasks, then apply the query filter.
	// The go-webdav library does NOT filter results after QueryCalendarObjects returns —
	// it expects us to apply the filter. Use gocaldav.Filter() if available,
	// or apply CompFilter manually. For a small VTODO collection this is acceptable.
	tasks, err := b.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var objects []gocaldav.CalendarObject
	for i := range tasks {
		obj := b.taskToCalendarObject(&tasks[i])
		// Apply filter: check if the object matches the query's CompFilter.
		// At minimum, verify the component type matches (VTODO).
		// The implementor should check if go-webdav exports a Filter() or Match()
		// helper and use it here. If not, a simple component name check suffices
		// since all our objects are VTODO.
		if query != nil && query.CompFilter.Name != "" &&
			query.CompFilter.Name != "VCALENDAR" {
			// If filter requests a specific component (e.g., VTODO), check it
			hasMatch := false
			for _, child := range obj.Data.Children {
				if child.Name == query.CompFilter.Name {
					hasMatch = true
					break
				}
			}
			if !hasMatch {
				continue
			}
		}
		objects = append(objects, *obj)
	}
	return objects, nil
}

func (b *Backend) PutCalendarObject(ctx context.Context, urlPath string, cal *ical.Calendar, opts *gocaldav.PutCalendarObjectOptions) (*gocaldav.CalendarObject, error) {
	// Reject VEVENTs — this collection only supports VTODO.
	// The implementor should check go-webdav's exported error types for the
	// "supported-calendar-component" precondition violation. If the library
	// provides a specific precondition error type, use it. Otherwise, return
	// a plain error — go-webdav will map it to an appropriate HTTP status.
	if HasVEvent(cal) {
		return nil, fmt.Errorf("only VTODO components are supported, not VEVENT")
	}

	task, err := VTODOToTask(cal, b.dueTimeMode)
	if err != nil {
		return nil, err
	}

	taskID := b.taskIDFromPath(urlPath)

	// Check if task exists
	existing, getErr := b.store.Get(ctx, taskID)
	if getErr != nil {
		// New task
		if task.TaskID == "" {
			task.TaskID = taskID
		}
		if err := b.store.Create(ctx, task); err != nil {
			return nil, err
		}
	} else {
		// Update existing
		task.TaskID = existing.TaskID
		if err := b.store.Update(ctx, task); err != nil {
			return nil, err
		}
	}

	// Re-fetch to get updated fields
	updated, err := b.store.Get(ctx, task.TaskID)
	if err != nil {
		return nil, err
	}
	return b.taskToCalendarObject(updated), nil
}

func (b *Backend) DeleteCalendarObject(ctx context.Context, urlPath string) error {
	taskID := b.taskIDFromPath(urlPath)
	if taskID == "" {
		return fmt.Errorf("invalid path")
	}
	return b.store.Delete(ctx, taskID)
}

func (b *Backend) collection() gocaldav.Calendar {
	return gocaldav.Calendar{
		Path:                  b.prefix + "/tasks/",
		Name:                  b.collectionName,
		Description:           "Supernote tasks via UltraBridge",
		SupportedComponentSet: []string{"VTODO"},
	}
}

func (b *Backend) taskToCalendarObject(t *taskstore.Task) *gocaldav.CalendarObject {
	cal := TaskToVTODO(t, b.dueTimeMode)
	return &gocaldav.CalendarObject{
		Path:    b.prefix + "/tasks/" + t.TaskID + ".ics",
		ModTime: taskstore.MsToTime(t.LastModified.Int64),
		ETag:    taskstore.ComputeETag(t),
		Data:    cal,
	}
}

// taskIDFromPath extracts the task ID from a path like /caldav/tasks/{id}.ics
func (b *Backend) taskIDFromPath(urlPath string) string {
	base := path.Base(urlPath)
	return strings.TrimSuffix(base, ".ics")
}
```

Note on VEVENT rejection: The `gocaldav.PreconditionError` type may need adjustment based on the exact error types exported by `go-webdav`. The implementor should check the library's error types and use the appropriate precondition error for "supported-calendar-component" violation. If the library doesn't export a specific precondition error for this, return a generic HTTP 403 or use `webdav.HTTPError` with status 403.

**Verification:**

```bash
go build ./internal/caldav/
```

Expected: Compiles.

**Commit:** `feat: implement caldav.Backend wrapping task store`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Mount CalDAV handler in main.go

**Files:**
- Modify: `cmd/ultrabridge/main.go`

**Implementation:**

After DB connection and user discovery, create the task store, backend, and CalDAV handler. Mount at the configured prefix. Add `/.well-known/caldav` redirect.

Add to main.go after user discovery:

```go
	store := taskstore.New(database, userID)

	backend := caldav.NewBackend(store, "/caldav", cfg.CalDAVCollectionName, cfg.DueTimeMode)
	caldavHandler := &gocaldav.Handler{
		Backend: backend,
		Prefix:  "/caldav",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("/caldav/", caldavHandler)
	mux.HandleFunc("/.well-known/caldav", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/caldav/", http.StatusMovedPermanently)
	})
```

Import additions:
```go
	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	gocaldav "github.com/emersion/go-webdav/caldav"
	"github.com/sysop/ultrabridge/internal/taskstore"
```

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles.

**Commit:** `feat: mount CalDAV handler with well-known redirect`

<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: CalDAV Backend tests

**Verifies:** ultrabridge-caldav.AC2.1, ultrabridge-caldav.AC2.2, ultrabridge-caldav.AC2.3, ultrabridge-caldav.AC2.4, ultrabridge-caldav.AC2.5

**Files:**
- Create: `internal/caldav/backend_test.go`

**Testing:**

Tests must verify each AC:
- **ultrabridge-caldav.AC2.1:** The `/.well-known/caldav` redirect is tested at integration level (Phase 8). For unit tests, verify `CalendarHomeSetPath` returns correct prefix path.
- **ultrabridge-caldav.AC2.2:** `ListCalendars` returns a collection with `SupportedComponentSet` containing `"VTODO"`.
- **ultrabridge-caldav.AC2.3:** `ListCalendars` returns a collection whose `Name` matches the configured collection name.
- **ultrabridge-caldav.AC2.4:** After `PutCalendarObject` (creating a task), `ListCalendarObjects` returns objects with changed ETags. CTag verification: list objects before and after a write — the max `last_modified` should differ.
- **ultrabridge-caldav.AC2.5:** `PutCalendarObject` with a VEVENT-containing calendar returns an error.

These tests need a mock or in-memory task store. Create a simple in-memory store that satisfies the same interface the backend expects. Since the backend directly calls `*taskstore.Store` methods (not an interface), the tests should either:
- Extract a `TaskStore` interface for the backend to depend on, OR
- Use a real `*taskstore.Store` with a test database

Recommended approach: Define a `TaskStoreInterface` in the caldav package that `*taskstore.Store` already satisfies, and accept that interface in `NewBackend`. This enables testing with an in-memory mock.

```go
// In internal/caldav/backend.go, change store field type:
type TaskStore interface {
    List(ctx context.Context) ([]taskstore.Task, error)
    Get(ctx context.Context, taskID string) (*taskstore.Task, error)
    Create(ctx context.Context, t *taskstore.Task) error
    Update(ctx context.Context, t *taskstore.Task) error
    Delete(ctx context.Context, taskID string) error
}
```

Then tests use an in-memory implementation of this interface.

Additional tests:
- `GetCalendarObject` returns correct CalendarObject for known task
- `DeleteCalendarObject` marks task deleted
- `PutCalendarObject` with new VTODO creates task
- `PutCalendarObject` with existing task ID updates task
- `QueryCalendarObjects` returns all non-deleted tasks

Follow Go standard testing patterns.

**Verification:**

```bash
go test ./internal/caldav/ -v
```

Expected: All tests pass.

**Commit:** `test: add CalDAV backend tests with in-memory store`

<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_B -->
