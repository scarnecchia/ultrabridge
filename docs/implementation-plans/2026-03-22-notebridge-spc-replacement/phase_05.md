# NoteBridge Phase 5: Tasks + Digests

**Goal:** Device task lists, individual tasks, and digest/summary data sync with NoteBridge.

**Architecture:** Schedule group and task CRUD endpoints matching opennotecloud's API surface. Summary/digest CRUD with file upload/download via signed URLs. nextSyncToken pagination for incremental task sync. All data stored in syncdb SQLite.

**Tech Stack:** Go 1.24, SQLite

**Scope:** Phase 5 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC5: Tasks
- **AC5.1 Success:** Task created on tablet syncs to NoteBridge, persists in schedule_tasks
- **AC5.2 Success:** Task list (group) CRUD works: create, update, delete, list
- **AC5.3 Success:** Batch task update atomically updates multiple tasks
- **AC5.4 Success:** nextSyncToken pagination returns only tasks modified since last sync
- **AC5.5 Success:** Recurrence field preserved through sync round-trip

### notebridge-spc-replacement.AC6: Digests
- **AC6.1 Success:** Summary created on tablet syncs to NoteBridge
- **AC6.2 Success:** Summary groups (collections) CRUD works
- **AC6.3 Success:** Summary file upload/download works via signed URLs

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: SyncDB Task + Digest Methods

<!-- START_TASK_1 -->
### Task 1: SyncDB store — task and digest query methods

**Files:**
- Modify: `/home/sysop/src/notebridge/internal/syncdb/store.go` — add task and digest methods

**Implementation:**

Add task and digest CRUD methods to the existing Store:

**Schedule Groups:**
- `UpsertScheduleGroup(ctx, g *ScheduleGroup) error` — INSERT OR REPLACE. If taskListId empty, generate via MD5(title+lastModified) with collision incrementing.
- `UpdateScheduleGroup(ctx, taskListID string, updates map[string]any) error` — partial update of specified fields
- `DeleteScheduleGroup(ctx, taskListID string, userID int64) error` — cascading: delete all tasks in group, then delete group
- `ListScheduleGroups(ctx, userID int64, page, pageSize int) ([]ScheduleGroup, int, error)` — paginated list, returns total count

**Schedule Tasks:**
- `UpsertScheduleTask(ctx, t *ScheduleTask) error` — INSERT OR REPLACE. If taskId empty, generate random nonce. Validates taskListId exists if provided.
- `BatchUpdateTasks(ctx, userID int64, tasks []TaskUpdate) error` — validates all taskIds exist first, then applies partial updates in single transaction
- `DeleteScheduleTask(ctx, taskID string, userID int64) error` — DELETE from schedule_tasks
- `ListScheduleTasks(ctx, userID int64, page, pageSize int, syncToken *int64) ([]ScheduleTask, *int64, error)` — paginated list sorted by last_modified DESC. If syncToken provided, filters to tasks where updated_at >= syncToken. Returns nextSyncToken (current time as millis) only on final page.

**Summaries:**
- `CreateSummary(ctx, s *Summary) error` — INSERT with Snowflake ID. Check uniqueness on (user_id, unique_identifier) → return ErrUniqueIDExists if duplicate.
- `UpdateSummary(ctx, id int64, userID int64, updates map[string]any) error` — partial update
- `DeleteSummary(ctx, id int64, userID int64) error` — DELETE
- `ListSummaryGroups(ctx, userID int64, page, pageSize int) ([]Summary, int, error)` — WHERE is_summary_group = 'Y', paginated
- `ListSummaries(ctx, userID int64, page, pageSize int, parentUID *string) ([]Summary, int, error)` — WHERE is_summary_group = 'N', optional filter by parent_unique_identifier
- `ListSummaryHashes(ctx, userID int64, page, pageSize int, parentUID *string) ([]SummaryHash, int, error)` — lightweight: returns only id, md5_hash, handwrite_md5, last_modified_time, metadata
- `GetSummariesByIDs(ctx, userID int64, ids []int64, page, pageSize int) ([]Summary, int, error)` — filter by specific IDs
- `GetSummary(ctx, id int64, userID int64) (*Summary, error)` — single lookup

**Types:**
- `ScheduleGroup` struct: TaskListID, UserID int64, Title string, LastModified, CreateTime int64
- `ScheduleTask` struct: TaskID string, UserID int64, TaskListID, Title, Detail, Status, Importance, Recurrence, Links string, IsReminderOn string, DueTime, CompletedTime, LastModified int64, Sort, SortCompleted, PlanerSort, SortTime, PlanerSortTime, AllSort, AllSortCompleted, AllSortTime int64, RecurrenceID string
- `TaskUpdate` struct: TaskID string, Fields map[string]any
- `Summary` struct: all fields from summaries table
- `SummaryHash` struct: ID int64, MD5Hash, HandwriteMD5, CommentHandwriteName string, LastModifiedTime int64, Metadata string

**Error types:**
- `ErrTaskGroupNotFound` (for E0328)
- `ErrTaskNotFound` (for E0329)
- `ErrSummaryGroupNotFound` (for E0339)
- `ErrSummaryNotFound` (for E0340)
- `ErrUniqueIDExists` (for E0338)

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add syncdb task and digest query methods`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: SyncDB task and digest tests

**Verifies:** AC5.3 (batch update atomicity), AC5.4 (nextSyncToken filtering)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/syncdb/store_task_test.go`
- Create: `/home/sysop/src/notebridge/internal/syncdb/store_digest_test.go`

**Testing:**

All tests use in-memory SQLite with `setupTestStore(t)` helper.

**store_task_test.go:**

- UpsertScheduleGroup creates group, second call updates
- UpsertScheduleGroup with empty taskListId auto-generates ID
- UpdateScheduleGroup with non-existent ID returns ErrTaskGroupNotFound
- DeleteScheduleGroup cascades: deletes group and all its tasks
- ListScheduleGroups pagination: create 5 groups, page 1 size 2 returns 2, page 3 returns 1
- UpsertScheduleTask creates task with all fields
- UpsertScheduleTask with empty taskId generates random ID
- UpsertScheduleTask with non-existent taskListId returns ErrTaskGroupNotFound
- AC5.3: BatchUpdateTasks updates multiple tasks atomically — create 3 tasks, batch update 2, verify both updated. If one taskId doesn't exist, entire batch fails (transaction rollback).
- DeleteScheduleTask removes task
- AC5.4: ListScheduleTasks with syncToken — create 3 tasks at different times, query with syncToken → returns only tasks modified after token. Final page returns nextSyncToken.
- ListScheduleTasks without syncToken returns all tasks paginated

**store_digest_test.go:**

- CreateSummary with Snowflake ID
- CreateSummary duplicate unique_identifier returns ErrUniqueIDExists
- UpdateSummary partial update
- UpdateSummary non-existent returns ErrSummaryNotFound
- DeleteSummary removes
- ListSummaryGroups returns only is_summary_group='Y'
- ListSummaries returns only is_summary_group='N'
- ListSummaries with parentUID filter
- ListSummaryHashes returns lightweight hash data
- GetSummariesByIDs returns matching summaries
- Pagination works correctly for all list operations

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/syncdb/
```

**Commit:** `test: add syncdb task and digest tests`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Add task/digest error codes

**Files:**
- Modify: `/home/sysop/src/notebridge/internal/sync/errors.go` — add new error codes

**Implementation:**

Add error codes for task and digest operations:
- `E0328` — task group not found
- `E0329` — task not found
- `E0338` — summary unique ID already exists
- `E0339` — summary group not found
- `E0340` — summary not found

Add constructor functions: `ErrTaskGroupNotFound()`, `ErrTaskNotFound()`, `ErrUniqueIDExists()`, `ErrSummaryGroupNotFound()`, `ErrSummaryNotFound()`.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add task and digest error codes`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-6) -->
## Subcomponent B: Task + Digest HTTP Handlers

<!-- START_TASK_4 -->
### Task 4: Task endpoint handlers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_tasks.go`
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — register task routes

**Implementation:**

All handlers require AuthMiddleware. Get userID from context.

`handleCreateScheduleGroup(w, r)`:
- Parse body: taskListId, title, lastModified, createTime
- Call store.UpsertScheduleGroup
- Return jsonSuccess with taskListId

`handleUpdateScheduleGroup(w, r)`:
- Parse body: taskListId, title, lastModified
- Call store.UpdateScheduleGroup with partial update map
- On ErrTaskGroupNotFound: return jsonError E0328

`handleDeleteScheduleGroup(w, r)`:
- Extract taskListId from URL path (`/api/file/schedule/group/{taskListId}`)
- Call store.DeleteScheduleGroup (cascading delete of tasks)

`handleListScheduleGroups(w, r)`:
- Parse body: maxResults (default 20), pageToken (default 1)
- Call store.ListScheduleGroups
- Return jsonSuccess with scheduleTaskGroup array, pageToken for next page if more results

`handleCreateScheduleTask(w, r)`:
- Parse body: all task fields (taskId, taskListId, title, detail, status, importance, dueTime, completedTime, recurrence, isReminderOn, links, sort columns, recurrenceId)
- Call store.UpsertScheduleTask
- On ErrTaskGroupNotFound: return jsonError E0328
- Return jsonSuccess with taskId

`handleBatchUpdateTasks(w, r)`:
- Parse body: updateScheduleTaskList array
- Call store.BatchUpdateTasks
- On ErrTaskNotFound: return jsonError E0329
- Return jsonSuccess

`handleDeleteScheduleTask(w, r)`:
- Extract taskId from URL path (`/api/file/schedule/task/{taskId}`)
- Call store.DeleteScheduleTask

`handleListScheduleTasks(w, r)`:
- Parse body: maxResults, nextPageTokens, nextSyncToken
- Call store.ListScheduleTasks with optional syncToken
- Return jsonSuccess with scheduleTask array, nextPageToken, nextSyncToken

**Register routes in server.go:**
- `POST /api/file/schedule/group` — create group
- `PUT /api/file/schedule/group` — update group
- `DELETE /api/file/schedule/group/{taskListId}` — delete group (use path variable)
- `POST /api/file/schedule/group/all` — list groups
- `POST /api/file/schedule/task` — create task
- `PUT /api/file/schedule/task/list` — batch update tasks
- `DELETE /api/file/schedule/task/{taskId}` — delete task (use path variable)
- `POST /api/file/schedule/task/all` — list tasks

For path variable extraction, use Go 1.22+ `http.Request.PathValue("taskListId")`.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add task CRUD endpoint handlers`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Digest endpoint handlers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_digests.go`
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — register digest routes

**Implementation:**

All handlers require AuthMiddleware.

`handleCreateSummaryGroup(w, r)`:
- Parse body: uniqueIdentifier, name, description, md5Hash, creationTime, lastModifiedTime
- Call store.CreateSummary with is_summary_group='Y'
- On ErrUniqueIDExists: return jsonError E0338
- Return jsonSuccess with id (Snowflake, as string)

`handleUpdateSummaryGroup(w, r)`:
- Parse body: id, partial update fields
- Call store.UpdateSummary
- On ErrSummaryGroupNotFound: return jsonError E0339

`handleDeleteSummaryGroup(w, r)`:
- Parse body: id
- Call store.DeleteSummary

`handleListSummaryGroups(w, r)`:
- Parse body: page, size
- Call store.ListSummaryGroups
- Return jsonSuccess with totalRecords, totalPages, currentPage, pageSize, summaryDOList

`handleCreateSummary(w, r)`:
- Parse body: all summary fields
- Call store.CreateSummary with is_summary_group='N'
- On ErrUniqueIDExists: return jsonError E0338
- Return jsonSuccess with id

`handleUpdateSummary(w, r)`:
- Parse body: id, partial update fields
- Call store.UpdateSummary
- On ErrSummaryNotFound: return jsonError E0340

`handleDeleteSummary(w, r)`:
- Parse body: id
- Call store.DeleteSummary

`handleQuerySummaryHash(w, r)`:
- Parse body: page, size, parentUniqueIdentifier (optional filter)
- Call store.ListSummaryHashes
- Return jsonSuccess with totalRecords, totalPages, summaryInfoVOList (lightweight hash data)

`handleQuerySummaryByIDs(w, r)`:
- Parse body: ids array, page, size
- Call store.GetSummariesByIDs

`handleQuerySummaries(w, r)`:
- Parse body: page, size, parentUniqueIdentifier (optional)
- Call store.ListSummaries

`handleUploadSummaryApply(w, r)`:
- Parse body: fileName
- Generate signed upload URLs (same pattern as file upload/apply)
- Return jsonSuccess with fullUploadUrl, partUploadUrl, innerName

`handleDownloadSummary(w, r)`:
- Parse body: id
- Get summary from store, extract handwrite_inner_name
- If empty: return jsonError E0340
- Generate signed download URL
- Return jsonSuccess with url

**Register routes in server.go:**
- `POST /api/file/add/summary/group` — create summary group
- `PUT /api/file/update/summary/group` — update summary group
- `DELETE /api/file/delete/summary/group` — delete summary group
- `POST /api/file/query/summary/group` — list summary groups
- `POST /api/file/add/summary` — create summary
- `PUT /api/file/update/summary` — update summary
- `DELETE /api/file/delete/summary` — delete summary
- `POST /api/file/query/summary/hash` — query summary hashes
- `POST /api/file/query/summary/id` — query by IDs
- `POST /api/file/query/summary` — query summaries
- `POST /api/file/upload/apply/summary` — upload apply
- `POST /api/file/download/summary` — download

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add digest CRUD endpoint handlers`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Task and digest integration tests

**Verifies:** AC5.1 (task sync), AC5.2 (group CRUD), AC5.3 (batch update), AC5.4 (nextSyncToken), AC5.5 (recurrence round-trip), AC6.1 (summary sync), AC6.2 (summary group CRUD), AC6.3 (summary file upload/download)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_tasks_test.go`
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_digests_test.go`

**Testing:**

Integration tests using `httptest.NewServer` with full server handler.

**handlers_tasks_test.go:**

- AC5.1 task sync:
  1. Login, create a schedule group
  2. Create a task in that group with title, detail, status, dueTime
  3. List tasks → task appears with all fields correct
  4. Verify task persisted (create, list round-trip)

- AC5.2 group CRUD:
  1. Create group → success, returns taskListId
  2. List groups → group appears
  3. Update group title → success
  4. List groups → updated title
  5. Delete group → success
  6. List groups → empty (group and its tasks removed)

- AC5.3 batch update:
  1. Create 3 tasks
  2. Batch update 2 of them (change status, importance)
  3. List tasks → verify 2 updated, 1 unchanged
  4. Batch update with non-existent taskId → error E0329, no changes applied

- AC5.4 nextSyncToken:
  1. Create task A at time T1
  2. List all tasks → get nextSyncToken T2
  3. Create task B at time T3
  4. List tasks with nextSyncToken=T2 → returns only task B
  5. Response includes nextSyncToken=T4 (current time)
  6. List tasks with nextSyncToken=T4 → empty (no changes since)

- AC5.5 recurrence:
  1. Create task with recurrence="RRULE:FREQ=DAILY;COUNT=5"
  2. List tasks → verify recurrence field preserved exactly
  3. Update task via batch with new recurrence
  4. List tasks → verify updated recurrence

**handlers_digests_test.go:**

- AC6.1 summary sync:
  1. Create summary item with uniqueIdentifier, content, tags
  2. Query summaries → item appears with all fields
  3. Update summary → fields updated
  4. Delete summary → no longer listed

- AC6.2 summary group CRUD:
  1. Create summary group → success, returns Snowflake ID
  2. List summary groups → group appears
  3. Update group name → success
  4. Delete group → success, no longer listed
  5. Create duplicate uniqueIdentifier → error E0338

- AC6.3 summary file upload/download:
  1. Upload apply → get signed URLs
  2. Upload file content to signed URL
  3. Create summary with handwriteInnerName referencing the uploaded file
  4. Download summary → get signed download URL
  5. Download file → content matches uploaded

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestTask
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestDigest
```

Expected: All tests pass.

**Commit:** `test: add task and digest integration tests`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_B -->
