# Test Requirements: RAG Retrieval Pipeline

Maps each acceptance criterion to automated tests or human verification.

---

## AC1: Embedding Pipeline

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC1.1 | Unit | `internal/notedb/db_test.go` | Open in-memory DB, query `pragma_table_info('note_embeddings')`, verify columns `note_path TEXT`, `page INTEGER`, `embedding BLOB`, `model TEXT`, `created_at INTEGER` exist. Insert two rows with same `(note_path, page)` to confirm UNIQUE constraint enforcement. |
| AC1.1 | Unit | `internal/rag/store_test.go` | `Save()` round-trips float32 vectors through BLOB storage correctly. `Save()` with duplicate `(note_path, page)` upserts without error. |
| AC1.2 | Unit | `internal/booxpipeline/worker_test.go` | Process a synthetic .note file with a mock embedder and in-memory DB. After processing, `SELECT count(*) FROM note_embeddings WHERE note_path = ?` equals the page count. Verify mock embedder received each page's OCR text. |
| AC1.3 | Unit | `internal/processor/worker_test.go` | Process a Supernote .note file with mock OCR client and mock embedder. After processing, verify `note_embeddings` row exists for each OCR'd page. |
| AC1.4 | Unit | `internal/rag/backfill_test.go` | Insert `note_content` rows without corresponding `note_embeddings` rows. Call `Backfill()`. Verify `note_embeddings` rows now exist for all pages. Verify `UnembeddedPages()` returns empty after backfill. Test partial failure: mock embedder fails on specific pages, verify remaining pages still embedded. Test context cancellation mid-backfill. |
| AC1.5 | Unit | `internal/web/handler_test.go` | POST `/settings/backfill-embeddings` returns 303 redirect to `/settings`. When embedder is nil, the route returns 404. |
| AC1.6 | Unit | `internal/rag/store_test.go` | Insert embeddings via `Save()`, call `LoadAll()`, verify returned count matches inserted count. Verify `AllEmbeddings()` returns all loaded records with correct data. |
| AC1.7 | Unit | `internal/rag/embedder_test.go` | Mock HTTP server returns connection refused: `Embed()` returns error, no panic. Mock server returns non-200 status: `Embed()` returns descriptive error. |
| AC1.7 | Unit | `internal/booxpipeline/worker_test.go` | Process .note with embedder that always returns error. Verify job completes successfully (OCR text indexed), no `note_embeddings` row created. |
| AC1.7 | Unit | `internal/processor/worker_test.go` | Same as above for Supernote worker: embedder errors do not block OCR indexing. |

### Human Verification

| Criterion | Justification | Verification Approach |
|-----------|--------------|----------------------|
| AC1.4 (startup) | Startup backfill requires a running Ollama instance and real SQLite database with existing `note_content` data. Automated test covers the `Backfill()` function itself; the startup goroutine wiring is verified manually. | Delete rows from `note_embeddings`, restart UltraBridge, check logs for "starting embedding backfill" and "embedding backfill complete" messages. Verify rows repopulated. |
| AC1.6 (startup log) | Verifying the startup log message "loaded N embeddings into memory" requires running the full binary. | Start UltraBridge with embeddings in the database, check startup logs for the expected message with correct count. |
| AC1.8 | Performance measurement requires a real Ollama instance with the nomic-embed-text model loaded. | Run the pipeline with and without embedding enabled, compare per-page timing in logs. A `BenchmarkEmbed` test in `internal/rag/embedder_test.go` validates Go HTTP overhead is negligible; the Ollama round-trip (~150ms typical) is verified against the <500ms threshold on the deployment host. |

---

## AC2: Hybrid Retriever

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC2.1 | Unit | `internal/rag/retriever_test.go` | Insert pages into `note_content` with distinct text. Create embeddings via `rag.Store.Save()` for some pages. Create a mock embedder returning deterministic query vectors. Search and verify results include pages found by both FTS5 and vector similarity, merged with RRF scores. Verify ordering is by RRF score descending. |
| AC2.2 | Unit | `internal/rag/retriever_test.go` | Insert pages from different folders, devices, and dates into `note_content`, `boox_notes`, and `notes` tables. Search with `Folder` filter: only matching folder pages returned. Search with `Device` filter: only matching device pages returned. Search with `DateFrom`/`DateTo`: only pages within range returned. |
| AC2.3 | Unit | `internal/rag/retriever_test.go` | Insert a `boox_notes` row with `device_model="Palma2"` and `folder="Work"`. Insert a `notes` row with `rel_path="MyNotes/Personal/test.note"`. Search and verify results have populated `Device` and `Folder` fields from the JOINed tables. Boox result shows "Palma2", Supernote result shows "Supernote" with folder extracted from `rel_path`. |
| AC2.4 | Unit | `internal/rag/retriever_test.go` | Verify `SearchResult` struct has all required fields: `NotePath`, `Page`, `BodyText`, `TitleText`, `Score`, `Folder`, `Device`, `NoteDate`. Verify search results return non-empty metadata for notes with populated metadata tables. |
| AC2.5 | Unit | `internal/rag/retriever_test.go` | Create retriever with empty embedding store (no embeddings loaded). Search returns FTS5-only results. Create retriever with nil embedder and nil embedStore: same FTS5-only behavior, no panic. |

### Human Verification

None. All AC2 criteria are fully automatable with in-memory SQLite and mock embedders.

---

## AC3: JSON API Endpoints

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC3.1 | Unit | `internal/web/api_test.go` | GET `/api/search?q=test` with mock retriever returns 200 with JSON array containing `note_path`, `page`, `body_text`, `score`, `url` fields. Verify query parameter parsing for `folder`, `device`, `from`, `to`, `limit`. GET `/api/search` without `q` returns 400 with JSON error body. |
| AC3.2 | Unit | `internal/web/api_test.go` | GET `/api/notes/{path}/pages` with valid indexed path returns 200 with JSON array of pages ordered by page number. GET with unknown path returns 404 with JSON error body. |
| AC3.3 | Unit | `internal/web/api_test.go` | GET `/api/notes/{path}/pages/0/image` with valid Boox note (pre-populated cache JPEG) returns 200 with `Content-Type: image/jpeg`. GET with invalid page number returns 404. |
| AC3.4 | Unit | `internal/web/api_test.go` | Auth is handled at the mux level in `main.go` via `authMW.Wrap(webHandler)`. API routes are on the same handler, so they share the auth middleware. Test that routes are registered on the authenticated handler. |
| AC3.5 | Unit | `internal/web/api_test.go` | 400 for missing `q` param, invalid `from` date format, invalid `limit` value. 404 for unknown note path, page out of range. All error responses have JSON body with `error` key. |

### Human Verification

| Criterion | Justification | Verification Approach |
|-----------|--------------|----------------------|
| AC3.4 (full auth) | Confirming that an unauthenticated HTTP request to a running server returns 401 is best done end-to-end. | `curl -v http://localhost:8443/api/search?q=test` without credentials returns 401. `curl -u user:pass http://localhost:8443/api/search?q=test` returns 200. |

---

## AC4: MCP Server

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC4.1 | Unit | (build verification) | `go build ./cmd/ub-mcp/` succeeds. Verified as part of CI `go build ./...`. |
| AC4.2 | Unit | `cmd/ub-mcp/tools_test.go` | Mock UltraBridge API server via `httptest.NewServer` returns canned search JSON. Call `search_notes` tool with query, verify text content contains note paths, pages, and body text. Call with folder/device/date filters, verify query params forwarded to mock API correctly. Call without query, verify error returned. |
| AC4.3 | Unit | `cmd/ub-mcp/tools_test.go` | Mock API returns canned pages JSON. Call `get_note_pages` with valid path, verify text content returned with ordered pages. Call with unknown path (mock returns 404), verify error message. |
| AC4.4 | Unit | `cmd/ub-mcp/tools_test.go` | Mock API returns JPEG bytes. Call `get_note_image`, verify `ImageContent` returned with `MIMEType: "image/jpeg"`. Decode base64 `Data` field and verify it matches the mock server's response bytes. |
| AC4.8 | Unit | `cmd/ub-mcp/tools_test.go` | Verify `search_notes` results include full URL: `baseURL + /files/history?path=...`. Check the formatted text output contains the expected URL pattern. |

### Human Verification

| Criterion | Justification | Verification Approach |
|-----------|--------------|----------------------|
| AC4.5 | Stdio transport requires bidirectional stdin/stdout piping with a real MCP client. | Run `ub-mcp` binary, pipe through MCP Inspector (`npx @modelcontextprotocol/inspector`). Verify tool list appears and `search_notes` returns results. |
| AC4.6 | HTTP SSE transport requires a running server and an SSE client connection. | Run `ub-mcp --http :8081`, connect via MCP Inspector in SSE mode. Verify connection established and tools are listed. |
| AC4.7 | Verifying the env var changes the API base URL requires running the binary. | Set `UB_MCP_API_URL=http://192.168.9.52:8443`, run `ub-mcp`, call `search_notes`, verify it connects to the specified URL. |

---

## AC5: Local Chat Tab

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC5.2 | Unit | `internal/chat/handler_test.go` | Mock vLLM server via `httptest.NewServer` that streams SSE chunks. POST to `HandleAsk` with a question, verify response has `Content-Type: text/event-stream`. Parse SSE events and verify `session` event (with session_id), one or more `content` events, and a `done` event. |
| AC5.3 | Unit | `internal/chat/handler_test.go` | Capture the request body sent to the mock vLLM server. Verify the system prompt message contains "cite your sources using the format [filename, p.N]" and includes retrieved note text with `[filename, p.N]` labels. |
| AC5.5 | Unit | `internal/chat/store_test.go` | Create session, add user and assistant messages, retrieve messages in order. Verify `GetMessages()` returns messages sorted by `created_at ASC`. Delete session, verify messages also deleted. Verify `ListSessions()` returns sessions ordered by `updated_at DESC`. Verify adding a message updates the session's `updated_at`. |
| AC5.5 | Unit | `internal/notedb/db_test.go` | Verify `chat_sessions` and `chat_messages` tables exist after `Open()` with expected columns and index on `session_id`. |
| AC5.6 | Unit | `internal/chat/handler_test.go` | Configure handler with unreachable vLLM URL. POST to `HandleAsk`, verify SSE stream contains an `error` event with user-friendly message. Verify no panic or crash. |
| AC5.7 | Unit | `internal/chat/handler_test.go` | Verify handler uses the configured `apiURL` and `model` values. Capture the request to mock vLLM, verify `model` field matches the configured value and the URL matches the configured API URL. |

### Human Verification

| Criterion | Justification | Verification Approach |
|-----------|--------------|----------------------|
| AC5.1 | UI rendering (tab visibility, message display, streaming render) requires a browser. | Navigate to the Chat tab in a browser. Type a question. Verify streaming response appears incrementally. |
| AC5.4 | Citation linkification happens in browser JavaScript. | Ask a question that triggers citations. Verify `[filename, p.N]` patterns render as clickable links to `/files/history?path=...`. |
| AC5.5 (persistence) | Verifying page refresh preserves conversation requires browser interaction. | Send a chat message, refresh the page, verify the conversation is still visible. |

---

## AC6: Configuration and Deployment

### Automated Tests

| Criterion | Test Type | Test File | Description |
|-----------|-----------|-----------|-------------|
| AC6.1 | Unit | (build verification / config test) | Verify `Config` struct has fields `EmbedEnabled`, `OllamaURL`, `OllamaEmbedModel`, `ChatEnabled`, `ChatAPIURL`, `ChatModel`. Verify `Load()` with no env vars produces correct defaults. |
| AC6.3 | Unit | `internal/booxpipeline/worker_test.go` | Process a file with nil embedder (embedding disabled). Verify no embedding calls made, job completes normally. |
| AC6.3 | Unit | `internal/processor/worker_test.go` | Same nil-embedder verification for Supernote worker. |
| AC6.4 | Unit | `internal/web/handler_test.go` | GET `/` with nil `chatHandler` renders HTML without a "Chat" tab link. GET `/` with non-nil `chatHandler` renders HTML with a "Chat" tab link. |

### Human Verification

| Criterion | Justification | Verification Approach |
|-----------|--------------|----------------------|
| AC6.2 | `install.sh` is an interactive bash script. Automated testing of interactive prompts is fragile. | Run `install.sh`, verify new RAG Pipeline prompts appear after Boox section. Verify values written to `.ultrabridge.env`. |
| AC6.3 (runtime) | Verifying default config does not attempt Ollama connection requires running the binary. | Start UltraBridge with default config, verify no Ollama connection attempts in logs. |
| AC6.4 (runtime) | Verifying Chat tab is hidden requires a browser with default config. | Start UltraBridge with default config, navigate to web UI, verify no "Chat" tab appears. |

---

## Summary

| AC Group | Total Criteria | Automated | Human Only | Both |
|----------|---------------|-----------|------------|------|
| AC1 | 8 | 5 | 0 | 3 |
| AC2 | 5 | 5 | 0 | 0 |
| AC3 | 5 | 4 | 0 | 1 |
| AC4 | 8 | 4 | 3 | 1 |
| AC5 | 7 | 4 | 0 | 3 |
| AC6 | 4 | 2 | 0 | 2 |
| **Total** | **37** | **24** | **3** | **10** |

All 37 acceptance criteria are covered. 34 have automated test coverage. 3 require human verification only (AC4.5, AC4.6, AC6.2).
