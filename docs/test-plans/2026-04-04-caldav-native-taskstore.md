# CalDAV-Native Task Store — Human Test Plan

## Prerequisites
- UltraBridge built and deployable to target machine (`sysop@192.168.9.52`)
- Supernote device paired with Supernote Private Cloud (SPC) and reachable on the network
- A CalDAV client available (e.g., `cadaver`, Apple Reminders, Thunderbird, or DAVx5)
- Unit tests passing: `go test ./internal/taskdb/ ./internal/caldav/ ./internal/tasksync/ ./internal/tasksync/supernote/ ./internal/web/ -v`

## Phase 1: Standalone Mode (No SPC)

| Step | Action | Expected |
|------|--------|----------|
| 1.1 | Set `UB_SN_SYNC_ENABLED=false` in `.ultrabridge.env`. Set `UB_SUPERNOTE_DBENV_PATH` to a nonexistent path. | Config file updated. |
| 1.2 | Start UltraBridge: `./ultrabridge` | Starts successfully. Log contains warning about DB connection failure or sync disabled. No crash. |
| 1.3 | Open the web UI. Navigate to the Tasks tab. | Page loads. Sync status panel shows "Disabled". "Sync Now" button is disabled. Task list is empty. |
| 1.4 | Using a CalDAV client, connect to `http://<host>:<port>/caldav/`. List the `tasks/` collection. | Collection exists and is empty. |
| 1.5 | Create a new task via CalDAV: title "Standalone Test", due date tomorrow. | PUT succeeds (HTTP 201 or 204). |
| 1.6 | GET the task back by its `.ics` path. | Returns valid VTODO with `SUMMARY:Standalone Test` and correct `DUE` date. |
| 1.7 | Refresh the web UI Tasks tab. | "Standalone Test" appears with correct title and due date. |
| 1.8 | Update the task via CalDAV: change title to "Standalone Updated", mark completed. | PUT succeeds. |
| 1.9 | GET the task again via CalDAV. | VTODO shows `SUMMARY:Standalone Updated` and `STATUS:COMPLETED`. |
| 1.10 | DELETE the task via CalDAV. | DELETE succeeds. Subsequent GET returns 404. LIST excludes it. |
| 1.11 | Refresh the web UI Tasks tab. | Task no longer appears. |

## Phase 2: CalDAV Tier 3 Property Round-Trip

| Step | Action | Expected |
|------|--------|----------|
| 2.1 | Create a recurring task via CalDAV client: title "Weekly Review", recurrence weekly on Monday, reminder 15 min before, categories "Work,Review". | PUT succeeds. |
| 2.2 | GET the task back via CalDAV. | VTODO contains `RRULE:FREQ=WEEKLY;BYDAY=MO`, `VALARM` with `TRIGGER:-PT15M`, `CATEGORIES:Work,Review`. |
| 2.3 | Update task title to "Weekly Review v2" via CalDAV (without changing recurrence/alarm/categories). | PUT succeeds. |
| 2.4 | GET the task again. | `SUMMARY:Weekly Review v2` AND all Tier 3 properties (RRULE, VALARM, CATEGORIES) still present and unchanged. |

## Phase 3: SPC Sync — First-Run Migration

| Step | Action | Expected |
|------|--------|----------|
| 3.1 | On Supernote device, create 2-3 tasks with various states (active with due date, completed, with notes/detail). | Tasks visible on device. |
| 3.2 | Set `UB_SN_SYNC_ENABLED=true` with correct SPC credentials. Ensure task DB is empty (fresh SQLite file). | Config ready. |
| 3.3 | Start UltraBridge. | Starts without error. Logs show migration messages and N tasks imported. |
| 3.4 | Open web UI Tasks tab. | All Supernote tasks appear with correct titles, statuses, and due dates. |
| 3.5 | Connect CalDAV client. List `tasks/` collection. | All imported tasks appear as VTODOs with correct field mapping. |
| 3.6 | Stop and restart UltraBridge (same DB file). | Starts without re-importing. No migration log messages. Task count unchanged. |

## Phase 4: SPC Sync — Bidirectional Sync

| Step | Action | Expected |
|------|--------|----------|
| 4.1 | Verify sync status panel shows: adapter "Active", last sync time, next scheduled sync. | All three fields populated with reasonable values. |
| 4.2 | Create task via CalDAV: title "From CalDAV", status needs-action. | Task created. |
| 4.3 | Click "Sync Now". | Status shows "Syncing..." then returns to "Active" with updated last sync time. |
| 4.4 | On Supernote device, trigger sync. | "From CalDAV" appears on device. |
| 4.5 | On device, create "From Device". | Task created on device. |
| 4.6 | Click "Sync Now". | "From Device" appears in web UI. |
| 4.7 | Verify "From Device" via CalDAV GET. | Returns VTODO with `SUMMARY:From Device`. |
| 4.8 | On device, edit title to "From Device (edited)". | Title changed. |
| 4.9 | Click "Sync Now". | Web UI shows "From Device (edited)". CalDAV GET returns updated SUMMARY. |
| 4.10 | Via CalDAV, mark "From CalDAV" as completed. | PUT succeeds. |
| 4.11 | Click "Sync Now". | Task shows completed on device. |

## Phase 5: SPC Sync — Conflict Resolution

| Step | Action | Expected |
|------|--------|----------|
| 5.1 | Edit task title in UltraBridge to "UB Edit". Before sync, edit same task on device to "Device Edit". | Both sides have different titles. |
| 5.2 | Click "Sync Now". | Task title in UltraBridge is "UB Edit" (UB wins). |
| 5.3 | Trigger sync on device. | Device shows "UB Edit" (UB's version pushed back). |

## Phase 6: SPC Sync — Delete Propagation

| Step | Action | Expected |
|------|--------|----------|
| 6.1 | Delete a synced task via CalDAV DELETE. | Task removed from CalDAV listing and web UI. |
| 6.2 | Click "Sync Now". | Task deleted on SPC side. Device no longer shows it. |
| 6.3 | On device, delete a different synced task. | Task removed from device. |
| 6.4 | Click "Sync Now". | Task no longer appears in web UI or CalDAV listing. |

## Phase 7: Web UI Sync Controls

| Step | Action | Expected |
|------|--------|----------|
| 7.1 | With sync active, observe sync status panel for 15+ seconds. | Panel auto-refreshes (~5 second interval). Times update as cycles occur. |
| 7.2 | Click "Sync Now". Immediately observe button. | Button becomes disabled, shows "Syncing..." indicator. |
| 7.3 | While "Syncing...", attempt to click button. | No additional sync triggered. Button remains disabled. |
| 7.4 | Wait for sync to complete. | Button re-enables. Panel shows updated last sync time. |

## Phase 8: Error Resilience

| Step | Action | Expected |
|------|--------|----------|
| 8.1 | With sync enabled, block network access to SPC server. | Network unreachable. |
| 8.2 | Click "Sync Now". | Sync fails. Logs show warning. Task store still functional (CalDAV CRUD works). |
| 8.3 | Restore network. Click "Sync Now". | Sync succeeds. Status returns to normal. |

## End-to-End: Full Task Lifecycle

1. Via CalDAV, PUT new task: "Lifecycle Test", due in 3 days, needs-action.
2. Verify in web UI with correct fields.
3. Click "Sync Now". Verify on Supernote device.
4. On device, change title to "Lifecycle Test (device edit)" and mark completed.
5. Click "Sync Now".
6. Via CalDAV GET, verify title is "Lifecycle Test (device edit)" and STATUS:COMPLETED.
7. Delete task via CalDAV or web UI.
8. Click "Sync Now". Verify gone from all three interfaces.

## Traceability

| AC | Automated Test | Manual Step |
|----|----------------|-------------|
| AC1.1 | `TestStore_Create_PersistsTask` | Phase 1: 1.5-1.6 |
| AC1.2 | `TestStore_Update_ChangesFieldsAndTimestamp` | Phase 1: 1.8-1.9 |
| AC1.3 | `TestStore_Delete_SoftDeletesAndHides` | Phase 1: 1.10 |
| AC1.4 | `TestBlobRoundTrip` (5 sub-tests) | Phase 2: all |
| AC1.5 | `TestSupernoteTaskNoBlob` | Phase 3: 3.5 |
| AC1.6 | `TestStore_CTag_IncrementsOnChanges` | — |
| AC1.7 | `TestStore_MaxLastModified_EmptyStore` | — |
| AC2.1 | `TestAdapter_AC2_1_Push_Create_SendsNotification` | Phase 4: 4.2-4.4 |
| AC2.2 | `TestAdapter_AC2_2_Push_Update_Status` | Phase 4: 4.10-4.11 |
| AC2.3 | `TestAdapter_AC2_3_Pull_FieldMapping` | Phase 4: 4.6-4.7 |
| AC2.4 | `TestAdapter_AC2_4_Pull_TitleChange` | Phase 4: 4.8-4.9 |
| AC2.5 | `TestSyncEngine_UBWinsConflict` | Phase 5: all |
| AC2.6 | `TestAdapter_AC2_6_ReAuth_On401` | — |
| AC2.7 | `TestAdapter_AC2_7_SPC_Unreachable` | Phase 8: 8.1-8.2 |
| AC2.8 | `TestAdapter_AC2_8_Auth_Failure` | — |
| AC2.9 | `TestSyncEngine_RemoteHardDelete` | Phase 6: 6.3-6.4 |
| AC3.1 | `TestHandleSyncStatus_AC31` | Phase 7: 7.1 |
| AC3.2 | `TestHandleSyncTrigger_AC32` | Phase 4: 4.3; Phase 7: 7.4 |
| AC3.3 | `TestHandleSyncStatus_AC33` | Phase 7: 7.2-7.3 |
| AC4.1 | `TestMigration_AC4_1_ImportsTasks` | Phase 3: 3.3-3.5 |
| AC4.2 | `TestMigration_AC4_2_IsEmpty` | Phase 3: 3.6 |
| AC4.3 | `TestMigration_AC4_3_LoginFailure` | Phase 1: 1.1-1.3 |
| AC5.1 | `TestSyncEngine_AC51_RegisterAndSync` | — |
| AC5.2 | `TestSyncEngine_AC52_MultipleAdapters` | — |
| AC5.3 | _(structural)_ | Phase 1: all |
