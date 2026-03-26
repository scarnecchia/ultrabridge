# NoteBridge Phase 8: Migration Script

**Goal:** One-time migration from SPC to NoteBridge.

**Architecture:** Go CLI tool (`cmd/migrate/main.go`) that reads SPC MariaDB + filesystem and writes to NoteBridge syncdb + blob storage. Generates Snowflake IDs for migrated files. Rebuilds directory tree. Verifies MD5s during copy.

**Tech Stack:** Go 1.24, go-sql-driver/mysql (MariaDB client), SQLite

**Scope:** Phase 8 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC10: Migration
- **AC10.1 Success:** migrate.sh copies files from SPC and creates correct syncdb entries
- **AC10.2 Success:** Tasks exported from SPC MariaDB appear in NoteBridge
- **AC10.3 Success:** Tablet pointed at NoteBridge after migration syncs without errors

---

<!-- START_SUBCOMPONENT_A (tasks 1-4) -->
## Subcomponent A: Migration Tool

<!-- START_TASK_1 -->
### Task 1: SPC MariaDB reader

**Files:**
- Create: `/home/sysop/src/notebridge/cmd/migrate/spcreader.go`

**Implementation:**

Functions to read from SPC's MariaDB database. Uses `go-sql-driver/mysql`.

Add dependency:
```bash
go get github.com/go-sql-driver/mysql
```

`SPCReader` struct wrapping `*sql.DB` (MariaDB connection).

Constructor: `NewSPCReader(dsn string) (*SPCReader, error)` — connects to MariaDB.

Methods:

`ReadUser(ctx) (*SPCUser, error)`:
- Query: `SELECT user_id, email, password, user_name FROM u_user WHERE is_normal = 'Y' LIMIT 1`
- Returns: SPCUser{UserID, Email, PasswordHash (MD5 hex), Username}

`ReadFiles(ctx, userID int64) ([]SPCFile, error)`:
- Query: `SELECT id, directory_id, file_name, inner_name, md5, size, is_folder, create_time, update_time FROM f_user_file WHERE user_id = ? AND is_active = 'Y' ORDER BY is_folder DESC, directory_id, file_name`
- Returns: []SPCFile with all fields
- Ordering ensures folders are processed before their children

`ReadTasks(ctx, userID int64) ([]SPCTask, error)`:
- Query: `SELECT task_id, task_list_id, title, detail, status, importance, due_time, completed_time, last_modified, recurrence, is_reminder_on, links FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'`
- Returns: []SPCTask with all fields

`ReadTaskGroups(ctx, userID int64) ([]SPCTaskGroup, error)`:
- Query: `SELECT task_list_id, title, last_modified FROM t_schedule_task_group WHERE user_id = ?` (or equivalent — check actual table name)
- If table doesn't exist, infer groups from distinct task_list_id values in tasks

`ReadSummaries(ctx, userID int64) ([]SPCSummary, error)`:
- Query: `SELECT id, unique_identifier, name, description, file_id, parent_unique_identifier, content, data_source, source_path, source_type, tags, md5_hash, metadata, is_summary_group, author, creation_time, last_modified_time FROM t_summary WHERE user_id = ? AND (is_deleted IS NULL OR is_deleted != 'Y')`
- Returns: []SPCSummary with all fields

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/migrate/
```

**Commit:** `feat: add SPC MariaDB reader for migration`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Migration engine

**Files:**
- Create: `/home/sysop/src/notebridge/cmd/migrate/migrator.go`

**Implementation:**

`Migrator` struct fields: spcReader (*SPCReader), syncStore (*syncdb.Store), blobStore (*blob.LocalStore), snowflake (*sync.SnowflakeGenerator), spcDataPath (string), logger (*slog.Logger)

Constructor: `NewMigrator(spc *SPCReader, store *syncdb.Store, blob *blob.LocalStore, sf *sync.SnowflakeGenerator, spcPath string, logger *slog.Logger) *Migrator`

`Run(ctx) error` — orchestrates the full migration:

1. **Migrate user:**
   - Read user from SPC
   - Create user in syncdb via store.EnsureUser(email, passwordHash)
   - Create equipment entry
   - Log: "Migrated user: {email}"

2. **Migrate files:**
   - Read all files from SPC
   - Build SPC directory_id → Snowflake ID mapping (for parent references)
   - SPC root folder IDs: DOCUMENT=1, NOTE=2, EXPORT=3, SCREENSHOT=4, INBOX=5
   - For each folder (processed first due to ordering):
     - Generate Snowflake ID
     - Map SPC directory_id to Snowflake ID
     - Create folder entry in syncdb
   - For each file:
     - Generate Snowflake ID
     - Determine blob storage key from path (reconstruct from folder tree)
     - Copy file: `cp {spcDataPath}/{email}/Supernote/{path}/{inner_name}` → blob store
     - Compute MD5 during copy, verify matches SPC's stored MD5
     - If mismatch: log warning but continue
     - Create file entry in syncdb with Snowflake ID, MD5, size
   - Track statistics: files copied, bytes total, MD5 mismatches, missing files

3. **Migrate task groups:**
   - Read task groups from SPC
   - For each: insert into syncdb schedule_groups
   - Preserve task_list_id (string ID)

4. **Migrate tasks:**
   - Read tasks from SPC
   - For each: insert into syncdb schedule_tasks
   - Preserve task_id (string ID)
   - Map completed_time quirk correctly (SPC's completed_time = creation time, last_modified = completion time)
   - Copy all fields: title, detail, status, importance, due_time, recurrence, is_reminder_on, links

5. **Migrate summaries:**
   - Read summaries from SPC
   - For each: generate Snowflake ID, insert into syncdb summaries
   - Preserve unique_identifier (for parent-child relationships)
   - Copy all content fields
   - **Handwrite files:** Summaries with `handwrite_inner_name` reference uploaded handwriting files stored in SPC's file storage. Copy these files to NoteBridge blob storage at `summaries/{inner_name}` and preserve the `handwrite_inner_name` reference. If the handwrite file is missing on disk, log a warning and continue (summary content is still in the DB row).

6. **Report:**
   - Log summary: users, files, tasks, groups, summaries migrated
   - Log warnings: missing files, MD5 mismatches, skipped records

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/migrate/
```

**Commit:** `feat: add migration engine for SPC → NoteBridge`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Migration CLI entrypoint and migrate.sh

**Files:**
- Create: `/home/sysop/src/notebridge/cmd/migrate/main.go`
- Create: `/home/sysop/src/notebridge/migrate.sh`

**Implementation:**

**cmd/migrate/main.go:**

CLI entrypoint that parses flags and runs the migration:

```go
func main() {
    spcDSN := flag.String("spc-dsn", "", "SPC MariaDB DSN (user:pass@tcp(host:port)/db)")
    spcPath := flag.String("spc-path", "/mnt/supernote/supernote_data", "SPC file storage path")
    nbDBPath := flag.String("nb-db", "/data/notebridge/notebridge.db", "NoteBridge SQLite path")
    nbStoragePath := flag.String("nb-storage", "/data/notebridge/storage", "NoteBridge blob storage path")
    dryRun := flag.Bool("dry-run", false, "Show what would be migrated without writing")
    flag.Parse()
    // ... validate flags, create components, run migrator
}
```

Dry-run mode: reads SPC, prints summary of what would be migrated, but doesn't write.

**migrate.sh:**

Wrapper script that:
1. Prompts for SPC MariaDB credentials (or reads from .dbenv)
2. Prompts for SPC data path
3. Prompts for NoteBridge data path
4. Builds the migrate tool (or uses pre-built binary)
5. Runs migration with progress output
6. Shows summary

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

info() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m OK \033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m WARN \033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m FAIL \033[0m %s\n' "$*"; exit 1; }

info "NoteBridge Migration Tool"
echo "This migrates data from Supernote Private Cloud to NoteBridge."
echo

# Read SPC .dbenv for MariaDB credentials
SPC_DBENV="${SPC_DBENV:-/mnt/supernote/.dbenv}"
if [[ -f "$SPC_DBENV" ]]; then
    info "Reading SPC credentials from $SPC_DBENV"
    # Parse .dbenv format
    DB_HOST=$(grep -oP 'DB_HOST=\K.*' "$SPC_DBENV" || echo "localhost")
    DB_PORT=$(grep -oP 'DB_PORT=\K.*' "$SPC_DBENV" || echo "3306")
    DB_NAME=$(grep -oP 'DB_NAME=\K.*' "$SPC_DBENV" || echo "supernotedb")
    DB_USER=$(grep -oP 'DB_USER=\K.*' "$SPC_DBENV" || echo "enote")
    DB_PASS=$(grep -oP 'DB_PASS=\K.*' "$SPC_DBENV" || echo "")
else
    warn "No .dbenv found. Enter SPC MariaDB credentials manually."
    read -rp "MariaDB host [localhost]: " DB_HOST; DB_HOST=${DB_HOST:-localhost}
    read -rp "MariaDB port [3306]: " DB_PORT; DB_PORT=${DB_PORT:-3306}
    read -rp "Database name [supernotedb]: " DB_NAME; DB_NAME=${DB_NAME:-supernotedb}
    read -rp "Database user [enote]: " DB_USER; DB_USER=${DB_USER:-enote}
    read -rsp "Database password: " DB_PASS; echo
fi

SPC_PATH="${SPC_PATH:-/mnt/supernote/supernote_data}"
NB_DATA="${NB_DATA:-/data/notebridge}"

info "Building migration tool..."
go build -C "$SCRIPT_DIR" -o "$SCRIPT_DIR/migrate" ./cmd/migrate/ || fail "Build failed"

info "Running migration..."
"$SCRIPT_DIR/migrate" \
    -spc-dsn "${DB_USER}:${DB_PASS}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}" \
    -spc-path "$SPC_PATH" \
    -nb-db "${NB_DATA}/notebridge.db" \
    -nb-storage "${NB_DATA}/storage"

ok "Migration complete!"
echo
echo "Next steps:"
echo "  1. Start NoteBridge: cd $SCRIPT_DIR && ./rebuild.sh"
echo "  2. Point your tablet at NoteBridge's IP address"
echo "  3. Sync and verify everything transferred correctly"
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/migrate/
```

**Commit:** `feat: add migration CLI and migrate.sh script`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Migration tests

**Verifies:** AC10.1 (file migration creates correct entries), AC10.2 (tasks exported correctly)

**Files:**
- Create: `/home/sysop/src/notebridge/cmd/migrate/migrator_test.go`

**Testing:**

Since the migration reads from MariaDB (external dependency), tests use a mock SPC reader that returns canned data. The migrator's `Run` method operates on the `SPCReader` interface — create a `mockSPCReader` that returns test data without a real MariaDB connection.

Alternatively, structure the test to directly call the migrator's sub-methods (migrateUser, migrateFiles, migrateTasks, migrateSummaries) with pre-built SPC structs.

**Test cases:**

- AC10.1 file migration:
  1. Create mock SPC data: 1 user, 3 folders (NOTE, DOCUMENT, EXPORT), 5 files (3 .note, 1 .pdf, 1 .png)
  2. Create temp directories for SPC data path and NoteBridge storage
  3. Write dummy files to SPC data path
  4. Run migrator
  5. Verify: syncdb has 3 folder entries + 5 file entries
  6. Verify: each file exists in NoteBridge blob storage with correct content
  7. Verify: MD5 in syncdb matches actual file hash
  8. Verify: directory tree structure preserved (files in correct folders)
  9. Verify: Snowflake IDs generated (not SPC IDs)

- AC10.1 missing file handling:
  1. Create mock SPC data with 3 files, but only write 2 to disk
  2. Run migrator
  3. Verify: 2 files migrated, 1 warning logged, migration continues

- AC10.2 task migration:
  1. Create mock SPC data: 2 task groups, 5 tasks (mixed statuses)
  2. Run migrator
  3. Verify: syncdb has 2 schedule_groups, 5 schedule_tasks
  4. Verify: task fields match (title, status, due_time, recurrence, links)
  5. Verify: completed_time quirk handled (SPC last_modified → NB completed semantics)
  6. Verify: soft-deleted tasks from SPC not migrated

- Summary migration:
  1. Create mock SPC data: 1 summary group, 3 summary items
  2. Run migrator
  3. Verify: syncdb has 1 summary group (is_summary_group='Y'), 3 items
  4. Verify: unique_identifier preserved, parent relationships intact

- User migration:
  1. Create mock SPC user with email, password hash
  2. Run migrator
  3. Verify: syncdb has user with correct email and password_hash

- Dry run:
  1. Run migrator with dry_run=true
  2. Verify: no writes to syncdb or blob storage
  3. Verify: summary logged with correct counts

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./cmd/migrate/
```

Expected: All tests pass.

**Commit:** `test: add migration tool tests`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_A -->
