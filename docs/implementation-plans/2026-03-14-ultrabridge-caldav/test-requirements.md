# UltraBridge CalDAV — Test Requirements

## Automated Test Coverage

### ultrabridge-caldav.AC1: Go service running as Docker container

| AC | Test Type | Test File | Phase/Task | Description |
|---|---|---|---|---|
| AC1.1 | integration | `tests/integration_test.go` | Phase 8 Task 2 | Start full service against real DB; verify `/health` returns 200 and user ID is discovered |
| AC1.2 | integration | `tests/integration_test.go` | Phase 8 Task 2 | Start with invalid DSN (wrong password); verify startup fails with clear error message containing DB connection context |
| AC1.3 | unit | `internal/logging/logging_test.go` | Phase 6 Task 4 | Verify structured log entries contain request ID, method, path, status, duration; verify level filtering (debug suppressed at info level); verify JSON format produces valid JSON; verify text format produces key=value pairs |

### ultrabridge-caldav.AC2: CalDAV server with VTODO collection

| AC | Test Type | Test File | Phase/Task | Description |
|---|---|---|---|---|
| AC2.1 | integration | `tests/integration_test.go` | Phase 8 Task 2 | Verify `/.well-known/caldav` returns 301 redirect to `/caldav/`; unit-level: verify `CalendarHomeSetPath` returns correct prefix path |
| AC2.2 | unit | `internal/caldav/backend_test.go` | Phase 3 Task 5 | `ListCalendars` returns a collection with `SupportedComponentSet` containing `"VTODO"` |
| AC2.3 | unit | `internal/caldav/backend_test.go` | Phase 3 Task 5 | `ListCalendars` returns a collection whose `Name` matches the configured collection name |
| AC2.4 | unit | `internal/caldav/backend_test.go` | Phase 3 Task 5 | After `PutCalendarObject`, list objects and verify ETags changed; verify max `last_modified` (CTag) differs before and after a write |
| AC2.5 | unit | `internal/caldav/vtodo_test.go` | Phase 3 Task 2 | `HasVEvent` returns true for calendar with VEVENT; `PutCalendarObject` with VEVENT-containing calendar returns error |

### ultrabridge-caldav.AC3: Bidirectional task sync

| AC | Test Type | Test File | Phase/Task | Description |
|---|---|---|---|---|
| AC3.1 | unit | `internal/taskstore/store.go` (verified via) `internal/caldav/backend_test.go` | Phase 2 Task 5 / Phase 3 Task 5 | Task created in store appears when listed via `ListCalendarObjects`; store `List` returns non-deleted tasks |
| AC3.2 | unit | `internal/taskstore/mapping_test.go` | Phase 2 Task 4 | `CompletionTime` returns `last_modified` (not `completed_time`) for completed tasks; test with task where both fields differ and assert returned time matches `last_modified` |
| AC3.3 | unit | `internal/taskstore/mapping_test.go` | Phase 2 Task 4 | `GenerateTaskID` produces correct MD5 from title + timestamp; verify hex output matches `md5(title + timestamp_string)` |
| AC3.4 | unit | `internal/caldav/backend_test.go` | Phase 3 Task 5 | `PutCalendarObject` with STATUS=COMPLETED writes `status=completed` via store update; verify `last_modified` is set to current time |
| AC3.5 | unit | `internal/sync/notifier_test.go` | Phase 5 Task 3 | `Notify()` sends correctly formatted STARTSYNC message to mock WebSocket server; validate `42["ServerMessage","{...}"]` format with `messageType`, `equipmentNo`, `timestamp`, `msgType`, `code` fields |
| AC3.6 | unit | `internal/sync/notifier_test.go` | Phase 5 Task 3 | When notifier has no connection (`conn == nil`), `Notify()` returns error without panic; CalDAV backend test verifies failed `Notify()` does not cause `PutCalendarObject` to fail |
| AC3.7 | integration | `tests/integration_test.go` | Phase 8 Task 2 | Full round-trip: PUT VTODO via CalDAV, verify task in DB, modify task directly in DB (simulating device), GET via CalDAV, verify updated fields appear |
| AC3.8 | integration | `tests/integration_test.go` | Phase 8 Task 2 | PUT VTODO with DESCRIPTION and PRIORITY, verify `detail` and `importance` columns in DB, GET via CalDAV, verify DESCRIPTION and PRIORITY round-trip |
| AC3.9 | unit | `internal/caldav/vtodo_test.go` | Phase 3 Task 2 | Task with `DueTime=0` produces VTODO with no DUE property |
| AC3.10 | unit | `internal/caldav/vtodo_test.go` | Phase 3 Task 2 | With `dueTimeMode="date_only"`, DUE is rendered as DATE (no time component) and round-trips correctly |
| AC3.11 | unit | `internal/caldav/vtodo_test.go` | Phase 3 Task 2 | With `dueTimeMode="preserve"`, DUE round-trips full DATE-TIME including time component |

### ultrabridge-caldav.AC4: Minimal web UI

| AC | Test Type | Test File | Phase/Task | Description |
|---|---|---|---|---|
| AC4.1 | unit | `internal/web/handler_test.go` | Phase 7 Task 4 | `GET /` returns 200 with HTML containing task titles from in-memory mock store |
| AC4.2 | unit | `internal/web/handler_test.go` | Phase 7 Task 4 | `POST /tasks` with form data `title=Test Task` creates task in store and redirects to `/`; verify task exists in store after POST |
| AC4.3 | unit | `internal/web/handler_test.go` | Phase 7 Task 4 | `POST /tasks/{id}/complete` marks task complete in store and redirects; verify task status is `"completed"` after POST |
| AC4.4 | unit | `internal/logging/broadcaster_test.go` | Phase 7 Task 4 | Test `LogBroadcaster` subscribe/unsubscribe and ring buffer: subscribers receive new entries, multiple subscribers each receive same entry, unsubscribe stops delivery, new subscribers receive backfill from ring buffer |

### ultrabridge-caldav.AC5: Simple auth

| AC | Test Type | Test File | Phase/Task | Description |
|---|---|---|---|---|
| AC5.1 | unit | `internal/auth/auth_test.go` | Phase 4 Task 2 | Request with valid username + password (matching bcrypt hash) passes through to wrapped handler; handler receives the request and responds |
| AC5.2 | unit | `internal/auth/auth_test.go` | Phase 4 Task 2 | Request with no auth header returns 401 with `WWW-Authenticate` header; wrong username returns 401; wrong password returns 401; response body contains no credential hints |

## Human Verification

| AC | Justification | Verification Steps |
|---|---|---|
| — | All acceptance criteria AC1.1 through AC5.2 are covered by automated tests above. No criteria require human-only verification. | — |
