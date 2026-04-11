# Platform-Neutral Configuration Implementation Plan

**Goal:** Wrap the existing Boox pipeline (booxpipeline.Processor) behind the Source interface so it can be managed as an interchangeable source.

**Architecture:** New `internal/source/boox` package implements `source.Source`. The adapter's `Start()` constructs and starts `booxpipeline.New` internally. Its `Stop()` tears it down. A type-specific config struct is unmarshalled from `config_json`. The Boox source does NOT include the WebDAV handler — WebDAV is a transport concern handled in the web layer, not a source lifecycle concern.

**Tech Stack:** Go stdlib, existing `internal/booxpipeline`, `internal/source`

**Scope:** 8 phases from original design (this is phase 4 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC2: Unified Source abstraction (Boox-specific)
- **platform-neutral-config.AC2.3 Success:** Boox source adapter starts/stops the existing boox pipeline via Source interface
- **platform-neutral-config.AC2.6 Success:** Source-specific config stored as JSON in config_json column, parsed by each factory

---

## Codebase Verification Findings

- ✓ `booxpipeline.Processor` at `/home/sysop/src/ultrabridge/internal/booxpipeline/processor.go:10-59`: `New(db, notesPath, cfg, logger)`, `Start(ctx) error`, `Stop()` (void)
- ✓ `booxpipeline.WorkerConfig` at `/home/sysop/src/ultrabridge/internal/booxpipeline/worker.go:41-54`: Indexer, ContentDeleter, OCR (OCRer), OCRPrompt (func), TodoEnabled (func), TodoPrompt (func), OnTodosFound callback, CachePath, Embedder, EmbedModel, EmbedStore
- ✓ `booxpipeline.OCRer` at `/home/sysop/src/ultrabridge/internal/booxpipeline/worker.go:26-28`: `Recognize(ctx, jpegData, prompt)` — satisfied by `processor.OCRClient`
- ✓ `booxpipeline.ContentDeleter` at `/home/sysop/src/ultrabridge/internal/booxpipeline/worker.go:31-33`: `Delete(ctx, path)` — satisfied by `search.Store`
- ✓ `booxpipeline.CreateTasksFromTodos` at `/home/sysop/src/ultrabridge/internal/booxpipeline/todotask.go:18` — takes `TaskCreator`, notePath, todos, logger
- ✓ Current Boox wiring in main.go:255-297 — constructs WorkerConfig, conditionally starts, defers Stop
- ✓ Closures for `OCRPrompt`, `TodoEnabled`, `TodoPrompt` read from settings table at job time (main.go:265-276)
- ✓ `OnTodosFound` callback creates CalDAV tasks and triggers Engine.IO notify (main.go:277-282)

**Key design decisions:**
- `ContentDeleter` (search.Store) and `OnTodosFound` callback are Boox-specific dependencies not in `source.SharedDeps`. The Boox source constructor accepts a `BooxDeps` struct for these.
- Runtime closures (`OCRPrompt`, `TodoEnabled`, `TodoPrompt`) continue reading from the settings table via `notedb.GetSetting` — no behavior change.
- WebDAV handler is NOT part of the Boox source. It's a transport layer concern wired separately by the web handler.

**Testing approach:** Real in-memory SQLite, stdlib testing. Config unmarshalling tested with JSON round-trip. Lifecycle tested by verifying Start/Stop don't panic with minimal deps.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Create `internal/source/boox/config.go` — type-specific config struct

**Verifies:** platform-neutral-config.AC2.6

**Files:**
- Create: `internal/source/boox/config.go`

**Implementation:**

Defines the config struct unmarshalled from `config_json`. Fields correspond to the Boox-specific settings currently in env vars (`BooxNotesPath` from `internal/config/config.go:72`).

```go
package boox

// Config holds Boox source-specific settings parsed from sources.config_json.
type Config struct {
	NotesPath string `json:"notes_path"` // filesystem root for uploads + page cache
}
```

**Notes on scope:** `CachePath` is derived from `NotesPath` (it's `filepath.Join(notesPath, ".cache")` — see main.go:261). `todo_enabled`, `todo_prompt`, and `ocr_prompt` are runtime-configurable settings read from the settings KV table at job time via closures — NOT in source config. `import_path` is also a runtime setting stored in the settings table.

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./internal/source/boox/`
Expected: Compiles without errors

**Commit:** `feat(source/boox): add type-specific config struct`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/source/boox/source.go` — Source interface implementation

**Verifies:** platform-neutral-config.AC2.3, platform-neutral-config.AC2.6

**Files:**
- Create: `internal/source/boox/source.go`
- Test: `internal/source/boox/source_test.go`

**Implementation:**

The Boox source adapter wraps `booxpipeline.New`. It constructs the processor in `Start()` and tears it down in `Stop()`.

```go
package boox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/sysop/ultrabridge/internal/booxpipeline"
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/source"
)

// BooxDeps holds Boox-specific dependencies not in source.SharedDeps.
type BooxDeps struct {
	ContentDeleter booxpipeline.ContentDeleter
	OnTodosFound   func(ctx context.Context, notePath string, todos []booxpipeline.TodoItem)
}

// Source implements source.Source for the Boox notes pipeline.
type Source struct {
	name     string
	cfg      Config
	db       *sql.DB
	deps     source.SharedDeps
	booxDeps BooxDeps
	proc     *booxpipeline.Processor
}

// NewSource constructs a Boox source from a source row and dependencies.
func NewSource(db *sql.DB, row source.SourceRow, deps source.SharedDeps, booxDeps BooxDeps) (*Source, error) {
	var cfg Config
	if err := json.Unmarshal([]byte(row.ConfigJSON), &cfg); err != nil {
		return nil, fmt.Errorf("parse boox config: %w", err)
	}
	return &Source{
		name:     row.Name,
		cfg:      cfg,
		db:       db,
		deps:     deps,
		booxDeps: booxDeps,
	}, nil
}

func (s *Source) Type() string { return "boox" }
func (s *Source) Name() string { return s.name }

func (s *Source) Start(ctx context.Context) error {
	workerCfg := booxpipeline.WorkerConfig{
		Indexer:        s.deps.Indexer,
		ContentDeleter: s.booxDeps.ContentDeleter,
		CachePath:      filepath.Join(s.cfg.NotesPath, ".cache"),
		Embedder:       s.deps.Embedder,
		EmbedModel:     s.deps.EmbedModel,
		EmbedStore:     s.deps.EmbedStore,
		OCRPrompt: func() string {
			v, _ := notedb.GetSetting(context.Background(), s.db, "boox_ocr_prompt")
			return v
		},
		TodoEnabled: func() bool {
			v, _ := notedb.GetSetting(context.Background(), s.db, "boox_todo_enabled")
			return v == "true"
		},
		TodoPrompt: func() string {
			v, _ := notedb.GetSetting(context.Background(), s.db, "boox_todo_prompt")
			return v
		},
		OnTodosFound: s.booxDeps.OnTodosFound,
	}
	if s.deps.OCRClient != nil {
		workerCfg.OCR = s.deps.OCRClient
	}

	logger := s.deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s.proc = booxpipeline.New(s.db, s.cfg.NotesPath, workerCfg, logger)
	if err := s.proc.Start(ctx); err != nil {
		return fmt.Errorf("start boox processor: %w", err)
	}
	return nil
}

func (s *Source) Stop() {
	if s.proc != nil {
		s.proc.Stop()
	}
}
```

**Key design points:**
- `BooxDeps` carries `ContentDeleter` and `OnTodosFound` — these are Boox-specific and don't belong in `source.SharedDeps`
- `CachePath` is derived as `filepath.Join(notesPath, ".cache")` matching current logic at main.go:261
- Runtime closures (`OCRPrompt`, `TodoEnabled`, `TodoPrompt`) read from settings table at job time — identical to current behavior at main.go:265-276
- `OCRer` is satisfied by `deps.OCRClient` (which is `*processor.OCRClient` implementing `Recognize`)

**Testing:**

Tests verify config unmarshalling and lifecycle:

- platform-neutral-config.AC2.3: Construct a Source with valid config, verify Type() returns "boox", Name() returns configured name. Call Start(ctx) with real in-memory SQLite and nil SharedDeps components, verify it returns no error. Call Stop(), verify no panic.
- platform-neutral-config.AC2.6: Unmarshal various config_json strings into Config struct — verify notes_path is parsed correctly. Test with missing fields (zero values). Test with extra unknown fields (ignored).
- Additional: NewSource with invalid JSON returns error (supports AC2.8 at the registry level)

**Note on test constraints:** `booxpipeline.New` expects `notesPath` to exist on the filesystem for cache directory creation. Tests should use `t.TempDir()` for `NotesPath`.

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/source/boox/`
Expected: All tests pass

**Commit:** `feat(source/boox): implement Source interface wrapping booxpipeline.Processor`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->
