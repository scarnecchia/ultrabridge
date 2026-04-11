# Platform-Neutral Configuration Implementation Plan

**Goal:** Replace per-pipeline wiring in main.go with source registry iteration. Migrate from `internal/config` to `internal/appconfig`.

**Architecture:** main.go splits into two-stage config loading: bootstrap env vars (UB_DB_PATH, UB_LISTEN_ADDR, logging) read directly before DB opens, then `appconfig.Load(ctx, db)` loads everything else from SQLite + env overlay. Source registry replaces the hardcoded per-pipeline `if` blocks. `internal/config` is removed after migration.

**Tech Stack:** Go stdlib, `internal/appconfig`, `internal/source`, source adapters from Phases 3-4

**Scope:** 8 phases from original design (this is phase 5 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC2: Unified Source abstraction
- **platform-neutral-config.AC2.5 Success:** main.go iterates enabled sources from DB, creates via factory, starts each, defers Stop()

---

## Codebase Verification Findings

- ✓ **Only `cmd/ultrabridge/main.go`** imports `internal/config` (line 22) — no other Go files import it
- ✓ `config.Load()` called at main.go:67 — single entry point for all 55 config fields
- ✓ Web handler receives **constructed components**, NOT config object — already decoupled (handler.go:141)
- ✓ `cmd/ub-mcp/main.go` does NOT import internal/config — reads env vars directly; no migration needed
- ✓ `internal/config/config_pipeline_test.go` has one test: `TestLoad_PipelineDefaults` (lines 8-55)
- ✓ Supernote pipeline wiring: main.go:173-253 (notestore → search → embedder → workerCfg → processor → pipeline)
- ✓ Boox pipeline wiring: main.go:255-297 (conditional on cfg.BooxEnabled && cfg.BooxNotesPath)
- ✓ Auth middleware: main.go:321 — uses cfg.Username, cfg.PasswordHash
- ✓ RAGDisplayConfig: main.go:379-383 — uses cfg.OllamaURL, cfg.OllamaEmbedModel, cfg.ChatAPIURL, cfg.ChatModel

**Bootstrap config (needed before DB opens):**
- Logging: UB_LOG_LEVEL, UB_LOG_FORMAT, UB_LOG_FILE, etc. (main.go:73-80 — before any DB opens)
- DB paths: UB_DB_PATH (main.go:159), UB_TASK_DB_PATH (main.go:119)
- Server: UB_LISTEN_ADDR (main.go:398)
- MariaDB: UB_SUPERNOTE_DBENV_PATH and MYSQL_* env vars (main.go:86 via cfg.DSN())

**Strategy:** Logging and DB paths are read from env vars directly during bootstrap. After notedb opens, `appconfig.Load(ctx, notedb)` provides everything else with env-var fallback. This means existing deployments work unchanged — env vars still provide all config until users save via Settings UI.

**Testing approach:** This phase is primarily infrastructure (rewiring main.go). Verification is operational: build succeeds, application starts, existing integration tests pass. No new unit tests needed for main.go wiring — the unit tests are in appconfig and source packages.

---

<!-- START_TASK_1 -->
### Task 1: Refactor main.go to two-stage config loading with appconfig

**Verifies:** None (infrastructure — rewiring)

**Files:**
- Modify: `cmd/ultrabridge/main.go` (replace `config.Load()` with bootstrap env vars + `appconfig.Load()`)

**Implementation:**

Replace the single `config.Load()` call (line 67) with two stages:

**Stage 1 (before DB opens):** Read bootstrap env vars directly using helper functions. These are a small, fixed set needed before the settings table is accessible:

```go
// Bootstrap — needed before DB opens
dbPath := envOrDefault("UB_DB_PATH", "/data/ultrabridge.db")
taskDBPath := envOrDefault("UB_TASK_DB_PATH", "/data/ultrabridge-tasks.db")
listenAddr := envOrDefault("UB_LISTEN_ADDR", ":8443")
dbEnvPath := envOrDefault("UB_SUPERNOTE_DBENV_PATH", "/run/secrets/dbenv")
passwordHashPath := envOrDefault("UB_PASSWORD_HASH_PATH", "/run/secrets/ub_password_hash")

// Logging (bootstrap — needed for structured log setup before any DB work)
logLevel := envOrDefault("UB_LOG_LEVEL", "info")
logFormat := envOrDefault("UB_LOG_FORMAT", "json")
// ... remaining log vars
```

Copy the three `envOrDefault`, `envIntOrDefault`, `envBoolOrDefault` helper functions from `internal/config/config.go:216-241` into main.go as unexported helpers (they're trivial 5-line functions). This avoids importing config just for helpers.

**MariaDB credentials (bootstrap — needed for SPC catalog sync):** Copy the `loadDBEnv()` logic from `internal/config/config.go:154-208` into main.go as an unexported helper. This reads `MYSQL_DATABASE`, `MYSQL_USER`, `MYSQL_PASSWORD` from env vars first, then falls back to parsing the `.dbenv` file at `dbEnvPath`. Returns `(dbName, dbUser, dbPassword string, err error)`. The MariaDB connection is optional — if credentials aren't available, MariaDB-dependent features (SPC catalog sync, user discovery) are skipped, matching the current behavior where `config.loadDBEnv()` errors are handled at main.go:86-89.

**Password hash file (bootstrap):** Read `UB_PASSWORD_HASH` from env var. If empty, attempt to read from `passwordHashPath` file (matching `internal/config/config.go:138-144`). This is needed for auth middleware before appconfig is loaded.

**Stage 2 (after notedb opens):** Load application config from the settings table:

```go
noteDB, err := notedb.Open(context.Background(), dbPath)
// ... error handling ...

cfg, err := appconfig.Load(context.Background(), noteDB)
// ... error handling ...
```

Now `cfg` is `*appconfig.Config` instead of `*config.Config`. Replace all `cfg.FieldName` accesses with the corresponding `appconfig.Config` field names. Most field names are identical; the key mapping is documented in `internal/appconfig/keys.go`.

**Key changes from current flow:**
- `cfg.NotesPath`, `cfg.BackupPath`, `cfg.BooxNotesPath` — these are per-source config. In Phase 5, they continue to work via env-var fallback in appconfig.Load(). They're removed in Phase 8 when sources own their paths.
- `cfg.ListenAddr`, `cfg.DBPath`, `cfg.TaskDBPath` — read directly from env vars in Stage 1, NOT from appconfig.Config
- `cfg.PasswordHash` — loaded from env var or secrets file in Stage 1 (needed for auth middleware before appconfig is available)

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./cmd/ultrabridge/`
Expected: Compiles without errors

Run: `go vet -C /home/sysop/src/ultrabridge ./...`
Expected: No issues

**Commit:** `refactor(main): two-stage config loading with appconfig`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Replace per-pipeline wiring with source registry iteration

**Verifies:** platform-neutral-config.AC2.5

**Files:**
- Modify: `cmd/ultrabridge/main.go` (replace pipeline `if` blocks with registry loop)

**Implementation:**

Replace the hardcoded Supernote pipeline block (lines 173-253) and Boox pipeline block (lines 255-297) with source registry iteration.

**Registry setup:**

```go
registry := source.NewRegistry()
registry.Register("supernote", func(db *sql.DB, row source.SourceRow, deps source.SharedDeps) (source.Source, error) {
    return supernote.NewSource(db, row, deps, database, notifier.Events())
})
registry.Register("boox", func(db *sql.DB, row source.SourceRow, deps source.SharedDeps) (source.Source, error) {
    return boox.NewSource(db, row, deps, boox.BooxDeps{
        ContentDeleter: si,
        OnTodosFound: func(ctx context.Context, notePath string, todos []booxpipeline.TodoItem) {
            created := booxpipeline.CreateTasksFromTodos(ctx, store, notePath, todos, logger)
            if created > 0 && notifier != nil {
                notifier.Notify(ctx)
            }
        },
    })
})
```

**Source iteration:**

```go
deps := source.SharedDeps{
    Indexer:    si,
    Embedder:   embedder,
    EmbedModel: cfg.OllamaEmbedModel,
    EmbedStore: embedStore,
    OCRClient:  ocrClient, // may be nil
    Logger:     logger,
}

rows, err := source.ListEnabledSources(context.Background(), noteDB)
if err != nil {
    logger.Error("list sources failed", "err", err)
    os.Exit(1)
}

var sources []source.Source
for _, row := range rows {
    s, err := registry.Create(noteDB, row, deps)
    if err != nil {
        logger.Warn("skipping source", "type", row.Type, "name", row.Name, "err", err)
        continue // AC2.7 + AC2.8: unknown type or bad config → skip, don't crash
    }
    if err := s.Start(context.Background()); err != nil {
        logger.Warn("source start failed", "type", row.Type, "name", row.Name, "err", err)
        continue
    }
    defer s.Stop()
    sources = append(sources, s)
    logger.Info("source started", "type", s.Type(), "name", s.Name())
}
```

**Backward compatibility:** If no sources exist in the DB (fresh install or upgrade from env-var config), seed sources from appconfig values. Check: if `ListEnabledSources` returns empty AND the env vars for pipeline paths are set, create source rows automatically:

```go
if len(rows) == 0 {
    // Seed from env vars for backward compatibility
    if cfg.NotesPath != "" {
        source.AddSource(ctx, noteDB, source.SourceRow{
            Type: "supernote", Name: "Supernote",
            Enabled: true,
            ConfigJSON: fmt.Sprintf(`{"notes_path":%q,"backup_path":%q}`, cfg.NotesPath, cfg.BackupPath),
            CreatedAt: time.Now().UnixMilli(),
            UpdatedAt: time.Now().UnixMilli(),
        })
    }
    if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
        source.AddSource(ctx, noteDB, source.SourceRow{
            Type: "boox", Name: "Boox",
            Enabled: true,
            ConfigJSON: fmt.Sprintf(`{"notes_path":%q}`, cfg.BooxNotesPath),
            CreatedAt: time.Now().UnixMilli(),
            UpdatedAt: time.Now().UnixMilli(),
        })
    }
    // Re-read after seeding
    rows, _ = source.ListEnabledSources(ctx, noteDB)
}
```

**Shared components stay in main.go:** The search index (`si`), embedder, embed store, retriever, and OCR client are still constructed in main.go before source iteration — they're shared infrastructure, not per-source. The notestore is now constructed inside each source adapter's Start() method.

**Web handler receives sources:** The handler still receives processor.Processor and BooxStore for the UI. After source iteration, find the relevant sources and extract the needed interfaces. For Phase 5, keep passing the same interfaces the handler expects. The handler API changes happen in Phase 6.

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./cmd/ultrabridge/`
Expected: Compiles without errors

Run: `go test -C /home/sysop/src/ultrabridge ./...`
Expected: All tests pass (no behavior changes)

**Commit:** `feat(main): replace per-pipeline wiring with source registry iteration`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Remove `internal/config` package

**Verifies:** None (infrastructure cleanup)

**Files:**
- Delete: `internal/config/config.go`
- Delete: `internal/config/config_pipeline_test.go`

**Implementation:**

After Task 2 successfully builds and tests pass, remove the `internal/config` package entirely. No other files import it (verified by investigation).

Run `go vet ./...` after removal to confirm no dangling references.

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./...`
Expected: Compiles without errors

Run: `go vet -C /home/sysop/src/ultrabridge ./...`
Expected: No issues

**Commit:** `refactor: remove deprecated internal/config package`
<!-- END_TASK_3 -->
