# UltraBridge Architecture

How the pieces fit together. For a high-level feature overview, see
the project [README](../README.md). For subsystem-level deep dives
see the `CLAUDE.md` file under each `internal/*` package.

## System diagram

```
┌──────────────────────────────────────────────────────────────────────┐
│ Supernote Private Cloud Stack (optional, only for Supernote sync)    │
├──────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────┐  ┌──────────────────┐  ┌──────────────────────┐   │
│  │  nginx       │  │  MariaDB         │  │  .note file store    │   │
│  │  (proxy)     │  │  (SPC catalog)   │  │  (NFS / volume)      │   │
│  └──────────────┘  └──────────────────┘  └──────────────────────┘   │
│                            ▲                       ▲                 │
│                            │ f_user_file           │                 │
│                            │ (post-OCR catalog     │                 │
│                            │  sync only)           │                 │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │  UltraBridge                                                  │  │
│  │                                                               │  │
│  │  ┌──────────────────────┐  ┌───────────────────────────────┐  │  │
│  │  │  CalDAV subsystem    │  │  Supernote notes pipeline     │  │  │
│  │  │  CalDAV ← TaskStore  │  │   ↓ fsnotify watcher          │  │  │
│  │  │        (SQLite)      │  │   ↓ reconciler                │  │  │
│  │  │        ↑ tasksync    │  │  NoteStore → SQLite           │  │  │
│  │  │          engine via  │  │  Processor (OCR jobs)         │  │  │
│  │  │          SPC REST    │  └───────────────────────────────┘  │  │
│  │  └──────────────────────┘                                     │  │
│  │  ┌──────────────────────┐  ┌───────────────────────────────┐  │  │
│  │  │  Boox notes pipeline │  │  Shared services              │  │  │
│  │  │  WebDAV server ←─────│──│  SearchIndex (FTS5)           │  │  │
│  │  │   ↓ .note parser    │  │  Embedding cache (Ollama)     │  │  │
│  │  │   ↓ page renderer   │  │  Hybrid retriever (RRF)       │  │  │
│  │  │   ↓ OCR + indexer ──│─▶│  JSON API + MCP server        │  │  │
│  │  │  Version archive     │  │  Chat (vLLM SSE proxy)        │  │  │
│  │  └──────────────────────┘  │  Web UI (Tasks/SN/Boox/...)   │  │  │
│  │                            │  Auth middleware               │  │  │
│  │                            └───────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

### Key points

- **SPC is optional.** UltraBridge only talks to MariaDB to sync the
  Supernote catalog (`f_user_file` etc.) after an OCR injection, so
  the device's listing reflects the modified file. If there's no SPC,
  everything else — CalDAV, RAG, Boox — works identically.
- **CalDAV is SQLite-backed.** The CalDAV subsystem reads and writes
  `internal/taskdb` (SQLite). The `tasksync` engine is a separate
  adapter-based layer that pushes local changes out to the device
  over SPC REST and pulls device changes back in. There is no direct
  CalDAV → MariaDB path.
- **Boox uses WebDAV, not SPC.** Boox devices push `.note` files into
  UltraBridge's embedded WebDAV server; no SPC involvement.
- **Unified search.** Both pipelines write into the same `note_content`
  FTS5 table and the same embedding store, so search and RAG chat
  cross device boundaries transparently even though the two Files
  tabs are per-source.

## Supernote notes pipeline flow

```
.note file written/changed on device
         │
         ▼
   fsnotify watcher  ──(2s debounce)──▶  Processor queue
         +
   reconciler (15 min)
         │
         ▼
   Worker picks up job
         │
         ├─ backup original (if backup path configured)
         ├─ extract existing MyScript RECOGNTEXT → index as "myScript"
         ├─ if OCR enabled:
         │    render page → JPEG → vision API → inject RECOGNTEXT
         │    index as "api"
         │    if embedding enabled: text → Ollama → vector stored
         └─ job marked done
                  │
                  ▼
           FTS5 search index + vector cache
```

## Boox notes pipeline flow

```
Boox device syncs via WebDAV
         │
         ▼
   WebDAV PUT /webdav/onyx/{model}/{type}/{folder}/{name}.note
         │
         ├─ version-on-overwrite (old file → .versions/)
         ├─ parent directories auto-created
         └─ upload callback → enqueue job
                  │
                  ▼
   Boox processor picks up job (5s poll)
         │
         ├─ parse ZIP (protobuf metadata, shapes, point files)
         ├─ extract title, device model, page count
         ├─ render each page → JPEG cache
         ├─ if OCR enabled: vision API → text
         ├─ index page text → FTS5
         ├─ if embedding enabled: text → Ollama → vector stored
         └─ job marked done
                  │
                  ▼
   Unified FTS5 search index + vector cache (shared with Supernote)
```

## Task mutation flow (CalDAV + MCP)

```
Web UI form / MCP tool call / CalDAV client PUT
         │
         ▼
   TaskService (Create / Update / Complete / Delete / PurgeCompleted)
         │
         ├─ write to internal/taskdb (SQLite)
         ├─ emit audit log line (op, auth_method, auth_label, task_id)
         └─ Notify() → tasksync engine
                  │
                  ▼
         Next sync cycle (or immediate if triggered)
                  │
                  ▼
         SPC REST push → Supernote device CalDAV store
         (UB-wins on conflict; adapter-agnostic so additional
          device adapters can register against the same engine)
```

## Service layer

Post-decoupling, the web Handler depends on four service interfaces
rather than individual stores:

- `TaskService` — task CRUD, bulk operations, partial updates, sync
  notification.
- `NoteService` — file listings (Supernote directory-tree and Boox
  flat catalog), per-file fetch, page content/rendering, processor
  controls for both pipelines, bulk delete / import / migrate.
- `SearchService` — FTS + hybrid search, chat sessions with
  RAG-augmented streaming responses.
- `ConfigService` — runtime config, sources, MCP tokens, sync status.

Each service is nil-safe: if Boox isn't configured, `HasBooxSource()`
returns false and the corresponding UI surfaces render empty-state
placeholders rather than crashing.
