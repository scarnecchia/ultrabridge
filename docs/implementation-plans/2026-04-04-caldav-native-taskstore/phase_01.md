# CalDAV-Native Task Store — Phase 1: SQLite Task Store

**Goal:** Replace the MariaDB-backed task store with a new SQLite-backed `taskdb` package that implements the existing `caldav.TaskStore` interface.

**Architecture:** New `internal/taskdb/` package follows the established `notedb` pattern (WAL mode, MaxOpenConns=1, idempotent migrations). The new store implements the same 6-method `caldav.TaskStore` interface consumed by the CalDAV backend and web handler. The existing `taskstore` package and its `Task` model remain unchanged — the new store operates on the same `taskstore.Task` type. A new `UB_TASK_DB_PATH` config var points to the SQLite file. `main.go` swaps `taskstore.New(database, userID)` for `taskdb.Open()` + `taskdb.NewStore()`.

**Tech Stack:** Go, `modernc.org/sqlite` (already a project dependency), `database/sql`

**Scope:** 1 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`. Workflow: write code locally, push, pull on server, rebuild and test.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC1: UltraBridge owns task storage
- **caldav-native-taskstore.AC1.1 Success:** CalDAV client creates task via PUT, task persists in SQLite and is retrievable via GET
- **caldav-native-taskstore.AC1.2 Success:** CalDAV client updates task (title, status, due date), changes persist and ETag updates
- **caldav-native-taskstore.AC1.3 Success:** CalDAV client deletes task, task is soft-deleted (not returned in LIST, still in DB)
- **caldav-native-taskstore.AC1.6 Edge:** CTag changes when any task is created, modified, or deleted
- **caldav-native-taskstore.AC1.7 Edge:** MaxLastModified returns 0 for empty store

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Create `internal/taskdb/db.go` and `internal/taskdb/schema.go`

**Files:**
- Create: `internal/taskdb/db.go`
- Create: `internal/taskdb/schema.go`

**Implementation:**

`db.go` follows the `notedb.Open()` pattern exactly (see `/home/jtd/ultrabridge/internal/notedb/db.go:13-30`):

```go
package taskdb

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) the SQLite task database at path, applies schema
// migrations, and returns the connection pool. Enables WAL mode and foreign keys.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("taskdb open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("taskdb ping: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("taskdb migrate: %w", err)
	}
	return db, nil
}
```

`schema.go` creates the `tasks` table from the design plan schema. Note: `sync_state` and `task_sync_map` tables are deferred to Phase 3. The schema matches the existing `taskstore.Task` model fields exactly, plus `ical_blob` (used in Phase 2), `created_at`, and `updated_at`:

```go
package taskdb

import (
	"context"
	"database/sql"
	"fmt"
)

func migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			task_id        TEXT PRIMARY KEY,
			title          TEXT,
			detail         TEXT,
			status         TEXT NOT NULL DEFAULT 'needsAction',
			importance     TEXT,
			due_time       INTEGER NOT NULL DEFAULT 0,
			completed_time INTEGER NOT NULL DEFAULT 0,
			last_modified  INTEGER NOT NULL DEFAULT 0,
			recurrence     TEXT,
			is_reminder_on TEXT NOT NULL DEFAULT 'N',
			links          TEXT,
			is_deleted     TEXT NOT NULL DEFAULT 'N',
			ical_blob      TEXT,
			created_at     INTEGER NOT NULL,
			updated_at     INTEGER NOT NULL
		)`,
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration statement %d: %w", i, err)
		}
	}
	return nil
}
```

**Verification:**

```bash
# On remote server (sysop@192.168.9.52):
go build -C ~/src/ultrabridge ./internal/taskdb/
```

Expected: Builds without errors.

**Commit:** `feat(taskdb): add SQLite database opener and schema migration`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/taskdb/store.go` — TaskStore implementation

**Verifies:** caldav-native-taskstore.AC1.1, caldav-native-taskstore.AC1.2, caldav-native-taskstore.AC1.3, caldav-native-taskstore.AC1.6, caldav-native-taskstore.AC1.7

**Files:**
- Create: `internal/taskdb/store.go`
- Test: `internal/taskdb/store_test.go` (unit — in-memory SQLite)

**Implementation:**

`store.go` implements the `caldav.TaskStore` interface (defined at `/home/jtd/ultrabridge/internal/caldav/backend.go:16-23`). It operates on the existing `taskstore.Task` type (from `/home/jtd/ultrabridge/internal/taskstore/model.go`). The new store does NOT have a `userID` field — the SQLite database is single-user by design (one DB per UltraBridge instance). All six methods mirror the behavior of the existing `taskstore.Store` (at `/home/jtd/ultrabridge/internal/taskstore/store.go`):

```go
package taskdb

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

// Store implements caldav.TaskStore against a local SQLite database.
type Store struct {
	db *sql.DB
}

// NewStore creates a new SQLite-backed task store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const taskColumns = `task_id, title, detail, status, importance, due_time,
	completed_time, last_modified, recurrence, is_reminder_on, links, is_deleted`

func (s *Store) List(ctx context.Context) ([]taskstore.Task, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE is_deleted = 'N'")
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []taskstore.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) Get(ctx context.Context, taskID string) (*taskstore.Task, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ? AND is_deleted = 'N'",
		taskID)
	t, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, taskstore.ErrNotFound
		}
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return &t, nil
}

func (s *Store) Create(ctx context.Context, t *taskstore.Task) error {
	now := time.Now().UnixMilli()
	if t.TaskID == "" {
		t.TaskID = taskstore.GenerateTaskID(taskstore.NullStr(t.Title), now)
	}
	if !t.CompletedTime.Valid {
		t.CompletedTime = sql.NullInt64{Int64: now, Valid: true}
	}
	if !t.LastModified.Valid {
		t.LastModified = sql.NullInt64{Int64: now, Valid: true}
	}
	if t.IsDeleted == "" {
		t.IsDeleted = "N"
	}
	if t.IsReminderOn == "" {
		t.IsReminderOn = "N"
	}
	if !t.Status.Valid {
		t.Status = sql.NullString{String: "needsAction", Valid: true}
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks
		(task_id, title, detail, status, importance, due_time,
		 completed_time, last_modified, recurrence, is_reminder_on,
		 links, is_deleted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.CompletedTime, t.LastModified, t.Recurrence, t.IsReminderOn,
		t.Links, t.IsDeleted, now, now)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Store) Update(ctx context.Context, t *taskstore.Task) error {
	now := time.Now().UnixMilli()
	t.LastModified = sql.NullInt64{Int64: now, Valid: true}

	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET
		title = ?, detail = ?, status = ?, importance = ?, due_time = ?,
		completed_time = ?, last_modified = ?, recurrence = ?,
		is_reminder_on = ?, links = ?, updated_at = ?
		WHERE task_id = ?`,
		t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.CompletedTime, t.LastModified, t.Recurrence,
		t.IsReminderOn, t.Links, now,
		t.TaskID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.TaskID, err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, taskID string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET
		is_deleted = 'Y', last_modified = ?, updated_at = ?
		WHERE task_id = ?`,
		now, now, taskID)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	return nil
}

func (s *Store) MaxLastModified(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT MAX(last_modified) FROM tasks WHERE is_deleted = 'N'").Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("max last_modified: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (taskstore.Task, error) {
	var t taskstore.Task
	err := s.Scan(
		&t.TaskID, &t.Title, &t.Detail, &t.Status, &t.Importance,
		&t.DueTime, &t.CompletedTime, &t.LastModified, &t.Recurrence,
		&t.IsReminderOn, &t.Links, &t.IsDeleted,
	)
	return t, err
}

func scanTaskRow(row *sql.Row) (taskstore.Task, error) {
	return scanTask(row)
}
```

Key differences from the existing `taskstore.Store`:
- No `userID` field — SQLite DB is single-user
- No `task_list_id` column — not needed for local store (Supernote-specific)
- No `user_id` column — not needed for local store
- Adds `created_at` and `updated_at` metadata columns
- `Update` does not filter by `user_id`

**Testing:**

Tests use in-memory SQLite (`:memory:`), following the pattern in `/home/jtd/ultrabridge/internal/notedb/db_test.go`. No mocking — real SQLite, same as production.

Tests must verify each AC listed above:
- **caldav-native-taskstore.AC1.1:** Create a task via `store.Create()`, retrieve via `store.Get()` — verify all fields match, task persists in SQLite
- **caldav-native-taskstore.AC1.2:** Create a task, update title/status/due_time via `store.Update()`, verify fields changed, verify ETag (computed externally) would differ (last_modified bumped)
- **caldav-native-taskstore.AC1.3:** Create a task, delete via `store.Delete()`, verify `store.Get()` returns `ErrNotFound`, verify `store.List()` excludes it
- **caldav-native-taskstore.AC1.6:** Create a task, note `MaxLastModified()` value; update the task, verify `MaxLastModified()` increased; delete the task with a fresh timestamp, verify `MaxLastModified()` reflects the deleted task's bumped timestamp is excluded (deleted tasks filtered from MAX query)
- **caldav-native-taskstore.AC1.7:** On empty store, `MaxLastModified()` returns 0

Follow project patterns: table-driven tests with `t.Run()`, explicit `if err != nil { t.Fatal() }` assertions, helper `openTestStore(t)` that calls `taskdb.Open(ctx, ":memory:")` + `taskdb.NewStore(db)` with `t.Cleanup(db.Close)`.

**Verification:**

```bash
# On remote server (sysop@192.168.9.52):
go test -C ~/src/ultrabridge ./internal/taskdb/ -v
```

Expected: All tests pass.

**Commit:** `feat(taskdb): implement TaskStore interface against SQLite`

<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_TASK_3 -->
### Task 3: Add `UB_TASK_DB_PATH` to config

**Files:**
- Modify: `internal/config/config.go:11-60` (Config struct) and `internal/config/config.go:62-100` (Load function)

**Implementation:**

Add `TaskDBPath` field to the `Config` struct (in the Paths section, near `DBPath` at line 51):

```go
// In Config struct, after DBPath:
TaskDBPath string
```

In `Load()`, after line 85 (`cfg.DBPath = ...`), add:

```go
cfg.TaskDBPath = envOrDefault("UB_TASK_DB_PATH", "/data/ultrabridge-tasks.db")
```

Default `/data/ultrabridge-tasks.db` follows the same convention as `UB_DB_PATH` which defaults to `/data/ultrabridge.db`.

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./...
```

Expected: Builds without errors.

**Commit:** `feat(config): add UB_TASK_DB_PATH env var for task SQLite database`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Wire new task store in `main.go`

**Files:**
- Modify: `cmd/ultrabridge/main.go:1-27` (imports) and `cmd/ultrabridge/main.go:56-90` (startup sequence)

**Implementation:**

In imports, add:

```go
"github.com/sysop/ultrabridge/internal/taskdb"
```

Replace the MariaDB task store creation at line 77 (`store := taskstore.New(database, userID)`) with the new SQLite-based store. Insert after the MariaDB connection block (after line 75) and before the notifier creation (line 79):

```go
	// Open the task SQLite DB
	taskDB, err := taskdb.Open(context.Background(), cfg.TaskDBPath)
	if err != nil {
		logger.Error("taskdb open failed", "err", err, "path", cfg.TaskDBPath)
		os.Exit(1)
	}
	defer taskDB.Close()

	store := taskdb.NewStore(taskDB)
```

Remove the old line 77: `store := taskstore.New(database, userID)`.

The `taskstore` import should remain because `taskstore.Task`, `taskstore.ErrNotFound`, mapping helpers, etc. are still used by the `caldav` and `web` packages. The old `taskstore.Store` type is no longer instantiated in main, but the package is still needed.

**Important:** The MariaDB connection (`database`) is still needed for the notes pipeline (processor catalog sync, user ID discovery). Do NOT remove the MariaDB connection code. The Supernote adapter (Phase 4) will handle reading tasks from SPC via REST API rather than MariaDB.

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./cmd/ultrabridge/
```

Expected: Builds without errors.

```bash
# Run existing tests to verify nothing broke:
go test -C ~/src/ultrabridge ./...
```

Expected: All existing tests pass. The CalDAV backend tests use a mock task store and are unaffected. The integration tests (which require MariaDB) may need updating in Phase 5 — they currently create tasks against MariaDB directly.

**Commit:** `feat(main): wire SQLite task store replacing MariaDB taskstore`

<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Add CLAUDE.md for new taskdb package

**Files:**
- Create: `internal/taskdb/CLAUDE.md`

**Implementation:**

Create a domain CLAUDE.md following the pattern established by existing packages (see `/home/jtd/ultrabridge/internal/notedb/CLAUDE.md` and `/home/jtd/ultrabridge/internal/taskstore/CLAUDE.md`):

```markdown
# Task Database

Last verified: 2026-04-04

## Purpose
Opens and migrates the SQLite database used for task storage.
Implements the `caldav.TaskStore` interface for CalDAV and web UI task operations.

## Contracts
- **Exposes**: `Open(ctx, path) (*sql.DB, error)` -- opens/creates SQLite DB, applies migrations, returns pool. `NewStore(db) *Store` -- creates TaskStore implementation.
- **Guarantees**: WAL mode and foreign keys enabled. Schema is idempotent (safe to call on existing DB). MaxOpenConns=1 (SQLite single-writer). Implements all 6 `caldav.TaskStore` methods. Uses `taskstore.ErrNotFound` sentinel for missing tasks.
- **Expects**: Writable filesystem path. Context for cancellation.

## Dependencies
- **Uses**: `modernc.org/sqlite` (pure-Go, no CGO), `taskstore` (Task model, ErrNotFound, mapping helpers)
- **Used by**: `cmd/ultrabridge` (startup), indirectly by `caldav.Backend`, `web.Handler` via `caldav.TaskStore` interface
- **Boundary**: Owns schema DDL and CRUD. Does not own iCal conversion (that's `caldav/vtodo.go`).

## Key Decisions
- Single-user: no user_id column (one SQLite DB per UltraBridge instance)
- Reuses `taskstore.Task` model: no new type, CalDAV/web code unchanged
- Default values match existing `taskstore.Store` behavior (GenerateTaskID, CompletedTime=now, etc.)

## Invariants
- Timestamps are always millisecond UTC unix (0 = unset)
- `completed_time` holds **creation** time (Supernote quirk preserved for compatibility)
- `is_deleted` is "Y" or "N", never NULL
- Soft deletes only: Delete sets is_deleted='Y', never removes rows
- `ical_blob` column exists but is unused until Phase 2
```

**Verification:**

No build step — documentation only.

**Commit:** `docs(taskdb): add CLAUDE.md for new task database package`

<!-- END_TASK_5 -->
