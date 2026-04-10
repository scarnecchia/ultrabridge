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
	Embedder   rag.Embedder
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
