# CalDAV-Native Task Store — Phase 5: Migration

**Goal:** Auto-import existing Supernote tasks into the new SQLite task store on first run.

**Architecture:** A migration function in `internal/tasksync/supernote/migration.go` uses the SPC REST API client (from Phase 4) to fetch all existing tasks and inserts them into the SQLite task store (from Phase 1), creating sync map entries for each. `main.go` detects an empty task DB + reachable SPC and runs the migration once before starting the sync engine. Subsequent starts with a populated DB skip migration. Starting without SPC (sync disabled) begins with an empty store — no error.

**Tech Stack:** Go

**Scope:** 5 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC4: Clean migration
- **caldav-native-taskstore.AC4.1 Success:** First run with empty task DB and reachable SPC imports all non-deleted tasks, creates sync map entries
- **caldav-native-taskstore.AC4.2 Success:** Subsequent starts with populated task DB skip import
- **caldav-native-taskstore.AC4.3 Success:** First run without SPC (standalone mode) starts with empty store, no error

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
<!-- START_TASK_1 -->
### Task 1: Add `IsEmpty` method to `taskdb.Store`

**Files:**
- Modify: `internal/taskdb/store.go` (add method)

**Implementation:**

Add a method to detect whether the task store has any tasks (used by main.go to decide whether to run migration):

```go
// IsEmpty returns true if the task store has no tasks (including deleted ones).
// Used to detect first-run state for migration.
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks").Scan(&count)
	if err != nil {
		return false, fmt.Errorf("count tasks: %w", err)
	}
	return count == 0, nil
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/taskdb/
```

Expected: Builds without errors.

**Commit:** `feat(taskdb): add IsEmpty method for migration detection`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/tasksync/supernote/migration.go`

**Files:**
- Create: `internal/tasksync/supernote/migration.go`

**Implementation:**

The migration function fetches tasks from SPC via the REST API client and imports them into the local SQLite store. It creates sync map entries so the sync engine knows these tasks already exist on the remote.

```go
package supernote

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
	"github.com/sysop/ultrabridge/internal/tasksync"
)

// MigrateFromSPC imports all non-deleted tasks from SPC into the local task store
// and creates sync map entries. Returns the number of tasks imported.
func MigrateFromSPC(ctx context.Context, client *Client, store TaskCreator, syncMap *tasksync.SyncMap, logger *slog.Logger) (int, error) {
	spcTasks, err := client.FetchTasks(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch SPC tasks for migration: %w", err)
	}

	imported := 0
	now := time.Now().UnixMilli()
	for _, spc := range spcTasks {
		if spc.IsDeleted == "Y" {
			continue
		}

		t := &taskstore.Task{
			TaskID:        spc.ID, // Preserve original SPC task ID
			Title:         taskstore.SqlStr(spc.Title),
			Detail:        taskstore.SqlStr(spc.Detail),
			Status:        taskstore.SqlStr(spc.Status),
			Importance:    taskstore.SqlStr(spc.Importance),
			DueTime:       spc.DueTime,
			CompletedTime: sql.NullInt64{Int64: spc.CompletedTime, Valid: spc.CompletedTime != 0},
			LastModified:  sql.NullInt64{Int64: spc.LastModified, Valid: spc.LastModified != 0},
			Recurrence:    taskstore.SqlStr(spc.Recurrence),
			IsReminderOn:  spc.IsReminderOn,
			Links:         taskstore.SqlStr(spc.Links),
			IsDeleted:     "N",
		}

		if err := store.Create(ctx, t); err != nil {
			logger.Warn("migration: failed to import task", "task_id", spc.ID, "title", spc.Title, "error", err)
			continue
		}

		// Create sync map entry so engine knows this task exists on SPC
		entry := &tasksync.SyncMapEntry{
			TaskID:     t.TaskID,
			AdapterID:  "supernote",
			RemoteID:   spc.ID,
			RemoteETag: computeSPCETag(spc),
			LastPulled: now,
			LastPushed: now, // Mark as pushed too so engine doesn't re-push
		}
		if err := syncMap.Upsert(ctx, entry); err != nil {
			logger.Warn("migration: failed to create sync map entry", "task_id", spc.ID, "error", err)
		}

		imported++
	}

	logger.Info("migration complete", "imported", imported, "total_spc", len(spcTasks))
	return imported, nil
}

// TaskCreator is the subset of the task store needed for migration.
type TaskCreator interface {
	Create(ctx context.Context, t *taskstore.Task) error
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/supernote/
```

Expected: Builds without errors.

**Commit:** `feat(supernote): add migration to import tasks from SPC on first run`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Wire migration into `main.go`

**Files:**
- Modify: `cmd/ultrabridge/main.go` (after taskdb.Open, before sync engine start)

**Implementation:**

After opening the task DB and creating the store (Phase 1 wiring), and before starting the sync engine, add migration logic. Insert between the taskdb.Open block and the notifier creation:

```go
	// Run migration if task DB is empty and SPC sync is enabled
	if cfg.SNSyncEnabled {
		isEmpty, err := store.IsEmpty(context.Background())
		if err != nil {
			logger.Error("taskdb empty check failed", "err", err)
			os.Exit(1)
		}
		if isEmpty {
			logger.Info("empty task DB detected, attempting migration from SPC")
			migClient := supernote.NewClient(cfg.SNAPIURL, cfg.SNPassword, logger)
			if err := migClient.Login(context.Background()); err != nil {
				logger.Warn("SPC login failed for migration, starting with empty store", "error", err)
			} else {
				sm := tasksync.NewSyncMap(taskDB)
				count, err := supernote.MigrateFromSPC(context.Background(), migClient, store, sm, logger)
				if err != nil {
					logger.Warn("migration from SPC failed", "error", err)
				} else {
					logger.Info("migrated tasks from SPC", "count", count)
				}
			}
		} else {
			logger.Info("task DB populated, skipping migration")
		}
	}
```

Add import for `supernote` and `tasksync` packages:

```go
"github.com/sysop/ultrabridge/internal/tasksync"
"github.com/sysop/ultrabridge/internal/tasksync/supernote"
```

Also wire the sync engine start after migration (and after notifier creation). **Important scoping:** Declare `syncEngine` before the `if` block so it's accessible from Phase 6's web handler wiring. After the notifier.Connect block:

```go
	// Start sync engine if enabled
	var syncEngine *tasksync.SyncEngine
	if cfg.SNSyncEnabled {
		syncEngine = tasksync.NewSyncEngine(
			store, taskDB, logger,
			time.Duration(cfg.SNSyncInterval)*time.Second,
		)
		snAdapter := supernote.NewAdapter(cfg.SNAPIURL, cfg.SNPassword, notifier, logger)
		syncEngine.RegisterAdapter(snAdapter)
		if err := syncEngine.Start(context.Background()); err != nil {
			logger.Warn("sync engine start failed", "error", err)
		} else {
			defer syncEngine.Stop()
		}
	}
```

**Important:** When `UB_SN_SYNC_ENABLED=false`, none of this code runs — task store and CalDAV work standalone.

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./cmd/ultrabridge/
```

Expected: Builds without errors.

**Commit:** `feat(main): wire migration and sync engine startup`

<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_TASK_4 -->
### Task 4: Migration tests

**Verifies:** caldav-native-taskstore.AC4.1, caldav-native-taskstore.AC4.2, caldav-native-taskstore.AC4.3

**Files:**
- Create: `internal/tasksync/supernote/migration_test.go`

**Testing:**

Tests use `httptest.NewServer` for the mock SPC and in-memory SQLite for the task store and sync map. Follow project patterns.

Tests must verify:
- **caldav-native-taskstore.AC4.1:** Mock SPC returns 5 tasks (3 active, 2 deleted). Call MigrateFromSPC. Verify 3 tasks imported into store, 3 sync map entries created, deleted tasks skipped. Verify task fields map correctly (title, status, due_time, completedTime quirk preserved).

- **caldav-native-taskstore.AC4.2:** Import tasks, then call store.IsEmpty() — returns false. This is the gate that prevents re-migration in main.go. Verify IsEmpty returns true on fresh DB and false after import.

- **caldav-native-taskstore.AC4.3:** Call MigrateFromSPC with a mock SPC that fails login. Verify function returns error. Verify store is still empty (no partial import). This simulates the main.go flow where login failure logs a warning and starts with empty store.

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/tasksync/supernote/ -v -run TestMigrat
```

Expected: All migration tests pass.

**Commit:** `test(supernote): add migration tests`

<!-- END_TASK_4 -->
