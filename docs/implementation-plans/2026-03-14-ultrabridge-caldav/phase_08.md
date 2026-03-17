# UltraBridge CalDAV — Phase 8: Integration Testing & Deployment

**Goal:** End-to-end verification with real CalDAV clients and Supernote device. Finalized Docker deployment and documentation.

**Architecture:** Integration tests run against the real Supernote Private Cloud MariaDB. Docker Compose override adds ultrabridge to the existing stack. README documents setup, CalDAV client configuration, and troubleshooting.

**Tech Stack:** Go 1.22, Docker Compose, `curl` for CalDAV testing, `database/sql` for test setup/teardown

**Scope:** 8 phases from original design (phase 8 of 8)

**Codebase verified:** 2026-03-17

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC1: Go service running as Docker container
- **ultrabridge-caldav.AC1.1 Success:** Container starts, connects to MariaDB, auto-discovers user ID, and responds to health check
- **ultrabridge-caldav.AC1.2 Failure:** Container exits with clear error message if MariaDB is unreachable or `.dbenv` is missing/malformed

### ultrabridge-caldav.AC3: Bidirectional task sync
- **ultrabridge-caldav.AC3.7 Success:** Full round-trip: create task on CalDAV → appears on device → modify on device → CalDAV client sees update on next sync
- **ultrabridge-caldav.AC3.8 Success:** Tier 2 fields (DESCRIPTION, PRIORITY) are stored in DB and round-trip through CalDAV

---

<!-- START_TASK_1 -->
### Task 1: Integration test infrastructure

**Files:**
- Create: `tests/integration_test.go`
- Create: `tests/testutil_test.go`

**Implementation:**

Integration tests require access to the Supernote Private Cloud MariaDB. They use a build tag `//go:build integration` so they don't run during normal `go test`.

`testutil_test.go` provides helpers:
- `connectTestDB()` — connects to MariaDB using `.dbenv` from the Supernote stack (at `/mnt/supernote/.dbenv` or via `TEST_DBENV_PATH` env)
- `createTestTask(db, userID, title)` — inserts a task directly into the DB for setup
- `cleanupTestTasks(db, prefix)` — deletes tasks with titles starting with a test prefix
- `discoverTestUserID(db)` — discovers user ID

`integration_test.go` provides end-to-end CalDAV tests. Each test:
1. Creates a task store and CalDAV backend
2. Starts an `httptest.Server` with the full handler chain (auth + CalDAV + health)
3. Uses `net/http` to send CalDAV requests (PROPFIND, PUT, GET, DELETE) against the test server
4. Verifies responses and DB state

Tests are tagged with `//go:build integration` and skipped if DB is unreachable.

**Verification:**

```bash
go build -tags integration ./tests/
```

Expected: Compiles.

**Commit:** `feat: add integration test infrastructure`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Integration tests

**Verifies:** ultrabridge-caldav.AC1.1, ultrabridge-caldav.AC1.2, ultrabridge-caldav.AC3.7, ultrabridge-caldav.AC3.8

**Files:**
- Modify: `tests/integration_test.go`

**Testing:**

Tests must verify each AC:
- **ultrabridge-caldav.AC1.1:** Start the full service against real DB. Verify `/health` returns 200. Verify user ID was discovered (logged or available via store).
- **ultrabridge-caldav.AC1.2:** Start with invalid DSN (wrong password). Verify startup fails with clear error message containing "database" or "connection" context.
- **ultrabridge-caldav.AC3.7:** Full round-trip:
  1. PUT a VTODO via CalDAV → verify task appears in DB with correct fields
  2. Modify the task directly in DB (simulating device change) → PROPFIND/GET via CalDAV → verify updated fields appear
  3. Clean up test task
- **ultrabridge-caldav.AC3.8:** PUT a VTODO with DESCRIPTION and PRIORITY → verify `detail` and `importance` columns in DB → GET the task via CalDAV → verify DESCRIPTION and PRIORITY round-trip.

Additional tests:
- `/.well-known/caldav` returns 301 redirect to `/caldav/`
- PROPFIND on `/caldav/tasks/` returns collection with `VTODO` component set
- PUT with VEVENT returns error (not 2xx)
- DELETE marks task as `is_deleted='Y'` in DB
- Auth: request without credentials returns 401

Use `//go:build integration` tag. Use test prefix like `__ubtest_` for task titles so cleanup can find them.

**Verification:**

```bash
# Run integration tests (requires running Supernote Private Cloud stack)
TEST_DBENV_PATH=/mnt/supernote/.dbenv go test -tags integration ./tests/ -v
```

Expected: All tests pass.

**Commit:** `test: add CalDAV integration tests`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Finalize Docker Compose and deployment config

**Files:**
- Verify: `Dockerfile` (from Phase 1)
- Verify: `docker-compose.override.yml` (from Phase 1)
- Create: `.ultrabridge.env.example`

**Implementation:**

Verify the Dockerfile builds cleanly with all dependencies from Phases 1-7.

Create `.ultrabridge.env.example` with documented defaults:

```bash
# UltraBridge Configuration
# Copy to .ultrabridge.env and customize

# Auth (REQUIRED)
UB_USERNAME=admin
UB_PASSWORD_HASH=$2a$10$... # Generate with: htpasswd -nbBC 10 "" "yourpassword" | cut -d: -f2

# CalDAV
UB_CALDAV_COLLECTION_NAME=Supernote Tasks
UB_DUE_TIME_MODE=preserve

# Server
UB_LISTEN_ADDR=:8443
UB_WEB_ENABLED=true

# Logging
UB_LOG_LEVEL=info
UB_LOG_FORMAT=json
# UB_LOG_FILE=/var/log/ultrabridge/ultrabridge.log
# UB_LOG_SYSLOG_ADDR=udp://graylog:1514

# Database (auto-read from .dbenv, rarely need to override)
# UB_DB_HOST=mariadb
# UB_DB_PORT=3306
# UB_SUPERNOTE_DBENV_PATH=/run/secrets/dbenv

# Socket.io (auto-discovered, rarely need to override)
# UB_SOCKETIO_URL=ws://supernote-service:8080/socket.io/
```

**Verification:**

```bash
cd /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
docker build -t ultrabridge:dev .
```

Expected: Image builds successfully with all code from Phases 1-7.

**Commit:** `feat: add example env file and finalize deployment config`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: README

**Files:**
- Create: `README.md`

**Implementation:**

Document:
1. **What UltraBridge is** — one paragraph summary
2. **Prerequisites** — Supernote Private Cloud running, Docker
3. **Quick Start** — copy `.ultrabridge.env.example`, generate password hash, add `docker-compose.override.yml` to Supernote stack, `docker compose up -d`
4. **CalDAV Client Setup** — how to add the CalDAV account in DAVx5/OpenTasks, 2Do, GNOME Evolution (server URL, credentials)
5. **Configuration** — table of all `UB_*` environment variables with defaults and descriptions
6. **Architecture** — brief diagram of how UltraBridge sits alongside the Supernote stack
7. **Development** — how to build, run tests, run integration tests
8. **Known Limitations** — Tier 3 fields dropped, single-user only, no TLS (use reverse proxy)

Keep it concise. This is a personal project, not a public product.

**Verification:**

Verify README renders correctly (visual check).

**Commit:** `docs: add README with setup and CalDAV client configuration`

<!-- END_TASK_4 -->
