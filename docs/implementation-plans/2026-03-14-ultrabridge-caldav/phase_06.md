# UltraBridge CalDAV — Phase 6: Structured Logging

**Goal:** Comprehensive multi-target structured logging with request ID tracing.

**Architecture:** `log/slog` with configurable handler (JSON or text). Multi-writer output: stdout (always), rotating file (via lumberjack, optional), syslog (optional). HTTP middleware injects a request ID (UUID) into context; all downstream log calls include it. Previous phases used `log.Printf` — this phase replaces those with `slog` calls and wires structured logging throughout.

**Tech Stack:** Go 1.22, `log/slog`, `gopkg.in/natefinch/lumberjack.v2`, `log/syslog`

**Scope:** 8 phases from original design (phase 6 of 8)

**Codebase verified:** 2026-03-17

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC1: Go service running as Docker container
- **ultrabridge-caldav.AC1.3 Success:** All operations produce structured log entries with request IDs, configurable level/format/targets

---

<!-- START_TASK_1 -->
### Task 1: Logging setup

**Files:**
- Create: `internal/logging/logging.go`

**Implementation:**

Package `logging` configures a `slog.Logger` based on config. Creates a multi-writer that fans out to stdout + optional file + optional syslog. Returns the configured logger.

Note: The `log/syslog` package is not available on Windows. Since the deployment target is Docker/Alpine Linux, this is acceptable. The syslog-related code (`dialSyslog` function) should be placed in a separate file with a build tag `//go:build !windows` to prevent compilation errors on Windows development machines. A stub file with `//go:build windows` can return nil.

```go
package logging

import (
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Note: dialSyslog is in syslog.go with //go:build !windows tag

type Config struct {
	Level         string // "debug", "info", "warn", "error"
	Format        string // "json" or "text"
	File          string // path, empty = no file logging
	FileMaxMB     int
	FileMaxAge    int
	FileMaxBackup int
	SyslogAddr    string // e.g., "udp://graylog:1514", empty = no syslog
}

func Setup(cfg Config) *slog.Logger {
	var writers []io.Writer
	writers = append(writers, os.Stdout)

	if cfg.File != "" {
		writers = append(writers, &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.FileMaxMB,
			MaxAge:     cfg.FileMaxAge,
			MaxBackups: cfg.FileMaxBackup,
		})
	}

	if cfg.SyslogAddr != "" {
		if w := dialSyslog(cfg.SyslogAddr); w != nil {
			writers = append(writers, w)
		}
	}

	w := io.MultiWriter(writers...)

	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func dialSyslog(addr string) io.Writer {
	u, err := url.Parse(addr)
	if err != nil {
		slog.Warn("invalid syslog address", "addr", addr, "error", err)
		return nil
	}
	w, err := syslog.Dial(u.Scheme, u.Host, syslog.LOG_INFO|syslog.LOG_DAEMON, "ultrabridge")
	if err != nil {
		slog.Warn("syslog connect failed", "addr", addr, "error", err)
		return nil
	}
	return w
}
```

**Verification:**

```bash
go build ./internal/logging/
```

Expected: Compiles.

**Commit:** `feat: add structured logging with multi-target output`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Request ID middleware

**Files:**
- Create: `internal/logging/requestid.go`

**Implementation:**

HTTP middleware that generates a UUID request ID for each request, stores it in context, and adds it to log output. Also logs request method, path, status, and duration.

```go
package logging

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// RequestIDFromContext extracts the request ID from context.
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// RequestID returns middleware that injects a request ID and logs requests.
func RequestID(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := generateID()
			ctx := context.WithValue(r.Context(), requestIDKey, id)
			r = r.WithContext(ctx)

			rw := &responseWriter{ResponseWriter: w, status: 200}
			start := time.Now()

			next.ServeHTTP(rw, r)

			logger.Info("request",
				"request_id", id,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
```

**Verification:**

```bash
go build ./internal/logging/
```

Expected: Compiles.

**Commit:** `feat: add request ID middleware for structured logging`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Wire logging into main.go and all components

**Files:**
- Modify: `cmd/ultrabridge/main.go`

**Implementation:**

Replace `log.Printf` calls with structured `slog` calls. Initialize logging first, before DB connection. Wrap the HTTP mux with request ID middleware.

In `main.go`:
1. Call `logging.Setup()` with config values first
2. Use returned `*slog.Logger` for all startup logs
3. Pass logger to `sync.NewNotifier`
4. Wrap mux with `logging.RequestID(logger)`

```go
	logger := logging.Setup(logging.Config{
		Level:         cfg.LogLevel,
		Format:        cfg.LogFormat,
		File:          cfg.LogFile,
		FileMaxMB:     cfg.LogFileMaxMB,
		FileMaxAge:    cfg.LogFileMaxAge,
		FileMaxBackup: cfg.LogFileMaxBackup,
		SyslogAddr:    cfg.LogSyslogAddr,
	})

	// ... DB setup, store creation, backend creation ...

	handler := logging.RequestID(logger)(mux)
	logger.Info("ultrabridge starting", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, handler); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
```

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles. No more `log.Printf` calls in main.go.

**Commit:** `feat: wire structured logging into startup and request pipeline`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Logging tests

**Verifies:** ultrabridge-caldav.AC1.3

**Files:**
- Create: `internal/logging/logging_test.go`

**Testing:**

Tests must verify:
- **ultrabridge-caldav.AC1.3:** Structured log entries contain request ID, method, path, status, duration. Log level filtering works (debug messages excluded at info level). JSON format produces valid JSON. Text format produces key=value pairs.

Specific tests:
- `Setup` with JSON format: write a log entry, verify output is valid JSON with expected fields
- `Setup` with text format: write a log entry, verify output contains key=value format
- Level filtering: at `info` level, `Debug` messages are suppressed, `Info`/`Warn`/`Error` pass through
- `RequestID` middleware: make a test HTTP request, verify log output includes `request_id`, `method`, `path`, `status`, `duration_ms`
- `RequestIDFromContext`: verify extraction from context

Use `bytes.Buffer` as writer for capturing log output. Use `httptest` for middleware testing.

**Verification:**

```bash
go test ./internal/logging/ -v
```

Expected: All tests pass.

**Commit:** `test: add logging and request ID middleware tests`

<!-- END_TASK_4 -->
