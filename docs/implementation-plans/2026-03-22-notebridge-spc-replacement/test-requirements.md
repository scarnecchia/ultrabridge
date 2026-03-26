# NoteBridge Test Requirements

Maps every acceptance criterion from the design plan to either automated tests or documented human verification.

**Test conventions:** Go stdlib `testing` only, no external test frameworks. In-memory SQLite (`:memory:`), `httptest`, table-driven patterns. Project location: `/home/sysop/src/notebridge`.

---

## notebridge-spc-replacement.AC1: Device Authentication

### notebridge-spc-replacement.AC1.1 — Challenge-response flow completes successfully

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | POST `/api/user/login/challenge` returns random code and timestamp. POST `/api/user/login/verify` with SHA256(MD5+code) returns JWT. Uses `httptest.NewServer` with full sync server handler and in-memory SQLite. |
| Rationale | Phase 1, Task 8 defines integration tests for auth endpoints. Challenge-response is deterministic given a known password hash and random code, fully automatable. |

### notebridge-spc-replacement.AC1.2 — JWT accepted by auth middleware

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | After login, use returned JWT to call an auth-protected endpoint (e.g., `list_folder_v3`). Assert 200 response. |
| Rationale | Phase 1 auth middleware wraps all `/api/file/*` routes. Test exercises real middleware chain via httptest. |

### notebridge-spc-replacement.AC1.3 — Wrong password returns E0019

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | Request challenge, submit verify with wrong SHA256 hash. Assert JSON response contains error code `E0019`. |
| Rationale | Error code mapping is deterministic. Phase 1, Task 8 explicitly lists this test case. |

### notebridge-spc-replacement.AC1.4 — Expired/invalid JWT returns E0712

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | Two sub-cases: (a) craft a JWT with expired `exp` claim, call protected endpoint, assert E0712; (b) send malformed token string, assert E0712. |
| Rationale | JWT validation is deterministic. Phase 1 middleware returns E0712 for all token validation failures. |

### notebridge-spc-replacement.AC1.5 — Account lockout after 6 failures

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | Submit 6 incorrect verify requests within the 12-hour window. Assert 7th attempt returns E0045 even with correct credentials. Verify lockout clears after the configured window (manipulate `locked_until` in DB). |
| Rationale | Lockout state is tracked in syncdb `users.error_count` and `locked_until`. Phase 1 design specifies 6 failures in 12 hours. Fully testable with in-memory SQLite and time manipulation. |

### notebridge-spc-replacement.AC1.6 — Expired challenge code rejected

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/auth_test.go` |
| Description | Request challenge, manually set `login_challenges.timestamp` to >5 minutes ago in DB, then submit verify. Assert rejection (error response, not E0019 — expired challenge is a distinct failure). |
| Rationale | Challenge TTL (5 min) is stored in `login_challenges`. Test manipulates DB timestamp directly. Phase 1, Task 8 lists this as an edge case test. |

---

## notebridge-spc-replacement.AC2: File Sync

### notebridge-spc-replacement.AC2.1 — Full sync cycle

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Login, POST sync/start, POST list_folder_v3 (empty), POST upload/apply (get signed URL), POST oss/upload (file content), POST upload/finish, POST list_folder_v3 (file appears with correct MD5 and size), POST sync/end. |
| Rationale | Phase 2, Task 10 defines this exact 8-step integration test. Uses httptest with real SQLite and real BlobStore backed by `t.TempDir()`. |

### notebridge-spc-replacement.AC2.2 — Download with Range header support

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload file, POST download_v3 to get signed URL, GET oss/download (full content matches), GET oss/download with `Range: bytes=0-9` header (returns first 10 bytes, HTTP 206). |
| Rationale | Phase 2 uses `http.ServeContent` which provides Range support natively. Test verifies both full and partial download. |

### notebridge-spc-replacement.AC2.3 — Chunked upload merges correctly

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload/apply for part URL, POST 3 chunk parts sequentially (auto-merge on final), upload/finish, download and verify content is concatenation of all parts. |
| Rationale | Phase 2, Task 10 defines this test. ChunkStore.MergeChunks streams parts in order through BlobStore.Put. |

### notebridge-spc-replacement.AC2.4 — Soft delete

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload file, POST delete_folder_v3 with file ID, POST list_folder_v3 (file gone), verify `recycle_files` table has the entry via direct DB query. |
| Rationale | Phase 2 design: SoftDelete inserts into `recycle_files` and deletes from `files`. DB state is directly queryable. |

### notebridge-spc-replacement.AC2.5 — Move/rename with autorename

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload "test.note", create destination folder, POST move_v3 to move file, verify original folder empty and destination has file. Create collision, move with autorename=true, verify "test(1).note". |
| Rationale | Phase 2, Task 10. Also tests autorename helper (Phase 2, Task 6 unit tests in `internal/sync/fileutil_test.go`). |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/sync/fileutil_test.go` |
| Description | AutoRename with no collision, with collision (returns "name(1).ext"), with multiple collisions ("name(2)"), folders without extensions. IsCircularMove detection. SplitNameExt edge cases. |
| Rationale | Phase 2, Task 6 defines these as pure-function unit tests with no DB dependency. |

### notebridge-spc-replacement.AC2.6 — Copy creates independent duplicate

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload file, POST copy_v3 with autorename, list folder (two files with different IDs), download both (same content), modify original and verify copy unchanged. |
| Rationale | Phase 2, Task 10. Copy generates a new Snowflake ID and creates an independent blob. |

### notebridge-spc-replacement.AC2.7 — Sync lock rejects second device

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Login as device A, sync/start. Login as device A again (different equipmentNo), sync/start. Assert error E0078. |
| Rationale | Phase 2, Task 10. Also covered at DB level in Phase 2, Task 5 (`internal/syncdb/store_file_test.go`). |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/syncdb/store_file_test.go` |
| Description | AcquireLock succeeds first time. AcquireLock by different device while first holds lock returns error. AcquireLock after lock expires succeeds. ReleaseLock allows re-acquire. RefreshLock extends expiry. |
| Rationale | Phase 2, Task 5 defines these as syncdb-level tests with in-memory SQLite. |

### notebridge-spc-replacement.AC2.8 — Expired signed URL rejected

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload/apply to get signed URL, manually set nonce expiry to past in DB, attempt upload with expired URL, assert rejection. |
| Rationale | Phase 2, Task 10. Nonce expiry tracked in `url_nonces.expires_at`. Direct DB manipulation. |

### notebridge-spc-replacement.AC2.9 — Reused nonce rejected

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Upload file successfully (consumes nonce), replay same signed URL, assert rejection. |
| Rationale | Phase 2, Task 10. ConsumeNonce deletes the nonce on first use. Second use finds no matching row. |

### notebridge-spc-replacement.AC2.10 — Sync lock expiry and refresh

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_file_test.go` |
| Description | Login, sync/start, manually expire lock in DB, login as different device, sync/start succeeds. Separately: upload/finish extends lock expiry (verify `sync_locks.expires_at` updated). |
| Rationale | Phase 2, Task 10. Lock TTL is 10 min; test manipulates DB timestamps. RefreshLock called in upload/finish handler. |

---

## notebridge-spc-replacement.AC3: Socket.IO

### notebridge-spc-replacement.AC3.1 — Socket.IO handshake with JWT

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/socketio_test.go` |
| Description | Connect via `golang.org/x/net/websocket` to `/socket.io/?token=<jwt>&type=test&EIO=3&transport=websocket`. Read first frame: type '0' with sid, pingInterval=25000, pingTimeout=5000. Read second frame: "40" (connect ack). |
| Rationale | Phase 3, Task 7. Engine.IO v3 handshake is fully deterministic. |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/sync/engineio_test.go` |
| Description | EncodeOpenPacket format, EncodeEvent produces `42["name",data]`, DecodeFrame splits type from payload, DecodeEvent extracts name and data, round-trip encode/decode. |
| Rationale | Phase 3, Task 7 defines these as frame encoding/decoding unit tests. |

### notebridge-spc-replacement.AC3.2 — Ping/pong keepalive

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/socketio_test.go` |
| Description | After handshake, send "2" (ping), read response "3" (pong). Also test ratta_ping: send `42["ratta_ping",{}]`, read `42["ratta_ping","Received"]`. |
| Rationale | Phase 3, Task 7. Ping/pong is a simple frame exchange. |

### notebridge-spc-replacement.AC3.3 — ServerMessage pushed on file change

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/socketio_test.go` |
| Description | Connect device A via Socket.IO. Upload a file via HTTP (triggers FileUploadedEvent via event bus). Read from device A's WebSocket: receives `42["ServerMessage",{...}]` with msgType "FILE-SYN". Also test multiple devices: connect two clients, upload file, both receive ServerMessage. |
| Rationale | Phase 3, Task 7. Exercises full event chain: upload/finish -> EventBus.Publish -> NotifyManager -> WebSocket write. |

### notebridge-spc-replacement.AC3.4 — Invalid JWT rejected on Socket.IO connect

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/socketio_test.go` |
| Description | Connect with `token=garbage`, read error frame `44{"message":"Authentication failed"}`, connection closed. |
| Rationale | Phase 3, Task 7. Auth validation happens before handshake. |

---

## notebridge-spc-replacement.AC4: OCR Pipeline + RECOGNTEXT Injection

### notebridge-spc-replacement.AC4.1 — Upload triggers OCR and syncdb update

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | Upload a Standard .note file via sync API. Wait for pipeline detection and processing (poll job status or channel). Verify syncdb file entry has updated MD5 (different from upload MD5). Verify job status is "done". Uses mock OCR client returning fixed text. |
| Rationale | Phase 4, Task 6. Full flow: upload -> FileUploadedEvent -> pipeline -> processor (mock OCR) -> AfterInject hook -> syncdb.UpdateFileMD5 + FileModifiedEvent. |

### notebridge-spc-replacement.AC4.2 — Next sync downloads injected version (no CONFLICT)

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | After injection completes, list_folder_v3 and verify content_hash matches post-injection MD5. Download via signed URL and verify file contains RECOGNTEXT (parse with go-sn). |
| Rationale | Phase 4, Task 6. This is the core CONFLICT-bug resolution test. NoteBridge updates syncdb MD5 post-injection; tablet sees updated hash and downloads. No second authority to conflict with. |

### notebridge-spc-replacement.AC4.3 — RTR notes indexed but not modified

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | Upload an RTR .note file (FILE_RECOGN_TYPE=1). Wait for processing. Verify file content unchanged (MD5 same as upload). Verify FTS5 index contains OCR'd text (indexed for search but file not modified). |
| Rationale | Phase 4, Task 6. Design specifies RTR notes are never modified because device auto-convert clobbers injected RECOGNTEXT within ~40 seconds. Injection policy is checked in worker based on FILE_RECOGN_TYPE. |

### notebridge-spc-replacement.AC4.4 — Re-processing on hash change

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | Upload and process a .note file. Upload a new version of same file (different content, same path). Verify pipeline detects SHA-256 mismatch. Verify job is re-queued with `requeue_after` set to ~30 seconds in the future (check DB directly). |
| Rationale | Phase 4, Task 6. Re-processing uses SHA-256 stored in `notestore.notes` for change detection. 30-second delay prevents re-processing during multi-page uploads. |

### notebridge-spc-replacement.AC4.5 — FTS5 search returns OCR'd content

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | After injection completes, call search.Search with the mock OCR text. Verify results contain the processed note with relevant snippet. |
| Rationale | Phase 4, Task 6. FTS5 indexing happens in the same worker pipeline via the Indexer interface. |

**Supporting unit tests (ported from UltraBridge):**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/search/index_test.go` |
| Description | FTS5 IndexPage and Search round-trip with BM25 scoring. Adapted from UltraBridge. |
| Rationale | Phase 4, Task 5. Search package ported unchanged; tests adapted for syncdb. |

| Test type | Unit |
|---|---|
| Test file | `internal/processor/processor_test.go` |
| Description | Job queue: enqueue, claim, complete, fail, skip, unskip, requeue-with-delay. Watchdog: stuck job reclamation. Worker: backup before modify, OCR client mocking. Adapted from UltraBridge. |
| Rationale | Phase 4, Task 5. Processor package ported with AfterInject replacing CatalogUpdater; tests adapted. |

### notebridge-spc-replacement.AC4.6 — Backup created before file modification

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_pipeline_test.go` |
| Description | After injection of a Standard .note file, verify backup file exists in the backup directory. Verify backup content matches original (pre-injection) file content. |
| Rationale | Phase 4, Task 6. Worker creates backup before any file modification. Backup path stored in `notestore.notes.backup_path`. |

---

## notebridge-spc-replacement.AC5: Tasks

### notebridge-spc-replacement.AC5.1 — Task created on tablet syncs to NoteBridge

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_tasks_test.go` |
| Description | Login, create a schedule group via POST, create a task in that group with title/detail/status/dueTime, list tasks and verify task appears with all fields correct. |
| Rationale | Phase 5, Task 6. Device sync API for tasks uses the same JSON format as opennotecloud. |

### notebridge-spc-replacement.AC5.2 — Task list (group) CRUD

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_tasks_test.go` |
| Description | Create group (returns taskListId), list groups (appears), update group title (success), list groups (updated title), delete group (cascading), list groups (empty). |
| Rationale | Phase 5, Task 6. Full CRUD cycle through HTTP handlers. |

### notebridge-spc-replacement.AC5.3 — Batch task update atomicity

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_tasks_test.go` |
| Description | Create 3 tasks. Batch update 2 (change status, importance). List tasks: verify 2 updated, 1 unchanged. Batch update with non-existent taskId: assert error E0329, no changes applied (transaction rollback). |
| Rationale | Phase 5, Task 6. BatchUpdateTasks uses a single SQLite transaction. |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/syncdb/store_task_test.go` |
| Description | BatchUpdateTasks atomicity at DB level: create 3 tasks, batch update 2, verify. Non-existent taskId causes full rollback. |
| Rationale | Phase 5, Task 2. DB-level tests verify transaction semantics. |

### notebridge-spc-replacement.AC5.4 — nextSyncToken pagination

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_tasks_test.go` |
| Description | Create task A at T1. List all tasks, get nextSyncToken T2. Create task B at T3. List with nextSyncToken=T2: returns only task B. Response includes nextSyncToken=T4. List with nextSyncToken=T4: empty. |
| Rationale | Phase 5, Task 6. nextSyncToken = millis timestamp. ListScheduleTasks filters `WHERE updated_at >= syncToken`. |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/syncdb/store_task_test.go` |
| Description | ListScheduleTasks with syncToken returns only tasks modified after token. Final page returns nextSyncToken. Without syncToken returns all paginated. |
| Rationale | Phase 5, Task 2. DB-level pagination and token filtering. |

### notebridge-spc-replacement.AC5.5 — Recurrence field preserved

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_tasks_test.go` |
| Description | Create task with recurrence="RRULE:FREQ=DAILY;COUNT=5". List tasks, verify recurrence field preserved exactly. Update via batch with new recurrence. List, verify update. |
| Rationale | Phase 5, Task 6. Recurrence is stored as a text field in `schedule_tasks.recurrence`, passed through without interpretation. |

---

## notebridge-spc-replacement.AC6: Digests

### notebridge-spc-replacement.AC6.1 — Summary syncs to NoteBridge

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_digests_test.go` |
| Description | Create summary item with uniqueIdentifier, content, tags. Query summaries, verify item appears with all fields. Update summary, verify fields updated. Delete, verify no longer listed. |
| Rationale | Phase 5, Task 6. Full CRUD round-trip through HTTP handlers. |

### notebridge-spc-replacement.AC6.2 — Summary groups (collections) CRUD

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_digests_test.go` |
| Description | Create summary group (returns Snowflake ID), list groups (appears), update group name, delete group (no longer listed). Create duplicate uniqueIdentifier: assert error E0338. |
| Rationale | Phase 5, Task 6. Groups are summaries with is_summary_group='Y'. |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/syncdb/store_digest_test.go` |
| Description | CreateSummary, duplicate uniqueIdentifier returns ErrUniqueIDExists, UpdateSummary, DeleteSummary, ListSummaryGroups/ListSummaries filtering, ListSummaryHashes, GetSummariesByIDs, pagination. |
| Rationale | Phase 5, Task 2. DB-level CRUD and constraint tests. |

### notebridge-spc-replacement.AC6.3 — Summary file upload/download

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/sync/handlers_digests_test.go` |
| Description | Upload apply for summary file (get signed URLs), upload file content to signed URL, create summary referencing uploaded file (handwriteInnerName), download summary (get signed download URL), download file and verify content matches. |
| Rationale | Phase 5, Task 6. Summary file upload/download reuses the same signed URL infrastructure as file sync (Phase 2). |

---

## notebridge-spc-replacement.AC7: CalDAV

### notebridge-spc-replacement.AC7.1 — Tablet tasks appear as CalDAV VTODOs

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/caldav/integration_test.go` |
| Description | Create task via sync API (POST /api/file/schedule/task). PROPFIND on CalDAV collection. GET specific task as .ics. Verify VTODO SUMMARY matches title, STATUS matches status, DUE matches due_time. |
| Rationale | Phase 6, Task 5. Uses dual httptest servers (sync on 19071, CalDAV on 8443) against shared syncdb. |

**Supporting unit tests:**

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `internal/caldav/vtodo_test.go` |
| Description | Task to VTODO to Task round-trip preserves all fields. Status mapping (needsAction/completed). DueTime modes. Empty/nil fields. CompletionTime quirk. Ported from UltraBridge. |
| Rationale | Phase 6, Task 4. Pure conversion tests, no DB. |

| Test type | Unit |
|---|---|
| Test file | `internal/taskstore/store_test.go` |
| Description | List, Get, Create, Update, Delete, MaxLastModified. Soft-delete behavior (is_deleted='Y' hides from List/Get). ETag/CTag changes. Adapted from UltraBridge for syncdb. |
| Rationale | Phase 6, Task 4. TaskStore rewritten against syncdb; tests verify interface contract. |

| Test type | Unit |
|---|---|
| Test file | `internal/caldav/backend_test.go` |
| Description | CalDAV object CRUD through Backend interface. Notifier called on Put and Delete. Adapted from UltraBridge. |
| Rationale | Phase 6, Task 4. Backend interface tests with in-memory SQLite. |

### notebridge-spc-replacement.AC7.2 — CalDAV VTODO syncs to tablet

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/caldav/integration_test.go` |
| Description | PUT a VTODO via CalDAV (PUT /caldav/tasks/{new_id}.ics). List tasks via sync API (POST /api/file/schedule/task/all). Verify new task appears with correct title, status, due time. Verify event bus received notification. |
| Rationale | Phase 6, Task 5. CalDAV PUT calls taskstore.Create, which writes to syncdb. Event bus notifier adapter publishes FileModifiedEvent for Socket.IO push. |

### notebridge-spc-replacement.AC7.3 — Task completion status round-trips

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/caldav/integration_test.go` |
| Description | Create task via sync API with status "needsAction". Read via CalDAV: STATUS is NEEDS-ACTION. Update via CalDAV: set STATUS to COMPLETED. Read via sync API: status is "completed". Update via sync API: set back to "needsAction". Read via CalDAV: STATUS is NEEDS-ACTION. |
| Rationale | Phase 6, Task 5. Exercises bidirectional status mapping through taskstore.mapping.go helpers (CalDAVStatus/SupernoteStatus). |

---

## notebridge-spc-replacement.AC8: Web UI

### notebridge-spc-replacement.AC8.1 — File browser shows files

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/web/handler_test.go` |
| Description | Seed notestore with 3 test files (different types). GET /files with Basic Auth, assert 200 and response contains file names and types. GET /files?path=Note shows subdirectory. |
| Rationale | Phase 7, Task 6. Web handler uses noteStore.List() abstraction. Test seeds files in blob storage and notestore DB. |

### notebridge-spc-replacement.AC8.2 — Job status shows counts

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/web/handler_test.go` |
| Description | Seed processor with jobs in various statuses (2 pending, 1 in_progress, 3 done). GET /files/status, assert JSON with correct counts. POST /processor/start returns 200. POST /processor/stop returns 200. |
| Rationale | Phase 7, Task 6. Handler queries processor for job counts. |

### notebridge-spc-replacement.AC8.3 — Search returns FTS5 results with snippets

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/web/handler_test.go` |
| Description | Index test content via search.IndexPage ("handwritten meeting notes about project planning"). GET /search?q=meeting, assert 200 with matching result containing snippet. GET /search?q=nonexistent returns empty results. |
| Rationale | Phase 7, Task 6. Web search handler calls search.Search and formats results with FTS5 snippets. |

### notebridge-spc-replacement.AC8.4 — Task list view shows tasks

| Field | Value |
|---|---|
| Test type | Integration |
| Test file | `internal/web/handler_test.go` |
| Description | Create 3 tasks via taskstore (different statuses, due dates). GET / with Basic Auth, assert 200 with all 3 task titles in response. POST /tasks/{id}/complete redirects and task status updated. |
| Rationale | Phase 7, Task 6. Web UI root route renders task list from taskstore.List(). |

---

## notebridge-spc-replacement.AC9: Deployment

### notebridge-spc-replacement.AC9.1 — install.sh creates directories and starts container

| Field | Value |
|---|---|
| Verification type | **Human verification** |
| Justification | install.sh is an interactive bash script that prompts for user input, creates system directories, generates secrets, invokes Docker Compose, and performs health checks. It interacts with the host filesystem and Docker daemon, which cannot be meaningfully tested in an isolated Go test or CI environment. |
| Verification approach | (1) Run install.sh on a clean test VM/container with Docker installed. (2) Verify .env file created with all NB_ variables. (3) Verify directory structure created at data path. (4) Verify Docker container running (`docker ps`). (5) Verify health endpoint responds at configured port. (6) Verify user can authenticate with provided credentials. |

### notebridge-spc-replacement.AC9.2 — rebuild.sh rebuilds and restarts

| Field | Value |
|---|---|
| Verification type | **Human verification** |
| Justification | rebuild.sh invokes `docker compose up -d --build --force-recreate` and performs a health check. It depends on the Docker daemon, host networking, and a running container from install.sh. Cannot be tested in isolation. |
| Verification approach | (1) Make a code change (e.g., update health endpoint message). (2) Run rebuild.sh. (3) Verify container rebuilt (new image hash). (4) Verify health endpoint returns updated response. (5) Verify no data loss (database and storage volumes preserved). |

### notebridge-spc-replacement.AC9.3 — Single container, no external dependencies

| Field | Value |
|---|---|
| Verification type | **Human verification** |
| Justification | This criterion is an architectural constraint verified by inspecting docker-compose.yml and observing runtime behavior. There is no code path to test --- it is the absence of MariaDB, Redis, and other services that must be confirmed. |
| Verification approach | (1) Inspect docker-compose.yml: only one service defined. (2) After install.sh, run `docker ps` and verify only the notebridge container runs. (3) Run `docker network ls` and verify no separate database containers. (4) Inspect .env: no MYSQL_, REDIS_, or SPC-related variables. (5) Verify all functionality works with just the single container (auth, sync, tasks, CalDAV, web UI). |

---

## notebridge-spc-replacement.AC10: Migration

### notebridge-spc-replacement.AC10.1 — migrate.sh creates correct syncdb entries

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `cmd/migrate/migrator_test.go` |
| Description | Create mock SPC data: 1 user, 3 folders, 5 files. Write dummy files to temp SPC data path. Run migrator. Verify: syncdb has 3 folder + 5 file entries, each file exists in blob storage with correct content, MD5s match, directory tree preserved, Snowflake IDs generated. Also test missing file handling: 3 files in DB but only 2 on disk, migration continues with warning. |
| Rationale | Phase 8, Task 4. Uses mock SPCReader (returns canned data without real MariaDB). Migrator sub-methods tested with pre-built SPC structs. |

### notebridge-spc-replacement.AC10.2 — Tasks exported from SPC appear in NoteBridge

| Field | Value |
|---|---|
| Test type | Unit |
| Test file | `cmd/migrate/migrator_test.go` |
| Description | Create mock SPC data: 2 task groups, 5 tasks (mixed statuses). Run migrator. Verify: syncdb has 2 schedule_groups and 5 schedule_tasks. Verify fields match (title, status, due_time, recurrence, links). Verify completed_time quirk handled (SPC last_modified maps to completion semantics). Verify soft-deleted SPC tasks not migrated. Also test summary migration: 1 group, 3 items, unique_identifier preserved. |
| Rationale | Phase 8, Task 4. Mock SPCReader avoids MariaDB dependency. Task field mapping is deterministic. |

### notebridge-spc-replacement.AC10.3 — Tablet syncs without errors after migration

| Field | Value |
|---|---|
| Verification type | **Human verification** |
| Justification | This criterion requires a physical Supernote tablet (or firmware-level emulator that does not exist) to perform an actual sync cycle against NoteBridge after migration. The tablet firmware implements the SPC protocol with device-specific behaviors (challenge-response with hardware-derived identifiers, Socket.IO keepalive timing, file hash comparison logic) that cannot be faithfully replicated in a test harness. |
| Verification approach | (1) Run migrate.sh against a live SPC installation (or test SPC with known data). (2) Point tablet at NoteBridge IP (change hostname in Supernote settings). (3) Trigger sync on tablet. (4) Verify: no error displayed on tablet. (5) Verify: all files appear in tablet file browser. (6) Verify: tasks appear in tablet task app. (7) Verify: open a .note file that was previously OCR'd --- search on tablet finds the content. (8) Verify: no CONFLICT files created. (9) Upload a new note from tablet, verify it appears in NoteBridge web UI. |

---

## Summary Matrix

| AC | Criterion | Automated | Human | Test File(s) |
|----|-----------|:---------:|:-----:|-------------|
| AC1.1 | Challenge-response flow | Yes | - | `internal/sync/auth_test.go` |
| AC1.2 | JWT accepted by middleware | Yes | - | `internal/sync/auth_test.go` |
| AC1.3 | Wrong password E0019 | Yes | - | `internal/sync/auth_test.go` |
| AC1.4 | Expired/invalid JWT E0712 | Yes | - | `internal/sync/auth_test.go` |
| AC1.5 | Account lockout E0045 | Yes | - | `internal/sync/auth_test.go` |
| AC1.6 | Expired challenge rejected | Yes | - | `internal/sync/auth_test.go` |
| AC2.1 | Full sync cycle | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.2 | Range download | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.3 | Chunked upload | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.4 | Soft delete | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.5 | Move/rename | Yes | - | `internal/sync/handlers_file_test.go`, `internal/sync/fileutil_test.go` |
| AC2.6 | Copy | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.7 | Sync lock conflict | Yes | - | `internal/sync/handlers_file_test.go`, `internal/syncdb/store_file_test.go` |
| AC2.8 | Expired signed URL | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.9 | Reused nonce | Yes | - | `internal/sync/handlers_file_test.go` |
| AC2.10 | Lock expiry/refresh | Yes | - | `internal/sync/handlers_file_test.go`, `internal/syncdb/store_file_test.go` |
| AC3.1 | Socket.IO handshake | Yes | - | `internal/sync/socketio_test.go`, `internal/sync/engineio_test.go` |
| AC3.2 | Ping/pong keepalive | Yes | - | `internal/sync/socketio_test.go` |
| AC3.3 | ServerMessage push | Yes | - | `internal/sync/socketio_test.go` |
| AC3.4 | Invalid JWT rejected | Yes | - | `internal/sync/socketio_test.go` |
| AC4.1 | Upload -> OCR -> inject -> syncdb | Yes | - | `internal/sync/handlers_pipeline_test.go` |
| AC4.2 | CONFLICT-free download | Yes | - | `internal/sync/handlers_pipeline_test.go` |
| AC4.3 | RTR notes not modified | Yes | - | `internal/sync/handlers_pipeline_test.go` |
| AC4.4 | Re-processing on hash change | Yes | - | `internal/sync/handlers_pipeline_test.go` |
| AC4.5 | FTS5 search | Yes | - | `internal/sync/handlers_pipeline_test.go`, `internal/search/index_test.go` |
| AC4.6 | Backup before modification | Yes | - | `internal/sync/handlers_pipeline_test.go` |
| AC5.1 | Task sync | Yes | - | `internal/sync/handlers_tasks_test.go` |
| AC5.2 | Group CRUD | Yes | - | `internal/sync/handlers_tasks_test.go` |
| AC5.3 | Batch update atomicity | Yes | - | `internal/sync/handlers_tasks_test.go`, `internal/syncdb/store_task_test.go` |
| AC5.4 | nextSyncToken pagination | Yes | - | `internal/sync/handlers_tasks_test.go`, `internal/syncdb/store_task_test.go` |
| AC5.5 | Recurrence round-trip | Yes | - | `internal/sync/handlers_tasks_test.go` |
| AC6.1 | Summary sync | Yes | - | `internal/sync/handlers_digests_test.go` |
| AC6.2 | Summary group CRUD | Yes | - | `internal/sync/handlers_digests_test.go`, `internal/syncdb/store_digest_test.go` |
| AC6.3 | Summary file upload/download | Yes | - | `internal/sync/handlers_digests_test.go` |
| AC7.1 | Tasks as CalDAV VTODOs | Yes | - | `internal/caldav/integration_test.go`, `internal/caldav/vtodo_test.go`, `internal/taskstore/store_test.go` |
| AC7.2 | CalDAV VTODO syncs to tablet | Yes | - | `internal/caldav/integration_test.go` |
| AC7.3 | Completion status round-trip | Yes | - | `internal/caldav/integration_test.go` |
| AC8.1 | File browser | Yes | - | `internal/web/handler_test.go` |
| AC8.2 | Job status counts | Yes | - | `internal/web/handler_test.go` |
| AC8.3 | FTS5 search with snippets | Yes | - | `internal/web/handler_test.go` |
| AC8.4 | Task list view | Yes | - | `internal/web/handler_test.go` |
| AC9.1 | install.sh | - | Yes | Manual: clean VM test |
| AC9.2 | rebuild.sh | - | Yes | Manual: rebuild + health check |
| AC9.3 | Single container | - | Yes | Manual: docker ps inspection |
| AC10.1 | File migration | Yes | - | `cmd/migrate/migrator_test.go` |
| AC10.2 | Task migration | Yes | - | `cmd/migrate/migrator_test.go` |
| AC10.3 | Tablet syncs after migration | - | Yes | Manual: physical tablet test |

---

## Coverage Statistics

- **Total acceptance criteria:** 47
- **Automated tests:** 43 (91%)
- **Human verification only:** 4 (9%)
- **Test files:** 18 unique files across 8 packages

### Human verification justifications

The 4 human-only criteria fall into two categories:

1. **Shell script deployment (AC9.1, AC9.2, AC9.3):** These test interactive bash scripts that create system directories, invoke Docker Compose, and interact with the Docker daemon. They are inherently host-environment-dependent and cannot run in `go test`. A CI pipeline could theoretically test AC9.1/AC9.2 with Docker-in-Docker, but the implementation plans do not define such infrastructure, and the scripts contain interactive prompts.

2. **Physical device sync (AC10.3):** The Supernote tablet firmware implements the SPC protocol with device-specific behaviors that no test harness can replicate. The tablet must actually connect, authenticate, compare file hashes, and sync without producing CONFLICT files. This is the ultimate validation that protocol compatibility is correct.
