# RAG Retrieval Pipeline — Phase 2: Hybrid Retriever

**Goal:** Build a hybrid retriever that combines FTS5 keyword search with vector cosine similarity via reciprocal rank fusion, supporting metadata filtering.

**Architecture:** Retriever wraps existing `search.Store.Search()` for FTS5 results and `rag.Store.AllEmbeddings()` for vector similarity. Results merged via RRF. Metadata enriched via SQL JOINs with `boox_notes` and `notes` tables. Falls back to FTS5-only when embedding cache is empty.

**Tech Stack:** Go stdlib, existing SQLite FTS5, in-memory vector cache from Phase 1

**Scope:** 6 phases from original design (phase 2 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC2: Hybrid Retriever
- **rag-retrieval-pipeline.AC2.1 Success:** `Retriever.Search(ctx, SearchRequest) ([]SearchResult, error)` returns results combining FTS5 and vector similarity via reciprocal rank fusion. Verified by: unit test with known content shows results from both sources merged.
- **rag-retrieval-pipeline.AC2.2 Success:** `SearchRequest` supports `Folder`, `Device`, and `DateRange` filters. Verified by: search with folder filter returns only pages from that folder; search with date range returns only pages within range.
- **rag-retrieval-pipeline.AC2.3 Success:** Metadata filtering JOINs `note_content` with `boox_notes`/`notes` tables on `note_path` for device model and date; filters by folder path segment for folder. Verified by: SQL query plan in test confirms JOIN behavior.
- **rag-retrieval-pipeline.AC2.4 Success:** Each `SearchResult` includes `NotePath`, `Page`, `BodyText`, `TitleText`, `Score`, `Folder`, `Device`, `NoteDate` — sufficient for citation. Verified by: struct definition includes all fields; search returns populated metadata.
- **rag-retrieval-pipeline.AC2.5 Success:** When no embeddings exist (Ollama disabled), retriever falls back to FTS5-only mode gracefully. Verified by: search returns FTS5 results when embedding cache is empty.

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: Retriever Implementation

<!-- START_TASK_1 -->
### Task 1: Create retriever types and interface

**Verifies:** rag-retrieval-pipeline.AC2.4

**Files:**
- Create: `/home/jtd/ultrabridge/internal/rag/retriever.go`

**Implementation:**

Create `internal/rag/retriever.go` with the retriever types, interface, and full implementation.

**Types (models):**

```go
package rag

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"path"
	"sort"
	"time"

	"github.com/sysop/ultrabridge/internal/search"
)

// SearchRequest is the input for hybrid search.
type SearchRequest struct {
	Query    string
	Folder   string    // filter by folder path segment (empty = all)
	Device   string    // filter by device model (empty = all)
	DateFrom time.Time // zero = no lower bound
	DateTo   time.Time // zero = no upper bound
	Limit    int       // 0 = default (20)
}

// SearchResult is one ranked result with full metadata for citation.
type SearchResult struct {
	NotePath  string
	Page      int
	BodyText  string
	TitleText string
	Score     float64
	Folder    string
	Device    string
	NoteDate  time.Time
}
```

**Retriever interface and implementation:**

```go
// SearchRetriever is the interface for hybrid search. Defined as an interface
// for testability — the web handler accepts this interface, not the concrete type.
type SearchRetriever interface {
	Search(ctx context.Context, req SearchRequest) ([]SearchResult, error)
}

// Retriever provides hybrid search over note content. Implements SearchRetriever.
type Retriever struct {
	db          *sql.DB
	searchIndex search.SearchIndex
	embedStore  *Store
	embedder    Embedder
	logger      *slog.Logger
}

func NewRetriever(db *sql.DB, searchIndex search.SearchIndex, embedStore *Store, embedder Embedder, logger *slog.Logger) *Retriever {
	return &Retriever{
		db:          db,
		searchIndex: searchIndex,
		embedStore:  embedStore,
		embedder:    embedder,
		logger:      logger,
	}
}
```

**Search method — the core hybrid retrieval algorithm:**

```go
func (r *Retriever) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	// 1. FTS5 keyword search (always available)
	ftsResults, err := r.searchIndex.Search(ctx, search.SearchQuery{
		Text:   req.Query,
		Folder: req.Folder,
		Limit:  limit * 2, // fetch extra for fusion
	})
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}

	// 2. Vector similarity search (if embeddings available and embedder can embed query)
	var vecRanked []rankedDoc
	if r.embedStore != nil && r.embedder != nil {
		allEmbeddings := r.embedStore.AllEmbeddings()
		if len(allEmbeddings) > 0 {
			queryVec, err := r.embedder.Embed(ctx, req.Query)
			if err != nil {
				r.logger.Warn("query embedding failed, falling back to FTS-only", "err", err)
			} else {
				// Score all embeddings by cosine similarity
				type scored struct {
					rec   EmbeddingRecord
					score float32
				}
				var candidates []scored
				for _, rec := range allEmbeddings {
					sim := CosineSimilarity(queryVec, rec.Embedding)
					if sim > 0 {
						candidates = append(candidates, scored{rec, sim})
					}
				}
				sort.Slice(candidates, func(i, j int) bool {
					return candidates[i].score > candidates[j].score
				})
				// Take top results for fusion
				topN := limit * 2
				if topN > len(candidates) {
					topN = len(candidates)
				}
				for rank, c := range candidates[:topN] {
					vecRanked = append(vecRanked, rankedDoc{
						notePath: c.rec.NotePath,
						page:     c.rec.Page,
						rank:     rank + 1,
					})
				}
			}
		}
	}

	// 3. Reciprocal Rank Fusion
	type docKey struct {
		notePath string
		page     int
	}
	rrfScores := map[docKey]float64{}

	// FTS5 ranks
	for rank, r := range ftsResults {
		key := docKey{r.Path, r.Page}
		rrfScores[key] += 1.0 / float64(60+rank+1)
	}

	// Vector ranks
	for _, r := range vecRanked {
		key := docKey{r.notePath, r.page}
		rrfScores[key] += 1.0 / float64(60+r.rank)
	}

	// Sort by RRF score descending
	type rrfEntry struct {
		key   docKey
		score float64
	}
	var merged []rrfEntry
	for k, s := range rrfScores {
		merged = append(merged, rrfEntry{k, s})
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}

	// 4. Enrich with metadata via SQL JOINs
	results := make([]SearchResult, 0, len(merged))
	for _, entry := range merged {
		result, err := r.enrichResult(ctx, entry.key.notePath, entry.key.page, entry.score)
		if err != nil {
			r.logger.Warn("enrich result failed", "path", entry.key.notePath, "page", entry.key.page, "err", err)
			continue
		}
		// Apply post-merge filters (device, date range)
		if req.Device != "" && result.Device != req.Device {
			continue
		}
		if !req.DateFrom.IsZero() && result.NoteDate.Before(req.DateFrom) {
			continue
		}
		if !req.DateTo.IsZero() && result.NoteDate.After(req.DateTo) {
			continue
		}
		results = append(results, *result)
	}

	return results, nil
}

type rankedDoc struct {
	notePath string
	page     int
	rank     int
}
```

**Metadata enrichment via JOINs:**

```go
func (r *Retriever) enrichResult(ctx context.Context, notePath string, page int, score float64) (*SearchResult, error) {
	result := &SearchResult{
		NotePath: notePath,
		Page:     page,
		Score:    score,
	}

	// Get body text and title from note_content
	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(body_text, ''), COALESCE(title_text, '') FROM note_content WHERE note_path = ? AND page = ?`,
		notePath, page,
	).Scan(&result.BodyText, &result.TitleText)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query note_content: %w", err)
	}

	// Try boox_notes first for metadata
	var folder, device sql.NullString
	var createdAt sql.NullInt64
	err = r.db.QueryRowContext(ctx,
		`SELECT folder, device_model, created_at FROM boox_notes WHERE path = ?`,
		notePath,
	).Scan(&folder, &device, &createdAt)
	if err == nil {
		result.Folder = folder.String
		result.Device = device.String
		if createdAt.Valid && createdAt.Int64 > 0 {
			// boox_notes.created_at is milliseconds
			result.NoteDate = time.UnixMilli(createdAt.Int64)
		}
		return result, nil
	}

	// Fall back to notes table (Supernote)
	var relPath sql.NullString
	var snCreatedAt sql.NullInt64
	err = r.db.QueryRowContext(ctx,
		`SELECT rel_path, created_at FROM notes WHERE path = ?`,
		notePath,
	).Scan(&relPath, &snCreatedAt)
	if err == nil {
		result.Device = "Supernote"
		if relPath.Valid {
			// Extract folder from relative path
			dir := path.Dir(relPath.String)
			result.Folder = path.Base(dir)
			if result.Folder == "." || result.Folder == "/" {
				result.Folder = ""
			}
		}
		if snCreatedAt.Valid && snCreatedAt.Int64 > 0 {
			result.NoteDate = time.Unix(snCreatedAt.Int64, 0)
		}
		return result, nil
	}

	// Neither table matched — return what we have
	return result, nil
}
```

Key design decisions:
- RRF constant k=60 (standard value from the original paper)
- Vector candidates filtered to sim > 0 (skip anti-correlated)
- Device/date filters applied post-merge (simpler than pre-filtering both result sets)
- Folder filter applied in FTS5 query (reuses existing LIKE filter) AND as post-merge filter for vector results
- Metadata enrichment: try boox_notes first, fall back to notes. This is an N+1 query pattern (1-3 queries per result, 10-60 total for a typical result set). This is acceptable at current scale (12K pages, 10-20 results per query). If performance becomes an issue at larger scale, batch enrichment with `WHERE note_path IN (?, ?, ...)` is a straightforward optimization.
- Boox created_at in milliseconds, Supernote created_at in seconds — handled with different conversion
- For Supernote folder: extracted from `rel_path` (relative path in notes table), same approach as `search.Store.ListFolders()`

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: Build succeeds.

**Commit:** `feat(rag): add hybrid retriever with RRF merge and metadata enrichment`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Tests for hybrid retriever

**Verifies:** rag-retrieval-pipeline.AC2.1, rag-retrieval-pipeline.AC2.2, rag-retrieval-pipeline.AC2.3, rag-retrieval-pipeline.AC2.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/rag/retriever_test.go`

**Testing:**

Use `notedb.Open(context.Background(), ":memory:")` for the database. Pre-populate `note_content`, `boox_notes`, and `notes` tables with known test data.

Create a mock embedder that returns deterministic vectors (e.g., specific known vectors for specific texts, so cosine similarity results are predictable).

Tests must verify each AC:

**rag-retrieval-pipeline.AC2.1 — Hybrid fusion:**
- Insert several pages into note_content with distinct text
- Create embeddings for some pages in note_embeddings (via rag.Store.Save)
- Search with a query that matches via both FTS5 (keyword match) and vector similarity (high cosine sim)
- Verify results include pages found by BOTH methods, merged with RRF scores
- Verify ordering is by RRF score descending

**rag-retrieval-pipeline.AC2.2 — Metadata filtering:**
- Insert pages from different folders, devices, and dates
- Search with `Folder` filter: verify only matching folder pages returned
- Search with `Device` filter: verify only matching device pages returned
- Search with `DateFrom`/`DateTo`: verify only pages within date range returned

**rag-retrieval-pipeline.AC2.3 — Metadata JOINs:**
- Insert a boox_notes row with device_model="Palma2" and folder="Work"
- Insert a notes row (Supernote) with rel_path="MyNotes/Personal/test.note"
- Search and verify results have populated Device and Folder fields from the JOINed tables

**rag-retrieval-pipeline.AC2.4 — SearchResult fields:**
- Verify SearchResult struct has all required fields: NotePath, Page, BodyText, TitleText, Score, Folder, Device, NoteDate
- Verify returned results have non-empty values for metadata fields

**rag-retrieval-pipeline.AC2.5 — FTS5-only fallback:**
- Create retriever with empty embedding store (no embeddings loaded)
- Search should still return FTS5 results
- Verify results are ordered by BM25 score (FTS5 ranking only, no vector component)
- Create retriever with nil embedder and nil embedStore — same behavior

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/rag/ -run TestRetriever -v
```

Expected: All tests pass.

**Commit:** `test(rag): add hybrid retriever tests`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Final build and test verification

**Verifies:** None (verification checkpoint)

**Files:** None (verification only)

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: All commands succeed. All Phase 1 and Phase 2 tests pass.

**Commit:** No commit needed — verification step only.
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->
