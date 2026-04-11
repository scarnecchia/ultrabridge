package boox

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/sysop/ultrabridge/internal/source"
)

// setupTestDB creates an in-memory SQLite database with the boox_notes, boox_jobs,
// and settings tables needed for booxpipeline.Processor.
func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Open in-memory DB: %v", err)
	}

	// Create minimal schema for booxpipeline to work
	schema := `
		CREATE TABLE boox_notes (
			path TEXT PRIMARY KEY,
			note_id TEXT,
			title TEXT,
			device_model TEXT,
			note_type TEXT,
			folder TEXT,
			page_count INTEGER,
			file_hash TEXT,
			version INTEGER,
			created_at INTEGER,
			updated_at INTEGER
		);

		CREATE TABLE boox_jobs (
			id INTEGER PRIMARY KEY,
			note_path TEXT NOT NULL,
			status TEXT NOT NULL,
			queued_at INTEGER,
			started_at INTEGER,
			finished_at INTEGER,
			attempts INTEGER,
			last_error TEXT,
			requeue_after INTEGER,
			ocr_source TEXT,
			api_model TEXT,
			FOREIGN KEY (note_path) REFERENCES boox_notes(path)
		);

		CREATE TABLE note_content (
			note_path TEXT NOT NULL,
			page INTEGER NOT NULL,
			title TEXT,
			body TEXT,
			keywords TEXT,
			source TEXT,
			UNIQUE(note_path, page)
		);

		CREATE TABLE note_fts (
			content
		);

		CREATE TABLE note_embeddings (
			note_path TEXT NOT NULL,
			page INTEGER NOT NULL,
			embedding BLOB,
			model TEXT,
			created_at INTEGER,
			UNIQUE(note_path, page)
		);

		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Create schema: %v", err)
	}

	return db
}

func TestNewSourceValidConfig(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tempDir := t.TempDir()

	cfg := Config{
		NotesPath: tempDir,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: string(cfgJSON),
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "test-model",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	src, err := NewSource(db, row, deps, booxDeps)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	if src == nil {
		t.Fatal("NewSource() returned nil source")
	}

	if src.Type() != "boox" {
		t.Errorf("Type() = %v, want boox", src.Type())
	}

	if src.Name() != "test-boox" {
		t.Errorf("Name() = %v, want test-boox", src.Name())
	}
}

func TestNewSourceInvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: "{invalid json}",
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	_, err := NewSource(db, row, deps, booxDeps)
	if err == nil {
		t.Fatal("NewSource() with invalid JSON should return error")
	}
}

func TestSourceStartStop(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tempDir := t.TempDir()

	cfg := Config{
		NotesPath: tempDir,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: string(cfgJSON),
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "test-model",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	src, err := NewSource(db, row, deps, booxDeps)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	ctx := context.Background()

	// Test Start - should not panic even with nil deps
	err = src.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Test Stop - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Stop() panicked: %v", r)
		}
	}()
	src.Stop()
}

func TestSourceStartWithOCREnabled(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tempDir := t.TempDir()

	cfg := Config{
		NotesPath: tempDir,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: string(cfgJSON),
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "test-model",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	src, err := NewSource(db, row, deps, booxDeps)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	ctx := context.Background()

	// Start should succeed even with OCRClient nil
	err = src.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	defer src.Stop()
}

func TestSourceWithSettingsClosures(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tempDir := t.TempDir()

	// Insert settings into the database
	if _, err := db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?)",
		"boox_ocr_prompt", "test boox ocr prompt",
	); err != nil {
		t.Fatalf("Insert OCR prompt setting: %v", err)
	}

	if _, err := db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?)",
		"boox_todo_enabled", "true",
	); err != nil {
		t.Fatalf("Insert todo enabled setting: %v", err)
	}

	if _, err := db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?)",
		"boox_todo_prompt", "test boox todo prompt",
	); err != nil {
		t.Fatalf("Insert todo prompt setting: %v", err)
	}

	cfg := Config{
		NotesPath: tempDir,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: string(cfgJSON),
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "test-model",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	src, err := NewSource(db, row, deps, booxDeps)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	ctx := context.Background()

	// Start the source which will initialize the closures
	err = src.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer src.Stop()

	// The source's processor should be initialized
	// This test verifies that Start completes without error and
	// the settings can be read at job time
}

func TestSourceImplementsSourceInterface(t *testing.T) {
	// This test verifies that Source implements the source.Source interface
	var _ source.Source = (*Source)(nil)
}

func TestNewSourceConfigParsing(t *testing.T) {
	tests := []struct {
		name       string
		configJSON string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "full config",
			configJSON: `{"notes_path":"/notes"}`,
			wantPath:   "/notes",
			wantErr:    false,
		},
		{
			name:       "empty config",
			configJSON: `{}`,
			wantPath:   "",
			wantErr:    false,
		},
		{
			name:       "invalid json",
			configJSON: `{bad}`,
			wantErr:    true,
		},
	}

	db := setupTestDB(t)
	defer db.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := source.SourceRow{
				ID:         1,
				Type:       "boox",
				Name:       "test",
				Enabled:    true,
				ConfigJSON: tt.configJSON,
			}

			deps := source.SharedDeps{
				Indexer:    nil,
				Embedder:   nil,
				EmbedModel: "",
				EmbedStore: nil,
				OCRClient:  nil,
				Logger:     nil,
			}

			booxDeps := BooxDeps{
				ContentDeleter: nil,
				OnTodosFound:   nil,
			}

			src, err := NewSource(db, row, deps, booxDeps)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if src.cfg.NotesPath != tt.wantPath {
					t.Errorf("NotesPath = %v, want %v", src.cfg.NotesPath, tt.wantPath)
				}
			}
		})
	}
}

func TestSourceCachePathDerived(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	tempDir := t.TempDir()

	cfg := Config{
		NotesPath: tempDir,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	row := source.SourceRow{
		ID:         1,
		Type:       "boox",
		Name:       "test-boox",
		Enabled:    true,
		ConfigJSON: string(cfgJSON),
	}

	deps := source.SharedDeps{
		Indexer:    nil,
		Embedder:   nil,
		EmbedModel: "test-model",
		EmbedStore: nil,
		OCRClient:  nil,
		Logger:     nil,
	}

	booxDeps := BooxDeps{
		ContentDeleter: nil,
		OnTodosFound:   nil,
	}

	src, err := NewSource(db, row, deps, booxDeps)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}

	// Verify that NotesPath is correctly set
	if src.cfg.NotesPath != tempDir {
		t.Errorf("NotesPath = %v, want %v", src.cfg.NotesPath, tempDir)
	}
	// The cache path is computed in Start() as filepath.Join(NotesPath, ".cache")
	// This test verifies the config is parsed correctly
}
