# CalDAV-Native Task Store — Phase 2: iCal Blob Round-Trip

**Goal:** Full RFC 5545 VTODO fidelity via iCal blob storage and overlay reads, enabling CalDAV clients to round-trip arbitrary VTODO properties (RRULE, VALARM, CATEGORIES, etc.) that have no Supernote equivalent.

**Architecture:** When a CalDAV client PUTs a VTODO, the full serialized VCALENDAR text is stored as `ical_blob` in the Task model alongside the structured fields. On read, if `ical_blob` exists, the blob is deserialized and DB-authoritative fields (title, status, due, last_modified, completed) are overlaid on top, producing a merged calendar that preserves all Tier 3 properties. Tasks without blobs (imported from Supernote) continue to render from structured fields as before.

**Tech Stack:** Go, `github.com/emersion/go-ical` (encoder/decoder for serialization)

**Scope:** 2 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC1: UltraBridge owns task storage
- **caldav-native-taskstore.AC1.4 Success:** CalDAV client sets Tier 3 properties (RRULE, VALARM, CATEGORIES), they round-trip perfectly on next GET
- **caldav-native-taskstore.AC1.5 Success:** Task created on Supernote (no ical_blob) renders as valid VTODO with correct Tier 1/2 fields

---

<!-- START_TASK_1 -->
### Task 1: Add `ICalBlob` field to `taskstore.Task` model

**Files:**
- Modify: `internal/taskstore/model.go:14-29` (Task struct)

**Implementation:**

Add `ICalBlob sql.NullString` to the Task struct. Place it after the `IsDeleted` field to match the SQLite column order from Phase 1's schema:

```go
// Add after IsDeleted string:
ICalBlob  sql.NullString
```

The field is `sql.NullString` because tasks imported from Supernote will have NULL blobs (no iCal representation), while tasks created by CalDAV clients will have the full VCALENDAR stored.

**Important:** This does NOT affect the existing MariaDB-backed `taskstore.Store` — that store's SQL queries select specific columns by name and do not include `ical_blob`. The field is only populated by the new `taskdb.Store`.

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./...
```

Expected: Builds without errors. Existing tests pass (field is added but not yet used by any existing code paths).

**Commit:** `feat(taskstore): add ICalBlob field to Task model for VTODO blob storage`

<!-- END_TASK_1 -->

<!-- START_SUBCOMPONENT_A (tasks 2-4) -->
<!-- START_TASK_2 -->
### Task 2: Update `taskdb.Store` to read/write `ical_blob`

**Files:**
- Modify: `internal/taskdb/store.go` (taskColumns, scanTask, Create, Update)

**Implementation:**

Update `taskColumns` to include `ical_blob`:

```go
const taskColumns = `task_id, title, detail, status, importance, due_time,
	completed_time, last_modified, recurrence, is_reminder_on, links, is_deleted,
	ical_blob`
```

Update `scanTask` to scan `ical_blob`:

```go
func scanTask(s scanner) (taskstore.Task, error) {
	var t taskstore.Task
	err := s.Scan(
		&t.TaskID, &t.Title, &t.Detail, &t.Status, &t.Importance,
		&t.DueTime, &t.CompletedTime, &t.LastModified, &t.Recurrence,
		&t.IsReminderOn, &t.Links, &t.IsDeleted, &t.ICalBlob,
	)
	return t, err
}
```

Update `Create` to insert `ical_blob` (add to both column list and values):

```go
_, err := s.db.ExecContext(ctx, `INSERT INTO tasks
	(task_id, title, detail, status, importance, due_time,
	 completed_time, last_modified, recurrence, is_reminder_on,
	 links, is_deleted, ical_blob, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	t.TaskID, t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
	t.CompletedTime, t.LastModified, t.Recurrence, t.IsReminderOn,
	t.Links, t.IsDeleted, t.ICalBlob, now, now)
```

Update `Update` to write `ical_blob`:

```go
_, err := s.db.ExecContext(ctx, `UPDATE tasks SET
	title = ?, detail = ?, status = ?, importance = ?, due_time = ?,
	completed_time = ?, last_modified = ?, recurrence = ?,
	is_reminder_on = ?, links = ?, ical_blob = ?, updated_at = ?
	WHERE task_id = ?`,
	t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
	t.CompletedTime, t.LastModified, t.Recurrence,
	t.IsReminderOn, t.Links, t.ICalBlob, now,
	t.TaskID)
```

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/taskdb/ -v
```

Expected: Existing Phase 1 tests still pass (they don't set ICalBlob, so it persists as NULL).

**Commit:** `feat(taskdb): read/write ical_blob column in task CRUD`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Add blob serialization to `VTODOToTask` and overlay deserialization to `TaskToVTODO`

**Files:**
- Modify: `internal/caldav/vtodo.go:1-9` (imports), `internal/caldav/vtodo.go:73-115` (VTODOToTask), `internal/caldav/vtodo.go:12-68` (TaskToVTODO)

**Implementation:**

**Write path — VTODOToTask (lines 73-115):** After extracting structured fields, serialize the full `*ical.Calendar` to text and store in `t.ICalBlob`. Add `"bytes"` and `"database/sql"` to imports.

After line 112 (the PRIORITY extraction), before the return statement, add blob serialization:

```go
	// Store full VCALENDAR as blob for round-trip fidelity
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(cal); err == nil {
		t.ICalBlob = sql.NullString{String: buf.String(), Valid: true}
	}
```

**Read path — TaskToVTODO (lines 12-68):** If `t.ICalBlob` is non-null, deserialize the blob and overlay DB-authoritative fields on top instead of building from scratch. Add `"strings"` to imports.

Replace the function body with a check-then-branch:

```go
func TaskToVTODO(t *taskstore.Task, dueTimeMode string) *ical.Calendar {
	if t.ICalBlob.Valid && t.ICalBlob.String != "" {
		return taskToVTODOFromBlob(t, dueTimeMode)
	}
	return taskToVTODOFromFields(t, dueTimeMode)
}
```

Extract the current `TaskToVTODO` body into a new `taskToVTODOFromFields` function (identical to the current implementation).

Add a new `taskToVTODOFromBlob` function:

```go
// taskToVTODOFromBlob deserializes the stored iCal blob and overlays
// DB-authoritative fields on top, preserving all Tier 3 properties.
func taskToVTODOFromBlob(t *taskstore.Task, dueTimeMode string) *ical.Calendar {
	dec := ical.NewDecoder(strings.NewReader(t.ICalBlob.String))
	cal, err := dec.Decode()
	if err != nil {
		// Fallback: if blob is corrupt, build from fields
		return taskToVTODOFromFields(t, dueTimeMode)
	}

	todo, err := FindVTODO(cal)
	if err != nil {
		return taskToVTODOFromFields(t, dueTimeMode)
	}

	// Overlay DB-authoritative fields (these may have been updated
	// via sync or direct DB operations since the blob was stored)
	todo.Props.SetText("UID", t.TaskID)

	if t.Title.Valid && t.Title.String != "" {
		todo.Props.SetText("SUMMARY", t.Title.String)
	}

	status := taskstore.CalDAVStatus(taskstore.NullStr(t.Status))
	todo.Props.SetText("STATUS", status)

	if t.DueTime != 0 {
		dueTime := taskstore.MsToTime(t.DueTime)
		if dueTimeMode == "date_only" {
			todo.Props.SetDate("DUE", dueTime)
		} else {
			todo.Props.SetDateTime("DUE", dueTime)
		}
	} else {
		// Remove DUE if cleared
		delete(todo.Props, "DUE")
	}

	if t.LastModified.Valid {
		lm := taskstore.MsToTime(t.LastModified.Int64)
		todo.Props.SetDateTime("DTSTAMP", lm)
		todo.Props.SetDateTime("LAST-MODIFIED", lm)
	}

	if ct, ok := taskstore.CompletionTime(t); ok {
		todo.Props.SetDateTime("COMPLETED", ct)
	} else {
		delete(todo.Props, "COMPLETED")
	}

	return cal
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/caldav/
```

Expected: Builds without errors.

**Commit:** `feat(caldav): blob serialization in VTODOToTask and overlay deserialization in TaskToVTODO`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Tests for iCal blob round-trip

**Verifies:** caldav-native-taskstore.AC1.4, caldav-native-taskstore.AC1.5

**Files:**
- Modify: `internal/caldav/vtodo_test.go` (add blob round-trip tests)

**Testing:**

Tests must verify each AC listed above. Follow the existing table-driven test pattern in `/home/jtd/ultrabridge/internal/caldav/vtodo_test.go` (uses `createTestCalendar` helper and direct property assertions).

- **caldav-native-taskstore.AC1.4:** Create an `*ical.Calendar` with Tier 3 properties (RRULE, VALARM child component, CATEGORIES). Call `VTODOToTask` to get a Task with `ICalBlob` populated. Then call `TaskToVTODO` with that Task. Verify the returned calendar contains all original Tier 3 properties with correct values. The overlay should also produce correct Tier 1/2 fields.

  Specific sub-cases:
  - RRULE (e.g., `FREQ=WEEKLY;BYDAY=MO`) survives round-trip
  - CATEGORIES (e.g., `Work,UltraBridge`) survives round-trip
  - VALARM component (with TRIGGER, ACTION, DESCRIPTION children) survives round-trip
  - X-properties (e.g., `X-CUSTOM-PROP:value`) survive round-trip

- **caldav-native-taskstore.AC1.5:** Create a `taskstore.Task` with structured fields set (title, status, due_time) but `ICalBlob` as `sql.NullString{}` (invalid/NULL). Call `TaskToVTODO`. Verify the returned calendar is a valid VTODO with correct Tier 1/2 fields — this is the backward-compatible path for Supernote-originated tasks.

Additional test case:
- **Overlay correctness:** Create a Task with ICalBlob containing SUMMARY="Old Title" but `t.Title = SqlStr("New Title")`. Call `TaskToVTODO`. Verify SUMMARY="New Title" (DB fields win over blob).

- **Corrupt blob fallback:** Create a Task with `ICalBlob = SqlStr("not valid ical")`. Call `TaskToVTODO`. Verify it falls back to building from fields (no panic/error).

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/caldav/ -v -run TestBlob
```

Expected: All blob round-trip tests pass.

**Commit:** `test(caldav): add iCal blob round-trip and overlay tests`

<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_A -->
