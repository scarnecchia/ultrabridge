# NoteBridge Phase 1: Project Skeleton + Auth

**Goal:** New Go repo with build/deploy infrastructure and working device authentication.

**Architecture:** Single Go binary, multi-stage Docker build (CGO_ENABLED=0), all-SQLite storage. Challenge-response auth ported from opennotecloud with JWT tokens and signed URL nonces.

**Tech Stack:** Go 1.24, SQLite (modernc.org/sqlite), golang-jwt/jwt/v5, Docker (alpine), SHA-256/MD5 crypto

**Scope:** Phase 1 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC1: Device Authentication
- **AC1.1 Success:** Tablet completes challenge-response flow (random code → SHA256(MD5+code) → JWT token)
- **AC1.2 Success:** JWT token accepted by auth middleware on subsequent requests
- **AC1.3 Failure:** Wrong password hash returns error with code E0019
- **AC1.4 Failure:** Expired/invalid JWT returns error with code E0712
- **AC1.5 Failure:** Account locked after 6 failures in 12 hours, returns E0045
- **AC1.6 Edge:** Expired challenge code (>5min) rejected

### notebridge-spc-replacement.AC9: Deployment
- **AC9.1 Success:** install.sh creates directories, prompts for credentials, starts container
- **AC9.2 Success:** rebuild.sh rebuilds and restarts with health check
- **AC9.3 Success:** Single container, no external dependencies (no MariaDB, Redis, etc.)

---

<!-- START_SUBCOMPONENT_A (tasks 1-4) -->
## Subcomponent A: Go Module + Build Infrastructure

<!-- START_TASK_1 -->
### Task 1: Initialize Go module and project structure

**Files:**
- Create: `/home/sysop/src/notebridge/go.mod`
- Create: `/home/sysop/src/notebridge/cmd/notebridge/main.go` (minimal, exits cleanly)
- Create: `/home/sysop/src/notebridge/.gitignore`

**Step 1: Create project directory and initialize module**

```bash
mkdir -p /home/sysop/src/notebridge/cmd/notebridge
cd /home/sysop/src/notebridge
git init
go mod init github.com/sysop/notebridge
```

**Step 2: Create minimal main.go**

Create `cmd/notebridge/main.go`:
```go
package main

import "fmt"

func main() {
	fmt.Println("notebridge starting")
}
```

**Step 3: Create .gitignore**

```
notebridge
*.db
*.db-journal
*.db-wal
*.db-shm
/data/
```

**Step 4: Verify build**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

Expected: Binary builds without errors.

**Step 5: Commit**

```bash
git -C /home/sysop/src/notebridge add go.mod cmd/ .gitignore
git -C /home/sysop/src/notebridge commit -m "chore: initialize notebridge Go module"
```
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Dockerfile and docker-compose.yml

**Files:**
- Create: `/home/sysop/src/notebridge/Dockerfile`
- Create: `/home/sysop/src/notebridge/docker-compose.yml`

**Step 1: Create Dockerfile**

Multi-stage build matching UltraBridge pattern (CGO_ENABLED=0, alpine runtime). Two exposed ports: 8443 (web UI + CalDAV) and 19071 (device sync API + Socket.IO).

```dockerfile
FROM golang:1.24-alpine AS builder

WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /notebridge ./cmd/notebridge/

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /notebridge /usr/local/bin/notebridge

EXPOSE 8443 19071
ENTRYPOINT ["notebridge"]
```

**Step 2: Create docker-compose.yml**

Standalone single-container compose file (no SPC dependencies). Volumes for database, storage, backups, cache.

```yaml
services:
  notebridge:
    build: .
    container_name: notebridge
    restart: unless-stopped
    ports:
      - "${NB_WEB_PORT:-8443}:8443"
      - "${NB_SYNC_PORT:-19071}:19071"
    volumes:
      - ${NB_DATA_DIR:-/data/notebridge}/notebridge.db:/data/notebridge.db
      - ${NB_DATA_DIR:-/data/notebridge}/storage:/data/storage
      - ${NB_DATA_DIR:-/data/notebridge}/backups:/data/backups
      - ${NB_DATA_DIR:-/data/notebridge}/cache:/data/cache
    environment:
      - NB_DB_PATH=/data/notebridge.db
      - NB_STORAGE_PATH=/data/storage
      - NB_BACKUP_PATH=/data/backups
      - NB_CACHE_PATH=/data/cache
    env_file:
      - .env
```

Note: `go.sum` won't exist until dependencies are added. The Dockerfile's `COPY go.sum` will need the file to exist — it will be created in Task 3 when we add the SQLite dependency.

**Step 3: Commit**

```bash
git -C /home/sysop/src/notebridge add Dockerfile docker-compose.yml
git -C /home/sysop/src/notebridge commit -m "chore: add Dockerfile and docker-compose.yml"
```
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: install.sh and rebuild.sh

**Files:**
- Create: `/home/sysop/src/notebridge/install.sh`
- Create: `/home/sysop/src/notebridge/rebuild.sh`

**Step 1: Create install.sh**

Adapted from UltraBridge's install.sh. Prompts for: data directory, web port, sync port, user email, password. Generates .env file. Creates directory structure. Builds and starts container.

Key differences from UltraBridge:
- No MariaDB/SPC dependency — NoteBridge is standalone
- Password is stored as MD5 hex (matching Supernote device protocol), not bcrypt
- Generates random JWT secret (32-byte hex)
- Creates NB_-prefixed env vars instead of UB_

The install script should:
1. Check Docker is installed
2. Prompt for data directory (default: /data/notebridge)
3. Prompt for web port (default: 8443), sync port (default: 19071)
4. Prompt for user email and password
5. Compute MD5 hex of password (this is what the device sends for auth — the challenge-response protocol uses MD5 password hash)
6. Generate random JWT secret (openssl rand -hex 32)
7. Write .env file with all NB_ vars
8. Create directories (storage, backups, cache)
9. Build and start with docker compose
10. Health check

**Step 2: Create rebuild.sh**

Adapted from UltraBridge's rebuild.sh. Simpler — no SPC compose overlay needed. Just rebuilds and restarts the single container with health check.

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m OK \033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m FAIL \033[0m %s\n' "$*"; exit 1; }

if [[ ! -f "$SCRIPT_DIR/.env" ]]; then
    fail "No .env found. Run install.sh first."
fi

info "Building and restarting NoteBridge..."
sudo docker compose -f "$SCRIPT_DIR/docker-compose.yml" up -d --build --force-recreate notebridge \
    || fail "Build/restart failed"
ok "Container running"

# Read web port from .env or default
WEB_PORT=$(grep -oP '^NB_WEB_PORT=\K.*' "$SCRIPT_DIR/.env" 2>/dev/null || echo "8443")

sleep 2
if curl -sf "http://localhost:${WEB_PORT}/health" >/dev/null 2>&1; then
    ok "Health check passed"
else
    sleep 3
    if curl -sf "http://localhost:${WEB_PORT}/health" >/dev/null 2>&1; then
        ok "Health check passed"
    else
        fail "Health check failed. Run: sudo docker logs notebridge"
    fi
fi

info "Done!"
```

**Step 3: Make scripts executable and commit**

```bash
chmod +x /home/sysop/src/notebridge/install.sh /home/sysop/src/notebridge/rebuild.sh
git -C /home/sysop/src/notebridge add install.sh rebuild.sh
git -C /home/sysop/src/notebridge commit -m "chore: add install.sh and rebuild.sh deployment scripts"
```
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Config loading

**Files:**
- Create: `/home/sysop/src/notebridge/internal/config/config.go`

**Implementation:**

Config struct and `Load()` function following UltraBridge's pattern (env vars with defaults, `envOrDefault` / `envIntOrDefault` helpers). All env vars use `NB_` prefix.

Config fields needed for Phase 1:
- `DBPath` (NB_DB_PATH, default: "/data/notebridge.db") — SQLite database
- `StoragePath` (NB_STORAGE_PATH, default: "/data/storage") — blob storage root
- `BackupPath` (NB_BACKUP_PATH, default: "/data/backups") — pre-injection backups
- `CachePath` (NB_CACHE_PATH, default: "/data/cache") — rendered page cache
- `WebListenAddr` (NB_WEB_LISTEN_ADDR, default: ":8443") — web UI + CalDAV
- `SyncListenAddr` (NB_SYNC_LISTEN_ADDR, default: ":19071") — device sync API
- `LogLevel` (NB_LOG_LEVEL, default: "info")
- `LogFormat` (NB_LOG_FORMAT, default: "json")
- `JWTSecret` (NB_JWT_SECRET, required — generated by install.sh)
- `UserEmail` (NB_USER_EMAIL, required — for single-user setup)
- `UserPasswordHash` (NB_USER_PASSWORD_HASH, required — MD5 hex of password)

`Load()` returns error if required fields (JWTSecret, UserEmail, UserPasswordHash) are missing.

No tests for this task — infrastructure config, verified operationally when main.go loads it.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add config loading with NB_ env vars`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 5-7) -->
## Subcomponent B: SQLite Database + Schema

<!-- START_TASK_5 -->
### Task 5: SQLite database opener and schema migration

**Files:**
- Create: `/home/sysop/src/notebridge/internal/syncdb/schema.go`

**Implementation:**

Single file that:
1. Opens SQLite database with WAL mode, MaxOpenConns=1 (single-writer, matching UltraBridge's notedb pattern)
2. Creates all tables if they don't exist (full schema from opennotecloud's schema.sql, plus UltraBridge's notes pipeline tables, plus `url_nonces` for signed URLs)
3. Uses `modernc.org/sqlite` driver (pure Go, CGO_ENABLED=0 compatible)

The schema must include all tables from the design's Data Model section:

**Auth & Users:**
- `users` — id (INTEGER PRIMARY KEY, Snowflake), email, password_hash (MD5 hex), username, error_count, last_error_at, locked_until
- `equipment` — id (AUTOINCREMENT), equipment_no, user_id, name, status, total_capacity
- `auth_tokens` — key (TEXT PRIMARY KEY), token, user_id, equipment_no, expires_at
- `login_challenges` — account, timestamp, random_code (PRIMARY KEY: account, timestamp)
- `sync_locks` — user_id (PRIMARY KEY), equipment_no, expires_at
- `server_settings` — key/value
- `url_nonces` — nonce (PRIMARY KEY), expires_at

**File Catalog:**
- `files` — id (Snowflake), user_id, directory_id, file_name, inner_name, storage_key, md5, size, is_folder ('Y'/'N'), is_active ('Y'/'N' for soft delete filtering), created_at, updated_at
- `recycle_files` — same columns + deleted_at, original_directory_id
- `chunk_uploads` — upload_id, part_number, total_chunks, chunk_md5, path (PRIMARY KEY: upload_id, part_number)

**Tasks:**
- `schedule_groups` — task_list_id (TEXT PRIMARY KEY), user_id, title, last_modified, create_time
- `schedule_tasks` — task_id (TEXT PRIMARY KEY), user_id, task_list_id, title, detail, status, importance, due_time, completed_time, recurrence, is_reminder_on, links, is_deleted (TEXT NOT NULL DEFAULT 'N'), sort columns (all the sort/planer_sort fields from opennotecloud)

**Digests:**
- `summaries` — id (Snowflake), user_id, unique_identifier, name, description, file_id, parent_unique_identifier, content, data_source, source_path, source_type, tags, md5_hash, metadata, comment fields, handwrite fields, is_summary_group, author, creation_time, last_modified_time

**Notes Pipeline (from UltraBridge):**
- `notes` — path, rel_path, file_type, size_bytes, mtime, sha256, backup_path
- `jobs` — id (INTEGER PRIMARY KEY AUTOINCREMENT), note_path, status, skip_reason, ocr_source, attempts, requeue_after, created_at, updated_at
- `note_content` — note_path, page, title_text, body_text, keywords, source
- `note_fts` — FTS5 virtual table on note_content (title_text, body_text, keywords)
- FTS5 triggers for insert/update/delete synchronization

Create indexes matching opennotecloud:
- `idx_files_user_dir` ON files(user_id, directory_id)
- `idx_summaries_user` ON summaries(user_id, is_summary_group)

`Open(path string) (*sql.DB, error)` function that:
1. Opens database with `modernc.org/sqlite` driver
2. Enables WAL mode: `PRAGMA journal_mode=WAL`
3. Sets `MaxOpenConns(1)`
4. Calls `ensureSchema(db)` to create tables
5. Returns the db handle

Add `modernc.org/sqlite` dependency:
```bash
go get modernc.org/sqlite
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add SQLite schema with all tables`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Snowflake ID generator

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/snowflake.go`

**Implementation:**

Port from opennotecloud's snowflake.go. 64-bit time-ordered ID:
- Epoch: 2020-01-01T00:00:00Z (1577836800000 ms)
- Layout: 1 unused | 41 bits timestamp | 10 bits worker | 12 bits sequence
- Worker ID: 1 (hardcoded for single-instance)
- Mutex-protected for goroutine safety

Key type: `SnowflakeGenerator` struct with `Generate() int64` method.

Also include `SnowflakeID` type (int64) with custom JSON marshaler that serializes as string (matching opennotecloud's response.go pattern — device expects string IDs in JSON).

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add Snowflake ID generator`
<!-- END_TASK_6 -->

<!-- START_TASK_7 -->
### Task 7: Snowflake ID and schema tests

**Verifies:** notebridge-spc-replacement.AC9.3 (SQLite, no external deps)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/snowflake_test.go`
- Create: `/home/sysop/src/notebridge/internal/syncdb/schema_test.go`

**Testing:**

**Snowflake tests:**
- IDs are unique: generate 1000 IDs, verify no duplicates
- IDs are monotonically increasing: generate 100 IDs, verify each > previous
- JSON marshal: SnowflakeID(12345) marshals to `"12345"` (string), unmarshals back to 12345
- Concurrent safety: 10 goroutines each generate 100 IDs, collect all, verify no duplicates

**Schema tests:**
- `Open(":memory:")` succeeds, returns non-nil db
- All tables exist after Open: query `sqlite_master` for each table name
- WAL mode is enabled: `PRAGMA journal_mode` returns "wal"
- Idempotent: calling schema creation twice doesn't error (IF NOT EXISTS)

Follow project testing patterns: Go stdlib `testing` only, table-driven with `t.Run()`, in-memory SQLite (`:memory:`), `t.Helper()` / `t.Cleanup()`.

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ ./internal/syncdb/
```

Expected: All tests pass.

**Commit:** `test: add snowflake and schema tests`
<!-- END_TASK_7 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 8-10) -->
## Subcomponent C: Error Codes + JSON Response Helpers

<!-- START_TASK_8 -->
### Task 8: SPC-compatible error codes

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/errors.go`

**Implementation:**

Define SPC error codes as constants (matching what the Supernote tablet firmware expects). Port from opennotecloud's error patterns.

Key error codes for Phase 1:
- `E0018` — invalid request / missing parameters
- `E0019` — wrong password
- `E0045` — account locked (too many failures)
- `E0078` — sync lock held by another device
- `E0712` — invalid/expired token
- `E9999` — internal server error

Each error code maps to an HTTP status and a human-readable message.

Create `SyncError` type with `Code`, `Message`, `HTTPStatus` fields and an `Error()` method. Create constructor functions: `ErrWrongPassword()`, `ErrAccountLocked()`, `ErrInvalidToken()`, `ErrSyncLocked()`, `ErrBadRequest(msg)`, `ErrInternal(msg)`.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add SPC-compatible error codes`
<!-- END_TASK_8 -->

<!-- START_TASK_9 -->
### Task 9: JSON response helpers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/response.go`

**Implementation:**

Port from opennotecloud's response.go. Standard JSON response format for the device sync API.

Functions:
- `jsonSuccess(w http.ResponseWriter, extra map[string]any)` — writes `{"cd": "000", ...extra}` with 200 status. "cd": "000" is the SPC success code.
- `jsonError(w http.ResponseWriter, err *SyncError)` — writes `{"cd": err.Code, "msg": err.Message}` with err.HTTPStatus
- `parseJSONBody(r *http.Request) (map[string]any, error)` — decodes request body with `UseNumber()` for safe number handling
- `bodyStr(m map[string]any, key string) string` — extract string from parsed body
- `bodyInt(m map[string]any, key string) int64` — extract int64 from parsed body
- `bodyBool(m map[string]any, key string) bool` — extract bool (handles "Y"/"N" text booleans too)

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add JSON response helpers for device API`
<!-- END_TASK_9 -->

<!-- START_TASK_10 -->
### Task 10: Error codes and response helper tests

**Verifies:** notebridge-spc-replacement.AC1.3 (E0019 error format), AC1.4 (E0712 error format)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/errors_test.go`
- Create: `/home/sysop/src/notebridge/internal/sync/response_test.go`

**Testing:**

**Error tests:**
- Each error constructor returns correct code, message, HTTP status
- `ErrWrongPassword()` returns code "E0019", HTTP 401
- `ErrAccountLocked()` returns code "E0045", HTTP 403
- `ErrInvalidToken()` returns code "E0712", HTTP 401
- `SyncError` implements `error` interface

**Response tests (using httptest.ResponseRecorder):**
- `jsonSuccess` writes status 200, Content-Type application/json, body contains `"cd":"000"`
- `jsonSuccess` with extra fields includes them in response
- `jsonError` writes correct status code and body with error code
- `parseJSONBody` correctly parses JSON with numbers preserved
- `bodyStr`, `bodyInt`, `bodyBool` extract typed values correctly
- `bodyBool` handles "Y"/"N" text booleans

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/
```

**Commit:** `test: add error code and response helper tests`
<!-- END_TASK_10 -->
<!-- END_SUBCOMPONENT_C -->

<!-- START_SUBCOMPONENT_D (tasks 11-14) -->
## Subcomponent D: Challenge-Response Auth + JWT

<!-- START_TASK_11 -->
### Task 11: SyncDB store — auth query methods

**Files:**
- Create: `/home/sysop/src/notebridge/internal/syncdb/store.go`

**Implementation:**

`Store` struct wrapping `*sql.DB` with auth-related query methods. This is the data access layer for Phase 1 auth. More methods will be added in later phases.

Constructor: `NewStore(db *sql.DB) *Store`

Methods:
- `EnsureUser(ctx, email, passwordHash string) error` — INSERT OR IGNORE user with Snowflake ID (accepts a SnowflakeGenerator). Used by startup to bootstrap the configured user.
- `GetUserByEmail(ctx, email string) (*User, error)` — returns User struct (id, email, password_hash, error_count, last_error_at, locked_until)
- `IncrementErrorCount(ctx, userID int64) error` — bumps error_count, sets last_error_at to now
- `ResetErrorCount(ctx, userID int64) error` — sets error_count=0
- `LockUser(ctx, userID int64, until time.Time) error` — sets locked_until
- `CreateChallenge(ctx, account string, code string, timestamp int64) error` — inserts login_challenges row
- `GetChallenge(ctx, account string, timestamp int64) (string, error)` — returns random_code
- `DeleteChallenge(ctx, account string, timestamp int64) error` — cleanup after use
- `StoreToken(ctx, key, token string, userID int64, equipmentNo string, expiresAt time.Time) error` — inserts auth_tokens row
- `GetToken(ctx, key string) (*AuthToken, error)` — returns token row if not expired
- `DeleteToken(ctx, key string) error` — revoke
- `GetOrCreateJWTSecret(ctx) (string, error)` — reads from server_settings "jwt_secret"; if missing, generates random 32-byte hex, stores, returns
- `EnsureEquipment(ctx, equipmentNo string, userID int64) error` — INSERT OR IGNORE equipment row
- `StoreNonce(ctx, nonce string, expiresAt time.Time) error` — inserts url_nonces row
- `ConsumeNonce(ctx, nonce string) (bool, error)` — deletes nonce if exists and not expired, returns true if consumed
- `CleanupExpired(ctx) error` — deletes expired challenges, tokens, nonces (called periodically)

Types:
- `User` struct: ID int64, Email, PasswordHash string, ErrorCount int, LastErrorAt, LockedUntil *time.Time
- `AuthToken` struct: Key, Token string, UserID int64, EquipmentNo string, ExpiresAt time.Time

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add syncdb store with auth query methods`
<!-- END_TASK_11 -->

<!-- START_TASK_12 -->
### Task 12: Auth service — challenge-response, JWT, signed URLs

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/auth.go`

**Implementation:**

Port from opennotecloud's auth.go. Auth service struct with methods for the challenge-response flow, JWT token management, and signed URL generation.

`AuthService` struct fields: store (*syncdb.Store), jwtSecret []byte, snowflake (*SnowflakeGenerator)

**Challenge-response flow** (matching Supernote tablet protocol):
1. `GenerateChallenge(ctx, account string) (randomCode string, timestamp int64, err error)`
   - Generate 8-char random alphanumeric code
   - Store in login_challenges with current timestamp
   - Return code and timestamp to caller (sent to tablet)

2. `VerifyLogin(ctx, account, submittedHash string, timestamp int64) (token string, err error)`
   - Look up user by email (account)
   - Check account lockout: if locked_until > now, return ErrAccountLocked
   - Look up challenge by (account, timestamp)
   - If challenge is older than 5 minutes, return error (AC1.6)
   - Compute expected: SHA256(user.PasswordHash + challenge.RandomCode) as hex
   - If submitted != expected: increment error_count, check if >= 6 errors in 12h → lock for 5min, return ErrWrongPassword (AC1.3, AC1.5)
   - On success: reset error_count, create JWT token, store in auth_tokens, delete challenge, return token

3. `CreateJWTToken(ctx, userID int64, equipmentNo string) (string, error)`
   - Claims: sub (userID as string), equipmentNo, iat, exp (30 days), jti (random key for DB lookup)
   - Sign with HMAC-SHA256 using jwtSecret
   - Store in auth_tokens table with expiry

4. `ValidateJWTToken(ctx, tokenString string) (userID int64, equipmentNo string, err error)`
   - Parse JWT, verify signature
   - Check expiry (jwt library handles this)
   - Look up jti in auth_tokens table — must exist and not be expired
   - Return userID and equipmentNo from claims

**Signed URL generation:**
5. `GenerateSignedURL(ctx, path, action string, ttl time.Duration) (string, error)`
   - Create JWT with claims: path, action ("upload"/"download"), nonce (random 16-byte hex), exp
   - Store nonce in url_nonces with expiry
   - Return signed token (caller embeds in URL query param)

6. `VerifySignedURL(ctx, tokenString string) (path, action string, err error)`
   - Parse JWT, verify signature and expiry
   - Consume nonce (single-use) — if already consumed, return error
   - Return path and action

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add auth service with challenge-response, JWT, and signed URLs`
<!-- END_TASK_12 -->

<!-- START_TASK_13 -->
### Task 13: Auth middleware

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/middleware.go`

**Implementation:**

HTTP middleware for the device sync API.

`AuthMiddleware(authService *AuthService) func(http.Handler) http.Handler`
- Extracts token from `Authorization: Bearer <token>` header
- Calls `authService.ValidateJWTToken(ctx, token)`
- On success: stores userID and equipmentNo in request context, calls next handler
- On failure: writes jsonError with ErrInvalidToken (AC1.4)
- Skip auth for public endpoints: challenge request, login verify

`RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler`
- Recovers from panics, logs stack trace, returns 500 with ErrInternal

`LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler`
- Logs method, path, status, duration for each request

Context helpers:
- `UserIDFromContext(ctx) int64`
- `EquipmentNoFromContext(ctx) string`

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add auth and logging middleware`
<!-- END_TASK_13 -->

<!-- START_TASK_14 -->
### Task 14: Auth service and middleware tests

**Verifies:** AC1.1 (challenge-response flow), AC1.2 (JWT accepted), AC1.3 (wrong password → E0019), AC1.4 (invalid JWT → E0712), AC1.5 (account lockout → E0045), AC1.6 (expired challenge rejected)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/auth_test.go`
- Create: `/home/sysop/src/notebridge/internal/sync/middleware_test.go`
- Create: `/home/sysop/src/notebridge/internal/syncdb/store_test.go`

**Testing:**

Each test function creates an in-memory SQLite database via `syncdb.Open(":memory:")`, creates a `syncdb.NewStore(db)`, and uses it directly. No mocking of the database.

**store_test.go — SyncDB store tests:**
- EnsureUser creates user, second call is no-op
- GetUserByEmail returns correct user
- GetUserByEmail for non-existent returns nil (not error)
- IncrementErrorCount increments, ResetErrorCount resets to 0
- LockUser sets locked_until, can be read back
- CreateChallenge + GetChallenge round-trip
- DeleteChallenge removes it
- StoreToken + GetToken round-trip
- GetToken for expired token returns nil
- GetOrCreateJWTSecret creates on first call, returns same on second
- StoreNonce + ConsumeNonce: first consume returns true, second returns false
- ConsumeNonce for expired nonce returns false
- CleanupExpired removes expired challenges, tokens, nonces

**auth_test.go — Auth service tests:**
- AC1.1: Full challenge-response flow succeeds — GenerateChallenge, compute SHA256(md5Hash+code), VerifyLogin returns token
- AC1.2: Token from VerifyLogin can be validated via ValidateJWTToken, returns correct userID
- AC1.3: Wrong hash in VerifyLogin returns SyncError with code E0019
- AC1.4: ValidateJWTToken with garbage token returns SyncError with code E0712
- AC1.4: ValidateJWTToken with expired token returns E0712
- AC1.5: 6 consecutive wrong passwords locks account, next attempt returns E0045
- AC1.6: Challenge older than 5 minutes is rejected
- Signed URL: GenerateSignedURL + VerifySignedURL round-trip succeeds
- Signed URL: VerifySignedURL with already-consumed nonce fails (single-use)
- Signed URL: VerifySignedURL with expired token fails

**middleware_test.go — Middleware tests (using httptest):**
- AC1.2: Request with valid Bearer token → next handler called, context has userID
- AC1.4: Request with no Authorization header → 401 with E0712
- AC1.4: Request with invalid token → 401 with E0712
- Public endpoints (challenge, login) bypass auth
- RecoveryMiddleware: handler that panics → 500 response (not crash)
- LoggingMiddleware: request logged with method, path, status

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ ./internal/syncdb/
```

Expected: All tests pass.

**Commit:** `test: add auth service, middleware, and store tests`
<!-- END_TASK_14 -->
<!-- END_SUBCOMPONENT_D -->

<!-- START_SUBCOMPONENT_E (tasks 15-17) -->
## Subcomponent E: HTTP Router + Auth Endpoints

<!-- START_TASK_15 -->
### Task 15: HTTP router and auth endpoint handlers

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/server.go`
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_auth.go`

**Implementation:**

**server.go:**

`Server` struct that holds dependencies (store, authService, blobStore, logger, etc.) and registers routes.

Constructor: `NewServer(store *syncdb.Store, auth *AuthService, logger *slog.Logger) *Server`

`(s *Server) Handler() http.Handler` — returns the fully-wired HTTP handler with middleware chain:
- RecoveryMiddleware (outermost)
- LoggingMiddleware
- Route to appropriate handler

Routes for Phase 1 (auth only — file operations added in Phase 2):
- `POST /api/user/login/challenge` — public, returns challenge code
- `POST /api/user/login/verify` — public, verifies challenge response, returns JWT
- `GET /health` — public, returns 200 OK

All other `/api/*` routes require AuthMiddleware.

Use Go 1.22+ `http.ServeMux` with method routing (`mux.HandleFunc("POST /api/user/login/challenge", ...)`) — no external router dependency needed.

**handlers_auth.go:**

`handleChallenge(w, r)`:
- Parse body: expects `{"account": "user@email.com"}`
- Call authService.GenerateChallenge
- Return jsonSuccess with randomCode and timestamp

`handleLoginVerify(w, r)`:
- Parse body: expects `{"account": "...", "password": "SHA256_hash", "timestamp": 12345, "equipmentNo": "SN100..."}`
- Call authService.VerifyLogin
- On success: ensure equipment row exists, return jsonSuccess with token and user info
- On failure: return jsonError with appropriate SyncError

`handleHealth(w, r)`:
- Return `{"status": "ok"}` with 200

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add HTTP router and auth endpoint handlers`
<!-- END_TASK_15 -->

<!-- START_TASK_16 -->
### Task 16: Wire main.go

**Files:**
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go`

**Implementation:**

Wire all Phase 1 components together in main.go:

1. Load config via `config.Load()`
2. Set up structured logging (slog with JSON handler — keep simple for now, full logging package comes in Phase 7)
3. Open SQLite database via `syncdb.Open(cfg.DBPath)`
4. Create `syncdb.NewStore(db)`
5. Bootstrap user: `store.EnsureUser(ctx, cfg.UserEmail, cfg.UserPasswordHash)` with Snowflake ID
6. Get or create JWT secret: `store.GetOrCreateJWTSecret(ctx)` (env var `NB_JWT_SECRET` takes precedence if set; otherwise auto-generate and store in DB)
7. Create SnowflakeGenerator
8. Create AuthService
9. Create sync.Server, get Handler
10. Start HTTP server on cfg.SyncListenAddr (port 19071)
11. Graceful shutdown on SIGINT/SIGTERM

For now, only the sync API server starts on port 19071. Web UI server (port 8443) is added in Phase 7.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire main.go with config, database, and auth server`
<!-- END_TASK_16 -->

<!-- START_TASK_17 -->
### Task 17: Auth endpoint integration tests

**Verifies:** AC1.1 (full challenge-response via HTTP), AC1.2 (authenticated request accepted), AC1.3 (wrong password → E0019 via HTTP), AC1.4 (bad token → E0712 via HTTP), AC1.5 (lockout via HTTP), AC1.6 (expired challenge via HTTP), AC9.3 (no external deps — tests use in-memory SQLite)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_auth_test.go`

**Testing:**

Integration tests using `httptest.NewServer` with the full sync.Server handler (real SQLite, real auth service, real middleware — no mocks).

Test helper: `setupTestServer(t *testing.T) (*httptest.Server, *syncdb.Store)` — creates in-memory SQLite, bootstraps a test user with known email/passwordHash, creates AuthService and Server, returns httptest server.

**Test cases:**

- AC1.1 full flow:
  1. POST /api/user/login/challenge with account → get randomCode, timestamp
  2. Compute SHA256(md5PasswordHash + randomCode)
  3. POST /api/user/login/verify with account, password (the SHA256 result), timestamp, equipmentNo
  4. Assert 200, response contains token

- AC1.2 authenticated request:
  1. Complete login flow, get token
  2. Make request to an auth-required endpoint with `Authorization: Bearer <token>`
  3. Assert request succeeds (not 401)

- AC1.3 wrong password:
  1. Get challenge
  2. POST verify with wrong hash
  3. Assert response code E0019, HTTP 401

- AC1.4 invalid token:
  1. Make request with `Authorization: Bearer garbage`
  2. Assert response code E0712, HTTP 401

- AC1.5 account lockout:
  1. Fail login 6 times with wrong password
  2. Attempt login with correct password
  3. Assert response code E0045, HTTP 403

- AC1.6 expired challenge:
  1. Get challenge
  2. Manually update the challenge timestamp in DB to >5 minutes ago
  3. Attempt verify with correct hash
  4. Assert challenge rejected

- Health endpoint:
  1. GET /health
  2. Assert 200, body contains "ok"

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/
```

Expected: All tests pass.

**Commit:** `test: add auth endpoint integration tests`
<!-- END_TASK_17 -->
<!-- END_SUBCOMPONENT_E -->
