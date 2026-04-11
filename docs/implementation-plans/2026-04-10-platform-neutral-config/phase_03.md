# Platform-Neutral Configuration Implementation Plan

**Goal:** Wrap the existing Supernote pipeline (notestore, processor, pipeline watcher) behind the Source interface so it can be managed as an interchangeable source.

**Architecture:** New `internal/source/supernote` package implements the `source.Source` interface. The adapter's `Start()` constructs and starts the existing `notestore.New` + `processor.New` + `pipeline.New` components internally. Its `Stop()` tears them down in reverse order. A type-specific config struct is unmarshalled from `config_json`.

**Tech Stack:** Go stdlib, existing `internal/processor`, `internal/notestore`, `internal/pipeline`, `internal/source`

**Scope:** 8 phases from original design (this is phase 3 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC2: Unified Source abstraction (Supernote-specific)
- **platform-neutral-config.AC2.2 Success:** Supernote source adapter starts/stops the existing processor pipeline via Source interface
- **platform-neutral-config.AC2.6 Success:** Source-specific config stored as JSON in config_json column, parsed by each factory

---

## Codebase Verification Findings

- ✓ `processor.Store` at `/home/sysop/src/ultrabridge/internal/processor/processor.go:76-120`: `New(db, cfg) *Store`, `Start(ctx) error`, `Stop() error`
- ✓ `processor.WorkerConfig` at `/home/sysop/src/ultrabridge/internal/processor/processor.go:33-46`: fields include OCREnabled, BackupPath, MaxFileMB, OCRClient, OCRPrompt (func), InjectEnabled (func), Indexer, CatalogUpdater, Embedder, EmbedModel, EmbedStore
- ✓ `notestore.New(noteDB, notesPath)` at `/home/sysop/src/ultrabridge/internal/notestore/store.go` — returns `NoteStore` interface
- ✓ `pipeline.New(cfg)` at `/home/sysop/src/ultrabridge/internal/pipeline/pipeline.go:34-47` — `Config` has NotesPath, Store (NoteStore), Proc (Processor), Events (<-chan []byte), Logger
- ✓ `pipeline.Pipeline.Start(ctx)` returns void; `Close()` tears down
- ✓ `processor.NewSPCCatalog(db)` at `/home/sysop/src/ultrabridge/internal/processor/catalog.go:22` — creates CatalogUpdater from MariaDB connection
- ✓ `sync.NewNotifier(url, logger)` at `/home/sysop/src/ultrabridge/cmd/ultrabridge/main.go:154` — creates Engine.IO notifier; `Events()` returns `<-chan []byte`
- ✓ Current Supernote wiring in main.go: lines 173-253 (notestore → search → embedder → workerCfg → processor → pipeline)
- ✓ Closures for `OCRPrompt` and `InjectEnabled` read from settings table at job time (main.go:222-229)

**Key design decisions:**
- The `sync.Notifier` (Engine.IO) and optional MariaDB `*sql.DB` (for CatalogUpdater) are Supernote-specific dependencies not in `source.SharedDeps`. The Supernote source constructor accepts them as explicit parameters. Phase 5 wires them via a closure adapter to the `source.Factory` signature.
- Runtime closures (`OCRPrompt`, `InjectEnabled`) continue reading from the settings table via `notedb.GetSetting` — no behavior change.

**Testing approach:** Real in-memory SQLite, stdlib testing. Config unmarshalling tested with JSON round-trip. Lifecycle tested by verifying Start/Stop don't panic with minimal deps.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Create `internal/source/supernote/config.go` — type-specific config struct

**Verifies:** platform-neutral-config.AC2.6

**Files:**
- Create: `internal/source/supernote/config.go`

**Implementation:**

Defines the config struct that gets unmarshalled from the `config_json` column in the `sources` table. Fields correspond to the Supernote-specific settings that are currently env vars in `internal/config/config.go:59-61` (`NotesPath`, `BackupPath`) plus the OCR pipeline toggle.

```go
package supernote

// Config holds Supernote source-specific settings parsed from sources.config_json.
type Config struct {
	NotesPath  string `json:"notes_path"`
	BackupPath string `json:"backup_path"`
}
```

**Notes on scope:** `inject_enabled` and `ocr_prompt` are runtime-configurable settings that continue to be read from the settings KV table at job-processing time via closures. They are NOT in the source config because they don't require restart to change. `OCREnabled`, `OCRAPIURL`, `OCRAPIKey`, `OCRModel`, `OCRFormat` are global OCR infrastructure settings that apply to all sources — they live in `appconfig.Config`, not per-source config.

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./internal/source/supernote/`
Expected: Compiles without errors

**Commit:** `feat(source/supernote): add type-specific config struct`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/source/supernote/source.go` — Source interface implementation

**Verifies:** platform-neutral-config.AC2.2, platform-neutral-config.AC2.6

**Files:**
- Create: `internal/source/supernote/source.go`
- Test: `internal/source/supernote/source_test.go`

**Implementation:**

The Supernote source adapter wraps the three existing components: `notestore.New`, `processor.New`, and `pipeline.New`. It constructs them in `Start()` and tears them down in `Stop()`.

**Note on shared infrastructure:** The `search.Store` instance (created once in main.go as `si := search.New(noteDB)`) is passed to this adapter via `SharedDeps.Indexer`. The adapter does NOT construct its own search store — it uses the shared one. Similarly, `Embedder` and `EmbedStore` are shared infrastructure constructed in main.go and passed via SharedDeps.

```go
package supernote

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/pipeline"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/source"
)

// Source implements source.Source for the Supernote notes pipeline.
type Source struct {
	name   string
	cfg    Config
	db     *sql.DB
	deps   source.SharedDeps

	// Supernote-specific dependencies (not in SharedDeps)
	mariaDB *sql.DB       // nil = SPC catalog sync disabled
	events  <-chan []byte  // nil = Engine.IO listener disabled

	proc *processor.Store
	pl   *pipeline.Pipeline
}

// NewSource constructs a Supernote source from a source row and dependencies.
// mariaDB and events are Supernote-specific: mariaDB enables SPC catalog sync
// after OCR injection, events enables Engine.IO file-change notifications.
func NewSource(db *sql.DB, row source.SourceRow, deps source.SharedDeps, mariaDB *sql.DB, events <-chan []byte) (*Source, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parse supernote config: %w", err)
	}
	return &Source{
		name:    row.Name,
		cfg:     cfg,
		db:      db,
		deps:    deps,
		mariaDB: mariaDB,
		events:  events,
	}, nil
}

func (s *Source) Type() string { return "supernote" }
func (s *Source) Name() string { return s.name }

func (s *Source) Start(ctx context.Context) error {
	ns := notestore.New(s.db, s.cfg.NotesPath)

	workerCfg := processor.WorkerConfig{
		OCREnabled: s.deps.OCRClient != nil,
		BackupPath: s.cfg.BackupPath,
		Indexer:    s.deps.Indexer,
		Embedder:   s.deps.Embedder,
		EmbedModel: s.deps.EmbedModel,
		EmbedStore: s.deps.EmbedStore,
		OCRPrompt: func() string {
			v, _ := notedb.GetSetting(context.Background(), s.db, "sn_ocr_prompt")
			return v
		},
		InjectEnabled: func() bool {
			v, _ := notedb.GetSetting(context.Background(), s.db, "sn_inject_enabled")
			return v != "false"
		},
	}
	if s.mariaDB != nil {
		workerCfg.CatalogUpdater = processor.NewSPCCatalog(s.mariaDB)
	}
	if s.deps.OCRClient != nil {
		workerCfg.OCRClient = s.deps.OCRClient
	}

	s.proc = processor.New(s.db, workerCfg)
	if workerCfg.OCREnabled {
		if err := s.proc.Start(ctx); err != nil {
			return fmt.Errorf("start processor: %w", err)
		}
	}

	s.pl = pipeline.New(pipeline.Config{
		NotesPath: s.cfg.NotesPath,
		Store:     ns,
		Proc:      s.proc,
		Events:    s.events,
		Logger:    s.deps.Logger,
	})
	s.pl.Start(ctx)
	return nil
}

func (s *Source) Stop() {
	if s.pl != nil {
		s.pl.Close()
	}
	if s.proc != nil {
		s.proc.Stop() // error logged internally
	}
}
```

**Key design points:**
- `OCREnabled` is derived from `deps.OCRClient != nil` rather than a separate flag — if no OCR client was provided, OCR is disabled
- `MaxFileMB` and `OCRFormat` are set on the `OCRClient` at construction time in main.go (Phase 5), not here
- Runtime closures for `OCRPrompt` and `InjectEnabled` read from settings table at job time — identical to current behavior in `main.go:222-229`
- `Stop()` tears down pipeline first, then processor (reverse of start order)

**Testing:**

Tests verify config unmarshalling and lifecycle:

- platform-neutral-config.AC2.2: Construct a Source with valid config, verify Type() returns "supernote", Name() returns the configured name. Call Start(ctx) with a real in-memory SQLite and nil SharedDeps components (OCRClient nil = OCR disabled, Indexer nil = indexing disabled), verify it returns no error. Call Stop(), verify no panic.
- platform-neutral-config.AC2.6: Unmarshal various config_json strings into Config struct — verify notes_path and backup_path are parsed correctly. Test with missing fields (should get zero values). Test with extra unknown fields (should be ignored by json.Unmarshal).
- Additional: NewSource with invalid JSON returns error (supports AC2.8 at the registry level)

**Note on test constraints:** Starting the full pipeline requires a real filesystem path for fsnotify. Tests should use `t.TempDir()` for `NotesPath` to avoid fsnotify errors. The processor won't process anything without jobs in the DB — it just starts the worker loop. This is safe for lifecycle testing.

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/source/supernote/`
Expected: All tests pass

**Commit:** `feat(source/supernote): implement Source interface wrapping processor + pipeline`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->
