# CalDAV-Native Task Store — Phase 3: Sync Engine

**Goal:** Adapter-agnostic reconciliation engine with configurable sync interval, manual trigger, and UB-wins conflict resolution. Tested with a mock adapter.

**Architecture:** New `internal/tasksync/` package defines the `DeviceAdapter` interface and a `SyncEngine` that manages adapter registration, runs a background sync loop on a configurable interval, exposes a manual trigger, and reports sync status. The engine uses `sync_state` and `task_sync_map` tables (added to `taskdb` schema) to track per-adapter sync tokens and per-task remote ID mappings. Reconciliation logic: pull remote changes, diff against sync map ETags, apply UB-wins conflict policy, push local changes. Follows the processor's Start/Stop/WithCancel goroutine pattern from `/home/jtd/ultrabridge/internal/processor/processor.go:87-113`.

**Tech Stack:** Go, `database/sql`, `context`, `sync`

**Scope:** 3 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC2: Supernote sync adapter
- **caldav-native-taskstore.AC2.9 Edge:** Task deleted on SPC side (hard delete) detected and soft-deleted locally

### caldav-native-taskstore.AC5: Adapter-ready architecture
- **caldav-native-taskstore.AC5.1 Success:** Sync engine accepts mock adapter implementing DeviceAdapter interface, runs full sync cycle
- **caldav-native-taskstore.AC5.2 Success:** Registering/unregistering an adapter requires no changes to task store, CalDAV backend, or web handler

---

<!-- START_TASK_1 -->
### Task 1: Add `sync_state` and `task_sync_map` tables to taskdb schema

**Files:**
- Modify: `internal/taskdb/schema.go` (add migration statements to the `stmts` slice)

**Implementation:**

Add two new `CREATE TABLE IF NOT EXISTS` statements to the existing `migrate()` function's `stmts` slice in `/home/jtd/ultrabridge/internal/taskdb/schema.go`, after the `tasks` table:

```go
		`CREATE TABLE IF NOT EXISTS sync_state (
			adapter_id      TEXT PRIMARY KEY,
			last_sync_token TEXT,
			last_sync_at    INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS task_sync_map (
			task_id     TEXT NOT NULL REFERENCES tasks(task_id),
			adapter_id  TEXT NOT NULL,
			remote_id   TEXT NOT NULL,
			remote_etag TEXT,
			last_pushed_at  INTEGER NOT NULL DEFAULT 0,
			last_pulled_at  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (task_id, adapter_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_sync_map_remote ON task_sync_map(adapter_id, remote_id)`,
```

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/taskdb/ -v
```

Expected: Existing tests pass (in-memory SQLite re-runs migrations including new tables).

**Commit:** `feat(taskdb): add sync_state and task_sync_map tables to schema`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/tasksync/types.go` — interfaces and types

**Files:**
- Create: `internal/tasksync/types.go`

**Implementation:**

Define the core types for the sync subsystem. These are pure data types with no I/O.

```go
package tasksync

import "context"

// ChangeType represents the kind of sync change.
type ChangeType int

const (
	ChangeCreate ChangeType = iota
	ChangeUpdate
	ChangeDelete
)

// RemoteTask is an adapter-neutral representation of a task from a remote device.
type RemoteTask struct {
	RemoteID      string
	Title         string
	Detail        string
	Status        string // "needsAction" or "completed" (Supernote convention)
	Importance    string
	DueTime       int64  // millisecond UTC unix, 0 = unset
	CompletedTime int64  // millisecond UTC unix — Supernote quirk: holds creation time
	Recurrence    string
	IsReminderOn  string
	Links         string
	ETag          string // opaque hash for change detection
}

// Change describes a single sync operation to push to a remote adapter.
type Change struct {
	Type     ChangeType
	TaskID   string     // local task ID
	RemoteID string     // remote task ID (empty for creates)
	Remote   RemoteTask // task data to push
}

// PushResult reports the outcome of a single push operation.
type PushResult struct {
	TaskID   string // local task ID from the Change
	RemoteID string // server-assigned remote ID (relevant for creates)
}

// SyncStatus reports the current state of the sync engine.
type SyncStatus struct {
	LastSyncAt    int64  // millisecond UTC unix
	NextSyncAt    int64  // millisecond UTC unix (0 = not scheduled)
	InProgress    bool
	LastError     string
	AdapterID     string
	AdapterActive bool
}

// DeviceAdapter is the interface all device sync adapters must implement.
type DeviceAdapter interface {
	// ID returns a unique identifier for this adapter (e.g., "supernote").
	ID() string

	// Start initializes the adapter (e.g., authenticates). Called once at registration.
	Start(ctx context.Context) error

	// Stop cleanly shuts down the adapter.
	Stop() error

	// Pull fetches remote tasks changed since the given sync token.
	// Returns the remote tasks, a new sync token, and any error.
	// An empty since token means "fetch all".
	Pull(ctx context.Context, since string) ([]RemoteTask, string, error)

	// Push applies local changes (creates, updates, deletes) to the remote device.
	// Returns a PushResult for each successfully pushed change (with server-assigned RemoteIDs for creates).
	Push(ctx context.Context, changes []Change) ([]PushResult, error)
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/
```

Expected: Builds without errors.

**Commit:** `feat(tasksync): define DeviceAdapter interface and sync types`

<!-- END_TASK_2 -->

<!-- START_SUBCOMPONENT_A (tasks 3-5) -->
<!-- START_TASK_3 -->
### Task 3: Create `internal/tasksync/syncmap.go` — sync map data access

**Files:**
- Create: `internal/tasksync/syncmap.go`

**Implementation:**

Data access layer for `sync_state` and `task_sync_map` tables. This is an Imperative Shell — pure SQL CRUD.

```go
package tasksync

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
)

// SyncMap provides data access for sync_state and task_sync_map tables.
type SyncMap struct {
	db *sql.DB
}

// NewSyncMap creates a sync map accessor.
func NewSyncMap(db *sql.DB) *SyncMap {
	return &SyncMap{db: db}
}

// SyncMapEntry represents a row in task_sync_map.
type SyncMapEntry struct {
	TaskID     string
	AdapterID  string
	RemoteID   string
	RemoteETag string
	LastPushed int64
	LastPulled int64
}

// GetSyncToken returns the last sync token for an adapter.
func (m *SyncMap) GetSyncToken(ctx context.Context, adapterID string) (string, error) {
	var token sql.NullString
	err := m.db.QueryRowContext(ctx,
		"SELECT last_sync_token FROM sync_state WHERE adapter_id = ?",
		adapterID).Scan(&token)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get sync token: %w", err)
	}
	if !token.Valid {
		return "", nil
	}
	return token.String, nil
}

// SetSyncToken upserts the sync token and timestamp for an adapter.
func (m *SyncMap) SetSyncToken(ctx context.Context, adapterID, token string, syncAt int64) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO sync_state (adapter_id, last_sync_token, last_sync_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(adapter_id) DO UPDATE SET
		   last_sync_token = excluded.last_sync_token,
		   last_sync_at = excluded.last_sync_at`,
		adapterID, token, syncAt)
	if err != nil {
		return fmt.Errorf("set sync token: %w", err)
	}
	return nil
}

// GetByTaskID returns the sync map entry for a task+adapter pair.
func (m *SyncMap) GetByTaskID(ctx context.Context, taskID, adapterID string) (*SyncMapEntry, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT task_id, adapter_id, remote_id, remote_etag, last_pushed_at, last_pulled_at
		 FROM task_sync_map WHERE task_id = ? AND adapter_id = ?`,
		taskID, adapterID)
	var e SyncMapEntry
	err := row.Scan(&e.TaskID, &e.AdapterID, &e.RemoteID, &e.RemoteETag, &e.LastPushed, &e.LastPulled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sync map entry: %w", err)
	}
	return &e, nil
}

// GetByRemoteID returns the sync map entry for a remote_id+adapter pair.
func (m *SyncMap) GetByRemoteID(ctx context.Context, adapterID, remoteID string) (*SyncMapEntry, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT task_id, adapter_id, remote_id, remote_etag, last_pushed_at, last_pulled_at
		 FROM task_sync_map WHERE adapter_id = ? AND remote_id = ?`,
		adapterID, remoteID)
	var e SyncMapEntry
	err := row.Scan(&e.TaskID, &e.AdapterID, &e.RemoteID, &e.RemoteETag, &e.LastPushed, &e.LastPulled)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sync map by remote ID: %w", err)
	}
	return &e, nil
}

// ListByAdapter returns all sync map entries for a given adapter.
func (m *SyncMap) ListByAdapter(ctx context.Context, adapterID string) ([]SyncMapEntry, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT task_id, adapter_id, remote_id, remote_etag, last_pushed_at, last_pulled_at
		 FROM task_sync_map WHERE adapter_id = ?`,
		adapterID)
	if err != nil {
		return nil, fmt.Errorf("list sync map: %w", err)
	}
	defer rows.Close()
	var entries []SyncMapEntry
	for rows.Next() {
		var e SyncMapEntry
		if err := rows.Scan(&e.TaskID, &e.AdapterID, &e.RemoteID, &e.RemoteETag, &e.LastPushed, &e.LastPulled); err != nil {
			return nil, fmt.Errorf("scan sync map: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Upsert creates or updates a sync map entry.
func (m *SyncMap) Upsert(ctx context.Context, e *SyncMapEntry) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO task_sync_map (task_id, adapter_id, remote_id, remote_etag, last_pushed_at, last_pulled_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(task_id, adapter_id) DO UPDATE SET
		   remote_id = excluded.remote_id,
		   remote_etag = excluded.remote_etag,
		   last_pushed_at = excluded.last_pushed_at,
		   last_pulled_at = excluded.last_pulled_at`,
		e.TaskID, e.AdapterID, e.RemoteID, e.RemoteETag, e.LastPushed, e.LastPulled)
	if err != nil {
		return fmt.Errorf("upsert sync map: %w", err)
	}
	return nil
}

// DeleteByTaskID removes the sync map entry for a task+adapter pair.
func (m *SyncMap) DeleteByTaskID(ctx context.Context, taskID, adapterID string) error {
	_, err := m.db.ExecContext(ctx,
		"DELETE FROM task_sync_map WHERE task_id = ? AND adapter_id = ?",
		taskID, adapterID)
	if err != nil {
		return fmt.Errorf("delete sync map: %w", err)
	}
	return nil
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/
```

Expected: Builds without errors.

**Commit:** `feat(tasksync): add SyncMap data access for sync state and task mapping`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Create `internal/tasksync/engine.go` — SyncEngine

**Files:**
- Create: `internal/tasksync/engine.go`

**Implementation:**

The SyncEngine manages adapter registration, runs a background sync loop, exposes manual trigger, and reports status. Follows the processor's Start/Stop pattern from `/home/jtd/ultrabridge/internal/processor/processor.go:87-113`.

Key behaviors:
- Registers one adapter at a time (single-adapter for now, extensible later)
- Background goroutine runs sync cycles on configurable interval
- Manual trigger via `TriggerSync()` sends on a channel
- `Status()` returns current `SyncStatus`
- Reconciliation cycle: Pull → diff against sync map → resolve conflicts (UB-wins) → push changes
- UB-wins: if both sides modified since last sync, local version wins and is pushed back to remote

```go
package tasksync

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

// TaskStore is the subset of caldav.TaskStore needed by the sync engine.
type TaskStore interface {
	List(ctx context.Context) ([]taskstore.Task, error)
	Get(ctx context.Context, taskID string) (*taskstore.Task, error)
	Create(ctx context.Context, t *taskstore.Task) error
	Update(ctx context.Context, t *taskstore.Task) error
	Delete(ctx context.Context, taskID string) error
}

// SyncEngine manages adapter registration and reconciliation cycles.
type SyncEngine struct {
	store    TaskStore
	syncMap  *SyncMap
	logger   *slog.Logger
	interval time.Duration

	mu        sync.Mutex
	adapter   DeviceAdapter
	cancel    context.CancelFunc
	done      chan struct{}
	trigger   chan struct{}
	status    SyncStatus
}

// NewSyncEngine creates a sync engine.
func NewSyncEngine(store TaskStore, db *sql.DB, logger *slog.Logger, interval time.Duration) *SyncEngine {
	return &SyncEngine{
		store:    store,
		syncMap:  NewSyncMap(db),
		logger:   logger,
		interval: interval,
		trigger:  make(chan struct{}, 1),
	}
}

// RegisterAdapter sets the active adapter. Must be called before Start.
func (e *SyncEngine) RegisterAdapter(adapter DeviceAdapter) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.adapter = adapter
	e.status.AdapterID = adapter.ID()
	e.status.AdapterActive = true
}

// UnregisterAdapter removes the active adapter.
func (e *SyncEngine) UnregisterAdapter() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.adapter != nil {
		e.adapter.Stop()
		e.adapter = nil
	}
	e.status.AdapterActive = false
}

// Start begins the background sync loop. Follows processor pattern.
func (e *SyncEngine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		return fmt.Errorf("sync engine already running")
	}
	if e.adapter == nil {
		return fmt.Errorf("no adapter registered")
	}
	if err := e.adapter.Start(ctx); err != nil {
		return fmt.Errorf("adapter start: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.done = make(chan struct{})
	go e.run(ctx)
	return nil
}

// Stop halts the sync loop and waits for completion.
func (e *SyncEngine) Stop() error {
	e.mu.Lock()
	cancel := e.cancel
	done := e.done
	e.cancel = nil
	e.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	if e.adapter != nil {
		e.adapter.Stop()
	}
	return nil
}

// TriggerSync requests an immediate sync cycle.
func (e *SyncEngine) TriggerSync() {
	select {
	case e.trigger <- struct{}{}:
	default:
		// Already triggered, don't block
	}
}

// Status returns the current sync status.
func (e *SyncEngine) Status() SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *SyncEngine) run(ctx context.Context) {
	defer close(e.done)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runCycle(ctx)
		case <-e.trigger:
			e.runCycle(ctx)
			ticker.Reset(e.interval)
		}
	}
}

func (e *SyncEngine) runCycle(ctx context.Context) {
	e.mu.Lock()
	adapter := e.adapter
	e.status.InProgress = true
	e.mu.Unlock()

	if adapter == nil {
		return
	}

	err := e.reconcile(ctx, adapter)

	e.mu.Lock()
	e.status.InProgress = false
	now := time.Now().UnixMilli()
	e.status.LastSyncAt = now
	e.status.NextSyncAt = now + e.interval.Milliseconds()
	if err != nil {
		e.status.LastError = err.Error()
		e.logger.Warn("sync cycle failed", "adapter", adapter.ID(), "error", err)
	} else {
		e.status.LastError = ""
		e.logger.Info("sync cycle completed", "adapter", adapter.ID())
	}
	e.mu.Unlock()
}

func (e *SyncEngine) reconcile(ctx context.Context, adapter DeviceAdapter) error {
	adapterID := adapter.ID()

	// 1. Get sync token
	token, err := e.syncMap.GetSyncToken(ctx, adapterID)
	if err != nil {
		return fmt.Errorf("get sync token: %w", err)
	}

	// 2. Pull remote tasks
	remoteTasks, newToken, err := adapter.Pull(ctx, token)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}

	// 3. Process remote changes (imports, updates, deletes)
	for _, rt := range remoteTasks {
		if err := e.processRemoteTask(ctx, adapterID, rt); err != nil {
			e.logger.Warn("process remote task failed", "remote_id", rt.RemoteID, "error", err)
		}
	}

	// 4. Detect remote hard-deletes: sync_map entries whose remote_id is
	//    not in the Pull results represent tasks deleted on the remote side.
	remoteIDs := make(map[string]bool, len(remoteTasks))
	for _, rt := range remoteTasks {
		remoteIDs[rt.RemoteID] = true
	}
	entries, err := e.syncMap.ListByAdapter(ctx, adapterID)
	if err != nil {
		return fmt.Errorf("list sync map: %w", err)
	}
	for _, entry := range entries {
		if !remoteIDs[entry.RemoteID] {
			// Remote task gone — soft-delete locally
			if err := e.store.Delete(ctx, entry.TaskID); err != nil {
				e.logger.Warn("soft-delete for remote hard-delete failed", "task_id", entry.TaskID, "error", err)
			}
			e.syncMap.DeleteByTaskID(ctx, entry.TaskID, adapterID)
			e.logger.Info("remote hard-delete detected, soft-deleted locally", "task_id", entry.TaskID, "remote_id", entry.RemoteID)
		}
	}

	// 5. Find local changes to push (renumbered from original)
	changes, err := e.findLocalChanges(ctx, adapterID)
	if err != nil {
		return fmt.Errorf("find local changes: %w", err)
	}

	// 5. Push local changes
	if len(changes) > 0 {
		results, err := adapter.Push(ctx, changes)
		if err != nil {
			return fmt.Errorf("push: %w", err)
		}
		// Update sync map after successful push using server-assigned remote IDs
		now := time.Now().UnixMilli()
		resultByTaskID := make(map[string]PushResult, len(results))
		for _, r := range results {
			resultByTaskID[r.TaskID] = r
		}
		for _, c := range changes {
			if c.Type == ChangeDelete {
				e.syncMap.DeleteByTaskID(ctx, c.TaskID, adapterID)
			} else {
				remoteID := c.RemoteID
				if r, ok := resultByTaskID[c.TaskID]; ok && r.RemoteID != "" {
					remoteID = r.RemoteID
				}
				e.syncMap.Upsert(ctx, &SyncMapEntry{
					TaskID:    c.TaskID,
					AdapterID: adapterID,
					RemoteID:  remoteID,
					LastPushed: now,
				})
			}
		}
	}

	// 6. Update sync token
	now := time.Now().UnixMilli()
	if err := e.syncMap.SetSyncToken(ctx, adapterID, newToken, now); err != nil {
		return fmt.Errorf("set sync token: %w", err)
	}

	return nil
}

func (e *SyncEngine) processRemoteTask(ctx context.Context, adapterID string, rt RemoteTask) error {
	entry, err := e.syncMap.GetByRemoteID(ctx, adapterID, rt.RemoteID)
	if err != nil {
		return err
	}

	if entry == nil {
		// New remote task — import to local store
		t := remoteToLocal(rt)
		if err := e.store.Create(ctx, t); err != nil {
			return fmt.Errorf("create imported task: %w", err)
		}
		now := time.Now().UnixMilli()
		return e.syncMap.Upsert(ctx, &SyncMapEntry{
			TaskID:     t.TaskID,
			AdapterID:  adapterID,
			RemoteID:   rt.RemoteID,
			RemoteETag: rt.ETag,
			LastPulled: now,
		})
	}

	// Existing task — check if remote changed
	if entry.RemoteETag == rt.ETag {
		return nil // No remote change
	}

	// Remote changed — check if local also changed (conflict)
	local, err := e.store.Get(ctx, entry.TaskID)
	if err != nil {
		if taskstore.IsNotFound(err) {
			// Local was deleted — soft-delete wins (UB-wins)
			return nil
		}
		return err
	}

	localModified := int64(0)
	if local.LastModified.Valid {
		localModified = local.LastModified.Int64
	}

	if localModified > entry.LastPulled {
		// Both sides changed — UB wins, will be pushed back in step 4
		e.logger.Info("conflict: UB wins", "task_id", entry.TaskID, "adapter", adapterID)
		return nil
	}

	// Only remote changed — apply remote update
	applyRemoteToLocal(local, rt)
	if err := e.store.Update(ctx, local); err != nil {
		return fmt.Errorf("update from remote: %w", err)
	}
	now := time.Now().UnixMilli()
	return e.syncMap.Upsert(ctx, &SyncMapEntry{
		TaskID:     entry.TaskID,
		AdapterID:  adapterID,
		RemoteID:   rt.RemoteID,
		RemoteETag: rt.ETag,
		LastPulled: now,
	})
}

func (e *SyncEngine) findLocalChanges(ctx context.Context, adapterID string) ([]Change, error) {
	tasks, err := e.store.List(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := e.syncMap.ListByAdapter(ctx, adapterID)
	if err != nil {
		return nil, err
	}

	// Build lookup: taskID -> sync map entry
	entryByTaskID := make(map[string]*SyncMapEntry, len(entries))
	for i := range entries {
		entryByTaskID[entries[i].TaskID] = &entries[i]
	}

	var changes []Change
	for i := range tasks {
		t := &tasks[i]
		entry := entryByTaskID[t.TaskID]

		if entry == nil {
			// Local task not yet synced — create on remote
			changes = append(changes, Change{
				Type:   ChangeCreate,
				TaskID: t.TaskID,
				Remote: localToRemote(t),
			})
			continue
		}

		// Check if local changed since last push
		localModified := int64(0)
		if t.LastModified.Valid {
			localModified = t.LastModified.Int64
		}
		if localModified > entry.LastPushed {
			changes = append(changes, Change{
				Type:     ChangeUpdate,
				TaskID:   t.TaskID,
				RemoteID: entry.RemoteID,
				Remote:   localToRemote(t),
			})
		}

		delete(entryByTaskID, t.TaskID)
	}

	// Remaining entries are tasks deleted locally (not in List because soft-deleted)
	for _, entry := range entryByTaskID {
		changes = append(changes, Change{
			Type:     ChangeDelete,
			TaskID:   entry.TaskID,
			RemoteID: entry.RemoteID,
		})
	}

	return changes, nil
}

// remoteToLocal converts a RemoteTask to a new local Task.
func remoteToLocal(rt RemoteTask) *taskstore.Task {
	now := time.Now().UnixMilli()
	ct := rt.CompletedTime
	if ct == 0 {
		ct = now
	}
	return &taskstore.Task{
		TaskID:        taskstore.GenerateTaskID(rt.Title, now),
		Title:         taskstore.SqlStr(rt.Title),
		Detail:        taskstore.SqlStr(rt.Detail),
		Status:        taskstore.SqlStr(rt.Status),
		Importance:    taskstore.SqlStr(rt.Importance),
		DueTime:       rt.DueTime,
		CompletedTime: sql.NullInt64{Int64: ct, Valid: true},
		LastModified:  sql.NullInt64{Int64: now, Valid: true},
		Recurrence:    taskstore.SqlStr(rt.Recurrence),
		IsReminderOn:  rt.IsReminderOn,
		Links:         taskstore.SqlStr(rt.Links),
		IsDeleted:     "N",
	}
}

// applyRemoteToLocal updates a local task with remote field values.
func applyRemoteToLocal(local *taskstore.Task, rt RemoteTask) {
	local.Title = taskstore.SqlStr(rt.Title)
	local.Detail = taskstore.SqlStr(rt.Detail)
	local.Status = taskstore.SqlStr(rt.Status)
	local.Importance = taskstore.SqlStr(rt.Importance)
	local.DueTime = rt.DueTime
	if rt.CompletedTime != 0 {
		local.CompletedTime = sql.NullInt64{Int64: rt.CompletedTime, Valid: true}
	}
	local.Recurrence = taskstore.SqlStr(rt.Recurrence)
	local.IsReminderOn = rt.IsReminderOn
	local.Links = taskstore.SqlStr(rt.Links)
}

// localToRemote converts a local Task to a RemoteTask for pushing.
func localToRemote(t *taskstore.Task) RemoteTask {
	return RemoteTask{
		Title:         taskstore.NullStr(t.Title),
		Detail:        taskstore.NullStr(t.Detail),
		Status:        taskstore.NullStr(t.Status),
		Importance:    taskstore.NullStr(t.Importance),
		DueTime:       t.DueTime,
		CompletedTime: t.CompletedTime.Int64,
		Recurrence:    taskstore.NullStr(t.Recurrence),
		IsReminderOn:  t.IsReminderOn,
		Links:         taskstore.NullStr(t.Links),
	}
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/
```

Expected: Builds without errors.

**Commit:** `feat(tasksync): implement SyncEngine with reconciliation and UB-wins conflict policy`

<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Tests for SyncEngine with mock adapter

**Verifies:** caldav-native-taskstore.AC5.1, caldav-native-taskstore.AC5.2

**Files:**
- Create: `internal/tasksync/engine_test.go`

**Testing:**

Tests use in-memory SQLite (open taskdb with `:memory:`) and a hand-rolled mock adapter implementing `DeviceAdapter`. Follow project patterns: table-driven tests, `t.Run()`, explicit `if err != nil { t.Fatal() }`.

The mock adapter should:
- Store tasks in a `map[string]RemoteTask`
- Track Push calls for verification
- Return configurable Pull results
- Track Start/Stop calls

Tests must verify:
- **caldav-native-taskstore.AC5.1:** Register mock adapter, start engine, trigger a sync cycle. Mock adapter's Pull returns 2 tasks. After cycle, both tasks exist in local store. Then create a local task, trigger sync. Mock adapter's Push receives the new task as a ChangeCreate.

- **caldav-native-taskstore.AC5.2:** Register adapter, trigger cycle, unregister adapter. Verify task store and its data are unchanged. Register a different mock adapter with a different ID, trigger cycle. Verify it works with no code changes.

Additional test cases:
- **UB-wins conflict:** Create local task, sync it (mock Pull returns it). Modify both local and remote versions. Run sync cycle. Verify local version wins — remote gets pushed the local version.
- **Remote create:** Mock Pull returns a new task. After sync, task exists in local store with sync map entry.
- **Remote update:** Mock Pull returns an existing task with different ETag. After sync, local task updated with remote values (if local didn't change).
- **Local delete:** Delete a synced task locally. Run sync. Verify ChangeDelete pushed to adapter.
- **Remote hard-delete (AC2.9):** Create a local task, sync it (mock Pull returns it with a sync_map entry). Next cycle, mock Pull returns empty list. Verify local task is soft-deleted and sync_map entry removed.
- **Status reporting:** Verify `engine.Status()` reflects InProgress during cycle, LastSyncAt after, LastError on failure.
- **Manual trigger:** Call `engine.TriggerSync()`, verify sync cycle runs promptly.

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/tasksync/ -v
```

Expected: All tests pass.

**Commit:** `test(tasksync): add SyncEngine tests with mock adapter`

<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_TASK_6 -->
### Task 6: Add CLAUDE.md for tasksync package

**Files:**
- Create: `internal/tasksync/CLAUDE.md`

**Implementation:**

```markdown
# Task Sync Engine

Last verified: 2026-04-04

## Purpose
Adapter-agnostic reconciliation engine for syncing tasks between UltraBridge's local store and external devices. Manages adapter registration, sync scheduling, conflict resolution, and sync state tracking.

## Contracts
- **Exposes**: `SyncEngine` (Start, Stop, TriggerSync, Status, RegisterAdapter, UnregisterAdapter), `DeviceAdapter` interface, `SyncMap` (sync state data access), types (RemoteTask, Change, SyncStatus, PushResult)
- **Guarantees**: UB-wins conflict resolution. Detects remote hard-deletes. Sync map tracks per-task remote ID mapping. Background loop with configurable interval + manual trigger.
- **Expects**: A `TaskStore` implementation and a `*sql.DB` with sync_state/task_sync_map tables.

## Dependencies
- **Uses**: `taskstore` (Task model, helpers), `database/sql` (sync tables)
- **Used by**: `cmd/ultrabridge` (startup), `web.Handler` (via SyncStatusProvider interface)
- **Boundary**: Does not import caldav, web, or vendor-specific packages. Adapters live in sub-packages.

## Key Decisions
- Single adapter at a time (extensible to multiple later)
- Follows processor Start/Stop/WithCancel pattern
- Sync map uses SQLite tables in the task DB
```

**Verification:** Documentation only.

**Commit:** `docs(tasksync): add CLAUDE.md for sync engine package`

<!-- END_TASK_6 -->
