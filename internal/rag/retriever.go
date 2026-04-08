// Pattern: Imperative Shell -- coordinates FTS5 search, vector similarity, and DB enrichment
package rag

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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

// Search implements SearchRetriever using hybrid fusion (FTS5 + vector similarity via RRF).
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
		// Apply post-merge filters (folder, device, date range)
		if req.Folder != "" && result.Folder != req.Folder {
			continue
		}
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

// enrichResult fetches metadata for a result via SQL JOINs and returns a populated SearchResult.
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
