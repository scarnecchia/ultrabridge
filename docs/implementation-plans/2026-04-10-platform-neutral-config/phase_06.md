# Platform-Neutral Configuration Implementation Plan

**Goal:** Expand the Settings tab to manage all config and sources via JSON API endpoints, with restart-required banner and config_dirty health check flag.

**Architecture:** New JSON API endpoints (`/api/config`, `/api/sources`) follow the existing pattern in `internal/web/api.go`. The Settings page template adds general settings form, sources list with CRUD, and a restart-required banner. The handler stores the running config (loaded at startup) and compares it against DB values to detect config drift. `/health` gains a `config_dirty` flag.

**Tech Stack:** Go stdlib, `html/template`, `encoding/json`, existing `internal/appconfig`, `internal/source`

**Scope:** 8 phases from original design (this is phase 6 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC1: SQLite-backed configuration system
- **platform-neutral-config.AC1.4 Success:** Settings UI displays current config values and allows editing all non-bootstrap settings
- **platform-neutral-config.AC1.5 Success:** Password change via Settings UI hashes with bcrypt and stores hash in settings table
- **platform-neutral-config.AC1.6 Failure:** Saving a config change to a restart-required key shows "restart required" banner
- **platform-neutral-config.AC1.7 Failure:** `/health` endpoint returns `config_dirty: true` when running config differs from DB

---

## Codebase Verification Findings

- ✓ Existing settings UI at `/home/sysop/src/ultrabridge/internal/web/handler.go:358-506` — POST `/settings/save` with `section` param, uses `notedb.SetSetting()`, redirect after save
- ✓ Existing JSON API pattern in `/home/sysop/src/ultrabridge/internal/web/api.go:22-220` — `apiError()` helper, `json.NewEncoder(w).Encode()`, routes at `/api/*`
- ✓ Templates embedded via `//go:embed templates` at handler.go:37-38, single `templates/index.html` file
- ✓ Health endpoint at `cmd/ultrabridge/main.go:331-334` — returns `{"status":"ok"}`, no config_dirty flag yet
- ✓ Handler struct at handler.go:92-120 — stores all pipeline components, noteDB, template, mux, logger
- ✓ Settings routes tests at `internal/web/routes_test.go:70-100` — `testHandler()` helper, `httptest.NewRequest/Recorder`
- ✓ MCP token management at handler.go:1533-1581 — handleMCPTokenCreate, handleMCPTokenRevoke — stays unchanged
- ✓ Setting key constants at handler.go:344-352 — will migrate to `appconfig.Key*` constants

**Testing approach:** Follow existing route test pattern from `routes_test.go`. Use `testHandler()` with in-memory SQLite. Test API endpoints with `httptest.NewRequest` + `httptest.NewRecorder`. See `/home/sysop/src/ultrabridge/internal/web/CLAUDE.md`.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add GET/PUT `/api/config` endpoints

**Verifies:** platform-neutral-config.AC1.4, platform-neutral-config.AC1.5, platform-neutral-config.AC1.6, platform-neutral-config.AC1.7

**Files:**
- Modify: `internal/web/handler.go` (add handler fields for running config, register new routes)
- Create: `internal/web/config_api.go` (new file for config API handlers)
- Modify: `cmd/ultrabridge/main.go` (pass running config to handler, update /health endpoint)

**Implementation:**

**Handler changes:**
- Add `runningConfig *appconfig.Config` field to Handler struct — stores the config loaded at startup. Used to detect config drift.
- Add `appDB *sql.DB` field if not already available (handler already has `noteDB` which is the same DB)
- Register new routes in handler setup

**`config_api.go`:**

```go
// GET /api/config — returns current config with secrets redacted
func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
    cfg, err := appconfig.Load(r.Context(), h.noteDB)
    // ... error handling ...
    // Redact secrets: PasswordHash → "[set]" or "[not set]", OCRAPIKey → "[set]" or "[not set]"
    json.NewEncoder(w).Encode(cfg)
}

// PUT /api/config — update config, returns changed keys + restart flag
func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
    var incoming appconfig.Config
    json.NewDecoder(r.Body).Decode(&incoming)
    
    // Special handling for password: if plaintext password provided,
    // hash with bcrypt before saving (AC1.5)
    // The incoming JSON uses a "password" field (plaintext);
    // if non-empty, hash it and set PasswordHash field
    
    result, err := appconfig.Save(r.Context(), h.noteDB, &incoming)
    // ... error handling ...
    json.NewEncoder(w).Encode(result) // {changed_keys: [...], restart_required: bool}
}
```

**Password handling (AC1.5):** The PUT `/api/config` accepts an optional `password` field in the JSON body. If non-empty, hash with `golang.org/x/crypto/bcrypt` (already a dependency — used by `internal/auth/`) and set the `PasswordHash` field before calling `appconfig.Save()`. The plaintext password is never stored.

**Health endpoint update (AC1.7):** Use a `sync/atomic.Bool` dirty flag rather than `reflect.DeepEqual` (which is fragile with float precision, pointer semantics, and added fields). The flag is set by the PUT `/api/config` handler when `Save()` returns `RestartRequired: true`:

```go
// Handler stores a shared dirty flag:
var configDirty atomic.Bool

// In PUT /api/config handler, after Save():
if result.RestartRequired {
    configDirty.Store(true)
}

// In /health handler:
json.NewEncoder(w).Encode(map[string]interface{}{
    "status": "ok",
    "config_dirty": configDirty.Load(),
})
```

**Testing:**

- platform-neutral-config.AC1.4: GET `/api/config` returns JSON with current config values; secrets are redacted
- platform-neutral-config.AC1.5: PUT `/api/config` with `{"password": "newpass"}` → GET `/api/config` shows `password_hash: "[set]"`; verify bcrypt hash in DB via `notedb.GetSetting`
- platform-neutral-config.AC1.6: PUT `/api/config` with a restart-required key changed → response includes `restart_required: true`; PUT with a non-restart key → `restart_required: false`
- platform-neutral-config.AC1.7: GET `/health` after PUT `/api/config` changes a key → `config_dirty: true`; before any changes → `config_dirty: false`

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/web/`
Expected: All tests pass

**Commit:** `feat(web): add GET/PUT /api/config with restart detection and config_dirty health flag`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Add CRUD `/api/sources` endpoints

**Verifies:** platform-neutral-config.AC1.4

**Files:**
- Create: `internal/web/sources_api.go` (new file for sources API handlers)

**Implementation:**

Follow the same JSON API pattern from `api.go`. All routes require Basic Auth (handled by existing middleware).

```go
// GET /api/sources — list configured sources
func (h *Handler) handleListSources(w http.ResponseWriter, r *http.Request) {
    rows, err := source.ListSources(r.Context(), h.noteDB)
    json.NewEncoder(w).Encode(rows)
}

// POST /api/sources — add a source
func (h *Handler) handleAddSource(w http.ResponseWriter, r *http.Request) {
    var row source.SourceRow
    json.NewDecoder(r.Body).Decode(&row)
    // Validate: type must be non-empty, name must be non-empty
    id, err := source.AddSource(r.Context(), h.noteDB, row)
    json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

// PUT /api/sources/{id} — update a source
func (h *Handler) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
    // Parse ID from URL path
    // Decode body into SourceRow
    source.UpdateSource(r.Context(), h.noteDB, row)
}

// DELETE /api/sources/{id} — remove a source
func (h *Handler) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
    // Parse ID from URL path
    source.RemoveSource(r.Context(), h.noteDB, id)
}
```

**URL path parsing:** Use `r.PathValue("id")` (Go 1.22+ routing) or `strings.TrimPrefix(r.URL.Path, "/api/sources/")` to extract the source ID. Match the existing routing pattern in the handler's mux setup.

**Input validation:** All mutating endpoints validate before writing to DB:
- `type` must be non-empty string
- `name` must be non-empty string
- `config_json` must be valid JSON (use `json.Valid([]byte(row.ConfigJSON))`) — reject malformed JSON with 400 error
- `enabled` must be 0 or 1 (handled by Go bool → int conversion)
- Unknown source types are accepted in the DB (they'll be skipped at startup per AC2.7) — the API does not enforce that type matches a registered factory. This keeps the API decoupled from the registry.

**Testing:**

- POST `/api/sources` with `{"type":"supernote","name":"My Notes","config_json":"{\"notes_path\":\"/data/notes\"}"}` → 200 with `{"id": N}`
- GET `/api/sources` returns array including the new source
- PUT `/api/sources/N` with updated name → GET shows updated name
- DELETE `/api/sources/N` → GET no longer includes deleted source
- POST with empty type → error response
- POST with empty name → error response

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/web/`
Expected: All tests pass

**Commit:** `feat(web): add CRUD /api/sources endpoints`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
<!-- START_TASK_3 -->
### Task 3: Update Settings template — general config form and restart banner

**Verifies:** platform-neutral-config.AC1.4, platform-neutral-config.AC1.6

**Files:**
- Modify: `internal/web/templates/index.html` (add general settings section, restart banner)
- Modify: `internal/web/handler.go` (pass config data to template, handle restart banner state)

**Implementation:**

Expand the Settings tab in the template. Currently it has Supernote/Boox/MCP sections. Add a new "General Settings" section at the top and a "restart required" banner.

**General Settings section** — organized by concern matching the design:
1. **Authentication:** Username (text), Password (password input — plaintext, hashed server-side)
2. **OCR:** Provider format (select: anthropic/openai), API URL, API Key, Model, Concurrency, Max File MB
3. **Embedding:** Ollama URL, Embed Model
4. **Chat:** Chat API URL, Chat Model
5. **Supernote Sync:** Enabled toggle, Interval (seconds), API URL, Account, Password

Each section uses the existing form POST pattern (`/settings/save` with `section` param) OR uses JavaScript to PUT to `/api/config`. The simplest approach consistent with the existing codebase is to keep the form POST pattern for the template and have JS-powered sections use the API.

**Restart-required banner:**
- Template includes a hidden banner div at the top of Settings
- Handler checks `config_dirty` by comparing running config to DB config
- If dirty, template data includes `RestartRequired: true`, banner is shown
- Banner text: "Configuration has changed. Restart the application for changes to take effect."
- Banner persists across page loads (it's driven by DB comparison, not session state)

**Template data updates:** The handler's settings page method adds new fields to template data:
- `Config` — current appconfig.Config (for pre-populating form fields)
- `RestartRequired` — bool from config drift comparison

**Testing:**

- platform-neutral-config.AC1.4: GET `/settings` returns HTML containing form fields for OCR, Embedding, Chat, Sync config with current values pre-populated
- platform-neutral-config.AC1.6: After changing a restart-required key, GET `/settings` HTML contains "restart required" banner text

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/web/`
Expected: All tests pass

**Commit:** `feat(web): add general settings form and restart-required banner to Settings tab`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Update Settings template — sources list with add/edit/remove

**Verifies:** platform-neutral-config.AC1.4

**Files:**
- Modify: `internal/web/templates/index.html` (add sources management section)
- Modify: `internal/web/handler.go` (pass sources data to template)

**Implementation:**

Add a "Sources" section to the Settings tab, between General Settings and MCP Tokens.

**Sources list:**
- Table showing: Name, Type, Enabled toggle, Edit button, Remove button
- "Add Source" button opens a form

**Add/Edit form:**
- Type dropdown: supernote, boox
- Name text field
- Enabled checkbox
- Type-specific config fields rendered dynamically based on selected type:
  - Supernote: Notes Path, Backup Path
  - Boox: Notes Path
- Save/Cancel buttons

**JavaScript interaction:** The sources section uses the `/api/sources` CRUD endpoints via `fetch()`. This follows the API-first guidance from the project design.

**Template data:** Handler passes `Sources []source.SourceRow` to the template.

**Migrate existing setting key constants:** Move `SettingKeySN*` and `SettingKeyBoox*` constants from `handler.go:344-352` to use `appconfig.Key*` constants from the appconfig package. Update all references in handler.go.

**Testing:**

- GET `/settings` returns HTML containing sources list section
- Sources section shows configured sources from DB
- Template renders correctly with zero sources (empty state)

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/web/`
Expected: All tests pass

**Commit:** `feat(web): add sources management UI to Settings tab`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->
