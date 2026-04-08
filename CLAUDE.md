# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Last verified: 2026-04-08

Go sidecar service for Supernote Private Cloud. Six subsystems:
1. **CalDAV task sync** -- CalDAV VTODO over local SQLite task store
2. **Device sync** -- bidirectional task sync with Supernote via SPC REST API (adapter-based)
3. **Supernote notes pipeline** -- scans Supernote .note files, extracts/OCRs text, indexes for full-text search
4. **Boox notes pipeline** -- receives Boox .note files via WebDAV, parses ZIP+protobuf, renders strokes, OCRs, indexes for unified search
5. **RAG retrieval pipeline** -- Ollama embeddings, hybrid FTS5+vector search, vLLM-powered chat with retrieval-augmented context
6. **MCP server** -- Model Context Protocol server exposing note search/retrieval tools for AI agents

## Bash Commands: No `cd &&` Compounds

**NEVER** use `cd /path && command` compound bash statements. This triggers a Claude Code bug where the permission prompt fires on `cd` instead of the actual command.

Instead: `git -C /path`, `go -C /path build`, or absolute paths.

## Project Structure

- `cmd/ultrabridge/` -- entry point, wires all components
- `cmd/ub-mcp/` -- MCP server binary: exposes search_notes, get_note_pages, get_note_image tools via stdio or HTTP SSE (see domain CLAUDE.md)
- `internal/caldav/` -- CalDAV backend (go-webdav), VTODO conversion with iCal blob overlay (see domain CLAUDE.md)
- `internal/taskstore/` -- Task model, field mapping helpers, MariaDB CRUD (legacy), ErrNotFound sentinel (see domain CLAUDE.md)
- `internal/taskdb/` -- SQLite task store: Open/migrate DB, implements caldav.TaskStore (see domain CLAUDE.md)
- `internal/tasksync/` -- adapter-agnostic sync engine: reconciliation, sync map, conflict resolution (see domain CLAUDE.md)
- `internal/tasksync/supernote/` -- Supernote SPC REST adapter: JWT auth, field mapping, migration (see domain CLAUDE.md)
- `internal/sync/` -- Engine.IO v3 notifier: STARTSYNC push + inbound events (see domain CLAUDE.md)
- `internal/auth/` -- Basic Auth middleware (bcrypt)
- `internal/config/` -- env vars (UB_ prefix) + .dbenv file loading + pipeline config + sync config + boox config + RAG/chat config
- `internal/db/` -- MariaDB pool + single-user discovery
- `internal/logging/` -- structured slog, file rotation, syslog, WebSocket broadcast
- `internal/web/` -- HTML UI: task list, Files tab, Search tab, Chat tab, processor C&C, sync status, Boox render/versions, JSON API, SSE log stream (see domain CLAUDE.md)
- `internal/rag/` -- RAG embedding infrastructure: Ollama embedder, embedding store with in-memory cache, hybrid FTS5+vector retriever, backfill (see domain CLAUDE.md)
- `internal/chat/` -- Chat subsystem: session/message store (SQLite), vLLM streaming handler with RAG context injection (see domain CLAUDE.md)
- `internal/booxnote/` -- Boox .note ZIP parser: protobuf pages, nested shape ZIPs, binary point files (see domain CLAUDE.md)
- `internal/booxnote/proto/` -- Generated protobuf code for Boox .note format (NoteInfo, VirtualPage, ShapeInfoProto)
- `internal/booxnote/testutil/` -- Exported test helper: builds synthetic .note ZIP files for tests
- `internal/booxrender/` -- Stroke renderer: pressure-sensitive scribbles, geometric shapes via fogleman/gg (see domain CLAUDE.md)
- `internal/booxpipeline/` -- Boox processing pipeline: store, worker, processor (parse/render/OCR/index) (see domain CLAUDE.md)
- `internal/pdfrender/` -- PDF page rendering via pdftoppm (poppler-utils) for bulk import pipeline
- `internal/webdav/` -- WebDAV server for Boox file uploads with versioning (see domain CLAUDE.md)
- `internal/notedb/` -- SQLite DB opener + schema migrations for notes pipeline + Boox pipeline (see domain CLAUDE.md)
- `internal/notestore/` -- file inventory (scan, list, get), content hashing, job transfer against SQLite notes table (see domain CLAUDE.md)
- `internal/processor/` -- background OCR job queue: backup, extract, render, OCR, inject, SPC catalog sync (see domain CLAUDE.md)
- `internal/search/` -- FTS5 full-text search over note content (see domain CLAUDE.md)
- `internal/pipeline/` -- file detection: fsnotify watcher, reconciler, Engine.IO listener (see domain CLAUDE.md)
- `tests/` -- integration tests (require real DB)

## Build & Test

Use `-C` flag to target the repo root without `cd`:

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go build -C /home/jtd/ultrabridge ./cmd/ub-mcp/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Run a single package's tests:
```bash
go test -C /home/jtd/ultrabridge ./internal/taskstore/
```

Integration tests (require running MariaDB with Supernote schema):
```bash
TEST_DBENV_PATH=/mnt/supernote/.dbenv go test -C /home/jtd/ultrabridge -tags integration ./tests/ -v
```

Docker build:
```bash
docker build -t ultrabridge:dev /home/jtd/ultrabridge
```

## Key Dependencies

- `github.com/jdkruzr/go-sn` -- Supernote .note file parser/writer (rendering, RECOGNTEXT injection, JIIX)
- `github.com/emersion/go-webdav` -- CalDAV protocol handler
- `github.com/fogleman/gg` -- 2D rendering (Boox stroke renderer)
- `google.golang.org/protobuf` -- Boox .note protobuf parsing
- `golang.org/x/net/webdav` -- WebDAV protocol handler (Boox uploads)
- `modernc.org/sqlite` -- pure-Go SQLite (no CGO)
- `github.com/modelcontextprotocol/go-sdk/mcp` -- MCP server (stdio + HTTP SSE transport)

## Subcommands

- `ultrabridge hash-password "pw"` -- generate bcrypt hash for UB_PASSWORD_HASH
- `ub-mcp` -- MCP server for AI agents (stdio by default, `--http :8081` for HTTP SSE)

## Conventions

- Module: `github.com/sysop/ultrabridge`
- Config: all env vars prefixed `UB_`, DB creds from shared `.dbenv` file
- Auth: single-user Basic Auth, password stored as bcrypt hash

### CalDAV Subsystem (SQLite)
- Local SQLite task store (internal/taskdb) replaces direct MariaDB access for CalDAV
- DB timestamps: millisecond UTC unix timestamps, 0 = unset
- IDs: MD5(title + timestamp) for task IDs (matches Supernote device convention)
- Supernote quirk: `completed_time` holds creation time; `last_modified` holds actual completion time
- Soft deletes only: `is_deleted = 'Y'`, never hard delete
- iCal blob: VTODO round-trip fidelity via `ical_blob` column; DB fields overlaid on read

### Device Sync (tasksync)
- Adapter-based: sync engine is device-agnostic, adapters implement DeviceAdapter interface
- UB-wins conflict resolution: local task store is authoritative
- Sync map: per-task local-to-remote ID mapping in SQLite tables (sync_state, task_sync_map)
- Supernote adapter: SPC REST API with JWT challenge-response auth
- Config: UB_SN_SYNC_ENABLED, UB_SN_SYNC_INTERVAL, UB_SN_API_URL, UB_SN_PASSWORD
- Task DB path: UB_TASK_DB_PATH (SQLite file for local task store)

### Notes Pipeline (SQLite + MariaDB catalog sync)
- Three databases: SQLite for tasks (taskdb), SQLite for notes pipeline (notedb), MariaDB for SPC catalog sync
- After OCR injection, processor updates SPC MariaDB catalog (f_user_file, f_file_action, f_capacity) so the device sees correct file size/md5 -- best-effort, failures logged not propagated
- SQLite in WAL mode, MaxOpenConns=1 (single-writer)
- Job statuses: pending -> in_progress -> done|failed|skipped
- Backup before modification: original .note copied to backup tree, never overwritten
- OCR source tracking: "myScript" (device RECOGNTEXT) vs "api" (vision API result)
- Standard-only injection: only notes with FILE_RECOGN_TYPE=0 (Standard) get RECOGNTEXT injection (JIIX v3 format); RTR notes (FILE_RECOGN_TYPE=1) are OCR'd and indexed but file is NOT modified
- Requeue with delay: jobs can be set back to pending with a future `requeue_after` timestamp
- Content hash dedup: SHA-256 stored on job completion; pipeline detects moved/renamed files and transfers job records instead of re-processing
- Pipeline env vars: UB_NOTES_PATH, UB_DB_PATH, UB_BACKUP_PATH, UB_OCR_*

### Boox Notes Pipeline (WebDAV + shared SQLite notedb)
- Boox .note format: ZIP containing protobuf metadata, nested shape ZIPs, binary point files
- WebDAV upload endpoint at `/webdav/` (behind Basic Auth) receives .note files from Boox devices
- On upload: parse ZIP, render pages to JPEG cache, OCR via vision API, index into shared FTS5 tables
- Shares SQLite notedb with Supernote pipeline (boox_notes, boox_jobs tables alongside notes, jobs)
- Shares search index: same note_content/note_fts tables, unified search across both device types
- Shares OCR client: same processor.Indexer and processor.OCRClient interfaces
- File versioning: overwritten .note files archived to `.versions/` directory tree
- Rendered page cache: JPEG images at `{UB_BOOX_NOTES_PATH}/.cache/{noteID}/page_{N}.jpg`
- Bulk import: filesystem paths can be imported in bulk via the web UI; importer scans for .note and .pdf files, enqueues each, and optionally migrates files to the Boox notes directory
- PDF support: .pdf files accepted alongside .note files; pages rendered via pdftoppm (pdfrender package), then OCR'd and indexed identically to .note files
- Config: UB_BOOX_ENABLED (feature flag), UB_BOOX_NOTES_PATH (filesystem root for uploads + cache), UB_BOOX_IMPORT_PATH (source directory for bulk imports)

### RAG Retrieval Pipeline (Ollama + SQLite)
- Embedding: Ollama `/api/embed` endpoint generates float32 vectors, stored as little-endian blobs in `note_embeddings` table
- In-memory cache: all embeddings loaded on startup; cache updated atomically on Save
- Hybrid retriever: combines FTS5 keyword search with cosine-similarity vector search, fuses results via reciprocal rank fusion
- Backfill: startup goroutine embeds unembedded pages; manual trigger via web UI for re-embedding after model upgrades
- Integration: both Supernote and Boox workers embed OCR'd text as part of the processing pipeline (best-effort, failures logged not propagated)
- Config: UB_EMBED_ENABLED (feature flag), UB_OLLAMA_URL (default http://localhost:11434), UB_OLLAMA_EMBED_MODEL (default nomic-embed-text:v1.5)

### Chat Subsystem (vLLM + RAG)
- RAG-powered chat: user question triggers hybrid search, top results injected as context into vLLM prompt
- SSE streaming: handler proxies vLLM OpenAI-compatible streaming response to browser via Server-Sent Events
- Session persistence: chat sessions and messages stored in SQLite (chat_sessions, chat_messages tables in notedb)
- Config: UB_CHAT_ENABLED (feature flag), UB_CHAT_API_URL (default http://localhost:8000), UB_CHAT_MODEL (default Qwen/Qwen3-8B)

### MCP Server (cmd/ub-mcp)
- Separate binary that calls UltraBridge JSON API endpoints via HTTP with Basic Auth
- Tools: `search_notes` (hybrid search), `get_note_pages` (page content), `get_note_image` (JPEG rendering)
- Transport: stdio (default) or HTTP SSE (`--http :8081`)
- Config: UB_MCP_API_URL (default http://localhost:8443), UB_MCP_API_USER, UB_MCP_API_PASS
