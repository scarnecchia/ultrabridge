# RAG Retrieval Pipeline — Phase 4: MCP Server

**Goal:** Create a separate MCP server binary that exposes UltraBridge's note search, content, and images as tools for Claude.

**Architecture:** Standalone `cmd/ub-mcp` binary using the official MCP Go SDK. Connects to UltraBridge's JSON API endpoints (from Phase 3) as an HTTP client. Supports stdio transport (Claude Desktop / Claude Code) and HTTP SSE transport (claude.ai web).

**Tech Stack:** `github.com/modelcontextprotocol/go-sdk/mcp` (official MCP Go SDK), Go stdlib `net/http`, `flag` package for CLI

**Scope:** 6 phases from original design (phase 4 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC4: MCP Server
- **rag-retrieval-pipeline.AC4.1 Success:** `cmd/ub-mcp` binary builds and runs. Verified by: `go build ./cmd/ub-mcp/` succeeds.
- **rag-retrieval-pipeline.AC4.2 Success:** `search_notes` tool accepts `query` (required), `folder`, `device`, `date_from`, `date_to`, `limit` parameters and returns text content with note metadata. Verified by: MCP client call returns results with note paths, pages, dates, and text.
- **rag-retrieval-pipeline.AC4.3 Success:** `get_note_pages` tool accepts `note_path` and returns all page content for that note. Verified by: MCP client call returns ordered page text.
- **rag-retrieval-pipeline.AC4.4 Success:** `get_note_image` tool accepts `note_path` and `page` and returns JPEG image via `ImageContent`. Verified by: MCP client call returns base64-encoded JPEG.
- **rag-retrieval-pipeline.AC4.5 Success:** MCP server supports stdio transport by default (for Claude Desktop / Claude Code). Verified by: running `ub-mcp` with stdin/stdout piping works with MCP inspector.
- **rag-retrieval-pipeline.AC4.6 Success:** MCP server supports HTTP SSE transport via `--http :PORT` flag (for claude.ai web). Verified by: running with `--http :8081` and connecting via SSE works.
- **rag-retrieval-pipeline.AC4.7 Success:** MCP server connects to UltraBridge's JSON API endpoints (configurable base URL). Verified by: `UB_MCP_API_URL` env var sets the API base URL.
- **rag-retrieval-pipeline.AC4.8 Success:** Search results include UltraBridge URLs for linking back to the web UI. Verified by: `search_notes` results include `url` field pointing to `/files/history?path=...`.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
## Subcomponent A: MCP Server Binary (Infrastructure)

<!-- START_TASK_1 -->
### Task 1: Add MCP SDK dependency and create cmd/ub-mcp scaffold

**Verifies:** rag-retrieval-pipeline.AC4.1

**Files:**
- Modify: `/home/jtd/ultrabridge/go.mod` (add MCP SDK dependency)
- Create: `/home/jtd/ultrabridge/cmd/ub-mcp/main.go`

**Implementation:**

Add the MCP Go SDK dependency:

```bash
go get -C /home/jtd/ultrabridge github.com/modelcontextprotocol/go-sdk@latest
```

**Note on SDK version:** The `@latest` tag resolves to whatever is current at execution time. The code samples are based on the v1.5.0 API surface (`mcp.NewServer`, `mcp.AddTool`, `mcp.NewStreamableHTTPHandler`, `mcp.StdioTransport`). If the SDK API has changed, the executor should check `pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp` and adjust the code accordingly. Consider pinning to a specific version after confirming the API works.

Create `cmd/ub-mcp/main.go` with the server scaffold:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	httpAddr := flag.String("http", "", "HTTP SSE address (e.g., :8081). If empty, uses stdio transport.")
	flag.Parse()

	apiURL := os.Getenv("UB_MCP_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8443"
	}
	apiUser := os.Getenv("UB_MCP_API_USER")
	apiPass := os.Getenv("UB_MCP_API_PASS")

	client := newAPIClient(apiURL, apiUser, apiPass)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ultrabridge-notes",
		Version: "1.0.0",
	}, nil)

	registerTools(server, client)

	if *httpAddr != "" {
		// HTTP SSE transport
		handler := mcp.NewStreamableHTTPHandler(server)
		log.Printf("ub-mcp listening on %s (HTTP SSE)", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, handler); err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	} else {
		// stdio transport (default)
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("stdio server failed: %v", err)
		}
	}
}
```

Add an API client helper struct in the same file (or a separate `client.go` if the executor prefers):

```go
type apiClient struct {
	baseURL  string
	user     string
	pass     string
	http     *http.Client
}

func newAPIClient(baseURL, user, pass string) *apiClient {
	return &apiClient{
		baseURL: baseURL,
		user:    user,
		pass:    pass,
		http:    &http.Client{},
	}
}

// get performs a GET request to the UltraBridge API with Basic Auth.
func (c *apiClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	return c.http.Do(req)
}
```

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
```

Expected: Build succeeds.

**Commit:** `feat(mcp): scaffold ub-mcp binary with MCP SDK`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Register MCP tools (search_notes, get_note_pages, get_note_image)

**Verifies:** rag-retrieval-pipeline.AC4.2, rag-retrieval-pipeline.AC4.3, rag-retrieval-pipeline.AC4.4, rag-retrieval-pipeline.AC4.7, rag-retrieval-pipeline.AC4.8

**Files:**
- Create: `/home/jtd/ultrabridge/cmd/ub-mcp/tools.go`

**Implementation:**

Create `cmd/ub-mcp/tools.go` with tool registration and handlers. Each tool calls the UltraBridge JSON API endpoints (from Phase 3) and formats results for Claude.

```go
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Input types for MCP tools

type SearchNotesInput struct {
	Query    string `json:"query" jsonschema:"required,search query string"`
	Folder   string `json:"folder,omitempty" jsonschema:"filter by folder name"`
	Device   string `json:"device,omitempty" jsonschema:"filter by device model"`
	DateFrom string `json:"date_from,omitempty" jsonschema:"start date filter (YYYY-MM-DD)"`
	DateTo   string `json:"date_to,omitempty" jsonschema:"end date filter (YYYY-MM-DD)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum results (default 10)"`
}

type GetNotePagesInput struct {
	NotePath string `json:"note_path" jsonschema:"required,absolute filesystem path to the note"`
}

type GetNoteImageInput struct {
	NotePath string `json:"note_path" jsonschema:"required,absolute filesystem path to the note"`
	Page     int    `json:"page" jsonschema:"required,page number (0-indexed)"`
}

func registerTools(server *mcp.Server, client *apiClient) {
	// Tool 1: search_notes
	mcp.AddTool(server, &mcp.Tool{
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

		// Format as readable text for Claude
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
	})

	// Tool 2: get_note_pages
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_note_pages",
		Description: "Get all page text content for a specific note. Returns pages ordered by page number with body text and title.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetNotePagesInput) (*mcp.CallToolResult, any, error) {
		if input.NotePath == "" {
			return nil, nil, fmt.Errorf("note_path is required")
		}

		// URL-encode the path (remove leading slash since API expects it in the path)
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
	})

	// Tool 3: get_note_image
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_note_image",
		Description: "Get the rendered page image (JPEG) from a note. Returns the image for visual inspection of handwritten content.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetNoteImageInput) (*mcp.CallToolResult, any, error) {
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

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					Type:     "image",
					Data:     base64.StdEncoding.EncodeToString(imageData),
					MIMEType: "image/jpeg",
				},
			},
		}, nil, nil
	})
}
```

Key design decisions:
- MCP server is a thin API client — no internal package imports, communicates via HTTP only
- `search_notes` formats results as readable text with URLs for Claude to reference
- `get_note_image` returns `ImageContent` with base64-encoded JPEG for visual inspection
- API credentials via env vars (`UB_MCP_API_URL`, `UB_MCP_API_USER`, `UB_MCP_API_PASS`) — not CLI args (security, AC4.7)
- Default limit of 10 for search (reasonable for LLM context windows)

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
```

Expected: Build succeeds.

**Commit:** `feat(mcp): register search_notes, get_note_pages, get_note_image tools`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
## Subcomponent B: Testing and Transport Verification

<!-- START_TASK_3 -->
### Task 3: Tests for MCP tools

**Verifies:** rag-retrieval-pipeline.AC4.2, rag-retrieval-pipeline.AC4.3, rag-retrieval-pipeline.AC4.4, rag-retrieval-pipeline.AC4.5, rag-retrieval-pipeline.AC4.6, rag-retrieval-pipeline.AC4.8

**Files:**
- Create: `/home/jtd/ultrabridge/cmd/ub-mcp/tools_test.go`

**Testing:**

Use `httptest.NewServer` to mock the UltraBridge JSON API. Create a mock API server that returns canned JSON responses for `/api/search`, `/api/notes/.../pages`, and `/api/notes/.../pages/N/image`.

Tests must verify:

**rag-retrieval-pipeline.AC4.2 — search_notes tool:**
- Call search_notes with query, verify text content returned contains note paths, pages, and text
- Call search_notes with folder/device/date filters, verify query params passed to API correctly
- Call search_notes without query, verify error returned

**rag-retrieval-pipeline.AC4.3 — get_note_pages tool:**
- Call get_note_pages with valid path, verify text content returned with ordered pages
- Call get_note_pages with unknown path, verify error message

**rag-retrieval-pipeline.AC4.4 — get_note_image tool:**
- Call get_note_image, verify ImageContent returned with base64-encoded JPEG and correct MIME type
- Verify the base64 data decodes to the same bytes the mock server returned

**rag-retrieval-pipeline.AC4.5 — stdio transport:**
- Verify server builds and can be started in stdio mode (build test is sufficient — actual stdio testing requires MCP inspector)

**rag-retrieval-pipeline.AC4.6 — HTTP transport:**
- Verify `--http` flag is parsed and server can be started in HTTP mode (verify flag parsing)

**rag-retrieval-pipeline.AC4.8 — URLs in results:**
- Verify search_notes results include full URL (baseURL + `/files/history?path=...`)

For testing tool handlers directly, create the `apiClient` with the mock server URL and call the tool handler functions through the MCP server's `CallTool` method, or test the API client + response formatting separately.

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./cmd/ub-mcp/ -v
```

Expected: All tests pass.

**Commit:** `test(mcp): add MCP tool tests`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Final build and verification

**Verifies:** rag-retrieval-pipeline.AC4.1

**Files:** None

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: Both binaries build. All tests pass. No vet warnings.

**Commit:** No commit — verification only.
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->
