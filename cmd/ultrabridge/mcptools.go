package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpAPIClient calls UltraBridge's own JSON API endpoints using a
// persistent internal bearer token for self-authentication.
type mcpAPIClient struct {
	baseURL       string
	internalToken string
	http          *http.Client
	logger        *slog.Logger
	verbose       bool
}

func newMCPAPIClient(baseURL string, internalToken string, logger *slog.Logger, verbose bool) *mcpAPIClient {
	return &mcpAPIClient{
		baseURL:       baseURL,
		internalToken: internalToken,
		http:          &http.Client{},
		logger:        logger,
		verbose:       verbose,
	}
}

func (c *mcpAPIClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.internalToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.internalToken)
	}
	return c.http.Do(req)
}

// MCP tool input types

type searchNotesInput struct {
	Query    string `json:"query"`
	Folder   string `json:"folder,omitempty"`
	Device   string `json:"device,omitempty"`
	DateFrom string `json:"date_from,omitempty"`
	DateTo   string `json:"date_to,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type getNotePagesInput struct {
	NotePath string `json:"note_path"`
}

type getNoteImageInput struct {
	NotePath string `json:"note_path"`
	Page     int    `json:"page"`
}

func registerMCPTools(server *mcp.Server, client *mcpAPIClient) {
	// search_notes
	mcp.AddTool[searchNotesInput, any](server, &mcp.Tool{
		Name:        "search_notes",
		Description: "Search handwritten notes by keyword query. Returns matching pages with text content, metadata, and links to the UltraBridge web UI. Supports filtering by folder, device, and date range.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input searchNotesInput) (*mcp.CallToolResult, any, error) {
		if client.verbose && client.logger != nil {
			client.logger.Info("MCP tool call", "tool", "search_notes", "input", input)
		}
		if input.Query == "" {
			return nil, nil, fmt.Errorf("query is required")
		}
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

		if client.verbose && client.logger != nil {
			client.logger.Info("MCP tool result", "tool", "search_notes", "results", len(results))
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: sb.String()},
			},
		}, nil, nil
	})

	// get_note_pages
	mcp.AddTool[getNotePagesInput, any](server, &mcp.Tool{
		Name:        "get_note_pages",
		Description: "Get all page text content for a specific note. Returns pages ordered by page number with body text and title.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getNotePagesInput) (*mcp.CallToolResult, any, error) {
		if client.verbose && client.logger != nil {
			client.logger.Info("MCP tool call", "tool", "get_note_pages", "input", input)
		}
		if input.NotePath == "" {
			return nil, nil, fmt.Errorf("note_path is required")
		}
		params := url.Values{"path": {input.NotePath}}
		resp, err := client.get(ctx, "/api/notes/pages?"+params.Encode())
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

	// get_note_image
	mcp.AddTool[getNoteImageInput, any](server, &mcp.Tool{
		Name:        "get_note_image",
		Description: "Get the rendered page image (JPEG) from a note. Returns the image for visual inspection of handwritten content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input getNoteImageInput) (*mcp.CallToolResult, any, error) {
		if client.verbose && client.logger != nil {
			client.logger.Info("MCP tool call", "tool", "get_note_image", "input", input)
		}
		if input.NotePath == "" {
			return nil, nil, fmt.Errorf("note_path is required")
		}
		params := url.Values{
			"path": {input.NotePath},
			"page": {fmt.Sprintf("%d", input.Page)},
		}
		resp, err := client.get(ctx, "/api/notes/pages/image?"+params.Encode())
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

		if client.verbose && client.logger != nil {
			client.logger.Info("MCP tool result", "tool", "get_note_image", "bytes", len(imageData))
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
