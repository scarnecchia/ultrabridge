# UltraBridge CalDAV — Phase 7: Web UI

**Goal:** Minimal web interface for task verification and log viewing.

**Architecture:** `net/http` handlers serving embedded HTML/CSS/JS via Go `embed` package. Task list, create, and complete are server-rendered HTML with form submissions. Logs tab uses a WebSocket connection to stream log entries in real time with level filtering. All endpoints are auth-protected (Phase 4 middleware).

**Tech Stack:** Go 1.22, `embed`, `html/template`, `github.com/coder/websocket` (already a dep from Phase 5)

**Scope:** 8 phases from original design (phase 7 of 8)

**Codebase verified:** 2026-03-17

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC4: Minimal web UI
- **ultrabridge-caldav.AC4.1 Success:** Web browser can view a list of all non-deleted tasks
- **ultrabridge-caldav.AC4.2 Success:** Web browser can create a new task (title, optional due date) and it appears in the list
- **ultrabridge-caldav.AC4.3 Success:** Web browser can mark a task complete and status updates in the list
- **ultrabridge-caldav.AC4.4 Success:** Logs tab shows live log entries via websocket with level filtering

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
<!-- START_TASK_1 -->
### Task 1: Web handler and templates

**Files:**
- Create: `internal/web/handler.go`
- Create: `internal/web/templates/index.html`

**Implementation:**

`handler.go` provides an `http.Handler` that serves the web UI. Routes:
- `GET /` — task list page (renders template with all non-deleted tasks)
- `POST /tasks` — create task (form fields: `title`, `due_date` optional)
- `POST /tasks/{id}/complete` — mark task complete

The handler takes a `taskstore.Store` (or the `TaskStore` interface from Phase 3) and a `SyncNotifier` (optional). After create/complete operations, it calls `Notify()` if available.

`index.html` is a single-page template with:
- Task list table: title, status, due date, actions (complete button for non-completed tasks)
- Create form: title input, optional due date input, submit button
- Logs tab (placeholder — wired in Task 2)
- Minimal CSS inline for readability

Use `embed.FS` to embed the templates directory:

```go
package web

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

//go:embed templates
var templateFS embed.FS

type Handler struct {
	store    TaskStore
	notifier SyncNotifier
	tmpl     *template.Template
	mux      *http.ServeMux
}

// Import TaskStore and SyncNotifier interfaces from the caldav package
// (defined in Phase 3, internal/caldav/backend.go) rather than redefining them.
// Use: ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
// Then reference as ubcaldav.TaskStore and ubcaldav.SyncNotifier.
```

The `Handler` implements `http.Handler` via an internal `*http.ServeMux`.

For task creation via form:
- Parse `title` from form
- Parse optional `due_date` (HTML date input format `2006-01-02`) → convert to ms UTC
- Call `store.Create` with new `Task` (GenerateTaskID, set defaults)
- Call `notifier.Notify` if available
- Redirect to `/`

For task completion:
- Extract task ID from URL path
- Call `store.Get`, set status to "completed", call `store.Update`
- Call `notifier.Notify` if available
- Redirect to `/`

Template should display tasks with:
- Title
- Status (with visual indicator — e.g., checkmark for completed)
- Due date (formatted, or "No due date" if 0)
- "Complete" button (hidden for already-completed tasks)

**Verification:**

```bash
go build ./internal/web/
```

Expected: Compiles.

**Commit:** `feat: add web UI handler with task list, create, and complete`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: WebSocket log streaming

**Files:**
- Create: `internal/web/logstream.go`
- Modify: `internal/logging/logging.go` (add log broadcast capability)

**Implementation:**

Add a `LogBroadcaster` to the logging package — a ring buffer that stores recent log entries and broadcasts new entries to subscribed WebSocket clients.

In `internal/logging/logging.go`, add:
- `Broadcaster` struct with a `Subscribe() <-chan string` and `Unsubscribe()` methods
- A custom `slog.Handler` wrapper that writes to the broadcaster in addition to the normal handler
- Ring buffer of recent entries (e.g., last 100) so new WebSocket connections get a backfill

In `internal/web/logstream.go`:
- `GET /ws/logs` endpoint that upgrades to WebSocket
- Accepts optional `level` query param for filtering (debug/info/warn/error)
- Subscribes to the broadcaster, sends entries to the client as text messages
- On disconnect, unsubscribes

The WebSocket endpoint uses `github.com/coder/websocket` (already a dependency).

Update `templates/index.html` to add a Logs tab:
- Two tabs: "Tasks" and "Logs"
- Logs tab has a level filter dropdown and a scrollable log output area
- JavaScript connects to `/ws/logs?level=info` and appends received messages
- Changing the level dropdown reconnects with the new level

**Verification:**

```bash
go build ./internal/web/
go build ./internal/logging/
```

Expected: Compiles.

**Commit:** `feat: add WebSocket log streaming with level filtering`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Wire web UI into main.go

**Files:**
- Modify: `cmd/ultrabridge/main.go`

**Implementation:**

If `cfg.WebEnabled`, create the web handler and mount it. Apply auth middleware.

```go
	if cfg.WebEnabled {
		webHandler := web.NewHandler(store, notifier)
		mux.Handle("/", authMW.Wrap(webHandler))
	}
```

Note: The web handler's routes (`/`, `/tasks`, `/tasks/{id}/complete`, `/ws/logs`) must not conflict with `/caldav/` and `/health`. The mux routes by longest prefix match, so `/caldav/` will still match CalDAV requests.

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles.

**Commit:** `feat: wire web UI into main server`

<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_TASK_4 -->
### Task 4: Web UI tests

**Verifies:** ultrabridge-caldav.AC4.1, ultrabridge-caldav.AC4.2, ultrabridge-caldav.AC4.3, ultrabridge-caldav.AC4.4

**Files:**
- Create: `internal/web/handler_test.go`
- Create: `internal/logging/broadcaster_test.go`

**Testing:**

Tests must verify each AC:
- **ultrabridge-caldav.AC4.1:** `GET /` returns 200 with HTML containing task titles from the store. Use an in-memory mock store pre-populated with tasks.
- **ultrabridge-caldav.AC4.2:** `POST /tasks` with form data `title=Test Task` creates a task in the store and redirects to `/`. Verify task exists in store after POST.
- **ultrabridge-caldav.AC4.3:** `POST /tasks/{id}/complete` marks a task complete in the store and redirects. Verify task status is "completed" after POST.
- **ultrabridge-caldav.AC4.4:** Test the `LogBroadcaster` subscribe/unsubscribe mechanism and ring buffer in `internal/logging/broadcaster_test.go`:
  - Subscribe returns a channel that receives new log entries
  - Multiple subscribers each receive the same entry
  - Unsubscribe stops delivery to that channel
  - Ring buffer provides recent entries to new subscribers (backfill)

Additional tests:
- `POST /tasks` with optional due date: verify DueTime is correctly set
- `POST /tasks/{id}/complete` for already-completed task: no error, task remains completed
- `GET /` with empty store: renders page with "No tasks" or empty table

Use `httptest.NewRecorder` and `httptest.NewRequest`. Use in-memory mock store.

**Verification:**

```bash
go test ./internal/web/ -v
```

Expected: All tests pass.

**Commit:** `test: add web UI handler tests`

<!-- END_TASK_4 -->
