# CalDAV-Native Task Store — Phase 7: Configuration & Deployment

**Goal:** Install script updates, documentation, and standalone mode support (running without SPC when sync is disabled).

**Architecture:** This is an infrastructure phase. Update `install.sh` to prompt for Supernote sync settings, update `.ultrabridge.env.example` with new vars, update `README.md` configuration tables, and make MariaDB connection failure non-fatal in `main.go` when `UB_SN_SYNC_ENABLED=false`. No new Go packages — only config/deployment changes.

**Tech Stack:** Bash (install.sh), Markdown (docs), Go (main.go graceful degradation)

**Scope:** 7 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC4: Clean migration
- **caldav-native-taskstore.AC4.3 Success:** First run without SPC (standalone mode) starts with empty store, no error

### caldav-native-taskstore.AC5: Adapter-ready architecture
- **caldav-native-taskstore.AC5.3 Success:** Disabling Supernote adapter (UB_SN_SYNC_ENABLED=false) leaves task store and CalDAV fully functional

---

<!-- START_TASK_1 -->
### Task 1: Make MariaDB connection non-fatal in `main.go`

**Verifies:** caldav-native-taskstore.AC4.3, caldav-native-taskstore.AC5.3

**Files:**
- Modify: `cmd/ultrabridge/main.go:56-77` (MariaDB connection and user resolution)

**Implementation:**

Currently `main.go` calls `os.Exit(1)` when MariaDB connection fails (line 59) and when user resolution fails (line 69). When sync is disabled (`UB_SN_SYNC_ENABLED=false`), MariaDB is not needed for task operations (tasks are in SQLite). MariaDB is still needed for the notes pipeline (catalog sync) and for sync when enabled.

Change the MariaDB connection block to allow graceful degradation:

```go
	// Connect to Supernote MariaDB.
	// Required when SN sync is enabled or notes pipeline uses catalog sync.
	// Non-fatal when sync is disabled — task store is SQLite-only.
	database, err := db.Connect(cfg.DSN())
	if err != nil {
		if cfg.SNSyncEnabled {
			logger.Error("database connection failed (required for sync)", "error", err)
			os.Exit(1)
		}
		logger.Warn("database connection failed, notes catalog sync disabled", "error", err)
		// database is nil — catalog updater won't be set, which is handled at line 100
	}
	if database != nil {
		defer database.Close()
	}

	var userID int64
	if database != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		userID, err = db.ResolveUserID(ctx, database, cfg.UserID)
		if err != nil {
			if cfg.SNSyncEnabled {
				logger.Error("user resolution failed (required for sync)", "error", err)
				os.Exit(1)
			}
			logger.Warn("user resolution failed", "error", err)
		} else if cfg.UserID != 0 {
			logger.Info("using configured user_id", "user_id", userID)
		} else {
			logger.Info("discovered user_id", "user_id", userID)
		}
	}
```

Remove the old `store := taskstore.New(database, userID)` line (already replaced by Phase 1's `taskdb.NewStore(taskDB)`).

The existing nil-check at line 100 (`if database != nil { workerCfg.CatalogUpdater = ... }`) already handles the case where database is nil.

**Verification:**

```bash
# On remote server (with MariaDB running):
go build -C ~/src/ultrabridge ./cmd/ultrabridge/
# Run with sync disabled and no DB to verify it starts:
UB_SN_SYNC_ENABLED=false UB_SUPERNOTE_DBENV_PATH=/dev/null UB_DB_HOST=localhost UB_DB_PORT=9999 ~/src/ultrabridge/ultrabridge &
# Check health endpoint:
curl -s http://localhost:8443/health
# Should return {"status":"ok"}
```

Expected: UltraBridge starts with a warning about DB connection failure, but serves CalDAV and web UI normally with the SQLite task store.

**Commit:** `feat(main): make MariaDB connection non-fatal when sync is disabled`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Add Supernote sync section to `install.sh`

**Files:**
- Modify: `install.sh` (add sync config section between OCR section and build section)

**Implementation:**

Add a new "Supernote Sync" section after the OCR configuration prompts (after line ~221) and before the Docker build section (line ~225). Follow the existing prompt pattern:

```bash
# ── Supernote Sync ──────────────────────────────────────────
echo ""
info "── Supernote Task Sync ──"
echo "Sync tasks between UltraBridge and your Supernote device."
echo "This requires the Supernote Private Cloud to be running."
echo ""

UB_SN_SYNC_ENABLED="false"
UB_SN_SYNC_INTERVAL="300"
UB_SN_API_URL=""
UB_SN_PASSWORD=""

read -rp "Enable Supernote task sync? (y/N): " enable_sync
if [[ "${enable_sync,,}" == "y" ]]; then
    UB_SN_SYNC_ENABLED="true"
    prompt UB_SN_API_URL "SPC API URL" "http://supernote-service:9000"
    prompt UB_SN_SYNC_INTERVAL "Sync interval (seconds)" "300"
    prompt_password UB_SN_PASSWORD "Supernote Private Cloud password"
fi
```

Add the new vars **within the existing `.ultrabridge.env` heredoc block** (around line 275-285). The existing heredoc runs from `cat > "$ENV_FILE" <<EOF` to `EOF`. Add the new vars before the closing `EOF`, following the existing pattern of conditional sections. Examine the actual heredoc structure in install.sh to ensure correct placement:

```bash
# ── Task Store ──
UB_TASK_DB_PATH=/data/ultrabridge-tasks.db

# ── Supernote Sync ──
UB_SN_SYNC_ENABLED=${UB_SN_SYNC_ENABLED}
```

Then, **after** the main heredoc closes, conditionally append sync-specific vars:

```bash
if [[ "$UB_SN_SYNC_ENABLED" == "true" ]]; then
cat >> "$ENV_FILE" <<EOF_SYNC
UB_SN_SYNC_INTERVAL=${UB_SN_SYNC_INTERVAL}
UB_SN_API_URL=${UB_SN_API_URL}
UB_SN_PASSWORD=${UB_SN_PASSWORD}
EOF_SYNC
fi
```

**Important:** The implementor must read the actual install.sh heredoc structure to find the correct insertion point. The existing pattern uses a single `cat >` heredoc with optional `cat >>` appends for conditional sections (see how OCR vars are handled).

**Verification:**

Run install.sh in a test scenario to verify prompts appear and env file is generated correctly. This is manual verification.

**Commit:** `feat(install): add Supernote sync configuration section`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Update `.ultrabridge.env.example`

**Files:**
- Modify: `.ultrabridge.env.example` (add new vars at end)

**Implementation:**

Add a new section after the Notes Pipeline section (after line ~50):

```bash

# ── Task Store ──────────────────────────────────────────────
# Path to the SQLite database for task storage.
# UB_TASK_DB_PATH=/data/ultrabridge-tasks.db

# ── Supernote Task Sync ─────────────────────────────────────
# Enable sync between UltraBridge and Supernote device via SPC REST API.
# Requires Supernote Private Cloud to be running and accessible.
# UB_SN_SYNC_ENABLED=false

# Sync interval in seconds (default: 300 = 5 minutes).
# UB_SN_SYNC_INTERVAL=300

# SPC REST API URL. Usually the internal Docker network address.
# UB_SN_API_URL=http://supernote-service:9000

# SPC password (plaintext, used for challenge-response auth).
# This is separate from UB_PASSWORD_HASH — SPC requires the plaintext
# password for SHA-256 challenge-response, while UltraBridge uses bcrypt.
# UB_SN_PASSWORD=
```

**Verification:**

No build step — documentation only.

**Commit:** `docs: add task store and sync env vars to .ultrabridge.env.example`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Update `README.md` configuration section

**Files:**
- Modify: `README.md` (Configuration section, after line ~142)

**Implementation:**

Add two new subsections to the Configuration section, after the Infrastructure subsection:

```markdown
### Task Store

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_TASK_DB_PATH` | `/data/ultrabridge-tasks.db` | Path to SQLite database for task storage |

### Supernote Sync

| Variable | Default | Description |
|----------|---------|-------------|
| `UB_SN_SYNC_ENABLED` | `false` | Enable task sync with Supernote device via SPC |
| `UB_SN_SYNC_INTERVAL` | `300` | Sync interval in seconds |
| `UB_SN_API_URL` | `http://localhost:9000` | SPC REST API URL |
| `UB_SN_PASSWORD` | _(none)_ | SPC password for challenge-response auth |

When sync is disabled, UltraBridge runs in standalone mode with tasks stored locally in SQLite. CalDAV and the web UI work normally. MariaDB connection failure is non-fatal in this mode.

When sync is enabled, tasks are bidirectionally synced between UltraBridge and the Supernote device. UltraBridge is authoritative on conflicts.
```

**Verification:**

No build step — documentation only.

**Commit:** `docs(README): add task store and sync configuration documentation`

<!-- END_TASK_4 -->
