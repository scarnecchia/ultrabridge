# UltraBridge CalDAV — Human Test Plan

## Prerequisites

- Docker Compose environment running at `/mnt/supernote/` with MariaDB accessible
- UltraBridge CalDAV container built and running (or `go run` locally with valid DB connection)
- A CalDAV client installed (e.g., `curl`, or a GUI client such as GNOME Calendar / Thunderbird)
- Unit tests passing: `go test ./internal/...`
- Integration tests passing: `go test -tags=integration ./tests/...` (requires live MariaDB)
- Basic Auth credentials configured (username and bcrypt password hash)

## Phase 1: Service Health and Startup

| Step | Action | Expected |
|------|--------|----------|
| 1.1 | Start the UltraBridge container with valid DB credentials. Run `curl http://localhost:8443/health` | Response: HTTP 200 with JSON body `{"status":"ok"}` |
| 1.2 | Stop the container. Change DB password to an invalid value. Attempt to start | Container exits with non-zero code. Logs contain error message referencing "database", "connection", or "access denied" |
| 1.3 | Start container with `UB_LOG_FORMAT=json`. Make a CalDAV request. Inspect container logs | Each request log line is valid JSON containing fields: `request_id`, `method`, `path`, `status`, `duration_ms` |
| 1.4 | Start container with `UB_LOG_LEVEL=debug`. Make a request. Then restart with `UB_LOG_LEVEL=info` | At debug level: debug-level entries visible. At info level: no debug-level entries |

## Phase 2: CalDAV Discovery and Collection

| Step | Action | Expected |
|------|--------|----------|
| 2.1 | `curl -u admin:pass -v http://localhost:8443/.well-known/caldav` | HTTP 301 with `Location: /caldav/` |
| 2.2 | `curl -u admin:pass -X PROPFIND -H "Depth: 0" http://localhost:8443/caldav/tasks/` | HTTP 207. XML contains `<comp name="VTODO"/>` in `supported-calendar-component-set` |
| 2.3 | Verify collection name in PROPFIND response | `<displayname>` matches configured `UB_CALDAV_COLLECTION_NAME` (default: "Supernote Tasks") |

## Phase 3: Task CRUD via CalDAV

| Step | Action | Expected |
|------|--------|----------|
| 3.1 | PUT a VTODO with `SUMMARY:Manual Test Task`, `STATUS:NEEDS-ACTION`, `UID:manual-test-1` | HTTP 201/204. DB shows `title='Manual Test Task'`, `status='needsAction'` |
| 3.2 | GET `/caldav/tasks/manual-test-1.ics` | HTTP 200. Valid iCalendar with `SUMMARY:Manual Test Task` and `STATUS:NEEDS-ACTION` |
| 3.3 | PUT updated VTODO with `STATUS:COMPLETED` for same UID | HTTP 2xx. DB shows `status='completed'`, `last_modified` updated |
| 3.4 | PUT a VTODO with `DESCRIPTION:Some notes` and `PRIORITY:3` | DB `detail='Some notes'`, `importance='3'`. GET returns both properties |
| 3.5 | PUT a VTODO with no DUE property | DB `due_time=0`. GET returns VTODO without DUE line |
| 3.6 | PUT a VEVENT instead of VTODO | HTTP 4xx error. No event created |
| 3.7 | DELETE `/caldav/tasks/manual-test-1.ics` | HTTP 2xx. DB shows `is_deleted='Y'`. LIST omits it |

## Phase 4: Bidirectional Sync (Device Simulation)

| Step | Action | Expected |
|------|--------|----------|
| 4.1 | Create task via CalDAV. Then UPDATE detail/importance directly in MariaDB | GET via CalDAV shows updated DESCRIPTION and PRIORITY |
| 4.2 | Create a task on the Supernote device. Wait for sync | Task appears in CalDAV LIST with correct fields |

## Phase 5: Authentication

| Step | Action | Expected |
|------|--------|----------|
| 5.1 | `curl -v http://localhost:8443/caldav/tasks/` (no credentials) | HTTP 401 with `WWW-Authenticate: Basic realm="UltraBridge"`. No credential hints |
| 5.2 | `curl -v -u wronguser:pass http://localhost:8443/caldav/tasks/` | HTTP 401 |
| 5.3 | `curl -v -u admin:wrongpass http://localhost:8443/caldav/tasks/` | HTTP 401 |
| 5.4 | `curl -v -u admin:correctpass http://localhost:8443/caldav/tasks/` | HTTP 207 success |

## Phase 6: Web UI

| Step | Action | Expected |
|------|--------|----------|
| 6.1 | Open `http://localhost:8443/` in browser | Task list page loads with current tasks |
| 6.2 | Create task via form: title "Browser Test Task", submit | Task appears in list with status "Needs Action" |
| 6.3 | Click "Complete" on "Browser Test Task" | Status changes to "Completed" |
| 6.4 | Switch to Logs tab | Live log entries appear via WebSocket |

## Phase 7: Sync Notification

| Step | Action | Expected |
|------|--------|----------|
| 7.1 | With supernote-service running, create task via CalDAV/web UI | STARTSYNC message sent. Device syncs shortly after |
| 7.2 | Stop supernote-service. Create task via CalDAV | Task creation succeeds. Warning logged about failed sync notification |

## End-to-End: CalDAV Client Integration

| Step | Action | Expected |
|------|--------|----------|
| E2E.1 | In Thunderbird/GNOME Calendar, add CalDAV account at `http://<host>:8443/.well-known/caldav` | Client discovers "Supernote Tasks" collection |
| E2E.2 | Create task "Thunderbird Task" with due date in client | Appears in web UI and DB |
| E2E.3 | Mark task completed on device (or via DB edit) | Client shows completed after sync |
| E2E.4 | Delete task in client | DB shows `is_deleted='Y'` |

## End-to-End: Docker Compose Full Stack

| Step | Action | Expected |
|------|--------|----------|
| DC.1 | `docker compose up -d` | All 5 containers start. `docker compose ps` shows healthy |
| DC.2 | Check UltraBridge logs | Successful DB connection and user ID discovery |
| DC.3 | Create task via web UI, verify on device. Create on device, verify in CalDAV client | Bidirectional flow works |

## Human Verification Required

| Item | Why Manual | Steps |
|------|-----------|-------|
| Visual layout | Automated tests verify content not visual correctness | Open `/` in browser, verify readability |
| CalDAV client compat | Real clients have discovery/caching behavior | E2E.1-E2E.4 |
| Device sync | Requires real Supernote hardware | Phase 7 steps |
| Log readability | Human judgment needed | Generate 20+ requests, review logs |

## Traceability

| AC | Automated Test | Manual Step |
|----|----------------|-------------|
| AC1.1 | TestIntegrationHealthCheck | 1.1 |
| AC1.2 | TestIntegrationStartupFailure | 1.2 |
| AC1.3 | TestRequestIDMiddleware, TestSetupJSONFormat, TestLevelFiltering | 1.3, 1.4 |
| AC2.1 | TestIntegrationWellKnownRedirect | 2.1 |
| AC2.2 | TestListCalendarsSupportedComponents | 2.2 |
| AC2.3 | TestListCalendarsName | 2.3 |
| AC2.4 | TestPutCalendarObjectCreateAndUpdateCTag | 3.3 |
| AC2.5 | TestPutCalendarObjectRejectVEVENT | 3.6 |
| AC3.1-3.4 | Multiple backend/mapping tests | 3.1-3.3 |
| AC3.5 | TestNotifySuccess | 7.1 |
| AC3.6 | TestNotifyNotConnected | 7.2 |
| AC3.7 | TestIntegrationRoundTrip | 4.1 |
| AC3.8 | TestIntegrationDescriptionAndPriority | 3.4 |
| AC3.9-3.11 | TestTaskToVTODO variants | 3.5 |
| AC4.1-4.4 | Handler + broadcaster tests | 6.1-6.4 |
| AC5.1-5.2 | Auth tests | 5.1-5.4 |
