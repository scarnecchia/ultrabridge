# Platform-Neutral Configuration Implementation Plan

**Goal:** Define the Source interface, factory pattern, sources table schema, and CRUD operations for managing note-ingestion sources.

**Architecture:** New `internal/source` package defines a `Source` interface modelled on the existing `Start(ctx)/Stop()` lifecycle both pipelines already follow. A `Registry` holds factory functions keyed by source type name. A `sources` table in SQLite persists source configuration. CRUD helpers operate on the sources table.

**Tech Stack:** Go stdlib, `database/sql`, `encoding/json`, existing `internal/notedb`

**Scope:** 8 phases from original design (this is phase 2 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC2: Unified Source abstraction
- **platform-neutral-config.AC2.1 Success:** Source interface defines Type(), Name(), Start(ctx), Stop() contract
- **platform-neutral-config.AC2.4 Success:** Sources table CRUD works — add, update, enable/disable, remove sources via API
- **platform-neutral-config.AC2.5 Success:** main.go iterates enabled sources from DB, creates via factory, starts each, defers Stop()
- **platform-neutral-config.AC2.6 Success:** Source-specific config stored as JSON in config_json column, parsed by each factory
- **platform-neutral-config.AC2.7 Failure:** Unknown source type in DB logs warning and is skipped, does not crash startup
- **platform-neutral-config.AC2.8 Failure:** Source with invalid config_json logs error and is skipped, does not crash startup

---

## Codebase Verification Findings

- ✓ `processor.Store` at `/home/sysop/src/ultrabridge/internal/processor/processor.go:76-120`: `New(db, cfg)`, `Start(ctx) error`, `Stop() error`
- ✓ `booxpipeline.Processor` at `/home/sysop/src/ultrabridge/internal/booxpipeline/processor.go:10-59`: `New(db, notesPath, cfg, logger)`, `Start(ctx) error`, `Stop()` (void — no error return)
- ✓ `pipeline.Pipeline` at `/home/sysop/src/ultrabridge/internal/pipeline/pipeline.go:14-55`: `New(cfg)`, `Start(ctx)` (void), `Close()`
- ✓ Shared interfaces confirmed: `processor.Indexer` (processor.go:15-17), `rag.Embedder` (embedder.go:15-17), `rag.EmbedStore` (store.go:36-73)
- ✓ `processor.OCRClient` at `/home/sysop/src/ultrabridge/internal/processor/ocrclient.go:29-52`: shared between both pipelines
- ✓ notedb schema: idempotent `CREATE TABLE IF NOT EXISTS` in stmts slice at `internal/notedb/schema.go`
- ✓ main.go wiring: Supernote pipeline at lines 173-253, Boox pipeline at lines 255-297

**Key lifecycle differences:**
- `processor.Store.Stop()` returns `error`
- `booxpipeline.Processor.Stop()` returns nothing
- The Source interface `Stop()` should return nothing (simpler contract — errors on shutdown are logged internally)

**Testing approach:** Real in-memory SQLite, stdlib testing. See `/home/sysop/src/ultrabridge/internal/notedb/CLAUDE.md` and `/home/sysop/src/ultrabridge/internal/booxpipeline/CLAUDE.md`.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add `sources` table migration to notedb schema

**Verifies:** None (infrastructure — DDL only)

**Files:**
- Modify: `internal/notedb/schema.go:90-127` (add CREATE TABLE to stmts slice)

**Implementation:**

Add the `sources` table DDL to the `stmts` slice in the `migrate` function, after the existing `settings` table creation (line 104). The table stores source configurations with type-specific JSON config.

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

This follows the exact pattern of existing DDL in the stmts slice. `created_at` and `updated_at` are millisecond UTC unix timestamps (matching the project convention documented in CLAUDE.md).

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/notedb/`
Expected: Existing tests pass (migration is idempotent)

**Commit:** `feat(notedb): add sources table schema migration`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/source/source.go` — Source interface, Factory, SharedDeps, SourceRow, CRUD

**Verifies:** platform-neutral-config.AC2.1, platform-neutral-config.AC2.4, platform-neutral-config.AC2.6, platform-neutral-config.AC2.7, platform-neutral-config.AC2.8

**Files:**
- Create: `internal/source/source.go`
- Create: `internal/source/registry.go`
- Test: `internal/source/source_test.go`

**Implementation:**

`source.go` defines the core types:

```go
package source

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/rag"
)

// Source is the lifecycle interface for a note-ingestion platform.
// Each implementation manages its own job queue, workers, and file watching internally.
type Source interface {
	Type() string
	Name() string
	Start(ctx context.Context) error
	Stop()
}

// Factory creates a Source from a database row and shared dependencies.
// This is the registry's internal contract. Individual adapter constructors
// (e.g., supernote.NewSource, boox.NewSource) may accept additional
// type-specific parameters beyond SharedDeps. The caller in main.go
// wraps these constructors in closure adapters that capture the extra
// dependencies and satisfy this Factory signature. See Phase 5 Task 2
// for the closure registration pattern.
type Factory func(db *sql.DB, row SourceRow, deps SharedDeps) (Source, error)

// SharedDeps bundles infrastructure shared across all source adapters.
type SharedDeps struct {
	Indexer    processor.Indexer
	Embedder  rag.Embedder
	EmbedModel string
	EmbedStore rag.EmbedStore
	OCRClient  *processor.OCRClient
	Logger     *slog.Logger
}

// SourceRow represents a row from the sources table.
type SourceRow struct {
	ID         int64
	Type       string
	Name       string
	Enabled    bool
	ConfigJSON string
	CreatedAt  int64
	UpdatedAt  int64
}
```

`registry.go` defines the Registry and CRUD operations on the sources table:

```go
// Registry holds factories keyed by source type name.
type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry

// Register adds a factory for a source type.
func (r *Registry) Register(typeName string, f Factory)

// Create looks up the factory for row.Type, calls it, and returns the Source.
// Returns an error if the type is unknown or the factory fails.
func (r *Registry) Create(db *sql.DB, row SourceRow, deps SharedDeps) (Source, error)
```

CRUD functions (stateless, taking `*sql.DB` — matching the `notedb.GetSetting`/`SetSetting` pattern):

```go
// ListSources returns all source rows from the DB.
func ListSources(ctx context.Context, db *sql.DB) ([]SourceRow, error)

// ListEnabledSources returns only enabled source rows.
func ListEnabledSources(ctx context.Context, db *sql.DB) ([]SourceRow, error)

// GetSource returns a single source by ID.
func GetSource(ctx context.Context, db *sql.DB, id int64) (SourceRow, error)

// AddSource inserts a new source row and returns the assigned ID.
func AddSource(ctx context.Context, db *sql.DB, row SourceRow) (int64, error)

// UpdateSource updates an existing source row (name, enabled, config_json, updated_at).
func UpdateSource(ctx context.Context, db *sql.DB, row SourceRow) error

// RemoveSource deletes a source row by ID.
func RemoveSource(ctx context.Context, db *sql.DB, id int64) error
```

Timestamps use `time.Now().UnixMilli()` for created_at/updated_at (matching project convention).

`GetSource` returns a sentinel error (e.g., `ErrSourceNotFound`) when the row doesn't exist, following the `taskstore.ErrNotFound` pattern documented in CLAUDE.md.

**Testing:**

Tests use real in-memory SQLite via `notedb.Open(ctx, ":memory:")`.

- platform-neutral-config.AC2.1: Verify Source interface is satisfied by a test stub implementing Type(), Name(), Start(ctx), Stop()
- platform-neutral-config.AC2.4: CRUD round-trip — AddSource, GetSource, ListSources, UpdateSource (change name/enabled/config_json), RemoveSource, verify GetSource returns ErrSourceNotFound after remove
- platform-neutral-config.AC2.6: AddSource with JSON config_json, GetSource reads it back unchanged; create a test factory that parses config_json into a typed struct, verify parsed values
- platform-neutral-config.AC2.7: Registry.Create with unknown type returns error (caller logs warning and skips)
- platform-neutral-config.AC2.8: Registry.Create with factory that receives invalid JSON config_json returns error (caller logs error and skips)
- Additional: ListEnabledSources returns only enabled=1 rows; disabled sources are excluded

For AC2.5 (main.go iteration), this is tested at integration level in Phase 5 when main.go is rewired. The Registry.Create method is the unit under test here.

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/source/`
Expected: All tests pass

**Commit:** `feat(source): add Source interface, Registry, SharedDeps, SourceRow CRUD`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->
