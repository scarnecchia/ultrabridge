# Human Test Plan: RAG Retrieval Pipeline

## Prerequisites

- UltraBridge deployed on target host (192.168.9.52) via `install.sh`
- Ollama running with `nomic-embed-text:v1.5` model loaded
- vLLM running with `Qwen/Qwen3-8B` model
- Existing indexed notes (Supernote and Boox) with `note_content` rows in SQLite
- `go test -C /home/jtd/ultrabridge ./...` passing

---

## Phase 1: Embedding Pipeline (AC1)

| Step | Action | Expected |
|------|--------|----------|
| 1.1 | SSH to target host. Stop UltraBridge. Run `sqlite3 /path/to/notes.db "DELETE FROM note_embeddings"`. | Rows deleted. |
| 1.2 | Start UltraBridge with `UB_EMBED_ENABLED=true`. | Service starts without error. |
| 1.3 | Check startup logs (`journalctl -u ultrabridge -f`). | See "starting embedding backfill" followed by "embedding backfill complete: N pages embedded". |
| 1.4 | Run `sqlite3 /path/to/notes.db "SELECT COUNT(*) FROM note_embeddings"`. | Count matches N from step 1.3. |
| 1.5 | Check startup logs for "loaded N embeddings into memory". | Message present with correct count. |
| 1.6 | Upload a new .note file via WebDAV. Wait for OCR to complete. | Log shows embedding save after OCR indexing. New `note_embeddings` row exists. |
| 1.7 | Time the per-page embedding in logs. | Embedding latency per page under 500ms. |

## Phase 2: JSON API Authentication (AC3)

| Step | Action | Expected |
|------|--------|----------|
| 2.1 | `curl -v http://192.168.9.52:8443/api/search?q=test` (no credentials). | HTTP 401 Unauthorized. |
| 2.2 | `curl -u user:password http://192.168.9.52:8443/api/search?q=test`. | HTTP 200 with JSON array. |

## Phase 3: MCP Server (AC4)

| Step | Action | Expected |
|------|--------|----------|
| 3.1 | Build: `go build -C /home/jtd/ultrabridge -o /tmp/ub-mcp ./cmd/ub-mcp/`. | Binary compiles. |
| 3.2 | Run `UB_MCP_API_URL=http://192.168.9.52:8443 UB_MCP_API_USER=user UB_MCP_API_PASS=password /tmp/ub-mcp` with MCP Inspector. | Tool list: `search_notes`, `get_note_pages`, `get_note_image`. |
| 3.3 | Call `search_notes` with query "test" in MCP Inspector. | Results with note paths, pages, body text, URLs. |
| 3.4 | Run with `--http :8081`, connect via SSE mode. | Connection established, tools listed. |
| 3.5 | Set `UB_MCP_API_URL=http://192.168.9.52:8443`, call `search_notes`. | Results from specified host. |

## Phase 4: Local Chat Tab (AC5)

| Step | Action | Expected |
|------|--------|----------|
| 4.1 | Navigate to web UI. Confirm "Chat" tab visible (`UB_CHAT_ENABLED=true`). | Chat tab link in navigation. |
| 4.2 | Click Chat tab, type question, press Send. | Response streams incrementally. |
| 4.3 | Examine response for citation patterns. | `[filename, p.N]` rendered as clickable links. |
| 4.4 | Click a citation link. | Navigates to `/files/history?path=...`. |
| 4.5 | Refresh browser page. | Previous conversation still visible. |
| 4.6 | Ask follow-up question in same session. | Response uses conversation history. |

## Phase 5: Configuration and Deployment (AC6)

| Step | Action | Expected |
|------|--------|----------|
| 5.1 | Run `install.sh` on fresh setup. | RAG Pipeline section appears after Boox prompts. |
| 5.2 | Accept defaults. | Values written to `.ultrabridge.env`. |
| 5.3 | Start with defaults (`UB_EMBED_ENABLED=false`, `UB_CHAT_ENABLED=false`). | No Ollama/vLLM connection attempts in logs. |
| 5.4 | Navigate to web UI with default config. | No "Chat" tab. Settings shows "not configured". |
| 5.5 | Set `UB_EMBED_ENABLED=true`, `UB_CHAT_ENABLED=true`, restart. | Chat tab appears. Backfill starts. |

## End-to-End: Full RAG Query Flow

| Step | Action | Expected |
|------|--------|----------|
| E2E.1 | Upload a new Boox .note with text about a specific topic. | File appears in Files tab. |
| E2E.2 | Wait for processing complete (logs: "job done", "embedding saved"). | OCR indexed, embedding created. |
| E2E.3 | Search tab: search for the topic. | New note in results with OCR text. |
| E2E.4 | `curl -u user:pass /api/search?q=topic`. | JSON with note, score, folder, device. |
| E2E.5 | Chat tab: ask about the topic. | Response references note with citations. |
| E2E.6 | Click citation link. | Correct note page displayed. |
| E2E.7 | MCP Inspector: `search_notes` for topic. | Results with URL. |
| E2E.8 | MCP Inspector: `get_note_pages` with note path. | Ordered page content. |
| E2E.9 | MCP Inspector: `get_note_image` page 0. | Base64 JPEG image. |

---

## Traceability

| AC | Automated Test | Manual Step |
|----|---------------|-------------|
| AC1.1 | notedb/db_test.go, rag/store_test.go | -- |
| AC1.2 | booxpipeline/processor_test.go | -- |
| AC1.3 | processor/worker_test.go | -- |
| AC1.4 | rag/backfill_test.go | 1.1-1.4 |
| AC1.5 | web/handler_test.go | -- |
| AC1.6 | rag/store_test.go | 1.5 |
| AC1.7 | rag/embedder_test.go + worker tests | -- |
| AC1.8 | rag/embedder_test.go (benchmark) | 1.7 |
| AC2.1-2.5 | rag/retriever_test.go | -- |
| AC3.1-3.3 | web/api_test.go | -- |
| AC3.4 | web/api_test.go | 2.1-2.2 |
| AC3.5 | web/api_test.go (6 error tests) | -- |
| AC4.1 | Build verification | -- |
| AC4.2-4.4 | cmd/ub-mcp/tools_test.go | -- |
| AC4.5 | -- | 3.2-3.3 |
| AC4.6 | -- | 3.4 |
| AC4.7 | -- | 3.5 |
| AC4.8 | cmd/ub-mcp/tools_test.go | -- |
| AC5.1 | -- | 4.1-4.2 |
| AC5.2-5.3 | chat/handler_test.go | -- |
| AC5.4 | -- | 4.3-4.4 |
| AC5.5 | chat/store_test.go + notedb/db_test.go | 4.5 |
| AC5.6-5.7 | chat/handler_test.go | -- |
| AC6.1 | Build verification | -- |
| AC6.2 | -- | 5.1-5.2 |
| AC6.3 | worker tests (implicit nil embedder) | 5.3 |
| AC6.4 | web/routes_test.go | 5.4 |
