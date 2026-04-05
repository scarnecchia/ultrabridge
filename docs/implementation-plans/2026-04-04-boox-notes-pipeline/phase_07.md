# Boox Notes Pipeline — Phase 7: Deployment Configuration

**Goal:** install.sh/rebuild.sh support Boox configuration, Docker volume setup, documentation.

**Architecture:** Extend existing install.sh with "Boox Device Integration" section following the Supernote task sync prompt pattern. Update rebuild.sh --fresh to clear Boox cache and jobs (preserving .versions/). Update --nuke to clear everything. Add Docker Compose volume mount for Boox notes directory.

**Tech Stack:** Bash scripts, Docker Compose.

**Scope:** 7 phases from original design (phase 7 of 7)

**Codebase verified:** 2026-04-05

**Reference files:**
- install.sh: `/home/jtd/ultrabridge/install.sh` (SN sync prompt pattern at lines 253-274)
- rebuild.sh: `/home/jtd/ultrabridge/rebuild.sh` (--fresh cleanup at lines 56-93)
- Dockerfile: `/home/jtd/ultrabridge/Dockerfile`
- Config: `/home/jtd/ultrabridge/internal/config/config.go`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC7: Deployment configuration
- **boox-notes-pipeline.AC7.1 Success:** install.sh prompts for Boox support and configures correctly when enabled
- **boox-notes-pipeline.AC7.2 Success:** rebuild.sh --fresh clears Boox cache and jobs but preserves .versions/
- **boox-notes-pipeline.AC7.3 Success:** rebuild.sh --nuke clears all Boox data including .versions/
- **boox-notes-pipeline.AC7.4 Success:** UB_BOOX_ENABLED=false disables all Boox functionality (no WebDAV mount, no processing)

---

<!-- START_TASK_1 -->
### Task 1: Update install.sh with Boox configuration prompts

**Verifies:** boox-notes-pipeline.AC7.1, boox-notes-pipeline.AC7.4

**Files:**
- Modify: `/home/jtd/ultrabridge/install.sh`

**Implementation:**

Add a "Boox Device Integration" section after the Supernote task sync section (after line ~274), following the exact same prompt pattern:

```bash
# ── Boox Device Integration ──
info "── Boox Device Integration ──"
info "Boox devices can auto-export handwritten notes via WebDAV."
info "When enabled, UltraBridge runs a WebDAV server at /webdav/ that"
info "accepts .note file uploads, renders pages, and indexes text."
echo ""

UB_BOOX_ENABLED="false"
UB_BOOX_NOTES_PATH=""

if [[ "$UNATTENDED" == true ]]; then
    enable_boox=$([[ "${UB_BOOX_ENABLED:-false}" == "true" ]] && echo "y" || echo "n")
else
    read -rp "Enable Boox device uploads via WebDAV? (y/N): " enable_boox
fi

if [[ "${enable_boox,,}" == "y" ]]; then
    UB_BOOX_ENABLED="true"
    prompt UB_BOOX_NOTES_PATH "Boox notes directory (WebDAV root)" "${SUPERNOTE_DIR}/boox-notes"
    mkdir -p "$UB_BOOX_NOTES_PATH"
fi
```

Add Boox env vars to the `.ultrabridge.env` generation section (in the env file heredoc):

```bash
UB_BOOX_ENABLED=${UB_BOOX_ENABLED}
```

Conditionally add path if enabled:
```bash
if [[ "$UB_BOOX_ENABLED" == "true" ]]; then
    echo "UB_BOOX_NOTES_PATH=${UB_BOOX_NOTES_PATH}" >> "$SUPERNOTE_DIR/.ultrabridge.env"
fi
```

Add Docker Compose volume mount for Boox notes directory in the `docker-compose.override.yml` generation. Conditionally add the volume mount (same pattern as UB_NOTES_PATH and UB_BACKUP_PATH):

```bash
if [[ -n "$UB_BOOX_NOTES_PATH" ]]; then
    echo "      - ${UB_BOOX_NOTES_PATH}:${UB_BOOX_NOTES_PATH}" >> "$COMPOSE_OVERRIDE"
fi
```

**Verification:**

Run install.sh with `--help` to verify it doesn't error:
```bash
bash /home/jtd/ultrabridge/install.sh --help
```

**Commit:** `feat(deploy): add Boox configuration prompts to install.sh`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Update rebuild.sh with Boox data cleanup

**Verifies:** boox-notes-pipeline.AC7.2, boox-notes-pipeline.AC7.3

**Files:**
- Modify: `/home/jtd/ultrabridge/rebuild.sh`

**Implementation:**

In the `--fresh` cleanup section (around lines 56-80), add Boox-specific cleanup. Read `UB_BOOX_NOTES_PATH` from the existing `.ultrabridge.env` file to know where Boox data lives:

```bash
# Read Boox path from existing config
BOOX_PATH=$(grep "^UB_BOOX_NOTES_PATH=" "$SUPERNOTE_DIR/.ultrabridge.env" 2>/dev/null | cut -d= -f2)
```

For `--fresh` — clear cache and reset jobs, preserve .versions/:

```bash
if [[ -n "$BOOX_PATH" ]]; then
    info "Clearing Boox rendered cache..."
    rm -rf "${BOOX_PATH}/.cache"
    # boox_jobs table will be cleared along with the SQLite database
    # (the DB file is already deleted by --fresh)
    # .versions/ is preserved intentionally
fi
```

Note: The existing `--fresh` already deletes the SQLite database file (`ultrabridge.db`), which contains `boox_notes` and `boox_jobs` tables. So job cleanup is automatic. The `.cache/` directory needs explicit cleanup since it's on the filesystem.

For `--nuke` — clear everything including .versions/:

```bash
if [[ -n "$BOOX_PATH" ]]; then
    info "Clearing all Boox data including versions..."
    rm -rf "${BOOX_PATH}/.cache"
    rm -rf "${BOOX_PATH}/.versions"
fi
```

Note: `--nuke` already deletes the entire `ultrabridge-data/` directory (containing the DB). The additional Boox-specific cleanup handles the cache and versions directories which live under `UB_BOOX_NOTES_PATH` (outside the data directory).

**Verification:**

Run rebuild.sh with `--help` to verify:
```bash
bash /home/jtd/ultrabridge/rebuild.sh --help
```

**Commit:** `feat(deploy): add Boox data cleanup to rebuild.sh --fresh and --nuke`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Verification tests for deployment scripts

**Verifies:** boox-notes-pipeline.AC7.1, boox-notes-pipeline.AC7.2, boox-notes-pipeline.AC7.3, boox-notes-pipeline.AC7.4

**Files:** No new test files — deployment scripts are verified operationally.

**Verification steps:**

Run shellcheck on modified scripts first:
```bash
shellcheck /home/jtd/ultrabridge/install.sh /home/jtd/ultrabridge/rebuild.sh
```
Expected: No new warnings introduced by Boox changes.

Then run operational verification commands manually:

**AC7.1 — install.sh prompts correctly:**
```bash
# Test unattended mode with Boox enabled
UB_BOOX_ENABLED=true UB_BOOX_NOTES_PATH=/tmp/boox-test \
  bash /home/jtd/ultrabridge/install.sh --unattended --help
# Verify: --help output mentions Boox options
```

**AC7.2 — --fresh preserves .versions/:**
```bash
# Create test data
mkdir -p /tmp/boox-test/.cache/test /tmp/boox-test/.versions/test
echo "cached" > /tmp/boox-test/.cache/test/page_0.jpg
echo "versioned" > /tmp/boox-test/.versions/test/20260404T120000.note

# After running --fresh:
# Verify: .cache/ deleted, .versions/ still exists
```

**AC7.3 — --nuke clears everything:**
```bash
# After running --nuke:
# Verify: both .cache/ and .versions/ deleted
```

**AC7.4 — UB_BOOX_ENABLED=false disables all:**
```bash
# Build and run with UB_BOOX_ENABLED=false
# Verify: no /webdav/ endpoint (404), no Boox processing, startup log doesn't mention "boox webdav enabled"
```

**Commit:** `docs(deploy): verify Boox deployment configuration`
<!-- END_TASK_3 -->
