# RAG Retrieval Pipeline Design

## Summary

Add retrieval-augmented generation over UltraBridge's OCR'd note content. An embedding pipeline generates vectors via Ollama (nomic-embed-text-v1.5) after OCR indexing. A hybrid retriever combines FTS5 keyword search and vector cosine similarity with reciprocal rank fusion. A separate MCP server binary exposes search, content, and image tools for Claude. A local chat tab in the web UI sends assembled context to a text generation model (Qwen3.5) via vLLM with SSE-streamed responses.

## Definition of Done

Build a RAG system over UltraBridge's OCR'd note content, consisting of four components:

1. **Embedding pipeline** — nomic-embed-text-v1.5 via Ollama on the UltraBridge host (.52). Generates embeddings after OCR indexing in both Supernote and Boox workers. Stored as BLOBs in a `note_embeddings` SQLite table. Backfills existing content via manual trigger with automatic backfill on startup. Ollama hosts the embedding model only (no text generation).

2. **Hybrid retriever** — combines FTS5 keyword search and vector cosine similarity with rank fusion. Supports metadata filtering (date range, device model, folder) from the start. Exposed as a clean domain interface in `internal/rag/`, designed API-first for future frontend split.

3. **MCP server** — separate `cmd/ub-mcp` binary connecting to UltraBridge's API for data. Exposes `search_notes`, `get_note_pages`, `get_note_image` tools for Claude. Supports both stdio transport (Claude Desktop / Claude Code) and HTTP SSE transport (claude.ai web) via the official MCP Go SDK (v1.5.0+).

4. **Local chat tab** — web UI tab using the same retriever, sending assembled context to a text generation model (e.g., Qwen3.5) running on the shared RTX 5060 Ti via vLLM. Streamed responses via SSE.

**Out of scope:** Replacing the existing OCR pipeline, changing the FTS5 schema, building the API/frontend split (but all new code designed to make it easy later per project design guidance). MCP write tools (e.g., task creation) are a natural follow-on but not in this scope.

## Acceptance Criteria

### AC1: Embedding Pipeline

- **rag-retrieval-pipeline.AC1.1** — `note_embeddings` table exists in notedb with columns: `note_path TEXT`, `page INTEGER`, `embedding BLOB`, `model TEXT`, `created_at INTEGER`, `UNIQUE(note_path, page)`.
- **rag-retrieval-pipeline.AC1.2** — After OCR indexing completes for a Boox note page, the worker calls the embedder and stores a 768-dim float32 vector in `note_embeddings`. Verified by: process a .note file, query `SELECT count(*) FROM note_embeddings WHERE note_path = ?` returns page count.
- **rag-retrieval-pipeline.AC1.3** — After OCR indexing completes for a Supernote note page, the same embedding flow runs. Verified by: process a Supernote .note file, check `note_embeddings` row exists.
- **rag-retrieval-pipeline.AC1.4** — On startup, pages in `note_content` without a corresponding `note_embeddings` row are automatically backfilled. Verified by: delete embeddings, restart, embeddings regenerated.
- **rag-retrieval-pipeline.AC1.5** — Backfill can be triggered manually via a Settings UI button or API endpoint. Verified by: trigger endpoint, observe backfill log entries.
- **rag-retrieval-pipeline.AC1.6** — Embeddings are loaded into an in-memory vector cache on startup. Verified by: startup log shows "loaded N embeddings into memory".
- **rag-retrieval-pipeline.AC1.7** — If Ollama is unreachable, embedding failure is logged but does not block OCR indexing. The page proceeds without an embedding. Verified by: stop Ollama, process a file, OCR completes, no embedding row created.
- **rag-retrieval-pipeline.AC1.8** — Embedding generation adds <500ms per page to the OCR pipeline. Verified by: compare pipeline timing with and without embedder.

### AC2: Hybrid Retriever

- **rag-retrieval-pipeline.AC2.1** — `Retriever.Search(ctx, SearchRequest) ([]SearchResult, error)` returns results combining FTS5 and vector similarity via reciprocal rank fusion. Verified by: unit test with known content shows results from both sources merged.
- **rag-retrieval-pipeline.AC2.2** — `SearchRequest` supports `Folder`, `Device`, and `DateRange` filters. Verified by: search with folder filter returns only pages from that folder; search with date range returns only pages within range.
- **rag-retrieval-pipeline.AC2.3** — Metadata filtering JOINs `note_content` with `boox_notes`/`notes` tables on `note_path` for device model and date; filters by folder path segment for folder. Verified by: SQL query plan in test confirms JOIN behavior.
- **rag-retrieval-pipeline.AC2.4** — Each `SearchResult` includes `NotePath`, `Page`, `BodyText`, `TitleText`, `Score`, `Folder`, `Device`, `NoteDate` — sufficient for citation. Verified by: struct definition includes all fields; search returns populated metadata.
- **rag-retrieval-pipeline.AC2.5** — When no embeddings exist (Ollama disabled), retriever falls back to FTS5-only mode gracefully. Verified by: search returns FTS5 results when embedding cache is empty.

### AC3: JSON API Endpoints

- **rag-retrieval-pipeline.AC3.1** — `GET /api/search?q=...&folder=...&device=...&from=...&to=...&limit=...` returns JSON array of search results with metadata. Verified by: curl returns valid JSON with expected fields.
- **rag-retrieval-pipeline.AC3.2** — `GET /api/notes/{path}/pages` returns JSON array of all page content for a note. Verified by: curl returns page text ordered by page number.
- **rag-retrieval-pipeline.AC3.3** — `GET /api/notes/{path}/pages/{page}/image` returns JPEG image bytes. Verified by: curl returns image/jpeg content-type with valid JPEG data.
- **rag-retrieval-pipeline.AC3.4** — All API endpoints require Basic Auth (same as existing web UI). Verified by: unauthenticated request returns 401.
- **rag-retrieval-pipeline.AC3.5** — API endpoints return appropriate error codes: 400 for bad parameters, 404 for unknown note paths, 500 for internal errors. Verified by: error scenarios return correct status codes with JSON error body.

### AC4: MCP Server

- **rag-retrieval-pipeline.AC4.1** — `cmd/ub-mcp` binary builds and runs. Verified by: `go build ./cmd/ub-mcp/` succeeds.
- **rag-retrieval-pipeline.AC4.2** — `search_notes` tool accepts `query` (required), `folder`, `device`, `date_from`, `date_to`, `limit` parameters and returns text content with note metadata. Verified by: MCP client call returns results with note paths, pages, dates, and text.
- **rag-retrieval-pipeline.AC4.3** — `get_note_pages` tool accepts `note_path` and returns all page content for that note. Verified by: MCP client call returns ordered page text.
- **rag-retrieval-pipeline.AC4.4** — `get_note_image` tool accepts `note_path` and `page` and returns JPEG image via `ImageContent`. Verified by: MCP client call returns base64-encoded JPEG.
- **rag-retrieval-pipeline.AC4.5** — MCP server supports stdio transport by default (for Claude Desktop / Claude Code). Verified by: running `ub-mcp` with stdin/stdout piping works with MCP inspector.
- **rag-retrieval-pipeline.AC4.6** — MCP server supports HTTP SSE transport via `--http :PORT` flag (for claude.ai web). Verified by: running with `--http :8081` and connecting via SSE works.
- **rag-retrieval-pipeline.AC4.7** — MCP server connects to UltraBridge's JSON API endpoints (configurable base URL). Verified by: `UB_MCP_API_URL` env var sets the API base URL.
- **rag-retrieval-pipeline.AC4.8** — Search results include UltraBridge URLs for linking back to the web UI. Verified by: `search_notes` results include `url` field pointing to `/files/history?path=...`.

### AC5: Local Chat Tab

- **rag-retrieval-pipeline.AC5.1** — New "Chat" tab in web UI with message input, conversation display, and SSE-streamed responses. Verified by: navigate to Chat tab, type question, see streaming response.
- **rag-retrieval-pipeline.AC5.2** — `POST /chat/ask` accepts a question, runs hybrid retrieval, assembles prompt with retrieved context, calls vLLM, and streams response via SSE. Verified by: POST with question returns `text/event-stream` with incremental text chunks.
- **rag-retrieval-pipeline.AC5.3** — Chat system prompt instructs the model to cite notes using `[filename, p.N]` format. Verified by: response includes citations matching retrieved notes.
- **rag-retrieval-pipeline.AC5.4** — Chat UI linkifies `[filename, p.N]` citations as clickable links to `/files/history?path=...`. Verified by: citation in rendered response is a clickable link.
- **rag-retrieval-pipeline.AC5.5** — Chat history persisted in SQLite (`chat_sessions`, `chat_messages` tables). Verified by: refresh page, previous conversation still visible.
- **rag-retrieval-pipeline.AC5.6** — Chat tab is functional when vLLM is unreachable (shows error message, doesn't crash). Verified by: stop vLLM, send question, UI shows error.
- **rag-retrieval-pipeline.AC5.7** — Configurable vLLM endpoint via `UB_CHAT_API_URL` and model via `UB_CHAT_MODEL`. Verified by: setting env vars changes which model/endpoint is used.

### AC6: Configuration and Deployment

- **rag-retrieval-pipeline.AC6.1** — New config fields: `UB_OLLAMA_URL` (Ollama base URL), `UB_OLLAMA_EMBED_MODEL` (embedding model name), `UB_CHAT_API_URL` (vLLM URL), `UB_CHAT_MODEL` (generation model name), `UB_CHAT_ENABLED` (feature flag), `UB_EMBED_ENABLED` (feature flag). Verified by: config.go loads all fields with sensible defaults.
- **rag-retrieval-pipeline.AC6.2** — `install.sh` prompts for Ollama URL, chat API URL, and model names. Verified by: running install.sh shows new prompts in correct section.
- **rag-retrieval-pipeline.AC6.3** — Embedding pipeline is disabled when `UB_EMBED_ENABLED=false` (default). Workers skip embedding step. Verified by: default config does not attempt Ollama connection.
- **rag-retrieval-pipeline.AC6.4** — Chat tab is hidden when `UB_CHAT_ENABLED=false` (default). Verified by: default config shows no Chat tab in UI.

## Architecture

### System Diagram

```
                         ┌─────────────────────────────────────────────┐
                         │              UltraBridge Server             │
                         │                                             │
  Boox Worker ──┐        │  ┌──────────┐    ┌──────────────────────┐  │
                ├─IndexPage──►  search  │    │    internal/rag/     │  │
  SN Worker ────┘        │  │  (FTS5)  │    │                      │  │
        │                │  └──────────┘    │  Embedder (Ollama)   │  │
        │                │                  │  Store (embeddings)  │  │
        └──EmbedPage────►│                  │  Retriever (hybrid)  │  │
                         │                  └──────────────────────┘  │
                         │                           │                │
                         │              ┌────────────┼────────────┐  │
                         │              │            │            │   │
                         │         ┌────▼───┐  ┌────▼───┐  ┌─────▼─┐│
                         │         │JSON API│  │Chat Tab│  │  MCP  ││
                         │         │/api/*  │  │/chat/* │  │(binary)│
                         │         └────────┘  └────────┘  └───────┘│
                         └─────────────────────────────────────────────┘
                              │                    │              │
                              │              ┌─────▼────┐   ┌────▼────┐
                              │              │  vLLM    │   │ Claude  │
                              │              │(Qwen3.5) │   │Desktop/ │
                         ┌────▼────┐         │RTX 5060Ti│   │claude.ai│
                         │ Ollama  │         └──────────┘   └─────────┘
                         │(embed)  │
                         │  CPU    │
                         └─────────┘
```

### Data Flow

**Indexing (per page, after OCR):**
1. Worker calls `Indexer.IndexPage()` → inserts into `note_content` → FTS5 triggers update `note_fts` (existing)
2. Worker calls `Embedder.EmbedPage()` → Ollama `/api/embed` → stores 768-dim BLOB in `note_embeddings` (new)
3. In-memory vector cache updated with new embedding

**Retrieval (on query):**
1. Parse query + filters
2. FTS5 keyword search (existing `search.Store.Search`) → ranked results
3. Vector cosine similarity over in-memory cache → ranked results
4. Reciprocal rank fusion: `RRF_score(d) = Σ 1/(60 + rank_i(d))`
5. Deduplicate by `(note_path, page)`, top-K results
6. JOIN with `boox_notes`/`notes` for metadata (device, folder, date)

**Chat (question → streamed answer):**
1. Browser POSTs question to `/chat/ask`
2. Handler calls `Retriever.Search()` for context
3. Assembles prompt: system instructions + retrieved pages with metadata + question
4. Calls vLLM OpenAI-compatible `/v1/chat/completions` with `stream: true`
5. Proxies SSE chunks to browser as `text/event-stream`
6. Browser renders streaming markdown with linkified citations

**MCP (Claude → tools):**
1. Claude calls `search_notes` → MCP binary calls `GET /api/search` → returns text + metadata + URLs
2. Claude calls `get_note_pages` → MCP binary calls `GET /api/notes/{path}/pages` → returns page text
3. Claude calls `get_note_image` → MCP binary calls `GET /api/notes/{path}/pages/{page}/image` → returns `ImageContent` (base64 JPEG)

### Package Structure

```
internal/rag/
    embedder.go       — Ollama HTTP client, EmbedPage, BatchEmbed, Backfill
    embedder_test.go
    retriever.go      — Hybrid search: FTS5 + vector, RRF merge, metadata filtering
    retriever_test.go
    store.go          — note_embeddings CRUD, in-memory vector cache load/update
    store_test.go

internal/chat/
    handler.go        — vLLM OpenAI-compatible client, prompt assembly, SSE proxy
    handler_test.go
    store.go          — chat_sessions, chat_messages table CRUD
    store_test.go

cmd/ub-mcp/
    main.go           — MCP server: tool registration, API client, transport selection
```

## Existing Patterns Followed

### SearchIndex Interface (internal/search/)
The existing `SearchIndex` interface exposes `Search(ctx, SearchQuery) ([]SearchResult, error)` with BM25 scoring. The hybrid retriever wraps this — calling the existing FTS5 search as one of two retrieval paths. No changes to the existing interface.

### processor.Indexer Interface
Both Supernote and Boox workers call `Indexer.IndexPage(ctx, path, pageIdx, source, bodyText, titleText, keywords)` after OCR. The embedding step hooks into the same call site — after `IndexPage` succeeds, call `Embedder.EmbedPage()`. The embedder is a new field on `WorkerConfig` (Boox) and processor config (Supernote), nil-safe like other optional dependencies.

### Config Loading (internal/config/)
New fields follow the existing pattern: struct fields on `Config`, loaded via `envOrDefault`/`envBoolOrDefault` in `Load()`, with `UB_` prefix. Feature flags default to `false` (opt-in).

### Handler Dependency Injection (internal/web/)
New interfaces (`ChatStore`, `Retriever`) injected into `NewHandler` as nil-safe parameters. New routes registered in `NewHandler`. Template functions added to `funcMap` for chat UI rendering.

### Nil-Safe Interfaces
All new interfaces (`Embedder`, `Retriever`, `ChatStore`) are nil-safe in the handler — nil disables the feature. Chat tab hidden when `ChatStore` is nil. Embedding step skipped when `Embedder` is nil.

### Schema Migrations (internal/notedb/)
New tables (`note_embeddings`, `chat_sessions`, `chat_messages`) added to `migrate()` in `schema.go` using `CREATE TABLE IF NOT EXISTS` (idempotent). No ALTER TABLE needed.

## Implementation Phases

### Phase 1: Embedding Infrastructure
- `note_embeddings` table in notedb schema
- Ollama HTTP client in `internal/rag/embedder.go` (EmbedPage, BatchEmbed)
- Embedding store (`internal/rag/store.go`) — CRUD + in-memory cache
- Config fields: `UB_EMBED_ENABLED`, `UB_OLLAMA_URL`, `UB_OLLAMA_EMBED_MODEL`
- Wire embedder into Boox worker (after IndexPage)
- Wire embedder into Supernote worker (after IndexPage)
- Startup backfill loop
- Manual backfill trigger endpoint

### Phase 2: Hybrid Retriever
- `internal/rag/retriever.go` — `Retriever` interface and implementation
- Vector cosine similarity search over in-memory cache
- Reciprocal rank fusion merge with FTS5 results
- Metadata filtering via JOINs (folder, device, date range)
- FTS5-only fallback when embedding cache is empty

### Phase 3: JSON API Endpoints
- `GET /api/search` — thin wrapper around `Retriever.Search()`
- `GET /api/notes/{path}/pages` — wraps `search.GetContent()`
- `GET /api/notes/{path}/pages/{page}/image` — serves rendered JPEG
- Basic Auth on all `/api/*` routes
- Error handling with JSON error bodies

### Phase 4: MCP Server
- `cmd/ub-mcp/main.go` — MCP server using official Go SDK v1.5.0
- `search_notes` tool — calls `/api/search`, formats results with UltraBridge URLs
- `get_note_pages` tool — calls `/api/notes/{path}/pages`
- `get_note_image` tool — calls `/api/notes/{path}/pages/{page}/image`, returns `ImageContent`
- Stdio transport (default) + HTTP SSE transport (`--http` flag)
- Config: `UB_MCP_API_URL`, `UB_MCP_API_USER`, `UB_MCP_API_PASS`

### Phase 5: Local Chat Tab
- Chat schema: `chat_sessions`, `chat_messages` tables in notedb
- `internal/chat/store.go` — session/message CRUD
- `internal/chat/handler.go` — vLLM client, prompt assembly, SSE streaming
- `POST /chat/ask` — orchestrates retrieval → prompt → stream
- Chat tab UI in `index.html` — message input, conversation display, streaming render
- Citation linkification in browser JS
- Config: `UB_CHAT_ENABLED`, `UB_CHAT_API_URL`, `UB_CHAT_MODEL`

### Phase 6: Deployment and Polish
- `install.sh` prompts for embedding and chat configuration
- Dockerfile: no new system dependencies (Ollama runs on host, vLLM runs separately)
- Settings UI: embedding status, backfill trigger, chat model display
- Error states: Ollama down, vLLM down, empty embeddings

## Additional Considerations

### Embedding Model Changes
At 12K pages and ~150ms/page, a full re-embed takes ~30 minutes. The `model` column in `note_embeddings` tracks which model generated each vector. On model change, the backfill loop detects mismatched model names and re-embeds. This is cheap enough to not need a migration strategy.

### GPU Contention
The RTX 5060 Ti (16GB) is shared between vLLM (OCR + chat generation). Ollama runs on CPU only for embeddings. Chat generation and OCR generation both use vLLM but are unlikely to be concurrent in practice (single user, OCR runs in background). If contention becomes an issue, vLLM's request queuing handles it.

### Security
The MCP binary authenticates to UltraBridge's API with the same Basic Auth credentials. The API endpoints are behind the same auth middleware as the web UI. No new attack surface beyond the existing auth boundary. The MCP binary should store credentials via environment variables, not command-line arguments.

### Write Tools (Future)
The MCP server architecture (thin client over JSON API) makes adding write tools trivial: expose `POST /api/tasks` in the main server, add `create_task` tool to `cmd/ub-mcp`. This is out of scope for this design but requires no architectural changes.

### Citation and Linking
Search results include UltraBridge URLs (`/files/history?path=...`) so Claude can link directly to notes. The chat tab system prompt instructs the model to cite as `[filename, p.N]`; browser JS linkifies these using the note paths from the retrieval context. No new infrastructure needed — just presentation.

## Glossary

- **BM25** — Best Matching 25, a probabilistic relevance scoring function used by FTS5 for keyword search ranking
- **Cosine similarity** — Measure of similarity between two vectors based on the cosine of the angle between them; used for comparing query and document embeddings
- **Embedder** — Component that converts text into a fixed-dimensional vector (embedding) via a language model
- **FTS5** — SQLite Full-Text Search extension version 5; provides keyword search with BM25 scoring
- **MCP** — Model Context Protocol; standard for connecting AI models to external data sources via tools
- **nomic-embed-text-v1.5** — 137M parameter embedding model with 768 dimensions, served via Ollama
- **Ollama** — Local model serving platform; used here for hosting the embedding model on CPU
- **Qwen3.5** — Text generation model family; used via vLLM for local chat synthesis
- **Reciprocal Rank Fusion (RRF)** — Method for merging ranked lists: `score(d) = Σ 1/(k + rank_i(d))` where k=60
- **SSE** — Server-Sent Events; HTTP streaming protocol used for chat responses and MCP transport
- **vLLM** — High-performance LLM serving engine; hosts the generation model on GPU
