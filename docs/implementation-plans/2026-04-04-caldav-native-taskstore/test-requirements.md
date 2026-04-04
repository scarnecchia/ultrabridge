# CalDAV-Native Task Store — Test Requirements

Maps each acceptance criterion from the [design plan](../../design-plans/2026-04-04-caldav-native-taskstore.md) to either automated tests or documented human verification.

Last updated: 2026-04-04

---

## AC1: UltraBridge owns task storage

### AC1.1 — CalDAV client creates task via PUT, task persists in SQLite and is retrievable via GET

- **Test type:** Unit
- **Test file:** `internal/taskdb/store_test.go`
- **Verified in:** Phase 1, Task 2
- **Description:** Create a task via `store.Create()`, retrieve it via `store.Get()`. Verify all fields match and the task persists in the in-memory SQLite database. Uses real SQLite (`:memory:`), no mocks.

### AC1.2 — CalDAV client updates task (title, status, due date), changes persist and ETag updates

- **Test type:** Unit
- **Test file:** `internal/taskdb/store_test.go`
- **Verified in:** Phase 1, Task 2
- **Description:** Create a task, update title/status/due_time via `store.Update()`. Verify fields changed and `last_modified` is bumped (ETag is computed externally from `last_modified`, so a timestamp bump proves ETag would differ).

### AC1.3 — CalDAV client deletes task, task is soft-deleted (not returned in LIST, still in DB)

- **Test type:** Unit
- **Test file:** `internal/taskdb/store_test.go`
- **Verified in:** Phase 1, Task 2
- **Description:** Create a task, delete via `store.Delete()`. Verify `store.Get()` returns `taskstore.ErrNotFound` and `store.List()` excludes it. The row remains in the database with `is_deleted='Y'`.

### AC1.4 — CalDAV client sets Tier 3 properties (RRULE, VALARM, CATEGORIES), they round-trip perfectly on next GET

- **Test type:** Unit
- **Test file:** `internal/caldav/vtodo_test.go`
- **Verified in:** Phase 2, Task 4
- **Description:** Create an `*ical.Calendar` with Tier 3 properties (RRULE with `FREQ=WEEKLY;BYDAY=MO`, VALARM with TRIGGER/ACTION/DESCRIPTION, CATEGORIES with `Work,UltraBridge`, and X-properties). Call `VTODOToTask` to get a Task with `ICalBlob` populated, then call `TaskToVTODO`. Verify all Tier 3 properties survive the round-trip with correct values. Also verifies that the overlay produces correct Tier 1/2 fields alongside the preserved blob.

### AC1.5 — Task created on Supernote (no ical_blob) renders as valid VTODO with correct Tier 1/2 fields

- **Test type:** Unit
- **Test file:** `internal/caldav/vtodo_test.go`
- **Verified in:** Phase 2, Task 4
- **Description:** Create a `taskstore.Task` with structured fields (title, status, due_time) but `ICalBlob` as `sql.NullString{}` (NULL). Call `TaskToVTODO`. Verify the returned calendar is a valid VTODO built from structured fields only -- the backward-compatible path for Supernote-originated tasks.

### AC1.6 — CTag changes when any task is created, modified, or deleted

- **Test type:** Unit
- **Test file:** `internal/taskdb/store_test.go`
- **Verified in:** Phase 1, Task 2
- **Description:** Create a task, note `MaxLastModified()` value. Update the task, verify `MaxLastModified()` increased. Delete the task, verify `MaxLastModified()` reflects that deleted tasks are excluded from the MAX query (CTag is derived from `MaxLastModified()`; the implementation filters `WHERE is_deleted = 'N'`, so after deletion the CTag value drops or returns 0 if no other tasks exist, which is correct because the collection changed).

### AC1.7 — MaxLastModified returns 0 for empty store

- **Test type:** Unit
- **Test file:** `internal/taskdb/store_test.go`
- **Verified in:** Phase 1, Task 2
- **Description:** On a freshly opened empty store, call `MaxLastModified()`. Verify it returns `(0, nil)`.

---

## AC2: Supernote sync adapter

### AC2.1 — Task created in UltraBridge appears on Supernote device after sync cycle + STARTSYNC push

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Create an adapter backed by `httptest.NewServer` mocking SPC endpoints. Call `Push` with a `ChangeCreate`. Verify the mock SPC server received the create request with correct field mapping. Verify the mock `SyncNotifier` received a `Notify` call (STARTSYNC). The adapter returns a `PushResult` with the server-assigned `RemoteID`.

### AC2.2 — Task completed in UltraBridge sets status=completed and updates lastModified on SPC side

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Push a `ChangeUpdate` with `status="completed"` to the mock SPC. Verify the mock server received the update with correct status field in the SPC wire format.

### AC2.3 — Task created on Supernote device appears in UltraBridge after sync cycle

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Configure the mock SPC to return a task from `Pull`. Verify the returned `RemoteTask` has correct fields mapped from the `SPCTask` wire format. The sync engine (tested separately in Phase 3) handles importing the `RemoteTask` into the local store.

### AC2.4 — Task edited on Supernote device (title change) reflected in UltraBridge after sync

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Configure mock SPC to return a task with a different title from a previous `Pull`. Verify the `RemoteTask` reflects the new title and has a different ETag (enabling the engine to detect the change).

### AC2.5 — Conflict (both sides edited) resolves with UltraBridge version winning and pushing back to SPC

- **Test type:** Unit (engine-level with mock adapter)
- **Test file:** `internal/tasksync/engine_test.go`
- **Verified in:** Phase 3, Task 5
- **Description:** Create a local task and sync it (mock Pull returns it, creating a sync map entry). Modify both the local task (bumping `last_modified`) and the remote version (changing ETag in mock Pull). Run a sync cycle. Verify the local version is preserved and the mock adapter receives a `ChangeUpdate` pushing the local version back. The adapter test in Phase 4 separately verifies that `Push` correctly sends UB's version to SPC.

### AC2.6 — Adapter authenticates via SPC challenge-response, re-authenticates on 401

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Start the adapter (triggers login). Verify the mock SPC received the challenge-response flow (random code request, then login with SHA-256 hashed password). Then configure the mock to return 401 on the next request. Verify the adapter re-authenticates and retries the request successfully.

### AC2.7 — SPC unreachable: sync cycle logs warning, retries next interval, task store continues working

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Shut down the mock SPC server. Call `Pull` and `Push`. Verify the adapter returns errors (which the engine logs as warnings). Verify the task store is unaffected (this is structural -- the adapter has no reference to the task store). The engine's retry-on-next-interval behavior is verified by the engine test's status reporting test (after a failed cycle, `LastError` is set and `NextSyncAt` is scheduled).

### AC2.8 — SPC auth failure (wrong password): logged as error, sync disabled until next restart or config change

- **Test type:** Unit (adapter-level with mock SPC server)
- **Test file:** `internal/tasksync/supernote/adapter_test.go`
- **Verified in:** Phase 4, Task 5
- **Description:** Configure the mock SPC to always reject login (return 401 for the login endpoint). Call `adapter.Start()`. Verify it returns an error. The sync engine's `Start` method propagates this error and does not begin the sync loop -- verified at the engine level by confirming that `Start` failure prevents `run()` from executing.

### AC2.9 — Task deleted on SPC side (hard delete) detected and soft-deleted locally

- **Test type:** Unit (engine-level with mock adapter)
- **Test file:** `internal/tasksync/engine_test.go`
- **Verified in:** Phase 3, Task 5
- **Description:** Create a local task and sync it (mock Pull returns it, creating a sync map entry). On the next cycle, mock Pull returns an empty list (simulating SPC hard delete). Verify the local task is soft-deleted via `store.Delete()` and the sync map entry is removed.

---

## AC3: Web UI sync control

### AC3.1 — Tasks tab shows sync status: last sync time, next scheduled sync, adapter state

- **Test type:** Unit (HTTP handler)
- **Test file:** `internal/web/handler_test.go`
- **Verified in:** Phase 6, Task 4
- **Description:** Create a `mockSyncProvider` returning a status with `LastSyncAt`, `NextSyncAt`, and `AdapterActive=true`. Issue `GET /sync/status`. Verify the JSON response contains all expected fields with correct values. Also tests the nil-safe case: handler with `syncProvider: nil` returns a zero-value `SyncStatus` without panicking.

- **Test type:** Human verification
- **Justification:** The visual layout of the sync status panel in the Tasks tab (badge styling, positioning, readability) requires manual inspection in a browser. The HTML/CSS/JS template rendering and 5-second polling behavior cannot be meaningfully verified by unit tests.
- **Verification steps:**
  1. Start UltraBridge with sync enabled and a reachable SPC.
  2. Open the web UI Tasks tab.
  3. Confirm the sync status panel shows: current adapter state ("Active"), last sync time, and next scheduled sync time.
  4. Confirm the panel updates every 5 seconds without page refresh.

### AC3.2 — "Sync Now" button triggers immediate sync cycle, status updates on completion

- **Test type:** Unit (HTTP handler)
- **Test file:** `internal/web/handler_test.go`
- **Verified in:** Phase 6, Task 4
- **Description:** Issue `POST /sync/trigger`. Verify `mockSyncProvider.triggered` is incremented. Verify the response contains the updated status JSON.

- **Test type:** Human verification
- **Justification:** The end-to-end behavior (button click triggers sync, status visually updates on completion, tasks appear/change) requires a running UltraBridge instance with SPC. The JavaScript fetch/timeout chain and DOM updates are not covered by Go unit tests.
- **Verification steps:**
  1. Start UltraBridge with sync enabled.
  2. Create a task on the Supernote device.
  3. Click "Sync Now" in the web UI.
  4. Confirm the status briefly shows "Syncing..." and then returns to "Active" with updated last sync time.
  5. Confirm the new task appears in the task list.

### AC3.3 — Sync in progress: button disabled or shows in-progress indicator, no double-trigger

- **Test type:** Unit (HTTP handler)
- **Test file:** `internal/web/handler_test.go`
- **Verified in:** Phase 6, Task 4
- **Description:** Set `mockSyncProvider.status.InProgress=true`. Issue `GET /sync/status`. Verify the JSON response shows `InProgress=true`. The UI disables the button client-side based on this field.

- **Test type:** Human verification
- **Justification:** The button disabling behavior is implemented in client-side JavaScript. The server provides the `InProgress` flag but does not enforce button state.
- **Verification steps:**
  1. Trigger a sync cycle that takes a few seconds (or add artificial delay).
  2. Observe that the "Sync Now" button is disabled and shows "Syncing..." while the cycle runs.
  3. Attempt to click the button while disabled -- confirm no additional sync cycle is triggered.
  4. Confirm the button re-enables after the cycle completes.

---

## AC4: Clean migration

### AC4.1 — First run with empty task DB and reachable SPC imports all non-deleted tasks, creates sync map entries

- **Test type:** Unit (migration function)
- **Test file:** `internal/tasksync/supernote/migration_test.go`
- **Verified in:** Phase 5, Task 4
- **Description:** Mock SPC returns 5 tasks (3 active, 2 with `isDeleted='Y'`). Call `MigrateFromSPC`. Verify 3 tasks are imported into the store, 3 sync map entries are created, and deleted tasks are skipped. Verify task fields map correctly (title, status, due_time, completedTime quirk preserved). Verify sync map entries have both `LastPushed` and `LastPulled` set (so the engine does not re-push imported tasks).

### AC4.2 — Subsequent starts with populated task DB skip import

- **Test type:** Unit (migration detection)
- **Test file:** `internal/tasksync/supernote/migration_test.go`
- **Verified in:** Phase 5, Task 4
- **Description:** Import tasks via `MigrateFromSPC`, then call `store.IsEmpty()`. Verify it returns `false`. This is the gate condition in `main.go` that prevents re-migration. Also verify `IsEmpty()` returns `true` on a fresh database before import.

### AC4.3 — First run without SPC (standalone mode) starts with empty store, no error

- **Test type:** Unit (migration function + main.go behavior)
- **Test files:** `internal/tasksync/supernote/migration_test.go`
- **Verified in:** Phase 5, Task 4 (migration) and Phase 7, Task 1 (main.go degradation)
- **Description:** Two complementary tests:
  1. **Migration test:** Call `MigrateFromSPC` with a mock SPC that fails login. Verify the function returns an error and the store remains empty (no partial import). This simulates the `main.go` flow where login failure logs a warning and proceeds with an empty store.
  2. **Standalone mode test (Phase 7):** Verified by `main.go` changes that make MariaDB connection non-fatal when `UB_SN_SYNC_ENABLED=false`. When sync is disabled, none of the migration or sync code runs.

- **Test type:** Human verification
- **Justification:** The full standalone startup path (no SPC, no MariaDB for tasks) involves `main.go` wiring that is integration-level. Unit tests verify the components; end-to-end startup requires a running instance.
- **Verification steps:**
  1. Set `UB_SN_SYNC_ENABLED=false` and point `UB_SUPERNOTE_DBENV_PATH` to a nonexistent path (or set invalid DB credentials).
  2. Start UltraBridge.
  3. Confirm it starts with a warning about DB connection failure but no crash.
  4. Confirm CalDAV and web UI are functional with an empty task store.
  5. Create a task via CalDAV client -- confirm it persists in SQLite.

---

## AC5: Adapter-ready architecture

### AC5.1 — Sync engine accepts mock adapter implementing DeviceAdapter interface, runs full sync cycle

- **Test type:** Unit (engine-level with mock adapter)
- **Test file:** `internal/tasksync/engine_test.go`
- **Verified in:** Phase 3, Task 5
- **Description:** Register a hand-rolled mock adapter implementing `DeviceAdapter` (stores tasks in `map[string]RemoteTask`, tracks Push calls). Start the engine, trigger a sync cycle. Mock adapter's `Pull` returns 2 tasks. After the cycle, verify both tasks exist in the local store. Then create a local task, trigger sync. Verify the mock adapter's `Push` receives the new task as a `ChangeCreate`.

### AC5.2 — Registering/unregistering an adapter requires no changes to task store, CalDAV backend, or web handler

- **Test type:** Unit (engine-level with mock adapter)
- **Test file:** `internal/tasksync/engine_test.go`
- **Verified in:** Phase 3, Task 5
- **Description:** Register an adapter and trigger a sync cycle. Unregister it and verify the task store and its data are unchanged. Register a different mock adapter with a different ID, trigger a cycle, and verify it works. This demonstrates that adapter lifecycle is fully decoupled from the task store, CalDAV backend, and web handler -- none of which are modified or even referenced during the test.

- **Test type:** Structural verification (code review)
- **Justification:** The "no changes required" aspect is an architectural property verified by examining the dependency graph. The `tasksync` package does not import `caldav` or `web`. The `caldav.TaskStore` interface is unchanged between phases. This is verified during code review and by the fact that existing CalDAV and web tests pass unchanged after adding sync support.

### AC5.3 — Disabling Supernote adapter (UB_SN_SYNC_ENABLED=false) leaves task store and CalDAV fully functional

- **Test type:** Human verification
- **Justification:** This criterion validates `main.go` wiring behavior: when `UB_SN_SYNC_ENABLED=false`, the sync engine is never created, the adapter is never registered, and MariaDB connection failure is non-fatal. The individual components are unit-tested, but the full startup path is integration-level.
- **Verification steps:**
  1. Set `UB_SN_SYNC_ENABLED=false` in the environment.
  2. Start UltraBridge (MariaDB may or may not be available).
  3. Confirm the web UI Tasks tab shows sync status as "Disabled" and the "Sync Now" button is disabled.
  4. Create, read, update, and delete tasks via a CalDAV client (e.g., `cadaver`, Apple Reminders, Thunderbird).
  5. Verify all CalDAV operations succeed -- the SQLite task store operates independently.
  6. Verify the web UI task list reflects CalDAV changes.

---

## Summary Matrix

| Criterion | Test Type | Test File | Phase |
|-----------|-----------|-----------|-------|
| AC1.1 | Unit | `internal/taskdb/store_test.go` | 1 |
| AC1.2 | Unit | `internal/taskdb/store_test.go` | 1 |
| AC1.3 | Unit | `internal/taskdb/store_test.go` | 1 |
| AC1.4 | Unit | `internal/caldav/vtodo_test.go` | 2 |
| AC1.5 | Unit | `internal/caldav/vtodo_test.go` | 2 |
| AC1.6 | Unit | `internal/taskdb/store_test.go` | 1 |
| AC1.7 | Unit | `internal/taskdb/store_test.go` | 1 |
| AC2.1 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.2 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.3 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.4 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.5 | Unit | `internal/tasksync/engine_test.go` | 3 |
| AC2.6 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.7 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.8 | Unit | `internal/tasksync/supernote/adapter_test.go` | 4 |
| AC2.9 | Unit | `internal/tasksync/engine_test.go` | 3 |
| AC3.1 | Unit + Human | `internal/web/handler_test.go` | 6 |
| AC3.2 | Unit + Human | `internal/web/handler_test.go` | 6 |
| AC3.3 | Unit + Human | `internal/web/handler_test.go` | 6 |
| AC4.1 | Unit | `internal/tasksync/supernote/migration_test.go` | 5 |
| AC4.2 | Unit | `internal/tasksync/supernote/migration_test.go` | 5 |
| AC4.3 | Unit + Human | `internal/tasksync/supernote/migration_test.go` | 5, 7 |
| AC5.1 | Unit | `internal/tasksync/engine_test.go` | 3 |
| AC5.2 | Unit + Structural | `internal/tasksync/engine_test.go` | 3 |
| AC5.3 | Human | _(main.go wiring)_ | 7 |
