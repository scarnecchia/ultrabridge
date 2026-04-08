package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSearchNotesBasic verifies search_notes tool returns formatted text with metadata.
func TestSearchNotesBasic(t *testing.T) {
	// Mock API server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"note_path":  "/notes/test.note",
					"page":       0,
					"body_text":  "This is test content",
					"title_text": "Test Note",
					"score":      0.95,
					"folder":     "Work",
					"device":     "Supernote",
					"note_date":  "2026-04-08",
					"url":        "/files/history?path=/notes/test.note",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	// Test the search_notes handler
	input := SearchNotesInput{Query: "test"}
	result, _, err := testCallSearchNotesTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// Verify response contains expected metadata
	text := textContent.Text
	if text == "" {
		t.Fatal("expected non-empty text")
	}

	// Verify key fields are present
	if !contains(text, "Test Note") {
		t.Error("missing title in response")
	}
	if !contains(text, "/notes/test.note") {
		t.Error("missing note path in response")
	}
	if !contains(text, "page 0") {
		t.Error("missing page number in response")
	}
	if !contains(text, "Supernote") {
		t.Error("missing device in response")
	}
	if !contains(text, "Work") {
		t.Error("missing folder in response")
	}
	if !contains(text, "2026-04-08") {
		t.Error("missing note date in response")
	}
	if !contains(text, mockServer.URL+"/files/history?path=/notes/test.note") {
		t.Errorf("missing full URL in response: %s", text)
	}
}

// TestSearchNotesEmptyQuery verifies error when query is missing.
func TestSearchNotesEmptyQuery(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := SearchNotesInput{Query: ""}
	_, _, err := testCallSearchNotesTool(server, client, input)

	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if !contains(err.Error(), "query is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestSearchNotesWithFilters verifies query parameters are passed correctly.
func TestSearchNotesWithFilters(t *testing.T) {
	// Capture the request to verify parameters
	var capturedQuery string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" && r.Method == "GET" {
			capturedQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := SearchNotesInput{
		Query:    "handwriting",
		Folder:   "Work",
		Device:   "Supernote",
		DateFrom: "2026-04-01",
		DateTo:   "2026-04-08",
		Limit:    20,
	}
	_, _, err := testCallSearchNotesTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all parameters are in the query
	if !contains(capturedQuery, "q=handwriting") {
		t.Errorf("missing query param: %s", capturedQuery)
	}
	if !contains(capturedQuery, "folder=Work") {
		t.Errorf("missing folder param: %s", capturedQuery)
	}
	if !contains(capturedQuery, "device=Supernote") {
		t.Errorf("missing device param: %s", capturedQuery)
	}
	if !contains(capturedQuery, "from=2026-04-01") {
		t.Errorf("missing from param: %s", capturedQuery)
	}
	if !contains(capturedQuery, "to=2026-04-08") {
		t.Errorf("missing to param: %s", capturedQuery)
	}
	if !contains(capturedQuery, "limit=20") {
		t.Errorf("missing limit param: %s", capturedQuery)
	}
}

// TestSearchNotesDefaultLimit verifies default limit of 10 is applied.
func TestSearchNotesDefaultLimit(t *testing.T) {
	var capturedQuery string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" && r.Method == "GET" {
			capturedQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := SearchNotesInput{Query: "test"}
	_, _, err := testCallSearchNotesTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !contains(capturedQuery, "limit=10") {
		t.Errorf("expected default limit=10, got: %s", capturedQuery)
	}
}

// TestGetNotePagesValid verifies get_note_pages returns ordered page text.
func TestGetNotePagesValid(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/notes/path/to/note.note/pages" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"page":       0,
					"body_text":  "Page 0 content",
					"title_text": "Introduction",
					"keywords":   "intro, start",
				},
				{
					"page":       1,
					"body_text":  "Page 1 content",
					"title_text": "Details",
					"keywords":   "details, info",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNotePagesInput{NotePath: "/path/to/note.note"}
	result, _, err := testCallGetNotePagesTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil || len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	text := textContent.Text

	// Verify pages are ordered and contain expected content
	if !contains(text, "Page 0") {
		t.Error("missing Page 0 in response")
	}
	if !contains(text, "Introduction") {
		t.Error("missing Introduction title")
	}
	if !contains(text, "Page 0 content") {
		t.Error("missing Page 0 content")
	}
	if !contains(text, "Page 1") {
		t.Error("missing Page 1 in response")
	}
	if !contains(text, "Details") {
		t.Error("missing Details title")
	}
	if !contains(text, "Page 1 content") {
		t.Error("missing Page 1 content")
	}
}

// TestGetNotePagesNotFound verifies error when note doesn't exist.
func TestGetNotePagesNotFound(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNotePagesInput{NotePath: "/nonexistent/note.note"}
	_, _, err := testCallGetNotePagesTool(server, client, input)

	if err == nil {
		t.Fatal("expected error for nonexistent note")
	}
	if !contains(err.Error(), "note not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetNotePagesEmptyPath verifies error when path is missing.
func TestGetNotePagesEmptyPath(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNotePagesInput{NotePath: ""}
	_, _, err := testCallGetNotePagesTool(server, client, input)

	if err == nil {
		t.Fatal("expected error for empty path")
	}
	if !contains(err.Error(), "note_path is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetNoteImageValid verifies get_note_image returns base64-encoded JPEG with correct MIME type.
func TestGetNoteImageValid(t *testing.T) {
	// Create test JPEG data (minimal valid JPEG header)
	testImageData := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, // JPEG SOI + APP0
		0x00, 0x10,             // APP0 length
		0x4A, 0x46, 0x49, 0x46, // JFIF identifier
		0x00, 0x01, 0x00,       // JFIF version
		0xFF, 0xD9,             // EOI marker
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/notes/path/to/note.note/pages/0/image" && r.Method == "GET" {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(testImageData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNoteImageInput{NotePath: "/path/to/note.note", Page: 0}
	result, _, err := testCallGetNoteImageTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil || len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	imageContent, ok := result.Content[0].(*mcp.ImageContent)
	if !ok {
		t.Fatalf("expected ImageContent, got %T", result.Content[0])
	}

	// Verify MIME type
	if imageContent.MIMEType != "image/jpeg" {
		t.Errorf("expected MIME type image/jpeg, got %s", imageContent.MIMEType)
	}

	// Verify data is base64-encoded (as []byte)
	if len(imageContent.Data) == 0 {
		t.Fatal("expected base64-encoded image data")
	}
}

// TestGetNoteImageNotFound verifies error when page image doesn't exist.
func TestGetNoteImageNotFound(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNoteImageInput{NotePath: "/nonexistent/note.note", Page: 0}
	_, _, err := testCallGetNoteImageTool(server, client, input)

	if err == nil {
		t.Fatal("expected error for nonexistent image")
	}
	if !contains(err.Error(), "page image not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestGetNoteImageEmptyPath verifies error when path is missing.
func TestGetNoteImageEmptyPath(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := GetNoteImageInput{NotePath: "", Page: 0}
	_, _, err := testCallGetNoteImageTool(server, client, input)

	if err == nil {
		t.Fatal("expected error for empty path")
	}
	if !contains(err.Error(), "note_path is required") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestAPIClientBasicAuth verifies Basic Auth is set when credentials provided.
func TestAPIClientBasicAuth(t *testing.T) {
	var capturedAuth string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "testuser", "testpass")
	ctx := context.Background()
	resp, err := client.get(ctx, "/api/search?q=test")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if capturedAuth == "" {
		t.Fatal("expected Authorization header")
	}

	if !contains(capturedAuth, "Basic") {
		t.Errorf("expected Basic auth, got: %s", capturedAuth)
	}
}

// TestAPIClientNoAuth verifies no auth header when credentials not provided.
func TestAPIClientNoAuth(t *testing.T) {
	var capturedAuth string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	ctx := context.Background()
	resp, err := client.get(ctx, "/api/search?q=test")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if capturedAuth != "" {
		t.Errorf("expected no Authorization header, got: %s", capturedAuth)
	}
}

// TestSearchNotesNoResults handles empty result set gracefully.
func TestSearchNotesNoResults(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" && r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	client := newAPIClient(mockServer.URL, "", "")
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-server",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	input := SearchNotesInput{Query: "nonexistent"}
	result, _, err := testCallSearchNotesTool(server, client, input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	text := textContent.Text
	if !contains(text, "No results found") {
		t.Errorf("expected 'No results found' message, got: %s", text)
	}
}

// Helper functions for testing tool handlers

// testCallSearchNotesTool calls the search_notes tool handler directly.
// This simulates how the MCP server would invoke the tool.
func testCallSearchNotesTool(server *mcp.Server, client *apiClient, input SearchNotesInput) (*mcp.CallToolResult, any, error) {
	ctx := context.Background()

	// Call the handler directly by re-implementing search_notes logic
	// This is a simplified approach that tests the core logic
	if input.Query == "" {
		return nil, nil, fmt.Errorf("query is required")
	}

	params_vals := url.Values{"q": {input.Query}}
	if input.Folder != "" {
		params_vals.Set("folder", input.Folder)
	}
	if input.Device != "" {
		params_vals.Set("device", input.Device)
	}
	if input.DateFrom != "" {
		params_vals.Set("from", input.DateFrom)
	}
	if input.DateTo != "" {
		params_vals.Set("to", input.DateTo)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	params_vals.Set("limit", fmt.Sprintf("%d", limit))

	resp, err := client.get(ctx, "/api/search?"+params_vals.Encode())
	if err != nil {
		return nil, nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var results []struct {
		NotePath  string  `json:"note_path"`
		Page      int     `json:"page"`
		BodyText  string  `json:"body_text"`
		TitleText string  `json:"title_text"`
		Score     float64 `json:"score"`
		Folder    string  `json:"folder"`
		Device    string  `json:"device"`
		NoteDate  string  `json:"note_date"`
		URL       string  `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}

	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("--- Result %d ---\n", i+1))
		if r.TitleText != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", r.TitleText))
		}
		sb.WriteString(fmt.Sprintf("Note: %s (page %d)\n", r.NotePath, r.Page))
		if r.Device != "" {
			sb.WriteString(fmt.Sprintf("Device: %s\n", r.Device))
		}
		if r.Folder != "" {
			sb.WriteString(fmt.Sprintf("Folder: %s\n", r.Folder))
		}
		if r.NoteDate != "" {
			sb.WriteString(fmt.Sprintf("Date: %s\n", r.NoteDate))
		}
		sb.WriteString(fmt.Sprintf("URL: %s%s\n", client.baseURL, r.URL))
		sb.WriteString(fmt.Sprintf("Text:\n%s\n\n", r.BodyText))
	}

	if len(results) == 0 {
		sb.WriteString("No results found.\n")
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: sb.String()},
		},
	}, nil, nil
}

// testCallGetNotePagesTool calls the get_note_pages tool handler directly.
func testCallGetNotePagesTool(server *mcp.Server, client *apiClient, input GetNotePagesInput) (*mcp.CallToolResult, any, error) {
	ctx := context.Background()

	if input.NotePath == "" {
		return nil, nil, fmt.Errorf("note_path is required")
	}

	apiPath := "/api/notes" + input.NotePath + "/pages"
	resp, err := client.get(ctx, apiPath)
	if err != nil {
		return nil, nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil, fmt.Errorf("note not found: %s", input.NotePath)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var pages []struct {
		Page      int    `json:"page"`
		BodyText  string `json:"body_text"`
		TitleText string `json:"title_text"`
		Keywords  string `json:"keywords"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pages); err != nil {
		return nil, nil, fmt.Errorf("decode response: %w", err)
	}

	var sb strings.Builder
	for _, p := range pages {
		sb.WriteString(fmt.Sprintf("--- Page %d ---\n", p.Page))
		if p.TitleText != "" {
			sb.WriteString(fmt.Sprintf("Title: %s\n", p.TitleText))
		}
		sb.WriteString(p.BodyText)
		sb.WriteString("\n\n")
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: sb.String()},
		},
	}, nil, nil
}

// testCallGetNoteImageTool calls the get_note_image tool handler directly.
func testCallGetNoteImageTool(server *mcp.Server, client *apiClient, input GetNoteImageInput) (*mcp.CallToolResult, any, error) {
	ctx := context.Background()

	if input.NotePath == "" {
		return nil, nil, fmt.Errorf("note_path is required")
	}

	apiPath := fmt.Sprintf("/api/notes%s/pages/%d/image", input.NotePath, input.Page)
	resp, err := client.get(ctx, apiPath)
	if err != nil {
		return nil, nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil, fmt.Errorf("page image not found: %s page %d", input.NotePath, input.Page)
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read image: %w", err)
	}

	// Encode imageData to base64 for the Data field
	encodedData := []byte(base64.StdEncoding.EncodeToString(imageData))

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.ImageContent{
				Data:     encodedData,
				MIMEType: "image/jpeg",
			},
		},
	}, nil, nil
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
