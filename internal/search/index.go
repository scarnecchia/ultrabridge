package search

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SearchIndex indexes and queries note page content.
type SearchIndex interface {
	Index(ctx context.Context, doc NoteDocument) error
	Search(ctx context.Context, q SearchQuery) ([]SearchResult, error)
	Delete(ctx context.Context, path string) error
	// IndexPage satisfies processor.Indexer — convenience wrapper around Index.
	// titleText and keywords are populated for page 0 only; pass empty strings for other pages.
	IndexPage(ctx context.Context, path string, pageIdx int, source, bodyText, titleText, keywords string) error
	// GetContent returns all indexed content for a note, ordered by page.
	GetContent(ctx context.Context, path string) ([]NoteDocument, error)
}

// Store implements SearchIndex using SQLite FTS5.
type Store struct {
	db *sql.DB
}

// New creates a search Store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) IndexPage(ctx context.Context, path string, pageIdx int, source, bodyText, titleText, keywords string) error {
	return s.Index(ctx, NoteDocument{
		Path:      path,
		Page:      pageIdx,
		BodyText:  bodyText,
		TitleText: titleText,
		Keywords:  keywords,
		Source:    source,
	})
}

func (s *Store) Index(ctx context.Context, doc NoteDocument) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO note_content (note_path, page, title_text, body_text, keywords, source, model, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_path, page) DO UPDATE SET
			title_text=excluded.title_text,
			body_text=excluded.body_text,
			keywords=excluded.keywords,
			source=excluded.source,
			model=excluded.model,
			indexed_at=excluded.indexed_at`,
		doc.Path, doc.Page, doc.TitleText, doc.BodyText, doc.Keywords,
		doc.Source, doc.Model, now,
	)
	if err != nil {
		return fmt.Errorf("search index: %w", err)
	}
	return nil
}

func (s *Store) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 25
	}

	// bm25() returns negative floats; ORDER BY ASC puts best matches first.
	// snippet() targets body_text (column index 3: note_path, page, title_text, body_text, keywords).
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			nc.note_path,
			nc.page,
			bm25(note_fts) AS score,
			snippet(note_fts, 3, '', '', '...', 25) AS snip
		FROM note_fts
		JOIN note_content nc ON nc.id = note_fts.rowid
		WHERE note_fts MATCH ?
		ORDER BY bm25(note_fts) ASC
		LIMIT ?`,
		escapeFTS5(q.Text), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.Path, &r.Page, &r.Score, &r.Snippet); err != nil {
			return nil, fmt.Errorf("search scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *Store) Delete(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM note_content WHERE note_path=?", path)
	return err
}

func (s *Store) GetContent(ctx context.Context, path string) ([]NoteDocument, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT note_path, page, COALESCE(title_text,''), COALESCE(body_text,''),
		       COALESCE(keywords,''), COALESCE(source,''), COALESCE(model,'')
		FROM note_content WHERE note_path=? ORDER BY page`, path)
	if err != nil {
		return nil, fmt.Errorf("get content: %w", err)
	}
	defer rows.Close()
	var docs []NoteDocument
	for rows.Next() {
		var d NoteDocument
		if err := rows.Scan(&d.Path, &d.Page, &d.TitleText, &d.BodyText, &d.Keywords, &d.Source, &d.Model); err != nil {
			return nil, fmt.Errorf("get content scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// escapeFTS5 wraps the user query in double quotes and escapes internal quotes,
// preventing FTS5 syntax injection while preserving phrase matching.
func escapeFTS5(input string) string {
	escaped := strings.ReplaceAll(input, `"`, `""`)
	return `"` + escaped + `"`
}
