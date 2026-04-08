# MCP Server (ub-mcp)

Last verified: 2026-04-08

## Purpose
Model Context Protocol server that exposes UltraBridge note search and retrieval
as MCP tools for AI agents (Claude Desktop, Cursor, etc.).

## Contracts
- **Exposes**: Three MCP tools: `search_notes` (hybrid search), `get_note_pages` (page content), `get_note_image` (JPEG rendering). Two transport modes: stdio (default) and HTTP SSE (`--http` flag).
- **Guarantees**: All tools delegate to UltraBridge JSON API via HTTP with Basic Auth. Image data returned as base64-encoded embedded images. Error responses use MCP error format.
- **Expects**: Running UltraBridge instance with JSON API endpoints enabled (requires retriever). Environment variables for API connection.

## Dependencies
- **Uses**: `github.com/modelcontextprotocol/go-sdk/mcp` (MCP server framework), UltraBridge JSON API (`/api/search`, `/api/notes/pages`, `/api/notes/pages/image`)
- **Used by**: AI agents via MCP protocol
- **Boundary**: Separate binary -- does NOT import any internal packages. Communicates with UltraBridge solely via HTTP API.

## Key Decisions
- Separate binary (not embedded in ultrabridge): allows independent deployment, different lifecycle
- HTTP API client: avoids importing internal packages, keeps MCP server loosely coupled
- Dual transport: stdio for Claude Desktop integration, HTTP SSE for network-accessible deployment

## Config
- `UB_MCP_API_URL` -- UltraBridge API base URL (default http://localhost:8443)
- `UB_MCP_API_USER` -- Basic Auth username
- `UB_MCP_API_PASS` -- Basic Auth password

## Key Files
- `main.go` -- Entry point, transport selection, API client setup
- `tools.go` -- MCP tool registration and handler implementations
- `tools_test.go` -- Tests with mock HTTP server

## Gotchas
- MCP SDK uses generics for tool input types (SearchNotesInput, GetNotePagesInput, GetNoteImageInput)
- Image responses encode JPEG as base64 with embedded image content type
- This binary has no access to SQLite or internal state -- it is purely an API client
