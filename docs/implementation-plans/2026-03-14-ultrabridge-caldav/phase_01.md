# UltraBridge CalDAV — Phase 1: Project Scaffolding

**Goal:** Initialize Go project with module, dependencies, directory structure, Dockerfile, and a health-check endpoint.

**Architecture:** Single Go binary with config loading from environment variables. Multi-stage Docker build. Health-check endpoint at `GET /health` for container readiness.

**Tech Stack:** Go 1.22, Docker multi-stage build, `net/http`

**Scope:** 8 phases from original design (phase 1 of 8)

**Codebase verified:** 2026-03-17 (greenfield project — worktree contains only docs/ and .gitignore)

---

## Acceptance Criteria Coverage

**Verifies: None** — this is an infrastructure phase. Verification is operational (build succeeds, container starts, health check responds).

---

<!-- START_TASK_1 -->
### Task 1: Initialize Go module and directory structure

**Files:**
- Create: `go.mod`
- Create: `cmd/ultrabridge/main.go`
- Create: `internal/config/config.go`

**Step 1: Create go.mod**

```go
module github.com/sysop/ultrabridge

go 1.22.2

require (
	github.com/emersion/go-webdav v0.7.0
	github.com/go-sql-driver/mysql v1.9.3
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
)
```

Note: Socket.io dependency will be added in Phase 5 after evaluating Go 1.22 compatibility.

**Step 2: Create directory structure**

```
cmd/ultrabridge/main.go
internal/config/config.go
```

**Step 3: Create `internal/config/config.go`**

Configuration struct that reads from environment variables with defaults. Also reads `.dbenv` file for database credentials.

The `.dbenv` file format (from the Supernote Private Cloud installer) is:
```
MYSQL_ROOT_PASSWORD=xxx
MYSQL_DATABASE=supernotedb
MYSQL_USER=xxx
MYSQL_PASSWORD=xxx
```

```go
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	// Database (read from .dbenv)
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string

	// Auth
	Username     string
	PasswordHash string

	// CalDAV
	CalDAVCollectionName string
	DueTimeMode          string // "preserve" or "date_only"

	// Logging
	LogLevel         string
	LogFormat        string
	LogFile          string
	LogFileMaxMB     int
	LogFileMaxAge    int
	LogFileMaxBackup int
	LogSyslogAddr    string

	// Server
	ListenAddr string
	WebEnabled bool

	// Socket.io
	SocketIOURL string

	// Paths
	DBEnvPath string
}

func Load() (*Config, error) {
	cfg := &Config{
		DBHost:               envOrDefault("UB_DB_HOST", "mariadb"),
		DBPort:               envOrDefault("UB_DB_PORT", "3306"),
		CalDAVCollectionName: envOrDefault("UB_CALDAV_COLLECTION_NAME", "Supernote Tasks"),
		DueTimeMode:          envOrDefault("UB_DUE_TIME_MODE", "preserve"),
		LogLevel:             envOrDefault("UB_LOG_LEVEL", "info"),
		LogFormat:            envOrDefault("UB_LOG_FORMAT", "json"),
		LogFile:              os.Getenv("UB_LOG_FILE"),
		LogFileMaxMB:         envIntOrDefault("UB_LOG_FILE_MAX_MB", 50),
		LogFileMaxAge:        envIntOrDefault("UB_LOG_FILE_MAX_AGE_DAYS", 30),
		LogFileMaxBackup:     envIntOrDefault("UB_LOG_FILE_MAX_BACKUPS", 5),
		LogSyslogAddr:        os.Getenv("UB_LOG_SYSLOG_ADDR"),
		ListenAddr:           envOrDefault("UB_LISTEN_ADDR", ":8443"),
		WebEnabled:           envOrDefault("UB_WEB_ENABLED", "true") == "true",
		SocketIOURL:          envOrDefault("UB_SOCKETIO_URL", "ws://supernote-service:8080/socket.io/"),
		Username:             os.Getenv("UB_USERNAME"),
		PasswordHash:         os.Getenv("UB_PASSWORD_HASH"),
		DBEnvPath:            envOrDefault("UB_SUPERNOTE_DBENV_PATH", "/run/secrets/dbenv"),
	}

	if err := cfg.loadDBEnv(); err != nil {
		return nil, fmt.Errorf("loading .dbenv: %w", err)
	}

	return cfg, nil
}

func (c *Config) loadDBEnv() error {
	f, err := os.Open(c.DBEnvPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", c.DBEnvPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "MYSQL_DATABASE":
			c.DBName = val
		case "MYSQL_USER":
			c.DBUser = val
		case "MYSQL_PASSWORD":
			c.DBPassword = val
		}
	}
	return scanner.Err()
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}
```

**Step 4: Create `cmd/ultrabridge/main.go`**

Minimal entry point that loads config and starts HTTP server with `/health` endpoint.

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/sysop/ultrabridge/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("ultrabridge starting on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "ultrabridge: %v\n", err)
		os.Exit(1)
	}
}
```

**Step 5: Verify build**

```bash
cd /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
go mod tidy
go build ./cmd/ultrabridge/
```

Expected: Build succeeds, produces `ultrabridge` binary. `go mod tidy` downloads dependencies and updates `go.sum`.

**Step 6: Commit**

```bash
git add go.mod go.sum cmd/ internal/
git commit -m "feat: initialize Go project with config and health endpoint"
```
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Dockerfile and Docker Compose override

**Files:**
- Create: `Dockerfile`
- Create: `docker-compose.override.yml`

**Step 1: Create `Dockerfile`**

Multi-stage build: compile in Go builder image, copy binary to minimal runtime image.

```dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ultrabridge ./cmd/ultrabridge/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /ultrabridge /usr/local/bin/ultrabridge

EXPOSE 8443
ENTRYPOINT ["ultrabridge"]
```

**Step 2: Create `docker-compose.override.yml`**

This file is placed in `/mnt/supernote/` (the Supernote Private Cloud directory) and adds ultrabridge to the existing stack. The build context points to the ultrabridge source.

```yaml
services:
  ultrabridge:
    build:
      context: /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
      dockerfile: Dockerfile
    container_name: ultrabridge
    ports:
      - "8443:8443"
    env_file:
      - .ultrabridge.env
    volumes:
      - ./sndata/logs/ultrabridge:/var/log/ultrabridge
      - ./.dbenv:/run/secrets/dbenv:ro
    depends_on:
      - mariadb
    restart: unless-stopped
```

Note: During development, the build context will be the worktree. For production, this would reference the built image instead.

**Step 3: Verify Docker build**

```bash
cd /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav
docker build -t ultrabridge:dev .
```

Expected: Image builds successfully.

**Step 4: Verify container starts (without DB)**

```bash
# Create a minimal .dbenv for testing
echo -e "MYSQL_DATABASE=supernotedb\nMYSQL_USER=test\nMYSQL_PASSWORD=test" > /tmp/test-dbenv

docker run --rm -e UB_SUPERNOTE_DBENV_PATH=/run/secrets/dbenv \
  -v /tmp/test-dbenv:/run/secrets/dbenv:ro \
  -p 18443:8443 \
  ultrabridge:dev &

sleep 2
curl -s http://localhost:18443/health
# Expected: {"status":"ok"}

docker stop $(docker ps -q --filter ancestor=ultrabridge:dev)
rm /tmp/test-dbenv
```

**Step 5: Commit**

```bash
git add Dockerfile docker-compose.override.yml
git commit -m "feat: add Dockerfile and Docker Compose override"
```
<!-- END_TASK_2 -->
