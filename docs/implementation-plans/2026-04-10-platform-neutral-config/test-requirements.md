# Test Requirements: Platform-Neutral Configuration

Rationalized against the design plan (`docs/design-plans/2026-04-10-platform-neutral-config.md`) and implementation phases 01-08.

Generated: 2026-04-10

---

## Automated Tests

Each row maps an acceptance criterion to a specific automated test. Test file paths follow the implementation plan's package structure.

### AC1: SQLite-backed configuration system

| AC ID | Criterion | Test Type | Test File Path | Test Description |
|-------|-----------|-----------|----------------|------------------|
| AC1.1 | `appconfig.Load()` reads all config keys from settings table and returns typed Config struct | Unit | `internal/appconfig/appconfig_test.go` | Pre-populate settings via `notedb.SetSetting`, call `Load`, verify each typed struct field matches expected values. Uses in-memory SQLite via `notedb.Open(ctx, ":memory:")`. |
| AC1.2 | `appconfig.Save()` writes changed keys to settings table and returns list of changed keys | Unit | `internal/appconfig/appconfig_test.go` | Load config, modify fields, call Save, verify `SaveResult.ChangedKeys` contains exactly the modified keys. Verify DB values via `notedb.GetSetting`. Verify unchanged save returns empty ChangedKeys. |
| AC1.3 | Env var set for a config key overrides the DB value in loaded Config | Unit | `internal/appconfig/appconfig_test.go` | Set a key in DB via `notedb.SetSetting`, set corresponding `UB_` env var to a different value via `os.Setenv` (deferred `os.Unsetenv`), call `Load`, verify struct field has env var value. |
| AC1.4 | Settings UI displays current config values and allows editing all non-bootstrap settings | Unit | `internal/web/routes_test.go` | GET `/api/config` returns JSON with all non-bootstrap config fields populated. GET `/settings` returns HTML containing form fields for OCR, Embedding, Chat, Sync config. GET `/api/sources` returns source list. POST/PUT/DELETE `/api/sources` CRUD round-trip. |
| AC1.5 | Password change via Settings UI hashes with bcrypt and stores hash in settings table | Unit | `internal/web/routes_test.go` | PUT `/api/config` with `{"password": "newpass"}` body. Verify `notedb.GetSetting(KeyPasswordHash)` returns a valid bcrypt hash. Verify `bcrypt.CompareHashAndPassword` succeeds for the plaintext. GET `/api/config` shows `password_hash: "[set]"` (redacted). |
| AC1.6 | Saving a config change to a restart-required key shows "restart required" banner | Unit | `internal/web/routes_test.go` | PUT `/api/config` changing a restart-required key (e.g., `ocr_api_url`). Verify response JSON includes `restart_required: true`. PUT changing a non-restart key, verify `restart_required: false`. GET `/settings` after restart-required change returns HTML containing "restart" banner text. |
| AC1.7 | `/health` endpoint returns `config_dirty: true` when running config differs from DB | Unit | `internal/web/routes_test.go` | GET `/health` before any config changes, verify `config_dirty: false`. PUT `/api/config` with a restart-required key changed. GET `/health`, verify `config_dirty: true`. |
| AC1.8 | First boot with no DB values falls back to env vars for all config keys | Unit | `internal/appconfig/appconfig_test.go` | Set `UB_` env vars via `os.Setenv` (deferred cleanup), call `Load` with empty in-memory SQLite DB. Verify struct fields have env var values. Also verify defaults are applied when neither DB nor env var is set. |

### AC2: Unified Source abstraction

| AC ID | Criterion | Test Type | Test File Path | Test Description |
|-------|-----------|-----------|----------------|------------------|
| AC2.1 | Source interface defines Type(), Name(), Start(ctx), Stop() contract | Unit | `internal/source/source_test.go` | Compile-time verification: a test stub struct implementing `Type() string`, `Name() string`, `Start(ctx) error`, `Stop()` is assigned to a `source.Source` variable. Verifies interface contract. |
| AC2.2 | Supernote source adapter starts/stops the existing processor pipeline via Source interface | Unit | `internal/source/supernote/source_test.go` | Construct `supernote.Source` with valid config JSON, `t.TempDir()` for NotesPath, nil OCRClient (OCR disabled). Verify `Type()` returns `"supernote"`, `Name()` returns configured name. Call `Start(ctx)` with in-memory SQLite, verify no error. Call `Stop()`, verify no panic. |
| AC2.3 | Boox source adapter starts/stops the existing boox pipeline via Source interface | Unit | `internal/source/boox/source_test.go` | Construct `boox.Source` with valid config JSON, `t.TempDir()` for NotesPath, nil SharedDeps components. Verify `Type()` returns `"boox"`, `Name()` returns configured name. Call `Start(ctx)` with in-memory SQLite, verify no error. Call `Stop()`, verify no panic. |
| AC2.4 | Sources table CRUD works -- add, update, enable/disable, remove sources via API | Unit | `internal/source/source_test.go` | Full CRUD round-trip against in-memory SQLite: `AddSource` returns ID, `GetSource` by ID returns matching row, `ListSources` includes it, `UpdateSource` changes name/enabled/config_json, `RemoveSource` deletes it, `GetSource` returns `ErrSourceNotFound`. `ListEnabledSources` excludes disabled rows. |
| AC2.5 | main.go iterates enabled sources from DB, creates via factory, starts each, defers Stop() | Integration | Build + `go test ./...` | Verified by Phase 5 operational testing: application builds and starts with source registry iteration. The `Registry.Create` method is unit-tested in `internal/source/source_test.go`; the main.go wiring is verified by successful build (`go build ./cmd/ultrabridge/`) and full test suite pass. |
| AC2.6 | Source-specific config stored as JSON in config_json column, parsed by each factory | Unit | `internal/source/source_test.go`, `internal/source/supernote/source_test.go`, `internal/source/boox/source_test.go` | Three-layer verification: (1) CRUD test stores and retrieves JSON in config_json unchanged. (2) Supernote factory parses `{"notes_path":"/data","backup_path":"/backup"}` into typed Config. (3) Boox factory parses `{"notes_path":"/boox"}` into typed Config. Extra/missing JSON fields handled gracefully. |
| AC2.7 | Unknown source type in DB logs warning and is skipped, does not crash startup | Unit | `internal/source/source_test.go` | Call `Registry.Create` with a `SourceRow` whose Type is `"unknown"`. Verify it returns a non-nil error. Caller (main.go) logs warning and continues -- tested by verifying the registry error message contains the unknown type name. |
| AC2.8 | Source with invalid config_json logs error and is skipped, does not crash startup | Unit | `internal/source/supernote/source_test.go`, `internal/source/boox/source_test.go` | Call `NewSource` with `config_json: "{{invalid"`. Verify it returns a non-nil error wrapping a JSON parse error. Both supernote and boox adapters tested independently. |

### AC3: Simplified installer and first-boot

| AC ID | Criterion | Test Type | Test File Path | Test Description |
|-------|-----------|-----------|----------------|------------------|
| AC3.2 | Generated compose file contains only bootstrap env vars (UB_DB_PATH, UB_LISTEN_ADDR) | Unit (grep) | Manual grep after install.sh run | After running install.sh, verify the generated `docker-compose.yml` contains only `UB_DB_PATH`, `UB_LISTEN_ADDR`, and `UB_TASK_DB_PATH` in the environment section. No `UB_NOTES_PATH`, `UB_BACKUP_PATH`, `UB_OCR_*`, `UB_BOOX_*`, `UB_PASSWORD_HASH`, or other config env vars present. |
| AC3.3 | Fresh container with no auth in DB shows setup page without requiring auth | Unit | `internal/web/routes_test.go`, `internal/appconfig/appconfig_test.go` | (1) `IsSetupRequired` with empty DB and no env vars returns `true`. (2) HTTP test: GET `/` through SetupMiddleware with empty DB returns 307 redirect to `/setup`. GET `/setup` returns 200 with HTML containing setup form. |
| AC3.4 | Saving credentials on setup page ends setup mode and enforces Basic Auth | Unit | `internal/web/routes_test.go` | POST `/setup/save` with username + password. Verify credentials saved to DB (bcrypt hash). Subsequent GET `/` returns 401 (auth required). GET `/setup` after save redirects to `/`. POST `/setup/save` after save returns 403. |
| AC3.5 | Existing install with .ultrabridge.env works on upgrade -- env vars provide all config | Unit | `internal/appconfig/appconfig_test.go` | `IsSetupRequired` with empty DB but `UB_USERNAME` + `UB_PASSWORD_HASH` env vars set returns `false`. `appconfig.Load` with empty DB + env vars set returns Config with env var values. |
| AC3.6 | Setup mode only exposes credential setup -- no data endpoints accessible without auth | Unit | `internal/web/routes_test.go` | With empty DB (setup mode active): GET `/api/search`, GET `/api/notes/pages`, GET `/settings`, GET `/api/config` all return 307 redirect to `/setup`. Only `/setup` and `/setup/save` are accessible. |

### AC4: Platform-neutral naming

| AC ID | Criterion | Test Type | Test File Path | Test Description |
|-------|-----------|-----------|----------------|------------------|
| AC4.1 | UB_NOTES_PATH, UB_BACKUP_PATH, UB_BOOX_ENABLED, UB_BOOX_NOTES_PATH env vars removed -- replaced by per-source config | Unit (grep) | Automated grep check | `grep -r "UB_NOTES_PATH\|UB_BACKUP_PATH\|UB_BOOX_ENABLED\|UB_BOOX_NOTES_PATH" internal/ cmd/` returns no matches in Go source files. These env vars no longer appear in `appconfig/keys.go` `envVarForKey` map. |
| AC4.2 | No handler field or variable named with `sn` prefix for generic functionality | Unit (grep) | Automated grep check | `grep -rn "snNotesPath" internal/web/` returns no matches. The field is renamed to `notesPathPrefix`. Note: `sn_` prefix is still correct for Supernote-specific settings like `sn_inject_enabled`, `sn_ocr_prompt`, and the `internal/tasksync/supernote/` package. |

---

## Human Verification

These criteria require manual or interactive verification because they involve shell script interaction, Docker container behavior, or documentation review that cannot be meaningfully automated.

### AC3.1: install.sh prompts for only port, username, and password

**Justification:** install.sh is an interactive bash script. Testing its prompt sequence requires a real terminal or an `expect`-style harness. The prompts involve reading stdin, displaying text, and masking password input. Shell script unit testing frameworks are not part of this project's test infrastructure, and the installer is run infrequently enough that manual verification is proportionate.

**Verification approach:**
1. Run `install.sh` in a test directory (without Docker running, just to observe prompts): `bash install.sh` and verify exactly 3 prompts appear: port, username, password.
2. Run `install.sh -y` with `UB_PORT=9999 UB_USERNAME=admin UB_PASSWORD=test` and verify it runs non-interactively with no prompts.
3. Syntax check: `bash -n install.sh` passes with no errors.
4. Review generated `docker-compose.yml` to verify only bootstrap env vars are present (supplements AC3.2 automated grep).

### AC4.3: install.sh does not default to /mnt/supernote or reference Supernote in prompts

**Justification:** Requires reading the install.sh source or running it interactively to verify prompt text and default values. This is a textual/behavioral property of a shell script, not a Go function that can be unit-tested.

**Verification approach:**
1. `grep -i "supernote\|/mnt/supernote\|DEFAULT_SUPERNOTE_DIR" install.sh` returns no matches.
2. `grep -i "supernote" rebuild.sh` returns no matches in default paths or help text (Supernote references in comments explaining the SPC integration are acceptable).
3. Run install.sh and read all prompt text -- no prompt mentions "Supernote" or assumes a Supernote directory.

### AC4.4: README and CLAUDE.md updated to reflect platform-neutral config and source model

**Justification:** Documentation correctness is a human judgment. Automated checks can verify the absence of removed env var names, but cannot verify that the documentation accurately and completely describes the new architecture, is internally consistent, and is helpful to a reader.

**Verification approach:**
1. Automated pre-check: `grep -c "UB_NOTES_PATH\|UB_BACKUP_PATH\|UB_BOOX_ENABLED\|UB_BOOX_NOTES_PATH" README.md CLAUDE.md` returns 0 for removed env vars (except in historical/migration context if applicable).
2. Manual review of README.md:
   - Project description is platform-neutral (not "Supernote sidecar")
   - Installation section describes 3-prompt installer + Settings UI for configuration
   - Configuration section documents bootstrap env vars (UB_DB_PATH, UB_LISTEN_ADDR) and Settings UI as primary interface
   - Source model (Supernote, Boox as equal plugins) is documented
   - No hardcoded `/mnt/supernote` paths in examples
3. Manual review of CLAUDE.md:
   - Project Structure includes `internal/appconfig/`, `internal/source/`, `internal/source/supernote/`, `internal/source/boox/`
   - `internal/config/` is removed from project structure
   - Config section documents two-stage loading (bootstrap env vars + settings table)
   - Notes Pipeline section documents Source abstraction
   - Build & Test commands still correct
   - Last verified date updated

---

## Test Coverage Summary

| Category | Total ACs | Automated | Human Verification |
|----------|-----------|-----------|-------------------|
| AC1: SQLite config | 8 | 8 | 0 |
| AC2: Source abstraction | 8 | 8 | 0 |
| AC3: Installer/first-boot | 6 | 4 | 2 (AC3.1, partial AC3.2) |
| AC4: Platform-neutral naming | 4 | 2 | 2 (AC4.3, AC4.4) |
| **Total** | **26** | **22** | **4** |

All 26 acceptance criteria are covered. The 4 human-verification items are shell script interaction (AC3.1, AC4.3) and documentation review (AC4.4), which are inherently non-automatable within the project's Go test infrastructure.

---

## Implementation Notes

- **Test database pattern:** All unit tests use `notedb.Open(ctx, ":memory:")` for real in-memory SQLite. No mocks for database access. Matches `internal/taskdb/store_test.go` and `internal/notedb/db_test.go`.
- **Env var test pattern:** Tests exercising env var override use `os.Setenv`/`os.Unsetenv` with `defer` cleanup. Matches `internal/config/config_pipeline_test.go`.
- **HTTP test pattern:** Web handler tests use `httptest.NewRequest` + `httptest.NewRecorder` with `testHandler()` helper. Matches `internal/web/routes_test.go`.
- **Filesystem tests:** Source adapter lifecycle tests use `t.TempDir()` for NotesPath to satisfy fsnotify requirements.
- **Factory closure pattern:** The `source.Factory` signature is `func(db, row, deps) (Source, error)`. Supernote and Boox constructors have additional parameters wrapped via closures in main.go (Phase 5). Tested via `Registry.Create` in `internal/source/source_test.go`.
