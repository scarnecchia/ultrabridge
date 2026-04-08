package rag

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	"github.com/sysop/ultrabridge/internal/notedb"
)

// mockEmbedder is a test double that can be configured to fail on specific inputs.
type mockEmbedder struct {
	embedFn func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text)
	}
	// Default: return a dummy embedding
	return []float32{0.1, 0.2, 0.3}, nil
}

// TestBackfill_AllPagesEmbedded verifies AC1.4:
// Insert several note_content rows without embeddings. Call Backfill().
// Verify note_embeddings rows now exist for each page.
func TestBackfill_AllPagesEmbedded(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := slog.Default()
	store := NewStore(db, logger)
	embedder := &mockEmbedder{}

	// Insert test note_content rows (no embeddings)
	_, err := db.ExecContext(ctx,
		`INSERT INTO note_content (note_path, page, body_text) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"note1.note", 0, "page 0 text",
		"note1.note", 1, "page 1 text",
		"note2.note", 0, "another note",
	)
	if err != nil {
		t.Fatalf("insert note_content: %v", err)
	}

	// Verify they have no embeddings yet
	unembedded, err := store.UnembeddedPages(ctx)
	if err != nil {
		t.Fatalf("UnembeddedPages: %v", err)
	}
	if len(unembedded) != 3 {
		t.Errorf("expected 3 unembedded pages, got %d", len(unembedded))
	}

	// Run backfill
	embedded, err := Backfill(ctx, store, embedder, "test-model", logger)
	if err != nil {
		t.Fatalf("Backfill failed: %v", err)
	}
	if embedded != 3 {
		t.Errorf("expected 3 embedded, got %d", embedded)
	}

	// Verify all pages now have embeddings
	unembedded, err = store.UnembeddedPages(ctx)
	if err != nil {
		t.Fatalf("UnembeddedPages after backfill: %v", err)
	}
	if len(unembedded) != 0 {
		t.Errorf("expected 0 unembedded pages after backfill, got %d", len(unembedded))
	}

	// Verify embeddings rows were created
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM note_embeddings`).Scan(&count)
	if err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 embeddings rows, got %d", count)
	}
}

// TestBackfill_EmptyPages returns 0 embedded with no error when there are no unembedded pages.
func TestBackfill_EmptyPages(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := slog.Default()
	store := NewStore(db, logger)
	embedder := &mockEmbedder{}

	embedded, err := Backfill(ctx, store, embedder, "test-model", logger)
	if err != nil {
		t.Fatalf("Backfill failed: %v", err)
	}
	if embedded != 0 {
		t.Errorf("expected 0 embedded, got %d", embedded)
	}
}

// TestBackfill_PartialFailure verifies:
// Set mock embedder to fail on specific pages. Verify other pages are still embedded.
// Verify failed pages remain in UnembeddedPages().
func TestBackfill_PartialFailure(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx := context.Background()
	logger := slog.Default()
	store := NewStore(db, logger)

	// Insert test note_content rows
	_, err := db.ExecContext(ctx,
		`INSERT INTO note_content (note_path, page, body_text) VALUES (?, ?, ?), (?, ?, ?), (?, ?, ?)`,
		"note1.note", 0, "page 0 text",
		"note1.note", 1, "page 1 text",
		"note2.note", 0, "another note",
	)
	if err != nil {
		t.Fatalf("insert note_content: %v", err)
	}

	// Mock embedder that fails on "page 1 text"
	embedder := &mockEmbedder{
		embedFn: func(ctx context.Context, text string) ([]float32, error) {
			if text == "page 1 text" {
				return nil, errTestEmbedFailed
			}
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}

	// Run backfill
	embedded, err := Backfill(ctx, store, embedder, "test-model", logger)
	if err != nil {
		t.Fatalf("Backfill failed: %v", err)
	}

	// Should have embedded 2 pages (note1.note page 0, note2.note page 0)
	if embedded != 2 {
		t.Errorf("expected 2 embedded, got %d", embedded)
	}

	// Verify failed page is still in UnembeddedPages
	unembedded, err := store.UnembeddedPages(ctx)
	if err != nil {
		t.Fatalf("UnembeddedPages: %v", err)
	}
	if len(unembedded) != 1 {
		t.Errorf("expected 1 unembedded page (the failed one), got %d", len(unembedded))
	}
	if unembedded[0].Page != 1 {
		t.Errorf("expected unembedded page to be page 1, got %d", unembedded[0].Page)
	}
}

// TestBackfill_ContextCancellation verifies:
// Cancel context mid-backfill. Verify some pages embedded, function returns with context error.
func TestBackfill_ContextCancellation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.Default()
	store := NewStore(db, logger)

	// Insert many test note_content rows
	for i := 0; i < 10; i++ {
		_, err := db.ExecContext(context.Background(),
			`INSERT INTO note_content (note_path, page, body_text) VALUES (?, ?, ?)`,
			"note1.note", i, "page text",
		)
		if err != nil {
			t.Fatalf("insert note_content: %v", err)
		}
	}

	// Mock embedder that cancels context after 3 calls
	callCount := 0
	embedder := &mockEmbedder{
		embedFn: func(ctx context.Context, text string) ([]float32, error) {
			callCount++
			if callCount >= 3 {
				cancel() // Cancel context on 3rd call
			}
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}

	// Run backfill
	embedded, err := Backfill(ctx, store, embedder, "test-model", logger)

	// Should return context error
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Some pages should be embedded before cancellation
	if embedded == 0 {
		t.Errorf("expected some pages embedded before cancellation, got 0")
	}
	if embedded > 10 {
		t.Errorf("expected at most 10 embedded, got %d", embedded)
	}
}

var errTestEmbedFailed = &embeddingError{"test embedding failed"}

type embeddingError struct {
	msg string
}

func (e *embeddingError) Error() string { return e.msg }
