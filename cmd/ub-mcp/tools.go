package main // FCIS: Imperative Shell

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchNotesInput is the input type for the search_notes tool.
type SearchNotesInput struct {
	Query    string `json:"query"`
	Folder   string `json:"folder,omitempty"`
	Device   string `json:"device,omitempty"`
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// GetNotePagesInput is the input type for the get_note_pages tool.
type GetNotePagesInput struct {
	NotePath string `json:"note_path"`
}

// GetNoteImageInput is the input type for the get_note_image tool.
type GetNoteImageInput struct {
	NotePath string `json:"note_path"`
	Page     int    `json:"page"`
}

// registerTools registers all MCP tools with the server.
func registerTools(server *mcp.Server, client *apiClient) {
	registerSearchNotes(server, client)
	registerGetNotePages(server, client)
	registerGetNoteImage(server, client)
	registerTaskTools(server, client)
}

// registerSearchNotes registers the search_notes tool.
func registerSearchNotes(server *mcp.Server, client *apiClient) {
	mcp.AddTool[SearchNotesInput, any](server, &mcp.Tool{
		Name:        "search_notes",
		Description: "Search handwritten notes by keyword query. Returns matching pages with text content, metadata, and links to the UltraBridge web UI. Supports filtering by folder, device, and date range.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchNotesInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
			return nil, nil, fmt.Errorf("query is required")
		}

		// Build query string for API
		params := url.Values{"q": {input.Query}}
		if input.Folder != "" {
			params.Set("folder", input.Folder)
		}
		if input.Device != "" {
			params.Set("device", input.Device)
		}
		if input.DateFrom != "" {
			params.Set("from", input.DateFrom)
		}
		if input.DateTo != "" {
			params.Set("to", input.DateTo)
		}
		limit := input.Limit
		if limit <= 0 {
			limit = 10
		}
		params.Set("limit", fmt.Sprintf("%d", limit))

		resp, err := client.get(ctx, "/api/search?"+params.Encode())
		if err != nil {
			return nil, nil, fmt.Errorf("API request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return nil, nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
		}

		// API shape: the /api/search endpoint returns service.SearchResult
		// with snake_case JSON tags — path/page/snippet/score. Earlier the
		// decoder expected richer fields (note_path/body_text/title_text/
		// folder/device/note_date/url) that the decoupled-architecture v1
		// API doesn't emit; every field silently got its zero value and the
		// MCP produced empty-body results.
		var results []struct {
			Path    string  `json:"path"`
			Page    int     `json:"page"`
			Snippet string  `json:"snippet"`
			Score   float64 `json:"score"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
			return nil, nil, fmt.Errorf("decode response: %w", err)
		}

		// Format as readable text for Claude. We synthesize a web-UI URL
		// that jumps directly to the file's detail view.
		var sb strings.Builder
		for i, r := range results {
			sb.WriteString(fmt.Sprintf("--- Result %d ---\n", i+1))
			sb.WriteString(fmt.Sprintf("Note: %s (page %d)\n", r.Path, r.Page))
			detailURL := fmt.Sprintf("%s/files?detail=%s", client.baseURL, url.QueryEscape(r.Path))
			sb.WriteString(fmt.Sprintf("URL: %s\n", detailURL))
			sb.WriteString(fmt.Sprintf("Text:\n%s\n\n", r.Snippet))
		}

		if len(results) == 0 {
			sb.WriteString("No results found.\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})
}

// registerGetNotePages registers the get_note_pages tool.
func registerGetNotePages(server *mcp.Server, client *apiClient) {
	mcp.AddTool[GetNotePagesInput, any](server, &mcp.Tool{
		Name:        "get_note_pages",
		Description: "Get all page text content for a specific note. Returns pages ordered by page number with body text and title.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetNotePagesInput) (*mcp.CallToolResult, any, error) {
		if input.NotePath == "" {
			return nil, nil, fmt.Errorf("note_path is required")
		}

		// API path construction
		params := url.Values{"path": {input.NotePath}}
		apiPath := "/api/notes/pages?" + params.Encode()
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
	})
}

// registerGetNoteImage registers the get_note_image tool.
func registerGetNoteImage(server *mcp.Server, client *apiClient) {
	mcp.AddTool[GetNoteImageInput, any](server, &mcp.Tool{
		Name:        "get_note_image",
		Description: "Get the rendered page image (JPEG) from a note. Returns the image for visual inspection of handwritten content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetNoteImageInput) (*mcp.CallToolResult, any, error) {
		if input.NotePath == "" {
			return nil, nil, fmt.Errorf("note_path is required")
		}

		params := url.Values{
			"path": {input.NotePath},
			"page": {fmt.Sprintf("%d", input.Page)},
		}
		apiPath := "/api/notes/pages/image?" + params.Encode()
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

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					Data:     imageData,
					MIMEType: "image/jpeg",
				},
			},
		}, nil, nil
	})
}
