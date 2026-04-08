package notedb

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpen_CreatesSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify all expected tables and virtual tables exist
	for _, tbl := range []string{"notes", "jobs", "note_content", "note_fts", "note_embeddings"} {
		var name string
		err := db.QueryRowContext(context.Background(),
			"SELECT name FROM sqlite_master WHERE name=?", tbl).Scan(&name)
		if err != nil {
			t.Errorf("table/vtable %q not found: %v", tbl, err)
		}
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db1.Close()

	db2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open (idempotent): %v", err)
	}
	db2.Close()
}

func TestOpen_WALMode(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode pragma: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpen_ForeignKeysEnabled(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	var fk int
	if err := db.QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestOpen_NoteEmbeddingsSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify table exists
	var name string
	err = db.QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name='note_embeddings'").Scan(&name)
	if err != nil {
		t.Fatalf("note_embeddings table not found: %v", err)
	}

	// Verify columns exist
	requiredCols := map[string]bool{
		"note_path":  false,
		"page":       false,
		"embedding":  false,
		"model":      false,
		"created_at": false,
	}
	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM pragma_table_info('note_embeddings')")
	if err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scanning column: %v", err)
		}
		if _, exists := requiredCols[col]; exists {
			requiredCols[col] = true
		}
	}

	for col, found := range requiredCols {
		if !found {
			t.Errorf("column %q not found in note_embeddings", col)
		}
	}

	// Verify UNIQUE constraint on (note_path, page)
	// Insert a row
	_, err = db.ExecContext(context.Background(),
		"INSERT INTO note_embeddings (note_path, page, embedding, model, created_at) VALUES (?, ?, ?, ?, ?)",
		"/test/note.pdf", 1, []byte{1, 2, 3}, "test-model", 1000)
	if err != nil {
		t.Fatalf("insert first row: %v", err)
	}

	// Try to insert a duplicate (same note_path, page)
	_, err = db.ExecContext(context.Background(),
		"INSERT INTO note_embeddings (note_path, page, embedding, model, created_at) VALUES (?, ?, ?, ?, ?)",
		"/test/note.pdf", 1, []byte{4, 5, 6}, "test-model", 2000)
	if err == nil {
		t.Error("expected UNIQUE constraint violation, but insert succeeded")
	}

	// Insert with different page should succeed
	_, err = db.ExecContext(context.Background(),
		"INSERT INTO note_embeddings (note_path, page, embedding, model, created_at) VALUES (?, ?, ?, ?, ?)",
		"/test/note.pdf", 2, []byte{4, 5, 6}, "test-model", 2000)
	if err != nil {
		t.Errorf("insert second row (different page): %v", err)
	}
}

func TestOpen_ChatSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// Verify chat_sessions table exists with expected columns
	requiredSessionCols := map[string]bool{
		"id":         false,
		"title":      false,
		"created_at": false,
		"updated_at": false,
	}
	rows, err := db.QueryContext(context.Background(),
		"SELECT name FROM pragma_table_info('chat_sessions')")
	if err != nil {
		t.Fatalf("pragma_table_info for chat_sessions: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scanning column: %v", err)
		}
		if _, exists := requiredSessionCols[col]; exists {
			requiredSessionCols[col] = true
		}
	}

	for col, found := range requiredSessionCols {
		if !found {
			t.Errorf("column %q not found in chat_sessions", col)
		}
	}

	// Verify chat_messages table exists with expected columns
	requiredMessageCols := map[string]bool{
		"id":         false,
		"session_id": false,
		"role":       false,
		"content":    false,
		"created_at": false,
	}
	rows, err = db.QueryContext(context.Background(),
		"SELECT name FROM pragma_table_info('chat_messages')")
	if err != nil {
		t.Fatalf("pragma_table_info for chat_messages: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatalf("scanning column: %v", err)
		}
		if _, exists := requiredMessageCols[col]; exists {
			requiredMessageCols[col] = true
		}
	}

	for col, found := range requiredMessageCols {
		if !found {
			t.Errorf("column %q not found in chat_messages", col)
		}
	}

	// Verify index exists
	var indexName string
	err = db.QueryRowContext(context.Background(),
		"SELECT name FROM sqlite_master WHERE type='index' AND name='idx_chat_messages_session'").Scan(&indexName)
	if err != nil {
		t.Errorf("index idx_chat_messages_session not found: %v", err)
	}
}
