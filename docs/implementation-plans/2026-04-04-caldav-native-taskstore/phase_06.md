# CalDAV-Native Task Store — Phase 6: Web UI Sync Control

**Goal:** Sync status display and manual sync trigger in the web UI Tasks tab.

**Architecture:** The web handler gains an optional sync engine dependency (nil-safe, following the existing pattern for all other dependencies). Two new endpoints: `GET /sync/status` returns JSON sync status, `POST /sync/trigger` fires an immediate sync cycle. The Tasks tab in `index.html` gets a sync status panel showing last sync time, next scheduled sync, in-progress indicator, and a "Sync Now" button. JavaScript polls the status endpoint (5-second interval, matching the existing Files tab pattern).

**Tech Stack:** Go, HTML/CSS/vanilla JS (matching existing web UI patterns)

**Scope:** 6 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC3: Web UI sync control
- **caldav-native-taskstore.AC3.1 Success:** Tasks tab shows sync status: last sync time, next scheduled sync, adapter state
- **caldav-native-taskstore.AC3.2 Success:** "Sync Now" button triggers immediate sync cycle, status updates on completion
- **caldav-native-taskstore.AC3.3 Failure:** Sync in progress — button disabled or shows in-progress indicator, no double-trigger

---

<!-- START_TASK_1 -->
### Task 1: Define `SyncStatusProvider` interface and add to Handler

**Files:**
- Modify: `internal/web/handler.go:34-50` (add interface and field)
- Modify: `internal/web/handler.go:71` (NewHandler signature)

**Implementation:**

Add a web-local `SyncStatus` struct and interface above the Handler struct. This avoids importing `tasksync` into the web package, following the existing pattern where all web dependencies are local interfaces:

```go
// SyncStatus represents sync engine state for the web UI.
type SyncStatus struct {
	LastSyncAt    int64  `json:"lastSyncAt"`
	NextSyncAt    int64  `json:"nextSyncAt"`
	InProgress    bool   `json:"inProgress"`
	LastError     string `json:"lastError"`
	AdapterID     string `json:"adapterId"`
	AdapterActive bool   `json:"adapterActive"`
}

// SyncStatusProvider provides sync status and manual trigger.
// Implemented by a wrapper around tasksync.SyncEngine. Nil-safe in Handler.
type SyncStatusProvider interface {
	Status() SyncStatus
	TriggerSync()
}
```

In `cmd/ultrabridge/main.go`, create a thin adapter to convert `tasksync.SyncStatus` to `web.SyncStatus`:

```go
// syncProviderAdapter wraps tasksync.SyncEngine to satisfy web.SyncStatusProvider.
type syncProviderAdapter struct{ engine *tasksync.SyncEngine }

func (a *syncProviderAdapter) Status() web.SyncStatus {
	s := a.engine.Status()
	return web.SyncStatus{
		LastSyncAt: s.LastSyncAt, NextSyncAt: s.NextSyncAt,
		InProgress: s.InProgress, LastError: s.LastError,
		AdapterID: s.AdapterID, AdapterActive: s.AdapterActive,
	}
}
func (a *syncProviderAdapter) TriggerSync() { a.engine.TriggerSync() }
```

Add field to Handler struct:

```go
syncProvider SyncStatusProvider
```

Update `NewHandler` signature to accept the new parameter. Add it after `scanner FileScanner` and before `logger *slog.Logger`:

```go
func NewHandler(store ubcaldav.TaskStore, notifier ubcaldav.SyncNotifier, noteStore notestore.NoteStore, searchIndex search.SearchIndex, proc processor.Processor, scanner FileScanner, syncProvider SyncStatusProvider, logger *slog.Logger, broadcaster *logging.LogBroadcaster) *Handler
```

Set the field in the constructor:

```go
h := &Handler{
	// ... existing fields ...
	syncProvider: syncProvider,
	// ...
}
```

Register new routes in the mux setup (after existing route registrations):

```go
h.mux.HandleFunc("GET /sync/status", h.handleSyncStatus)
h.mux.HandleFunc("POST /sync/trigger", h.handleSyncTrigger)
```

**Important:** Update the `NewHandler` call in `cmd/ultrabridge/main.go` to pass the sync engine (or `nil` if sync disabled). The sync engine satisfies `SyncStatusProvider` because it has `Status()` and `TriggerSync()` methods.

In `main.go`, change the `NewHandler` call (currently at line 153):

```go
// If sync is enabled, wrap syncEngine for web UI; otherwise nil
var syncProvider web.SyncStatusProvider
if cfg.SNSyncEnabled && syncEngine != nil {
	syncProvider = &syncProviderAdapter{engine: syncEngine}
}
webHandler := web.NewHandler(store, notifier, ns, si, proc, pl, syncProvider, logger, broadcaster)
```

This requires the `syncEngine` variable to be declared before the `if cfg.SNSyncEnabled` block (see Phase 5 Task 3 scoping fix). The `syncProviderAdapter` struct should be defined in main.go (private, thin adapter).

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./...
```

Expected: Builds without errors. Must update all existing `NewHandler` calls including tests.

**Commit:** `feat(web): add SyncStatusProvider interface and sync route registration`

<!-- END_TASK_1 -->

<!-- START_SUBCOMPONENT_A (tasks 2-4) -->
<!-- START_TASK_2 -->
### Task 2: Implement `handleSyncStatus` and `handleSyncTrigger` endpoints

**Files:**
- Modify: `internal/web/handler.go` (add two handler methods)

**Implementation:**

Follow the JSON endpoint pattern from `handleFilesStatus` at `/home/jtd/ultrabridge/internal/web/handler.go:480-487`:

```go
func (h *Handler) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.syncProvider == nil {
		json.NewEncoder(w).Encode(SyncStatus{})
		return
	}
	json.NewEncoder(w).Encode(h.syncProvider.Status())
}

func (h *Handler) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	if h.syncProvider == nil {
		http.Error(w, "sync not configured", http.StatusNotFound)
		return
	}
	h.syncProvider.TriggerSync()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.syncProvider.Status())
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/web/
```

Expected: Builds without errors.

**Commit:** `feat(web): implement sync status and trigger JSON endpoints`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Add sync status panel to Tasks tab in template

**Files:**
- Modify: `internal/web/templates/index.html` (Tasks tab section)

**Implementation:**

Add a sync status panel at the top of the Tasks tab content, before the task table. Follow the existing styling patterns (badges, buttons, inline styles).

Add this block inside the Tasks tab `<div>`, before the `<table>`:

```html
<div style="display:flex; align-items:center; gap:12px; margin-bottom:16px; padding:10px; background:#f8f9fa; border-radius:6px;">
  <span style="font-weight:600;">Sync:</span>
  <span id="sync-status" class="badge badge-pending">Loading...</span>
  <span id="sync-last" style="color:#666; font-size:0.9em;"></span>
  <span id="sync-next" style="color:#666; font-size:0.9em;"></span>
  <span id="sync-error" style="color:#c00; font-size:0.9em;"></span>
  <button id="sync-now-btn" class="btn-small" onclick="triggerSync()" style="margin-left:auto;">Sync Now</button>
</div>
```

Add JavaScript functions for polling and triggering sync. Place inside the existing `<script>` block:

```javascript
function updateSyncStatus() {
  fetch('/sync/status')
    .then(r => r.json())
    .then(data => {
      const statusEl = document.getElementById('sync-status');
      const lastEl = document.getElementById('sync-last');
      const nextEl = document.getElementById('sync-next');
      const errorEl = document.getElementById('sync-error');
      const btn = document.getElementById('sync-now-btn');

      if (!data.AdapterActive) {
        statusEl.textContent = 'Disabled';
        statusEl.className = 'badge';
        btn.disabled = true;
        lastEl.textContent = '';
        nextEl.textContent = '';
        errorEl.textContent = '';
        return;
      }

      if (data.InProgress) {
        statusEl.textContent = 'Syncing...';
        statusEl.className = 'badge badge-progress';
        btn.disabled = true;
      } else {
        statusEl.textContent = 'Active';
        statusEl.className = 'badge badge-done';
        btn.disabled = false;
      }

      lastEl.textContent = data.LastSyncAt > 0
        ? 'Last: ' + new Date(data.LastSyncAt).toLocaleTimeString()
        : 'Last: never';
      nextEl.textContent = data.NextSyncAt > 0
        ? 'Next: ' + new Date(data.NextSyncAt).toLocaleTimeString()
        : '';
      errorEl.textContent = data.LastError ? 'Error: ' + data.LastError : '';
    })
    .catch(() => {
      document.getElementById('sync-status').textContent = 'Offline';
      document.getElementById('sync-status').className = 'badge';
    });
}

function triggerSync() {
  const btn = document.getElementById('sync-now-btn');
  btn.disabled = true;
  document.getElementById('sync-status').textContent = 'Syncing...';
  document.getElementById('sync-status').className = 'badge badge-progress';
  fetch('/sync/trigger', {method: 'POST'})
    .then(() => setTimeout(updateSyncStatus, 1000))
    .catch(() => { btn.disabled = false; });
}

// Poll sync status every 5 seconds (matches Files tab pattern)
setInterval(updateSyncStatus, 5000);
updateSyncStatus();
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./...
```

Expected: Builds without errors. Visual verification: start UltraBridge, open web UI, verify sync panel visible in Tasks tab.

**Commit:** `feat(web): add sync status panel and polling to Tasks tab`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Tests for sync status and trigger endpoints

**Verifies:** caldav-native-taskstore.AC3.1, caldav-native-taskstore.AC3.2, caldav-native-taskstore.AC3.3

**Files:**
- Modify: `internal/web/handler_test.go` (add sync tests)

**Testing:**

Add a `mockSyncProvider` implementing `SyncStatusProvider` to the test file, following the existing mock patterns (mockTaskStore, mockNotifier, etc.):

```go
type mockSyncProvider struct {
	status    SyncStatus // web-local SyncStatus (not tasksync.SyncStatus)
	triggered int
}
func (m *mockSyncProvider) Status() SyncStatus { return m.status }
func (m *mockSyncProvider) TriggerSync()       { m.triggered++ }
```

Tests must verify:
- **caldav-native-taskstore.AC3.1:** GET /sync/status with mockSyncProvider returning status with LastSyncAt, NextSyncAt, AdapterActive=true. Verify JSON response contains correct fields.

- **caldav-native-taskstore.AC3.2:** POST /sync/trigger. Verify mockSyncProvider.triggered incremented. Verify response contains updated status.

- **caldav-native-taskstore.AC3.3:** Set mockSyncProvider.status.InProgress=true. GET /sync/status. Verify JSON shows InProgress=true. The UI uses this to disable the button (client-side, not server-enforced).

Additional test:
- **Nil-safe:** Create handler with `syncProvider: nil`. GET /sync/status returns zero-value SyncStatus (no panic). POST /sync/trigger returns 404.

**Important:** Update all existing `NewHandler` calls in handler_test.go to include the new `syncProvider` parameter (pass `nil` for tests that don't need sync).

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/web/ -v -run TestSync
```

Expected: All sync tests pass. Existing tests also pass with updated `NewHandler` calls.

**Commit:** `test(web): add sync status and trigger endpoint tests`

<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_A -->
