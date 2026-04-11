# Human Test Plan: Platform-Neutral Configuration

**Implementation Plan:** `docs/implementation-plans/2026-04-10-platform-neutral-config/`
**Generated:** 2026-04-10

## Prerequisites

- UltraBridge built and running in a Docker container or locally
- Access to a clean test environment (no pre-existing configuration)
- `go test -C /home/sysop/src/ultrabridge ./internal/appconfig/ ./internal/source/... ./internal/web/` passing (22 automated criteria green)
- `install.sh` and `rebuild.sh` scripts present in the repository root

## Phase 1: Installer Verification

| Step | Action | Expected |
|------|--------|----------|
| 1.1 | Create a temporary directory: `mkdir /tmp/ub-test-install` | Directory created |
| 1.2 | Run `bash /home/sysop/src/ultrabridge/install.sh` in the temp directory | Exactly 3 prompts appear: (1) port number, (2) username, (3) password. No prompts for notes path, backup path, OCR settings, or Boox configuration. |
| 1.3 | Enter port=9999, username=testadmin, password=TestPass123 when prompted | Script completes without error. Generates `docker-compose.yml` in the current directory. |
| 1.4 | Examine the generated `docker-compose.yml` environment section | Contains ONLY `UB_DB_PATH`, `UB_LISTEN_ADDR`, and `UB_TASK_DB_PATH`. Does NOT contain `UB_NOTES_PATH`, `UB_BACKUP_PATH`, `UB_OCR_*`, `UB_BOOX_*`, `UB_PASSWORD_HASH`, or other config env vars. |
| 1.5 | Run `bash -n /home/sysop/src/ultrabridge/install.sh` | Exits with code 0 (no syntax errors). |
| 1.6 | Run `UB_PORT=9999 UB_USERNAME=admin UB_PASSWORD=test bash /home/sysop/src/ultrabridge/install.sh -y` | Runs non-interactively with zero prompts. Generates valid `docker-compose.yml`. |

## Phase 2: Platform-Neutral Naming Verification

| Step | Action | Expected |
|------|--------|----------|
| 2.1 | Run `grep -i "supernote\|/mnt/supernote\|DEFAULT_SUPERNOTE_DIR" /home/sysop/src/ultrabridge/install.sh` | Returns no matches. No Supernote-specific defaults or references in the installer. |
| 2.2 | Run `grep -i "supernote" /home/sysop/src/ultrabridge/rebuild.sh` | No matches in default paths or help text. (Supernote references in comments explaining SPC integration are acceptable.) |
| 2.3 | Review all installer prompt text by reading `install.sh` | No prompt mentions "Supernote", no default paths reference `/mnt/supernote`. Prompts are device-neutral (port, username, password only). |

## Phase 3: First-Boot Setup Flow

| Step | Action | Expected |
|------|--------|----------|
| 3.1 | Start UltraBridge with a fresh (empty) SQLite database and NO `UB_USERNAME`/`UB_PASSWORD_HASH` env vars | Application starts without error. |
| 3.2 | Navigate to `http://localhost:{port}/` in a browser | Browser is redirected to `http://localhost:{port}/setup` (307 redirect). The setup page displays "Welcome to UltraBridge" with fields for username, password, and password confirmation. |
| 3.3 | Navigate to `http://localhost:{port}/settings` | Browser is redirected to `/setup` (307). Settings page is NOT accessible without credentials. |
| 3.4 | Navigate to `http://localhost:{port}/api/config` | Browser is redirected to `/setup` (307). Config API is NOT accessible. |
| 3.5 | Navigate to `http://localhost:{port}/api/search?q=test` | Browser is redirected to `/setup` (307). Search API is NOT accessible. |
| 3.6 | On the setup page, enter username="admin", password="SecureP@ss123", confirm="SecureP@ss123", click Submit | Browser redirects to `/` (303 redirect). |
| 3.7 | Observe that `http://localhost:{port}/` now prompts for Basic Auth | Browser shows Basic Auth dialog. Enter admin/SecureP@ss123 to authenticate. |
| 3.8 | Navigate to `http://localhost:{port}/setup` after completing setup | Browser is redirected to `/` (307). Setup page is no longer accessible. |

## Phase 4: Settings UI and Config Management

| Step | Action | Expected |
|------|--------|----------|
| 4.1 | Navigate to `http://localhost:{port}/settings` (authenticated) | Settings page loads with "General Settings" section containing form fields for username, password, OCR settings, embedding settings, etc. |
| 4.2 | Verify the "General Settings" section shows current config values | Username field pre-populated with "admin". Password field shows placeholder (not actual hash). |
| 4.3 | Navigate to `http://localhost:{port}/api/config` (authenticated) | JSON response shows all config fields. `password_hash` shown as `"[set]"`. `ocr_api_key` shown as `"[not set]"` or `"[set]"` depending on whether it was configured. |
| 4.4 | Change the username via PUT `/api/config` with body `{"username":"newadmin"}` | Response JSON includes `restart_required: true` and `changed_keys` containing `"username"`. |
| 4.5 | Navigate to `/settings` after the restart-required change | A banner reading "Configuration has changed" with "Restart the application" appears at the top of the settings page. |
| 4.6 | Navigate to `http://localhost:{port}/health` | JSON response includes `"config_dirty": true`. |

## Phase 5: Source CRUD via API

| Step | Action | Expected |
|------|--------|----------|
| 5.1 | GET `/api/sources` | Returns empty JSON array `[]`. |
| 5.2 | POST `/api/sources` with body `{"type":"supernote","name":"My Supernote","enabled":true,"config_json":"{\"notes_path\":\"/data/notes\",\"backup_path\":\"/data/backup\"}"}` | Returns JSON with `{"id": N}` where N > 0. |
| 5.3 | GET `/api/sources` | Returns array with 1 element. Type is "supernote", name is "My Supernote", enabled is true, config_json contains the paths. |
| 5.4 | PUT `/api/sources/{id}` with updated name and enabled=false | Returns 200. Subsequent GET `/api/sources` shows updated name and enabled=false. |
| 5.5 | DELETE `/api/sources/{id}` | Returns 200. Subsequent GET `/api/sources` returns empty array. |
| 5.6 | POST `/api/sources` with missing type (empty string) | Returns 400 with error "type must be non-empty". |
| 5.7 | POST `/api/sources` with invalid config_json | Returns 400 with error "config_json must be valid JSON". |

## Phase 6: Existing Installation Upgrade

| Step | Action | Expected |
|------|--------|----------|
| 6.1 | Start UltraBridge with `UB_USERNAME=legacy_user` and `UB_PASSWORD_HASH=<valid bcrypt hash>` env vars set, but empty SQLite DB | Application starts. Setup mode is NOT triggered (env vars provide credentials). |
| 6.2 | Navigate to `http://localhost:{port}/` | Browser shows Basic Auth dialog (not setup page). Authenticate with legacy_user and the password corresponding to the bcrypt hash. |
| 6.3 | Navigate to `/settings` | Settings page loads. Username field shows "legacy_user" (from env var override). |

## End-to-End: Fresh Install to Configured System

**Purpose:** Validates the complete journey from clean install through first boot, setup, configuration, and source management.

1. Build UltraBridge Docker image: `docker build -t ultrabridge:test /home/sysop/src/ultrabridge`
2. Run container with only bootstrap env vars: `docker run -d -e UB_DB_PATH=/data/ultrabridge.db -e UB_LISTEN_ADDR=:8443 -v /tmp/ub-data:/data ultrabridge:test`
3. Open `http://localhost:8443/` -- verify redirect to `/setup`
4. Complete setup with username "admin", password "TestP@ss2026"
5. Authenticate with Basic Auth, verify task list page loads
6. Navigate to `/settings`, verify General Settings form is visible
7. Use `/api/sources` to add a Boox source with `{"notes_path":"/data/boox-notes"}`
8. Verify the source appears in GET `/api/sources`
9. Change a config setting via PUT `/api/config` (e.g., `ocr_concurrency` to 4)
10. Verify `/settings` shows the restart banner if the changed key requires restart
11. Verify `/health` shows `config_dirty: true`
12. Stop and restart the container
13. Verify the configured settings persist (GET `/api/config` returns saved values)
14. Verify the Boox source persists (GET `/api/sources` returns the previously added source)

## Traceability

| Acceptance Criterion | Automated Test | Manual Step |
|----------------------|----------------|-------------|
| AC1.1: Load reads config from DB | `TestLoadReadsFromDB` | -- |
| AC1.2: Save writes changed keys | `TestSaveWritesChangedKeys`, `TestSaveNoChanges` | -- |
| AC1.3: Env var overrides DB | `TestLoadEnvVarOverride` | -- |
| AC1.4: Settings UI displays config | `TestGetConfigRedacts`, `TestSettingsPage_GeneralSettings`, sources API tests | Phase 4, steps 4.1-4.3 |
| AC1.5: Password change hashes bcrypt | `TestPutConfigHashesPassword` | -- |
| AC1.6: Restart banner | `TestPutConfigDetectsRestartRequired`, `TestSettingsPage_RestartBanner` | Phase 4, steps 4.4-4.5 |
| AC1.7: /health config_dirty | `TestHealthEndpointConfigDirty` | Phase 4, step 4.6 |
| AC1.8: First boot env var fallback | `TestLoadFirstBootFallsBackToEnv`, `TestLoadAppliesDefaults` | Phase 6 |
| AC2.1: Source interface contract | `TestSourceInterface` | -- |
| AC2.2: Supernote source adapter | `TestNewSourceValidConfig`, `TestSourceStartStop` (supernote) | -- |
| AC2.3: Boox source adapter | `TestNewSourceValidConfig`, `TestSourceStartStop` (boox) | -- |
| AC2.4: Sources CRUD | `TestSourceRowRoundtrip`, `TestListSources`, `TestListEnabledSources` | Phase 5 |
| AC2.5: main.go iterates sources | `TestRegistryCreate` + build verification | End-to-End scenario |
| AC2.6: Config JSON per source | `TestRegistryCreate`, `TestNewSourceConfigParsing` (both) | Phase 5, steps 5.2-5.3 |
| AC2.7: Unknown source type skipped | `TestRegistryCreateUnknownType` | -- |
| AC2.8: Invalid config_json skipped | `TestRegistryCreateInvalidJSON`, `TestNewSourceInvalidJSON` (both) | -- |
| AC3.1: install.sh prompts | -- | Phase 1, steps 1.1-1.6 |
| AC3.2: Compose file bootstrap only | -- | Phase 1, step 1.4 |
| AC3.3: Fresh container shows setup | `TestIsSetupRequiredWithEmptyDB`, `TestSetupPage_RendersWithEmptyDB`, `TestSetupMiddleware_RedirectsToSetup` | Phase 3, steps 3.1-3.2 |
| AC3.4: Setup saves and enforces auth | `TestSetupSave_SavesCredentials`, `TestSetupSave_RejectsWhenSetupComplete` | Phase 3, steps 3.6-3.8 |
| AC3.5: Existing env vars skip setup | `TestIsSetupRequiredWithEnvVars` | Phase 6 |
| AC3.6: Setup blocks data endpoints | `TestSetupMiddleware_RedirectsToSetup`, `TestSetupMiddleware_AllowsSetupEndpoints` | Phase 3, steps 3.3-3.5 |
| AC4.1: Removed env vars | Grep check (verified) | -- |
| AC4.2: No snNotesPath | Grep check (verified) | -- |
| AC4.3: install.sh platform-neutral | -- | Phase 2 |
| AC4.4: Documentation updated | -- | Manual review |
