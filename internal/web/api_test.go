package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/appconfig"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/rag"
	"github.com/sysop/ultrabridge/internal/search"
)

// mockRetriever implements rag.SearchRetriever for testing
type mockRetriever struct {
	results []rag.SearchResult
	err     error
}

func (m *mockRetriever) Search(_ context.Context, req rag.SearchRequest) ([]rag.SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

// mockSearchIndexWithContent implements SearchIndex with configurable content
type mockSearchIndexWithContent struct {
	docs map[string][]search.NoteDocument
}

func newMockSearchIndexWithContent() *mockSearchIndexWithContent {
	return &mockSearchIndexWithContent{
		docs: make(map[string][]search.NoteDocument),
	}
}

func (m *mockSearchIndexWithContent) Index(_ context.Context, _ search.NoteDocument) error {
	return nil
}
func (m *mockSearchIndexWithContent) Search(_ context.Context, _ search.SearchQuery) ([]search.SearchResult, error) {
	return nil, nil
}
func (m *mockSearchIndexWithContent) Delete(_ context.Context, _ string) error {
	return nil
}
func (m *mockSearchIndexWithContent) IndexPage(_ context.Context, _ string, _ int, _, _, _, _ string) error {
	return nil
}
func (m *mockSearchIndexWithContent) GetContent(_ context.Context, path string) ([]search.NoteDocument, error) {
	if docs, ok := m.docs[path]; ok {
		return docs, nil
	}
	return []search.NoteDocument{}, nil
}
func (m *mockSearchIndexWithContent) ListFolders(_ context.Context) ([]string, error) {
	return nil, nil
}

// TestAPISearchSuccess verifies AC3.1: GET /api/search?q=... returns JSON array
func TestAPISearchSuccess(t *testing.T) {
	retriever := &mockRetriever{
		results: []rag.SearchResult{
			{
				NotePath:  "/home/user/test.note",
				Page:      0,
				BodyText:  "This is test content",
				TitleText: "Test Title",
				Score:     0.95,
				Folder:    "folder1",
				Device:    "Supernote",
				NoteDate:  time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var results []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&results); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	result := results[0]
	if result["note_path"] != "/home/user/test.note" {
		t.Errorf("Expected note_path '/home/user/test.note', got %v", result["note_path"])
	}
	if result["page"] != float64(0) {
		t.Errorf("Expected page 0, got %v", result["page"])
	}
	if result["body_text"] != "This is test content" {
		t.Errorf("Expected body_text 'This is test content', got %v", result["body_text"])
	}
	if result["score"] != 0.95 {
		t.Errorf("Expected score 0.95, got %v", result["score"])
	}
	if _, ok := result["url"]; !ok {
		t.Error("Expected 'url' field in response")
	}
}

// TestAPISearchMissingQ verifies AC3.5: missing q parameter returns 400
func TestAPISearchMissingQ(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPISearchInvalidFromDate verifies AC3.5: invalid from date returns 400
func TestAPISearchInvalidFromDate(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search?q=test&from=invalid-date", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if errMsg, ok := errResp["error"]; !ok || !bytes.Contains([]byte(errMsg), []byte("YYYY-MM-DD")) {
		t.Error("Expected error message to mention YYYY-MM-DD format")
	}
}

// TestAPISearchInvalidLimit verifies AC3.5: invalid limit returns 400
func TestAPISearchInvalidLimit(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search?q=test&limit=invalid", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}
}

// TestAPISearchWithParameters verifies query parameter parsing
func TestAPISearchWithParameters(t *testing.T) {
	mockRetr := &mockRetriever{
		results: []rag.SearchResult{},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", mockRetr, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	// Test with all parameters
	req := httptest.NewRequest("GET", "/api/search?q=test&folder=docs&device=Supernote&limit=50", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}
}

// TestAPIGetPagesSuccess verifies AC3.2: GET /api/notes/pages?path=... returns JSON array
func TestAPIGetPagesSuccess(t *testing.T) {
	searchIndex := newMockSearchIndexWithContent()
	searchIndex.docs["/home/user/test.note"] = []search.NoteDocument{
		{
			Path:      "/home/user/test.note",
			Page:      0,
			TitleText: "Page 1 Title",
			BodyText:  "Page 1 content",
			Keywords:  "keyword1,keyword2",
		},
		{
			Path:      "/home/user/test.note",
			Page:      1,
			TitleText: "Page 2 Title",
			BodyText:  "Page 2 content",
			Keywords:  "keyword3",
		},
	}

	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, searchIndex, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages?path=/home/user/test.note", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var pages []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&pages); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if len(pages) != 2 {
		t.Fatalf("Expected 2 pages, got %d", len(pages))
	}

	if pages[0]["page"] != float64(0) {
		t.Errorf("Expected page 0, got %v", pages[0]["page"])
	}
	if pages[0]["title_text"] != "Page 1 Title" {
		t.Errorf("Expected title 'Page 1 Title', got %v", pages[0]["title_text"])
	}
	if pages[1]["page"] != float64(1) {
		t.Errorf("Expected page 1, got %v", pages[1]["page"])
	}
}

// TestAPIGetPagesNotFound verifies AC3.2: unknown path returns 404
func TestAPIGetPagesNotFound(t *testing.T) {
	searchIndex := newMockSearchIndexWithContent()

	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, searchIndex, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages?path=/nonexistent/path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPIGetImageInvalidPageNumber verifies AC3.5: invalid page number returns 400
func TestAPIGetImageInvalidPageNumber(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages/image?path=/home/user/test.note&page=invalid", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPIGetImageNotAvailable verifies AC3.3: page image not available returns 404
func TestAPIGetImageNotAvailable(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	// Handler with no notesPathPrefix and no booxStore, so images aren't available
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages/image?path=/home/user/test.note&page=0", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPIGetPagesMissingPath verifies missing path parameter returns 400
func TestAPIGetPagesMissingPath(t *testing.T) {
	searchIndex := newMockSearchIndexWithContent()
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, searchIndex, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPIGetImageMissingPath verifies missing path parameter returns 400
func TestAPIGetImageMissingPath(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages/image?page=0", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPIGetImageMissingPage verifies missing page parameter returns 400
func TestAPIGetImageMissingPage(t *testing.T) {
	retriever := &mockRetriever{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/notes/pages/image?path=/home/user/test.note", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Error("Expected 'error' field in response")
	}
}

// TestAPISearchDisabledWhenRetrieverNil verifies that API endpoints aren't registered when retriever is nil
func TestAPISearchDisabledWhenRetrieverNil(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	// Create handler with retriever = nil
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", nil, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Route not registered should return 404
	if w.Code != http.StatusNotFound {
		t.Fatalf("Expected 404 (route not registered), got %d", w.Code)
	}
}

// TestAPIResponseContentType verifies JSON content-type header
func TestAPIResponseContentType(t *testing.T) {
	retriever := &mockRetriever{
		results: []rag.SearchResult{},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster, nil, nil, "", retriever, nil, nil, RAGDisplayConfig{}, &appconfig.Config{})

	req := httptest.NewRequest("GET", "/api/search?q=test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
	}
}
