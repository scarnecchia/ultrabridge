# UltraBridge CalDAV — Phase 2: Database Layer

**Goal:** Connect to MariaDB, read/write tasks, auto-discover user ID.

**Architecture:** Thin database package for connection pooling and user discovery. Task store package with CRUD operations on `t_schedule_task`. Pure-function mapping layer for field conversions, MD5 ID generation, ETag/CTag computation. Unit tests cover mapping logic; CRUD is verified operationally.

**Tech Stack:** Go 1.22, `database/sql`, `github.com/go-sql-driver/mysql`, `crypto/md5`

**Scope:** 8 phases from original design (phase 2 of 8)

**Codebase verified:** 2026-03-17 (schema from `/mnt/supernote/supernotedb.sql`, quirks from `/mnt/supernote/FINDINGS.md`)

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC3: Bidirectional task sync
- **ultrabridge-caldav.AC3.1 Success:** Task created on Supernote device appears as VTODO in CalDAV client on next sync
- **ultrabridge-caldav.AC3.2 Success:** Task completed on Supernote device shows STATUS=COMPLETED in CalDAV with correct completion timestamp (from `last_modified`, not `completed_time`)
- **ultrabridge-caldav.AC3.3 Success:** Task created via CalDAV client (PUT with VTODO) appears in `t_schedule_task` with correct field mapping, MD5 task ID, and `user_id`
- **ultrabridge-caldav.AC3.4 Success:** Task completed via CalDAV client (STATUS=COMPLETED) writes `status=completed` and updates `last_modified`

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Database connection and user discovery

**Files:**
- Create: `internal/db/db.go`

**Implementation:**

Package `db` provides a thin wrapper around `database/sql` for MariaDB connection pooling and user ID auto-discovery.

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Connect opens a connection pool to MariaDB and verifies connectivity.
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return db, nil
}

// DiscoverUserID returns the single user's ID from u_user.
// The Supernote Private Cloud is single-user; this fails if no users exist.
func DiscoverUserID(ctx context.Context, db *sql.DB) (int64, error) {
	var userID int64
	err := db.QueryRowContext(ctx, "SELECT user_id FROM u_user LIMIT 1").Scan(&userID)
	if err != nil {
		return 0, fmt.Errorf("discover user_id: %w", err)
	}
	return userID, nil
}
```

Note: `user_id` in the DB is `bigint(20) unsigned` but the actual Snowflake IDs fit in `int64` (signed). Using `int64` is simpler for Go and compatible with the values observed (e.g., `1184673925533868032`).

**Verification:**

```bash
go build ./internal/db/
```

Expected: Compiles without errors.

**Commit:** `feat: add database connection and user discovery`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Wire DB into main.go startup

**Files:**
- Modify: `cmd/ultrabridge/main.go`

**Implementation:**

Update `main.go` to connect to MariaDB at startup using the config DSN, discover user ID, and log success. If DB is unreachable, exit with a clear error (supports AC1.2 intent).

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/sysop/ultrabridge/internal/config"
	"github.com/sysop/ultrabridge/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}

	database, err := db.Connect(cfg.DSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userID, err := db.DiscoverUserID(ctx, database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: user discovery failed: %v\n", err)
		os.Exit(1)
	}
	log.Printf("discovered user_id: %d", userID)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("ultrabridge starting on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}
}
```

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles. (Runtime verification requires MariaDB — tested in Phase 8 integration.)

**Commit:** `feat: wire database connection into startup`

<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-5) -->
<!-- START_TASK_3 -->
### Task 3: Task model and field mapping

**Files:**
- Create: `internal/taskstore/model.go`
- Create: `internal/taskstore/mapping.go`

**Implementation:**

`model.go` defines the Go struct matching `t_schedule_task` columns. `mapping.go` contains pure functions for:
- MD5 task ID generation: `MD5(title + creation_timestamp_ms)`
- ETag computation: `MD5(task_id + title + status + due_time_str + last_modified_str)`
- CTag computation: `MAX(last_modified)` across all tasks
- Status mapping: `"needsAction"` ↔ `"NEEDS-ACTION"`, `"completed"` ↔ `"COMPLETED"`
- Timestamp conversion: ms UTC ↔ `time.Time`
- Completion time extraction: use `last_modified` (NOT `completed_time`) when `status == "completed"`

**model.go:**

```go
package taskstore

import "database/sql"

// Task represents a row in t_schedule_task.
// Note: The DB table has 8 additional sort columns (sort, sort_completed,
// planer_sort, all_sort, all_sort_completed, sort_time, planer_sort_time,
// all_sort_time) that are NOT included here. Tasks created via CalDAV will
// have NULL for these columns. This is acceptable because:
// 1. The Supernote device populates sort columns when it syncs
// 2. All sort columns are NULLable with no NOT NULL constraints
// 3. Observed behavior: the device reassigns sort order on sync
// If device behavior differs, the Create method can set default sort values.
type Task struct {
	TaskID        string
	TaskListID    sql.NullString
	UserID        int64
	Title         sql.NullString
	Detail        sql.NullString
	LastModified  sql.NullInt64
	Recurrence    sql.NullString
	IsReminderOn  string
	Status        sql.NullString
	Importance    sql.NullString
	DueTime       int64
	CompletedTime sql.NullInt64
	Links         sql.NullString
	IsDeleted     string
}
```

**mapping.go:**

```go
package taskstore

import (
	"crypto/md5"
	"fmt"
	"strconv"
	"time"
)

// GenerateTaskID creates an MD5 hash from title + creation timestamp,
// matching the convention used by Supernote devices.
func GenerateTaskID(title string, createdAtMs int64) string {
	data := title + strconv.FormatInt(createdAtMs, 10)
	return fmt.Sprintf("%x", md5.Sum([]byte(data)))
}

// ComputeETag generates an ETag for a task based on its mutable fields.
func ComputeETag(t *Task) string {
	data := t.TaskID +
		nullStr(t.Title) +
		nullStr(t.Status) +
		strconv.FormatInt(t.DueTime, 10) +
		nullInt64Str(t.LastModified)
	return fmt.Sprintf("%x", md5.Sum([]byte(data)))
}

// ComputeCTag returns the max last_modified value as a string,
// suitable for use as a CalDAV collection CTag.
func ComputeCTag(tasks []Task) string {
	var max int64
	for _, t := range tasks {
		if t.LastModified.Valid && t.LastModified.Int64 > max {
			max = t.LastModified.Int64
		}
	}
	return strconv.FormatInt(max, 10)
}

// CompletionTime returns the actual completion timestamp for a completed task.
// Per Supernote quirk: completed_time holds creation time, last_modified holds
// the real completion time.
func CompletionTime(t *Task) (time.Time, bool) {
	if nullStr(t.Status) != "completed" {
		return time.Time{}, false
	}
	if !t.LastModified.Valid {
		return time.Time{}, false
	}
	return MsToTime(t.LastModified.Int64), true
}

// MsToTime converts a millisecond UTC timestamp to time.Time.
func MsToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// TimeToMs converts a time.Time to millisecond UTC timestamp.
// Returns 0 for zero time.
func TimeToMs(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// CalDAVStatus converts a Supernote status string to a CalDAV STATUS value.
func CalDAVStatus(supernoteStatus string) string {
	switch supernoteStatus {
	case "completed":
		return "COMPLETED"
	case "needsAction", "":
		return "NEEDS-ACTION"
	default:
		return "NEEDS-ACTION"
	}
}

// SupernoteStatus converts a CalDAV STATUS value to a Supernote status string.
func SupernoteStatus(caldavStatus string) string {
	switch caldavStatus {
	case "COMPLETED":
		return "completed"
	case "NEEDS-ACTION", "":
		return "needsAction"
	default:
		return "needsAction"
	}
}

// NullStr extracts a string from sql.NullString. Exported for use by caldav package.
func NullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// SqlStr creates a sql.NullString. Exported for use by caldav package.
func SqlStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt64Str(ni sql.NullInt64) string {
	if ni.Valid {
		return strconv.FormatInt(ni.Int64, 10)
	}
	return "0"
}
```

Note: Internal callers in this file should also use `NullStr` (the exported version). Update `ComputeETag` and `CompletionTime` to call `NullStr` instead of the old unexported `nullStr`.

**Verification:**

```bash
go build ./internal/taskstore/
```

Expected: Compiles without errors.

**Commit:** `feat: add task model and field mapping functions`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Mapping unit tests

**Verifies:** ultrabridge-caldav.AC3.2, ultrabridge-caldav.AC3.3

**Files:**
- Create: `internal/taskstore/mapping_test.go`

**Testing:**

Tests must verify each AC listed above:
- **ultrabridge-caldav.AC3.2:** CompletionTime returns `last_modified` (not `completed_time`) for completed tasks. Test with a task where `completed_time` and `last_modified` differ — assert the returned time matches `last_modified`.
- **ultrabridge-caldav.AC3.3:** GenerateTaskID produces correct MD5 from title + timestamp. Test with known input and verify hex output matches `md5(title + timestamp_string)`.

Additional tests:
- `CalDAVStatus` / `SupernoteStatus` round-trip: `"needsAction"` ↔ `"NEEDS-ACTION"`, `"completed"` ↔ `"COMPLETED"`
- `MsToTime` / `TimeToMs` round-trip with known values
- `MsToTime(0)` returns zero time
- `ComputeETag` changes when any field changes
- `ComputeCTag` returns max `last_modified` across multiple tasks
- `CompletionTime` returns false for non-completed tasks

Follow Go standard testing patterns (`testing` package, table-driven tests).

**Verification:**

```bash
go test ./internal/taskstore/ -v
```

Expected: All tests pass.

**Commit:** `test: add mapping unit tests for field conversion and ID generation`

<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Task store CRUD operations

**Verifies:** ultrabridge-caldav.AC3.1, ultrabridge-caldav.AC3.3, ultrabridge-caldav.AC3.4

**Files:**
- Create: `internal/taskstore/store.go`

**Implementation:**

`Store` holds a `*sql.DB` and `userID`. Provides CRUD methods that read/write `t_schedule_task`:

- `List(ctx) ([]Task, error)` — `SELECT * FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'`
- `Get(ctx, taskID) (*Task, error)` — `SELECT * FROM t_schedule_task WHERE task_id = ? AND user_id = ? AND is_deleted = 'N'`
- `Create(ctx, *Task) error` — `INSERT INTO t_schedule_task (...) VALUES (...)`; sets `user_id` from store's configured value, generates `task_id` via `GenerateTaskID` if empty, sets `completed_time` to current ms timestamp (matching Supernote convention), sets `last_modified` to current ms timestamp, defaults `is_deleted` to `'N'` and `is_reminder_on` to `'N'`
- `Update(ctx, *Task) error` — `UPDATE t_schedule_task SET ... WHERE task_id = ? AND user_id = ?`; updates `title`, `detail`, `status`, `importance`, `due_time`, `last_modified`, `recurrence`. When status changes to `completed`, sets `last_modified` to current ms timestamp.
- `Delete(ctx, taskID) error` — Soft delete: `UPDATE t_schedule_task SET is_deleted = 'Y', last_modified = ? WHERE task_id = ? AND user_id = ?`

```go
package taskstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Store struct {
	db     *sql.DB
	userID int64
}

func New(db *sql.DB, userID int64) *Store {
	return &Store{db: db, userID: userID}
}

func (s *Store) UserID() int64 { return s.userID }

const taskColumns = `task_id, task_list_id, user_id, title, detail, last_modified,
	recurrence, is_reminder_on, status, importance, due_time, completed_time,
	links, is_deleted`

func (s *Store) List(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'",
		s.userID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) Get(ctx context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM t_schedule_task WHERE task_id = ? AND user_id = ? AND is_deleted = 'N'",
		taskID, s.userID)
	var t Task
	if err := scanTaskRow(row, &t); err != nil {
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return &t, nil
}

func (s *Store) Create(ctx context.Context, t *Task) error {
	now := time.Now().UnixMilli()
	t.UserID = s.userID
	if t.TaskID == "" {
		t.TaskID = GenerateTaskID(nullStr(t.Title), now)
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

	_, err := s.db.ExecContext(ctx, `INSERT INTO t_schedule_task
		(task_id, task_list_id, user_id, title, detail, last_modified,
		 recurrence, is_reminder_on, status, importance, due_time, completed_time,
		 links, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, t.TaskListID, t.UserID, t.Title, t.Detail, t.LastModified,
		t.Recurrence, t.IsReminderOn, t.Status, t.Importance, t.DueTime, t.CompletedTime,
		t.Links, t.IsDeleted)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Store) Update(ctx context.Context, t *Task) error {
	now := time.Now().UnixMilli()
	t.LastModified = sql.NullInt64{Int64: now, Valid: true}

	_, err := s.db.ExecContext(ctx, `UPDATE t_schedule_task SET
		title = ?, detail = ?, status = ?, importance = ?, due_time = ?,
		last_modified = ?, recurrence = ?
		WHERE task_id = ? AND user_id = ?`,
		t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.LastModified, t.Recurrence,
		t.TaskID, s.userID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.TaskID, err)
	}
	return nil
}

// MaxLastModified returns the maximum last_modified value across all non-deleted tasks.
// Used for CTag computation without loading all tasks into memory.
func (s *Store) MaxLastModified(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT MAX(last_modified) FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'",
		s.userID).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("max last_modified: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

func (s *Store) Delete(ctx context.Context, taskID string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE t_schedule_task SET
		is_deleted = 'Y', last_modified = ?
		WHERE task_id = ? AND user_id = ?`,
		now, taskID, s.userID)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner, t *Task) error {
	return s.Scan(
		&t.TaskID, &t.TaskListID, &t.UserID, &t.Title, &t.Detail,
		&t.LastModified, &t.Recurrence, &t.IsReminderOn, &t.Status,
		&t.Importance, &t.DueTime, &t.CompletedTime, &t.Links, &t.IsDeleted,
	)
}

func scanTaskRow(row *sql.Row, t *Task) error {
	return scanTask(row, t)
}
```

**Verification:**

```bash
go build ./internal/taskstore/
```

Expected: Compiles. (CRUD verification against live DB happens in Phase 8 integration testing.)

**Commit:** `feat: add task store CRUD operations`

<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_B -->
