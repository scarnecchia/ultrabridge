# Platform-Neutral Configuration Design

## Summary

UltraBridge currently treats Supernote as a first-class assumption baked into its configuration: environment variables reference Supernote-specific paths, variable names in code carry `sn` prefixes for generic functionality, and the installer hard-codes Supernote directory defaults. This design removes those assumptions by moving almost all application configuration from environment variables into a SQLite-backed settings store managed through the web UI, and by introducing a `Source` abstraction that treats Supernote, Boox, and any future note-ingestion platform as equal, interchangeable plugins.

The approach builds directly on patterns already in the codebase rather than introducing new ones. The existing `settings` key-value table in notedb is extended to hold all application config (OCR credentials, RAG/chat endpoints, auth). A new `Source` interface — modelled on the `Start(ctx)/Stop()` lifecycle both existing pipelines already follow — wraps each pipeline behind a common contract, with per-source configuration stored as JSON in a new `sources` table. The installer shrinks to three prompts and a Docker image build; everything else is configured through the Settings UI after first launch. Existing installs upgrade seamlessly because `appconfig.Load()` falls back to environment variables when the database has no values, so no migration step is required.

## Definition of Done

1. **SQLite-backed configuration system** — A new domain package manages all application config in SQLite with env-var override support. The Settings UI becomes the primary configuration interface, replacing the interactive installer prompts. Only DB path, listen address, and port remain as env vars.

2. **Unified Source abstraction** — A common Source interface that Supernote, Boox, and future ingestion platforms implement. Sources are configured and registered via the Settings UI, with a consistent lifecycle (enable/disable/configure requires restart).

3. **Simplified installer** — install.sh reduces to: prompt for port + credentials, build Docker image, generate minimal compose file, start container. All other configuration happens through the web UI after first launch.

4. **Platform-neutral naming** — Generic env vars (UB_NOTES_PATH), code references (snNotesPath), and UI copy no longer assume Supernote as the default. Each platform is an equal citizen.

**Out of scope:** Task sync refactoring, SPC MariaDB catalog sync changes, Docker healthcheck, hot-reload of pipelines, Viwoods-specific implementation.

## Acceptance Criteria

### platform-neutral-config.AC1: SQLite-backed configuration system
- **platform-neutral-config.AC1.1 Success:** `appconfig.Load()` reads all config keys from settings table and returns typed Config struct
- **platform-neutral-config.AC1.2 Success:** `appconfig.Save()` writes changed keys to settings table and returns list of changed keys
- **platform-neutral-config.AC1.3 Success:** Env var set for a config key overrides the DB value in loaded Config
- **platform-neutral-config.AC1.4 Success:** Settings UI displays current config values and allows editing all non-bootstrap settings
- **platform-neutral-config.AC1.5 Success:** Password change via Settings UI hashes with bcrypt and stores hash in settings table
- **platform-neutral-config.AC1.6 Failure:** Saving a config change to a restart-required key shows "restart required" banner
- **platform-neutral-config.AC1.7 Failure:** `/health` endpoint returns `config_dirty: true` when running config differs from DB
- **platform-neutral-config.AC1.8 Edge:** First boot with no DB values falls back to env vars for all config keys

### platform-neutral-config.AC2: Unified Source abstraction
- **platform-neutral-config.AC2.1 Success:** Source interface defines Type(), Name(), Start(ctx), Stop() contract
- **platform-neutral-config.AC2.2 Success:** Supernote source adapter starts/stops the existing processor pipeline via Source interface
- **platform-neutral-config.AC2.3 Success:** Boox source adapter starts/stops the existing boox pipeline via Source interface
- **platform-neutral-config.AC2.4 Success:** Sources table CRUD works — add, update, enable/disable, remove sources via API
- **platform-neutral-config.AC2.5 Success:** main.go iterates enabled sources from DB, creates via factory, starts each, defers Stop()
- **platform-neutral-config.AC2.6 Success:** Source-specific config stored as JSON in config_json column, parsed by each factory
- **platform-neutral-config.AC2.7 Failure:** Unknown source type in DB logs warning and is skipped, does not crash startup
- **platform-neutral-config.AC2.8 Failure:** Source with invalid config_json logs error and is skipped, does not crash startup

### platform-neutral-config.AC3: Simplified installer and first-boot
- **platform-neutral-config.AC3.1 Success:** install.sh prompts for only port, username, and password
- **platform-neutral-config.AC3.2 Success:** Generated compose file contains only bootstrap env vars (UB_DB_PATH, UB_LISTEN_ADDR)
- **platform-neutral-config.AC3.3 Success:** Fresh container with no auth in DB shows setup page without requiring auth
- **platform-neutral-config.AC3.4 Success:** Saving credentials on setup page ends setup mode and enforces Basic Auth
- **platform-neutral-config.AC3.5 Success:** Existing install with .ultrabridge.env works on upgrade — env vars provide all config
- **platform-neutral-config.AC3.6 Failure:** Setup mode only exposes credential setup — no data endpoints accessible without auth

### platform-neutral-config.AC4: Platform-neutral naming
- **platform-neutral-config.AC4.1 Success:** UB_NOTES_PATH, UB_BACKUP_PATH, UB_BOOX_ENABLED, UB_BOOX_NOTES_PATH env vars removed — replaced by per-source config
- **platform-neutral-config.AC4.2 Success:** No handler field or variable named with `sn` prefix for generic functionality
- **platform-neutral-config.AC4.3 Success:** install.sh does not default to /mnt/supernote or reference Supernote in prompts
- **platform-neutral-config.AC4.4 Success:** README and CLAUDE.md updated to reflect platform-neutral config and source model

## Glossary

- **appconfig**: The new `internal/appconfig` package proposed by this design. Owns loading and saving all application configuration from the SQLite settings table, with environment-variable override support.
- **Basic Auth**: HTTP Basic Authentication — username and password sent (base64-encoded) with each request. UltraBridge uses bcrypt-hashed passwords for the single-user auth layer.
- **bcrypt**: A password-hashing function designed to be slow and resistant to brute-force attacks. UltraBridge stores the password hash rather than the plaintext password.
- **bootstrap env vars**: The small set of environment variables (`UB_DB_PATH`, `UB_LISTEN_ADDR`, `UB_PORT`) that must remain as env vars because they are needed before the database — and therefore the settings table — can be opened.
- **Boox**: An e-ink device brand (BOOX by Onyx) that produces `.note` files in a ZIP+protobuf format. One of the two note-ingestion platforms UltraBridge currently supports alongside Supernote.
- **config_dirty**: A flag exposed on the `/health` endpoint. Set to `true` when the configuration loaded at startup differs from what is currently stored in the database, indicating a restart is needed for changes to take effect.
- **config_json**: A TEXT column in the `sources` table holding type-specific source configuration as a JSON object. Each source adapter parses this column into its own typed struct.
- **Engine.IO**: A real-time communication protocol (used in `internal/sync`) that UltraBridge uses to receive push notifications from the Supernote Private Cloud (SPC).
- **factory pattern**: A function that takes a database row and shared dependencies and returns a constructed object. Used here so `main.go` can create Source implementations by type name without importing each adapter directly.
- **FTS5**: SQLite's fifth-generation full-text search extension. UltraBridge uses FTS5 tables (`note_fts`) for keyword search across indexed note content.
- **KV table / settings table**: A simple `(key TEXT, value TEXT)` table in SQLite used as a key-value store. Already exists in notedb via `internal/notedb/settings.go`; this design extends it to hold all application config.
- **MCP (Model Context Protocol)**: An open protocol for exposing tools and resources to AI agents. UltraBridge runs an MCP server (`cmd/ub-mcp`) that exposes note search and retrieval tools.
- **notedb**: The SQLite database file used by the notes pipeline (managed by `internal/notedb`). Holds note metadata, job queue, FTS index, embeddings, chat sessions, and — after this design — the settings and sources tables.
- **Ollama**: A local inference server for running language models. UltraBridge uses it to generate embeddings for the RAG retrieval pipeline.
- **RAG (Retrieval-Augmented Generation)**: A technique that retrieves relevant documents from a knowledge base and injects them as context into a language model prompt. UltraBridge's chat subsystem uses RAG over indexed note content.
- **restart-required key**: A configuration key where a change cannot take effect without restarting the application (e.g., auth credentials, pipeline paths, feature flags). The Settings UI shows a banner when any such key is saved.
- **setup mode**: A first-boot state where no auth credentials exist in the database. In setup mode, only the credential-setup page is accessible without authentication; all data endpoints are blocked.
- **SharedDeps**: A struct (`source.SharedDeps`) that bundles infrastructure shared across all source adapters — indexer, embedder, embedding store, OCR client, and logger — so each factory receives them via a single argument.
- **Source / Source interface**: The new `internal/source` abstraction proposed by this design. Any note-ingestion platform (Supernote, Boox, future devices) implements `Type()`, `Name()`, `Start(ctx)`, and `Stop()` to participate in the unified lifecycle.
- **SPC (Supernote Private Cloud)**: Supernote's self-hosted cloud service, which UltraBridge integrates with for task sync and file catalog updates. Runs a MariaDB database and an Engine.IO-based push notification system.
- **Supernote**: An e-ink note-taking device brand whose `.note` files and Private Cloud API UltraBridge was originally built around. After this design, Supernote becomes one source among many rather than the assumed default.
- **vLLM**: An inference engine for large language models with an OpenAI-compatible API. UltraBridge's chat subsystem proxies streaming responses from vLLM to the browser.
- **WAL mode**: SQLite's Write-Ahead Logging mode. Allows concurrent readers alongside a single writer, which is why `internal/notedb` sets `MaxOpenConns=1` to enforce the single-writer constraint.
- **WebDAV**: A protocol extension to HTTP for collaborative file management. UltraBridge exposes a WebDAV endpoint for Boox devices to upload `.note` files.

## Architecture

### Config Layer (`internal/appconfig`)

New domain package replacing `internal/config`. All application configuration lives in the existing `settings` KV table in notedb, with env-var override support.

**Load path:** `appconfig.Load(ctx, db)` reads all keys from the `settings` table, parses them into a typed `Config` struct, then checks env vars. If an env var is set for a given key, it wins over the DB value. The Config struct groups fields by concern: Auth, OCR, RAG, Chat, Sync.

**Save path:** `appconfig.Save(ctx, db, cfg)` writes changed settings to the DB and returns the list of changed keys. A set of keys is flagged as "requires restart" (auth credentials, pipeline paths, feature flags). The web handler compares changed keys against this set to show a restart banner.

**Bootstrap env vars (remain as env vars, not in DB):**
- `UB_DB_PATH` — needed to open the DB that holds config
- `UB_LISTEN_ADDR` — needed before config is loaded
- `UB_PORT` — host-side port mapping (compose-level, not app config)

**Runtime-readable settings:** The existing closure pattern (`func() string` / `func() bool`) stays for settings that take effect immediately without restart (OCR prompts, injection toggle, to-do extraction toggle). These read directly from the KV table at job-processing time, bypassing the Config struct.

**Env-var fallback on first boot:** If the settings table has no auth keys, `Load()` populates all values from env vars. This provides seamless upgrade for existing installs — `.ultrabridge.env` values work on first boot, then the user manages via Settings UI going forward.

### Sources Layer (`internal/source`)

New package defining the Source interface and factory pattern.

**Source interface:**

```go
type Source interface {
    Type() string
    Name() string
    Start(ctx context.Context) error
    Stop()
}
```

Thin lifecycle interface — each implementation manages its own job queue, workers, and file watching internally.

**Factory pattern:**

```go
type Factory func(db *sql.DB, row SourceRow, deps SharedDeps) (Source, error)

type SharedDeps struct {
    Indexer    processor.Indexer
    Embedder   rag.Embedder
    EmbedModel string
    EmbedStore rag.EmbedStore
    OCRClient  interface{}  // shared OCR client
    Logger     *slog.Logger
}
```

`SharedDeps` bundles infrastructure that all sources need. Each factory unpacks `config_json` from the `SourceRow` into its type-specific struct and constructs the pipeline.

**Registration:** Explicit map in main.go, no `init()` magic:

```go
factories := map[string]source.Factory{
    "supernote": supernote.NewSource,
    "boox":      boox.NewSource,
}
```

main.go queries the `sources` table, iterates enabled rows, calls the matching factory, starts each source, and defers `Stop()`.

**Sources table schema:**

```sql
CREATE TABLE IF NOT EXISTS sources (
    id          INTEGER PRIMARY KEY,
    type        TEXT NOT NULL,
    name        TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
)
```

`config_json` holds type-specific settings as JSON. For Supernote: `{"notes_path": "...", "backup_path": "...", "inject_enabled": true}`. For Boox: `{"notes_path": "...", "cache_path": "...", "todo_enabled": true}`.

### Settings UI

The Settings tab becomes the primary configuration interface, organized by concern:

1. **General Settings** — auth (username, password change), OCR provider config (format, API URL, key, model), RAG/embedding config (Ollama URL, model), chat config (vLLM URL, model), Supernote device sync config
2. **Sources** — list of configured sources with type, name, enabled toggle, edit/remove. "Add Source" opens a form with type dropdown and type-specific fields rendered dynamically.
3. **MCP Tokens** — unchanged from current implementation

**API-first design (per project guidance):**

```
GET    /api/config         — current config (secrets redacted)
PUT    /api/config         — update config, returns changed keys + restart flag
GET    /api/sources        — list configured sources
POST   /api/sources        — add a source
PUT    /api/sources/{id}   — update a source
DELETE /api/sources/{id}   — remove a source
```

**"Restart Required" banner:** Appears when a save changes any restart-required key. Persists across page loads by comparing running config (loaded at startup) against current DB values. The `/health` endpoint exposes a `config_dirty` flag.

**Password handling:** Password change takes plaintext in the UI, hashes with bcrypt server-side, stores hash in settings table. Replaces the `docker run ultrabridge hash-password` workflow.

### First-Boot and Upgrade Flow

**New install (first boot):**
1. Container starts with only bootstrap env vars
2. `appconfig.Load()` finds no auth keys — app enters setup mode
3. Setup mode: only Settings page accessible, no auth required
4. User sets credentials, configures OCR, adds sources
5. First save of auth credentials ends setup mode, Basic Auth enforced
6. Container restart starts configured pipelines

**Upgrade from existing install:**
1. Container starts with existing `.ultrabridge.env` env vars
2. `appconfig.Load()` finds no auth keys in DB, falls back to env vars
3. App works immediately — env vars provide everything
4. User can visit Settings to see config (populated from env var fallback)
5. Saving from Settings writes to DB; env vars still override if present
6. Old `.ultrabridge.env` removable at user's leisure

### Simplified Installer

`install.sh` reduces to:
1. Pre-flight: check Docker + Docker Compose
2. Prompt for: port, username, password (3 prompts)
3. Build Docker image
4. Generate minimal compose file (only `UB_DB_PATH`, `UB_LISTEN_ADDR`, volume mounts)
5. Start container, health check, print Settings URL

Password hash written to settings table on first boot via init command or seed endpoint after container is healthy.

`rebuild.sh` stays largely unchanged — build + recreate + health check.

Unattended mode (`-y`) reads `UB_USERNAME`, `UB_PASSWORD`, `UB_PORT` from env.

### Platform-Neutral Naming

| Current | New | Reason |
|---------|-----|--------|
| `UB_NOTES_PATH` | Removed — per-source `config_json` | Not a global setting |
| `UB_BACKUP_PATH` | Removed — per-source `config_json` | Not a global setting |
| `UB_BOOX_ENABLED` | Removed — source enabled/disabled in DB | Per-source |
| `UB_BOOX_NOTES_PATH` | Removed — per-source `config_json` | Not a global setting |
| `UB_OCR_*` | Settings table keys | Shared infrastructure |
| `UB_SN_SYNC_*` | Settings table keys | Supernote-specific, SN prefix accurate |
| `UB_EMBED_*` / `UB_CHAT_*` | Settings table keys | Shared infrastructure |
| `snNotesPath` (handler) | Removed — handler reads from source | No Supernote default |
| `DEFAULT_SUPERNOTE_DIR` | Removed — no default install dir assumption | UB is independent |
| Install dir `/mnt/supernote` | User's choice via prompt | UB is independent |

**Stays Supernote-specific (correctly named):**
- `internal/tasksync/supernote/` — adapter for a specific device
- `internal/sync/` — Engine.IO notifier for SPC
- `UB_SN_SYNC_*` settings — Supernote sync config, SN prefix is accurate
- SPC MariaDB catalog sync — out of scope

## Existing Patterns

**Settings KV table:** `internal/notedb/settings.go` already implements `GetSetting`/`SetSetting` with upsert semantics on a `(key TEXT, value TEXT)` table. The new `appconfig` package builds on this exact pattern — same table, same access functions, more keys.

**Runtime callback pattern:** Both `processor.WorkerConfig` and `booxpipeline.WorkerConfig` use `func() string` / `func() bool` closures that read from the settings table at job-processing time. This pattern is preserved unchanged for settings that take effect immediately.

**Processor lifecycle:** Both Supernote (`processor.Store`) and Boox (`booxpipeline.Processor`) follow `Start(ctx)/Stop()` with defer-based shutdown. The new Source interface mirrors this exactly — `Start(ctx) error` / `Stop()`.

**Shared interfaces:** Both pipelines already share `processor.Indexer`, `rag.Embedder`, and `rag.EmbedStore` interfaces. The `SharedDeps` struct in the source package formalizes this existing dependency injection pattern.

**Web handler nil-safety:** The current handler accepts nil for all optional components (syncProvider, booxStore, chatHandler, etc.). This pattern extends naturally — the handler receives sources and checks type/nil before rendering source-specific UI.

**API-first guidance:** `.ed3d/design-plan-guidance.md` requires domain logic in packages with thin HTTP wrappers. The `appconfig` and `source` packages own the logic; web handlers are thin CRUD wrappers over them.

**No new patterns introduced.** This design extends existing patterns (KV settings, processor lifecycle, shared interface injection, nil-safe handler) rather than introducing unfamiliar abstractions.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Config Domain Package
**Goal:** Create `internal/appconfig` package that loads and saves config from/to the settings KV table with env-var override support.

**Components:**
- `internal/appconfig/config.go` — Config struct with typed fields grouped by concern (Auth, OCR, RAG, Chat, Sync), Load/Save functions
- `internal/appconfig/keys.go` — constants for all setting keys and the restart-required set
- `internal/appconfig/env.go` — env-var overlay logic (check env, override DB value if set)
- `internal/notedb/schema.go` — migration to seed default setting keys (idempotent)

**Dependencies:** None (first phase)

**Done when:** Config can be loaded from DB with env-var override, saved back to DB, and changed keys identified. Tests verify Load/Save round-trip, env-var precedence, and restart-required detection. Covers `platform-neutral-config.AC1.*`.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Source Interface and Registry
**Goal:** Define the Source interface, factory pattern, sources table, and CRUD operations.

**Components:**
- `internal/source/source.go` — Source interface, Factory type, SharedDeps struct, SourceRow type
- `internal/source/registry.go` — Registry that holds factories and creates sources from DB rows
- `internal/notedb/schema.go` — migration to create `sources` table

**Dependencies:** Phase 1 (config package exists)

**Done when:** Sources can be registered, created from DB rows, and CRUD operations work against the sources table. Tests verify factory dispatch, JSON config parsing, and source lifecycle. Covers `platform-neutral-config.AC2.*`.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: Supernote Source Adapter
**Goal:** Wrap existing Supernote pipeline (processor, notestore, pipeline watcher) behind the Source interface.

**Components:**
- `internal/source/supernote/source.go` — implements Source interface, constructs processor.Store + pipeline.Pipeline internally
- `internal/source/supernote/config.go` — type-specific config struct (notes_path, backup_path, inject_enabled, ocr_prompt)

**Dependencies:** Phase 2 (Source interface exists)

**Done when:** Supernote pipeline starts and stops via Source interface. Existing processor behavior unchanged. Tests verify lifecycle and config unmarshalling. Covers `platform-neutral-config.AC2.*` (Supernote-specific).
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: Boox Source Adapter
**Goal:** Wrap existing Boox pipeline (booxpipeline.Processor, WebDAV handler) behind the Source interface.

**Components:**
- `internal/source/boox/source.go` — implements Source interface, constructs booxpipeline.Processor internally
- `internal/source/boox/config.go` — type-specific config struct (notes_path, cache_path, todo_enabled, import_path)

**Dependencies:** Phase 2 (Source interface exists)

**Done when:** Boox pipeline starts and stops via Source interface. Existing pipeline behavior unchanged. Tests verify lifecycle and config unmarshalling. Covers `platform-neutral-config.AC2.*` (Boox-specific).
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: main.go Rewire
**Goal:** Replace the current per-pipeline wiring in main.go with source registry iteration. Remove `internal/config` usage in favor of `internal/appconfig`.

**Components:**
- `cmd/ultrabridge/main.go` — load config via appconfig, query sources table, create via registry, start/defer-stop, pass to web handler
- `internal/config/` — deprecated or removed; all callers migrated to appconfig

**Dependencies:** Phases 1, 3, 4 (config package and both source adapters exist)

**Done when:** Application starts with config from DB + env overlay, pipelines run via Source interface. Build succeeds, existing integration tests pass.
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: Settings UI and Config API
**Goal:** Expand the Settings tab to manage all config and sources via JSON API endpoints.

**Components:**
- `internal/web/handler.go` — new API endpoints: GET/PUT `/api/config`, CRUD `/api/sources`
- `internal/web/handler.go` — Settings page template updates: general settings form, sources list with add/edit/remove, restart-required banner
- `internal/web/templates/` — updated settings template with dynamic source forms

**Dependencies:** Phase 5 (main.go uses appconfig and source registry)

**Done when:** All config manageable via Settings UI, restart banner shows when needed, sources can be added/edited/removed. Tests verify API endpoints and restart detection. Covers `platform-neutral-config.AC1.*` (UI), `platform-neutral-config.AC3.*`.
<!-- END_PHASE_6 -->

<!-- START_PHASE_7 -->
### Phase 7: First-Boot Setup Mode
**Goal:** When no auth credentials exist in DB, serve an unauthenticated setup page for initial configuration.

**Components:**
- `internal/web/handler.go` — setup mode detection (no auth keys in DB), middleware bypass for setup page
- `internal/web/templates/` — setup page template (credentials + initial config)
- `internal/appconfig/config.go` — `IsSetupRequired()` function

**Dependencies:** Phase 6 (Settings UI exists)

**Done when:** Fresh container shows setup page, credentials save ends setup mode, subsequent requests require auth. Tests verify setup detection, auth bypass, and transition to normal mode. Covers `platform-neutral-config.AC3.*`.
<!-- END_PHASE_7 -->

<!-- START_PHASE_8 -->
### Phase 8: Installer Simplification and Naming Cleanup
**Goal:** Reduce install.sh to bootstrap-only, update rebuild.sh, remove Supernote-centric naming from env vars and code references.

**Components:**
- `install.sh` — reduce to 3 prompts (port, username, password), minimal compose file generation, password seeding after container start
- `rebuild.sh` — update for new compose file structure
- `README.md` — update installation and configuration docs
- Platform-neutral naming changes across codebase (remove `UB_NOTES_PATH`, `UB_BOOX_ENABLED`, `snNotesPath`, `DEFAULT_SUPERNOTE_DIR`, etc.)

**Dependencies:** Phase 7 (setup mode handles first-boot config)

**Done when:** install.sh prompts for 3 values only, compose file has only bootstrap env vars, no Supernote-default naming remains in env vars or handler code. Covers `platform-neutral-config.AC4.*`.
<!-- END_PHASE_8 -->

## Additional Considerations

**Setup mode security:** The unauthenticated setup page is acceptable for a single-user, LAN-deployed service. The setup page only allows setting credentials — no data access is exposed before auth is configured. If this ever becomes a concern, a first-boot token could be printed to container logs.

**SPC MariaDB integration:** The Supernote source adapter still needs access to MariaDB for catalog sync after OCR injection. The SPC `.dbenv` connection is loaded as part of the Supernote source's type-specific config, not as global config. This keeps MariaDB as a Supernote concern, not a platform assumption.

**Config precedence documentation:** The precedence order (env var > DB value > default) should be documented in the Settings UI itself — a small note explaining that env vars override UI values when set, so power users understand why a setting might not change when they edit it.
