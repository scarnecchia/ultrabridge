# NoteBridge Phase 7: Web UI + Polish

**Goal:** Web interface for file browsing, job management, and search. Production-ready error handling and rate limiting.

**Architecture:** Transfer web UI, logging, and auth packages from UltraBridge. Wire to NoteBridge's interfaces (taskstore, notestore, search, processor, event bus). Add rate limiting on auth endpoints. Add health endpoint.

**Tech Stack:** Go 1.24, gorilla/websocket (log streaming), lumberjack (log rotation), bcrypt, html/template

**Scope:** Phase 7 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC8: Web UI
- **AC8.1 Success:** File browser shows files from blob storage
- **AC8.2 Success:** Job status shows pending/in-progress/done counts from processor
- **AC8.3 Success:** Search returns FTS5 results with snippets
- **AC8.4 Success:** Task list view shows tasks from syncdb

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: Port Logging and Auth

<!-- START_TASK_1 -->
### Task 1: Port logging package

**Files:**
- Create: `/home/sysop/src/notebridge/internal/logging/logging.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/logging/broadcaster.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/logging/requestid.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/logging/syslog.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/logging/syslog_windows.go` (from UB, unchanged)

**Implementation:**

Copy all logging files from UltraBridge unchanged. Change module imports.

The logging package provides:
- `Setup(Config)` — creates multi-writer (stdout + optional file rotation + optional syslog), returns `*slog.Logger`
- `LogBroadcaster` — ring buffer (100 entries) + subscriber channels for WebSocket streaming
- `BroadcastingHandler` — wraps slog.Handler to broadcast all log records
- `RequestID` middleware — generates per-request IDs, logs method/path/status/duration

Add dependencies:
```bash
go get github.com/gorilla/websocket
go get gopkg.in/natefinish/lumberjack.v2
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port logging package from UltraBridge`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Port auth middleware

**Files:**
- Create: `/home/sysop/src/notebridge/internal/auth/auth.go` (from UB, unchanged)

**Implementation:**

Copy from UltraBridge unchanged. Change module imports.

Single-user Basic Auth middleware with bcrypt password hash:
- `New(username, passwordHash string) *Middleware`
- `Wrap(next http.Handler) http.Handler`
- Constant-time username comparison, bcrypt password verification
- WWW-Authenticate header with `Basic realm="NoteBridge"`

This protects the web UI and CalDAV. The device sync API uses its own JWT auth (Phase 1).

Add dependency:
```bash
go get golang.org/x/crypto
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port Basic Auth middleware`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Add rate limiting to auth endpoints

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/ratelimit.go`
- Create: `/home/sysop/src/notebridge/internal/sync/ratelimit_test.go`

**Implementation:**

Simple in-memory rate limiter for device auth endpoints (challenge + login verify). Prevents brute-force attacks. Adapted from allenporter's pattern.

`RateLimiter` struct:
- Per-IP tracking: map[string]*ipState (attempt count, first attempt time, blocked until)
- Per-account tracking: map[string]*accountState (same structure)
- Configurable limits:
  - Per IP: 20 requests per 15 minutes
  - Per account: already handled by account lockout (Phase 1, AC1.5: 6 failures → 5min lock)

`RateLimitMiddleware(limiter *RateLimiter) func(http.Handler) http.Handler`:
- Extract client IP from request (X-Forwarded-For or RemoteAddr)
- Check if IP is currently blocked → return 429 Too Many Requests
- Increment counter
- If counter exceeds limit within window → block IP for 15 minutes
- Periodic cleanup of expired entries (background goroutine, every 5 minutes)

Apply only to auth endpoints:
- `POST /api/user/login/challenge`
- `POST /api/user/login/verify`

**Testing (`ratelimit_test.go`):**

Unit tests for the rate limiter:
- Under-limit requests pass through (no 429)
- 21st request from same IP within 15 minutes returns 429
- Blocked IP unblocks after 15-minute window expires
- Different IPs tracked independently
- Cleanup goroutine removes expired entries

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestRateLimit
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add rate limiting for auth endpoints`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-6) -->
## Subcomponent B: Web UI Handlers

<!-- START_TASK_4 -->
### Task 4: Port web handler and templates

**Files:**
- Create: `/home/sysop/src/notebridge/internal/web/handler.go` (from UB, adapted)
- Create: `/home/sysop/src/notebridge/internal/web/logstream.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/web/templates/index.html` (from UB, minor branding update)

**Implementation:**

**handler.go** — Copy from UltraBridge. Change module imports. The handler's `NewHandler` function accepts interfaces that NoteBridge already implements:
- `TaskStore` — from `internal/taskstore` (rewritten in Phase 6)
- `SyncNotifier` — event bus notifier adapter (from Phase 6)
- `NoteStore` — from `internal/notestore` (ported in Phase 4)
- `SearchIndex` — from `internal/search` (ported in Phase 4)
- `Processor` — from `internal/processor` (ported in Phase 4)
- `FileScanner` — pipeline's `ScanNow` method (ported in Phase 4)
- `*slog.Logger` and `*logging.LogBroadcaster` (ported in Task 1)

The Files tab needs one adaptation: file paths in UltraBridge point to the SPC shared mount. In NoteBridge, they point to blob storage. The `handleFiles` handler uses `noteStore.List(ctx, relPath)` which already abstracts this — as long as notestore is configured with the correct root path, no handler code changes needed.

**logstream.go** — Copy unchanged. WebSocket endpoint at `/ws/logs` with level filtering.

**templates/index.html** — Copy from UltraBridge. Update branding:
- Title: "NoteBridge" instead of "UltraBridge"
- Header/footer references

All routes remain the same:
- `GET /` — Tasks tab
- `POST /tasks`, `/tasks/{id}/complete`, `/tasks/bulk` — Task CRUD
- `GET /files`, `/files/status`, `/files/history`, `/files/content`, `/files/render`, `/files/scan` — Files tab
- `POST /files/queue`, `/files/skip`, `/files/unskip`, `/files/force` — File actions
- `GET /search` — Search tab
- `POST /processor/start`, `/processor/stop` — Processor C&C
- `GET /ws/logs` — Log streaming

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port web UI handler and templates`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Wire web UI to main.go

**Files:**
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go` — add web server, auth, logging

**Implementation:**

Update main.go to wire the complete web server on port 8443:

1. Replace basic slog setup with full logging package:
   ```go
   broadcaster := logging.NewLogBroadcaster()
   logger := logging.Setup(logging.Config{
       Level:     cfg.LogLevel,
       Format:    cfg.LogFormat,
       File:      cfg.LogFile,
       // ... file rotation, syslog config
   })
   logger = slog.New(logging.NewBroadcastingHandler(logger.Handler(), broadcaster))
   slog.SetDefault(logger)
   ```

2. Create web auth middleware:
   ```go
   webAuth := auth.New(cfg.WebUsername, cfg.WebPasswordHash)
   ```

3. Create web handler:
   ```go
   webHandler := web.NewHandler(taskStore, caldavNotifier, noteStore,
       searchIndex, proc, pipe, logger, broadcaster)
   ```

4. Set up web server mux:
   ```go
   webMux := http.NewServeMux()
   webMux.Handle("/caldav/", webAuth.Wrap(caldavHandler))
   webMux.Handle("/", webAuth.Wrap(webHandler))
   webMux.HandleFunc("GET /health", handleHealth)
   ```

5. Start web server:
   ```go
   webServer := &http.Server{Addr: cfg.WebListenAddr, Handler: webMux}
   go func() {
       logger.Info("web server starting", "addr", cfg.WebListenAddr)
       if err := webServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
           logger.Error("web server failed", "error", err)
       }
   }()
   ```

6. Graceful shutdown for both servers (sync + web):
   ```go
   quit := make(chan os.Signal, 1)
   signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
   <-quit

   ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
   defer cancel()
   syncServer.Shutdown(ctx)
   webServer.Shutdown(ctx)
   pipe.Close()
   proc.Stop()
   ```

Add config fields:
- `WebUsername` (NB_WEB_USERNAME, default: "admin")
- `WebPasswordHash` (NB_WEB_PASSWORD_HASH, required — bcrypt hash, generated by install.sh)
- `LogFile` (NB_LOG_FILE, default: empty)
- `LogFileMaxMB`, `LogFileMaxAge`, `LogFileMaxBackup` (with UB-matching defaults)
- `LogSyslogAddr` (NB_LOG_SYSLOG_ADDR, default: empty)

Update install.sh to:
- Prompt for web UI password (separate from device sync password)
- Generate bcrypt hash: `notebridge hash-password <password>`
- Store as `NB_WEB_PASSWORD_HASH` in .env

Add `hash-password` subcommand to main.go (same pattern as UltraBridge):
```go
if len(os.Args) >= 3 && os.Args[1] == "hash-password" {
    hash, _ := bcrypt.GenerateFromPassword([]byte(os.Args[2]), 10)
    fmt.Println(string(hash))
    return
}
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire web UI, logging, and auth to main.go`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Web UI integration tests

**Verifies:** AC8.1 (file browser), AC8.2 (job status), AC8.3 (FTS5 search), AC8.4 (task list view)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/web/handler_test.go`

**Testing:**

Integration tests using `httptest.NewServer` with the full web handler. Use in-memory SQLite for all services. Tests authenticate with Basic Auth.

Test helper: `setupWebTestServer(t) (*httptest.Server, *syncdb.Store)` — creates in-memory SQLite, all services, web handler with auth. Returns test server and store for seeding data.

**Test cases:**

- AC8.1 file browser:
  1. Seed notestore with 3 test files (different types)
  2. GET /files → 200, response contains file names and types
  3. GET /files?path=Note → shows files in subdirectory

- AC8.2 job status:
  1. Seed processor with jobs in various statuses (2 pending, 1 in_progress, 3 done)
  2. GET /files/status → JSON with correct counts
  3. POST /processor/start → 200 (processor running)
  4. POST /processor/stop → 200 (processor stopped)

- AC8.3 FTS5 search:
  1. Index test content via search.IndexPage ("handwritten meeting notes about project planning")
  2. GET /search?q=meeting → 200, response contains matching result with snippet
  3. GET /search?q=nonexistent → 200, empty results

- AC8.4 task list:
  1. Create 3 tasks via taskstore (different statuses, due dates)
  2. GET / → 200, response contains all 3 task titles
  3. POST /tasks/{id}/complete → redirects, task status updated

- Health endpoint:
  1. GET /health (no auth) → 200, body contains "ok"

- Auth rejection:
  1. GET / without Basic Auth → 401 with WWW-Authenticate header
  2. GET / with wrong credentials → 401

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/web/
```

Expected: All tests pass.

**Commit:** `test: add web UI integration tests`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_B -->
