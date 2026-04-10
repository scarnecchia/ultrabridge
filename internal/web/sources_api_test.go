package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/source"
)

// initSourceTestDB creates an in-memory SQLite DB with sources table.
func initSourceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	// Create sources table
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create sources table: %v", err)
	}

	return db
}

// TestListSourcesEmpty verifies listing sources when empty.
func TestListSourcesEmpty(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	req := httptest.NewRequest("GET", "/api/sources", nil)
	w := httptest.NewRecorder()
	h.handleListSources(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleListSources returned %d, want 200", w.Code)
	}

	var sources []source.SourceRow
	if err := json.NewDecoder(w.Body).Decode(&sources); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(sources) != 0 {
		t.Errorf("sources length = %d, want 0", len(sources))
	}
}

// TestAddSourceValidation verifies input validation on POST /api/sources.
func TestAddSourceValidation(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	tests := []struct {
		name        string
		row         source.SourceRow
		wantCode    int
		wantErrText string
	}{
		{
			name:        "missing type",
			row:         source.SourceRow{Name: "Test", Enabled: true},
			wantCode:    http.StatusBadRequest,
			wantErrText: "type must be non-empty",
		},
		{
			name:        "missing name",
			row:         source.SourceRow{Type: "supernote", Enabled: true},
			wantCode:    http.StatusBadRequest,
			wantErrText: "name must be non-empty",
		},
		{
			name:        "invalid config_json",
			row:         source.SourceRow{Type: "supernote", Name: "Test", ConfigJSON: "{invalid"},
			wantCode:    http.StatusBadRequest,
			wantErrText: "config_json must be valid JSON",
		},
		{
			name:     "valid source",
			row:      source.SourceRow{Type: "supernote", Name: "Test", ConfigJSON: `{"path":"/data"}`},
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.row)
			req := httptest.NewRequest("POST", "/api/sources", bytes.NewReader(body))
			w := httptest.NewRecorder()
			h.handleAddSource(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}

			if tt.wantErrText != "" {
				var errResp map[string]string
				json.NewDecoder(w.Body).Decode(&errResp)
				if errResp["error"] != tt.wantErrText {
					t.Errorf("error = %q, want %q", errResp["error"], tt.wantErrText)
				}
			}
		})
	}
}

// TestAddSourceSucceedsAndReturnsID verifies POST /api/sources creates a source.
func TestAddSourceSucceedsAndReturnsID(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	row := source.SourceRow{
		Type:       "supernote",
		Name:       "My Notes",
		Enabled:    true,
		ConfigJSON: `{"notes_path":"/data/notes"}`,
	}
	body, _ := json.Marshal(row)
	req := httptest.NewRequest("POST", "/api/sources", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.handleAddSource(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
		t.Logf("body: %s", w.Body.String())
	}

	var result map[string]int64
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if result["id"] == 0 {
		t.Error("id = 0, want non-zero")
	}

	// Verify it was saved by listing
	req = httptest.NewRequest("GET", "/api/sources", nil)
	w = httptest.NewRecorder()
	h.handleListSources(w, req)

	var sources []source.SourceRow
	json.NewDecoder(w.Body).Decode(&sources)

	if len(sources) != 1 {
		t.Errorf("sources length = %d, want 1", len(sources))
	}
	if sources[0].Name != "My Notes" {
		t.Errorf("name = %q, want 'My Notes'", sources[0].Name)
	}
}

// TestUpdateSourceValidation verifies input validation on PUT /api/sources/{id}.
func TestUpdateSourceValidation(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	// Add a source first
	ctx := context.Background()
	id, _ := source.AddSource(ctx, db, source.SourceRow{
		Type: "supernote",
		Name: "Original",
	})

	// Try to update with empty type
	update := source.SourceRow{
		Name: "Updated",
	}
	body, _ := json.Marshal(update)
	idStr := strconv.FormatInt(id, 10)
	req := httptest.NewRequest("PUT", "/api/sources/"+idStr, bytes.NewReader(body))
	req.SetPathValue("id", idStr)
	w := httptest.NewRecorder()
	h.handleUpdateSource(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty type", w.Code)
	}
}

// TestUpdateSourceSucceeds verifies PUT /api/sources/{id} updates a source.
func TestUpdateSourceSucceeds(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	// Add a source
	ctx := context.Background()
	id, _ := source.AddSource(ctx, db, source.SourceRow{
		Type:    "supernote",
		Name:    "Original",
		Enabled: false,
	})

	// Update it
	update := source.SourceRow{
		ID:      id,
		Type:    "supernote",
		Name:    "Updated",
		Enabled: true,
	}
	body, _ := json.Marshal(update)
	req := httptest.NewRequest("PUT", "/api/sources/1", bytes.NewReader(body))
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.handleUpdateSource(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
		t.Logf("body: %s", w.Body.String())
	}

	// Verify the update
	req = httptest.NewRequest("GET", "/api/sources", nil)
	w = httptest.NewRecorder()
	h.handleListSources(w, req)

	var sources []source.SourceRow
	json.NewDecoder(w.Body).Decode(&sources)

	if len(sources) != 1 {
		t.Errorf("sources length = %d, want 1", len(sources))
	}
	if sources[0].Name != "Updated" {
		t.Errorf("name = %q, want 'Updated'", sources[0].Name)
	}
	if !sources[0].Enabled {
		t.Error("enabled = false, want true")
	}
}

// TestDeleteSourceSucceeds verifies DELETE /api/sources/{id} removes a source.
func TestDeleteSourceSucceeds(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	// Add a source
	ctx := context.Background()
	_, _ = source.AddSource(ctx, db, source.SourceRow{
		Type: "supernote",
		Name: "ToDelete",
	})

	// Delete it
	req := httptest.NewRequest("DELETE", "/api/sources/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h.handleDeleteSource(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify it's gone
	req = httptest.NewRequest("GET", "/api/sources", nil)
	w = httptest.NewRecorder()
	h.handleListSources(w, req)

	var sources []source.SourceRow
	json.NewDecoder(w.Body).Decode(&sources)

	if len(sources) != 0 {
		t.Errorf("sources length = %d, want 0", len(sources))
	}
}

// TestDeleteSourceNotFound verifies DELETE returns 404 for nonexistent source.
func TestDeleteSourceNotFound(t *testing.T) {
	db := initSourceTestDB(t)
	defer db.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	h := &Handler{
		noteDB:        db,
		logger:        logger,
		broadcaster:   broadcaster,
		runningConfig: nil,
	}

	req := httptest.NewRequest("DELETE", "/api/sources/999", nil)
	req.SetPathValue("id", "999")
	w := httptest.NewRecorder()
	h.handleDeleteSource(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
