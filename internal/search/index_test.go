package search

import (
	"context"
	"testing"

	"github.com/sysop/ultrabridge/internal/notedb"
)

func openTestIndex(t *testing.T) *Store {
	t.Helper()
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

// AC6.1 + AC6.2: Indexed content is retrievable; result has path, page, snippet
func TestSearch_IndexAndQuery(t *testing.T) {
	idx := openTestIndex(t)
	ctx := context.Background()

	if err := idx.Index(ctx, NoteDocument{Path: "/note1.note", Page: 0, BodyText: "hello world"}); err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := idx.Search(ctx, SearchQuery{Text: "hello"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Path != "/note1.note" {
		t.Errorf("path = %q, want /note1.note", results[0].Path)
	}
	if results[0].Snippet == "" {
		t.Error("expected non-empty snippet")
	}
}

// AC6.3: Results ordered by relevance
func TestSearch_Ordering(t *testing.T) {
	idx := openTestIndex(t)
	ctx := context.Background()

	idx.Index(ctx, NoteDocument{Path: "/low.note", Page: 0, BodyText: "hello once"})
	idx.Index(ctx, NoteDocument{Path: "/high.note", Page: 0, BodyText: "hello hello hello hello"})

	results, err := idx.Search(ctx, SearchQuery{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Path != "/high.note" {
		t.Errorf("expected high.note ranked first, got %s", results[0].Path)
	}
}

// AC6.4: Re-indexing same path+page replaces content
func TestSearch_Reindex(t *testing.T) {
	idx := openTestIndex(t)
	ctx := context.Background()

	idx.Index(ctx, NoteDocument{Path: "/note.note", Page: 0, BodyText: "old text"})
	idx.Index(ctx, NoteDocument{Path: "/note.note", Page: 0, BodyText: "new text"})

	newResults, _ := idx.Search(ctx, SearchQuery{Text: "new"})
	if len(newResults) == 0 {
		t.Error("expected to find 'new text' after re-index")
	}
	oldResults, _ := idx.Search(ctx, SearchQuery{Text: "old"})
	if len(oldResults) != 0 {
		t.Error("expected 'old text' to be replaced and not findable")
	}
}

// AC6.5: Empty query returns empty results, not an error
func TestSearch_EmptyQuery(t *testing.T) {
	idx := openTestIndex(t)
	results, err := idx.Search(context.Background(), SearchQuery{Text: ""})
	if err != nil {
		t.Errorf("empty query returned error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("empty query returned %d results, want 0", len(results))
	}
}

func TestSearch_Delete(t *testing.T) {
	idx := openTestIndex(t)
	ctx := context.Background()
	idx.Index(ctx, NoteDocument{Path: "/del.note", Page: 0, BodyText: "deleteme"})
	idx.Delete(ctx, "/del.note")
	results, _ := idx.Search(ctx, SearchQuery{Text: "deleteme"})
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}
