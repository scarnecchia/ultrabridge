# Note Ingest and Search Design

## Summary

UltraBridge is adding a note ingestion and search pipeline on top of its existing CalDAV task sync service. The new capability lets users browse their Supernote file tree through a web UI, automatically detect new and changed `.note` files, extract or generate recognized text for each file, and search that text across all notes. The feature is designed to work incrementally: if a Supernote device has already run MyScript handwriting recognition, UltraBridge can extract and index that existing text without any additional processing. The optional OCR pipeline — disabled by default — goes further by rendering `.note` stroke data to JPEG and submitting it to a configurable vision API, then writing the result back into the file as a RECOGNTEXT block.

The implementation extends UltraBridge without touching the existing CalDAV, task, or sync layers. All new state (file records, job queue, and full-text search index) lives in a dedicated SQLite database separate from the Supernote MariaDB. Four new internal packages handle the distinct concerns: `notestore` tracks file state on disk, `processor` manages a background job queue with start/stop control and a stuck-job watchdog, `search` maintains a SQLite FTS5 full-text index, and `pipeline` wires automatic file detection using filesystem events, a periodic reconciliation scan, and optional Engine.IO sync events. The work is split into eight sequential phases so each layer is testable before the next builds on it.

## Definition of Done

- UltraBridge displays a Files tab that browses all files and folders under `UB_NOTES_PATH`
- `.note` files show a processing status badge (unprocessed / queued / done / failed / skipped)
- Other file types are visible but marked unsupported
- A background processor can be started and stopped; it renders `.note` strokes to JPEG, submits them to a configurable vision API, and injects the result back as RECOGNTEXT — disabled by default
- The service also works without the processor running by extracting existing MyScript RECOGNTEXT
- Recognized text (titles, body, keywords) is indexed in a SQLite FTS5 index and searchable from a Search tab
- Original files are copied to a backup location before first modification when `UB_BACKUP_PATH` is configured
- New and changed files are detected automatically and queued without manual intervention
- All new functionality has tests

## Acceptance Criteria

### note-ingest-search.AC1: File browser displays directory contents
- **AC1.1 Success:** `GET /files` with no path shows top-level contents of `UB_NOTES_PATH`
- **AC1.2 Success:** `GET /files?path=Note/Folder` shows files in that subdirectory with breadcrumb
- **AC1.3 Success:** `.note` files show a status badge (at minimum "unprocessed" before any job exists)
- **AC1.4 Success:** PDF, EPUB, and unknown file types are visible with an "unsupported" badge
- **AC1.5 Failure:** Path traversal attempt (e.g. `?path=../../etc`) returns 400
- **AC1.6 Failure:** `UB_NOTES_PATH` not configured renders an informative error state, not a crash

### note-ingest-search.AC2: Processor lifecycle
- **AC2.1 Success:** Processor is stopped by default (`UB_OCR_ENABLED=false`); no jobs dispatched at startup
- **AC2.2 Success:** Processor can be started via UI; status updates to running
- **AC2.3 Success:** Stop is graceful — in-flight job completes before shutdown
- **AC2.4 Success:** Pending jobs survive a service restart and are visible on next startup
- **AC2.5 Success:** Processor status (running/stopped, queue depth) is visible in the Files tab

### note-ingest-search.AC3: MyScript RECOGNTEXT extraction
- **AC3.1 Success:** `.note` with existing RECOGNTEXT is indexed without the processor running
- **AC3.2 Success:** `.note` with `RECOGNTEXT=0` is indexed with empty `body_text` (no error)
- **AC3.3 Success:** TITLE blocks and KEYWORD blocks are extracted into `title_text` and `keywords` respectively

### note-ingest-search.AC4: OCR pipeline
- **AC4.1 Success:** With OCR enabled, worker renders each page to JPEG and POSTs to the configured vision API
- **AC4.2 Success:** API response text is injected as RECOGNTEXT in the `.note` file on disk
- **AC4.3 Success:** Search index is updated with `source="api"` after successful OCR
- **AC4.4 Failure:** Vision API error increments `attempts`, sets `last_error`, marks job `failed`
- **AC4.5 Failure:** File exceeding `UB_OCR_MAX_FILE_MB` is set to `skipped` with `skip_reason="size_limit"`
- **AC4.6 Edge:** Job stuck `in_progress` >10 minutes is reclaimed by watchdog and reset to `pending`

### note-ingest-search.AC5: Backup before modification
- **AC5.1 Success:** When `UB_BACKUP_PATH` set and no backup exists, file is copied to backup tree before any write
- **AC5.2 Success:** When backup already exists, copy is skipped and write proceeds normally
- **AC5.3 Failure:** When `UB_BACKUP_PATH` set and backup copy fails, job is marked `failed` and original is not modified
- **AC5.4 Edge:** When `UB_BACKUP_PATH` unset, write proceeds without any backup attempt

### note-ingest-search.AC6: Search
- **AC6.1 Success:** Indexed content is retrievable by keyword query from the Search tab
- **AC6.2 Success:** Each result includes file path, page number, and a text snippet
- **AC6.3 Success:** Results are ordered by bm25 relevance (score not shown to user)
- **AC6.4 Success:** Re-indexed content (after re-OCR) replaces the previous entry for the same path + page
- **AC6.5 Edge:** Empty query string returns empty results, not an error

### note-ingest-search.AC7: Per-file command & control
- **AC7.1 Success:** Queue action creates a `pending` job for an unprocessed file
- **AC7.2 Success:** Re-queue resets a `failed` or `done` job to `pending`
- **AC7.3 Success:** Skip sets status to `skipped` with `skip_reason="manual"`
- **AC7.4 Success:** Unskip allows a manually-skipped file to be queued again
- **AC7.5 Success:** Force-include overrides a `size_limit` skip and creates a `pending` job
- **AC7.6 Success:** Job history (attempts, timestamps, last error) is visible per file in the UI

### note-ingest-search.AC8: Automatic file detection
- **AC8.1 Success:** A new `.note` file appearing under `UB_NOTES_PATH` is added to `notes` table automatically
- **AC8.2 Success:** A changed `.note` file (mtime bump) is detected and re-queued
- **AC8.3 Success:** An unchanged file produces no new queue entry on reconciliation scan
- **AC8.4 Edge:** Rapid successive writes to the same file produce a single queue entry (2-second debounce)

### note-ingest-search.AC9: Cross-cutting
- **AC9.1:** All existing UltraBridge tests continue to pass after each phase
- **AC9.2:** New config vars with defaults do not break existing deployments that omit them

## Glossary

- **`.note` file**: Supernote's proprietary binary format for handwritten notes. It stores stroke vector data per page and can optionally embed RECOGNTEXT blocks containing recognized text.
- **RECOGNTEXT**: A named block embedded inside a `.note` file that holds the text recognized from handwritten strokes on a given page. Can be produced by the device's built-in MyScript engine or injected by an external tool.
- **MyScript**: The third-party handwriting recognition engine built into Supernote devices. When a user runs recognition on-device, MyScript writes RECOGNTEXT blocks directly into the `.note` file.
- **go-sn**: The companion Go library (`github.com/jdkruzr/go-sn`) for parsing and mutating `.note` files. Provides `note.Load`, `note.ReadRecognText`, `note.RenderObjects`, and `note.InjectRecognText`.
- **FTS5**: SQLite's fifth-generation full-text search extension. Supports ranked keyword queries using the BM25 relevance algorithm via virtual tables.
- **BM25**: A standard probabilistic ranking function used by FTS5 to score search results by term frequency and document length. Used here for result ordering only, not displayed to the user.
- **content= mode**: An FTS5 configuration where the virtual table stores no text of its own — it references a separate base table (`note_content`) for the actual document content, keeping storage compact.
- **fsnotify**: A Go library that wraps OS-level filesystem event notifications (inotify on Linux). Used here to detect when `.note` files are created, renamed, or written under `UB_NOTES_PATH`.
- **Engine.IO**: The transport protocol underlying Socket.IO. UltraBridge already maintains an outbound Engine.IO connection to push STARTSYNC commands to Supernote devices; Phase 8 proposes listening on that same connection for inbound sync-complete events.
- **Vision API**: A generic term for any HTTP endpoint that accepts image data and returns transcribed text. The model identifier (e.g. `claude-haiku-4-5-20251001`) is configurable.
- **Debounce**: A technique that delays processing until a burst of rapid events has settled. Filesystem events for the same path are held for 2 seconds before a queue entry is created.
- **Watchdog**: A background goroutine that periodically checks for jobs stuck in `in_progress` longer than 10 minutes and resets them to `pending` so they can be retried.
- **Reconciler**: A periodic full directory scan (every 15 minutes) that compares the filesystem against the SQLite `notes` table and enqueues any files that are new or have a changed mtime.
- **mtime**: The filesystem modification timestamp on a file. Used as a cheap change-detection signal before falling back to a SHA-256 content hash.
- **SHA-256**: A cryptographic hash function used here as a content fingerprint to confirm whether file content actually changed when mtime is unreliable.
- **CGo-free**: Describes a Go package that does not require a C compiler at build time. `modernc.org/sqlite` is chosen for this property, simplifying cross-compilation and container builds.
- **`//go:embed`**: A Go compiler directive that bundles files from the source tree directly into the compiled binary. Used here to embed HTML templates.
- **UB_ prefix**: The naming convention for all UltraBridge environment variables, used to avoid collisions with system or third-party env vars.
- **TITLE / KEYWORD blocks**: Named metadata blocks in `.note` files storing heading annotations and user-defined keyword markers respectively. Extracted alongside RECOGNTEXT and indexed as `title_text` and `keywords`.

## Architecture

Four new packages extend UltraBridge without modifying the existing CalDAV, task, auth, or sync layers.

```
internal/
  notestore/   — file discovery and state tracking
  processor/   — background OCR job queue
  search/      — SQLite FTS5 index (vector DB slot)
  pipeline/    — lifecycle wiring for the above
```

All new state lives in a single SQLite file (`UB_DB_PATH`, default `/data/ultrabridge.db`). The Supernote MariaDB is not touched.

### New configuration

All variables follow the existing `UB_` prefix convention:

```
UB_NOTES_PATH        — root of Supernote user directory
                       e.g. /mnt/supernote/supernote_data/user@example.com/Supernote
UB_DB_PATH           — SQLite file path (default: /data/ultrabridge.db)
UB_BACKUP_PATH       — copy originals here before first write (optional)
UB_OCR_ENABLED       — bool, default false
UB_OCR_API_URL       — vision API base URL
UB_OCR_API_KEY       — API key
UB_OCR_MODEL         — model identifier (e.g. claude-haiku-4-5-20251001)
UB_OCR_CONCURRENCY   — parallel workers, default 1
UB_OCR_MAX_FILE_MB   — skip files larger than this (0 = no limit, default 0)
```

### SQLite schema

**notestore — file state**
```sql
notes (
    path          TEXT PRIMARY KEY,
    rel_path      TEXT NOT NULL,
    file_type     TEXT NOT NULL,   -- "note" | "pdf" | "epub" | "other"
    size_bytes    INTEGER,
    mtime         INTEGER,         -- unix seconds, change detection
    sha256        TEXT,            -- set after first read
    backup_path   TEXT,            -- set once backup copy exists
    backed_up_at  INTEGER,
    created_at    INTEGER,
    updated_at    INTEGER
)
```

**processor — job queue**
```sql
jobs (
    id           INTEGER PRIMARY KEY,
    note_path    TEXT NOT NULL REFERENCES notes(path),
    status       TEXT NOT NULL,    -- pending|in_progress|done|failed|skipped
    skip_reason  TEXT,             -- "manual" | "size_limit"
    ocr_source   TEXT,             -- "myScript" | "api"
    api_model    TEXT,
    attempts     INTEGER DEFAULT 0,
    last_error   TEXT,
    queued_at    INTEGER,
    started_at   INTEGER,
    finished_at  INTEGER
)
```

**search — FTS index**
```sql
note_content (
    note_path    TEXT NOT NULL,
    page         INTEGER NOT NULL,
    title_text   TEXT,
    body_text    TEXT,
    keywords     TEXT,
    source       TEXT,   -- "myScript" | "api"
    model        TEXT,
    indexed_at   INTEGER,
    PRIMARY KEY (note_path, page)
)

-- FTS5 virtual table (content= mode)
note_fts USING fts5(
    note_path, page, title_text, body_text, keywords,
    content="note_content", content_rowid="rowid"
)
```

### Key interfaces

**NoteStore**
```go
type NoteStore interface {
    Scan(ctx context.Context) error
    List(ctx context.Context, relPath string) ([]NoteFile, error)
    Get(ctx context.Context, path string) (*NoteFile, error)
}

type NoteFile struct {
    Path      string
    RelPath   string
    Name      string
    IsDir     bool
    FileType  string   // "note" | "pdf" | "epub" | "other"
    SizeBytes int64
    MTime     time.Time
    JobStatus string   // last job status, joined from jobs table
}
```

**Processor**
```go
type Processor interface {
    Start(ctx context.Context) error
    Stop() error
    Status() ProcessorStatus
    Enqueue(ctx context.Context, path string) error
    Skip(ctx context.Context, path string, reason string) error
    Unskip(ctx context.Context, path string) error
}

type ProcessorStatus struct {
    Running   bool
    Pending   int
    InFlight  int
}
```

**SearchIndex**
```go
type SearchIndex interface {
    Index(ctx context.Context, doc NoteDocument) error
    Search(ctx context.Context, q SearchQuery) ([]SearchResult, error)
    Delete(ctx context.Context, path string) error
}

type NoteDocument struct {
    Path       string
    Page       int
    TitleText  string
    BodyText   string
    Keywords   []string
    Source     string   // "myScript" | "api"
    Model      string
}

type SearchQuery struct {
    Text  string
    Limit int
}

type SearchResult struct {
    Path    string
    Page    int
    Snippet string
    Score   float64   // bm25, used for ordering only, not displayed
}
```

### Worker flow (per job)

1. Load `.note` file with `go-sn` `note.Load`
2. If `UB_BACKUP_PATH` set and `notes.backup_path` is empty: copy file to backup tree, set `notes.backup_path` — abort job if copy fails
3. Extract existing RECOGNTEXT from all pages via `note.ReadRecognText`; index immediately as source `"myScript"`
4. If `UB_OCR_ENABLED`: render each page to JPEG via `note.RenderObjects`; POST to vision API; receive text; call `note.InjectRecognText`; write file to disk; re-index as source `"api"`
5. Mark job `done`; on error increment `attempts`, set `last_error`, mark `failed`

A watchdog goroutine reclaims jobs stuck in `in_progress` for >10 minutes (handles worker crashes).

### File detection

Three overlapping mechanisms feed the processor queue:

1. **Engine.IO inbound events** — listen for sync-complete events on the existing `UB_SOCKETIO_URL` connection (verify event names during Phase 8 implementation)
2. **fsnotify** — watch `UB_NOTES_PATH` recursively for CREATE/RENAME/WRITE; debounce 2 seconds per path before enqueuing
3. **Periodic reconciliation scan** — full directory walk every 15 minutes; catches files missed during downtime

### Web UI additions

- **Files tab** — directory listing for `?path=` query param; breadcrumb navigation; file type icons; status badge per `.note` file; per-file actions (queue, re-queue, skip/unskip, force-include); global processor start/stop control
- **Search tab** — text input; result list with file path, page number, and snippet; results ranked by FTS5 bm25 score (not shown to user)

## Existing Patterns

Investigation found:

- **Single HTML template** (`internal/web/templates/index.html`) with inline CSS/JS, embedded via `//go:embed`. New tabs follow the same pattern.
- **Handler struct** in `internal/web/handler.go` with all routes registered in `NewHandler()`. New `/files` and `/search` routes follow this.
- **`UB_` env var config** in `internal/config/config.go`. New variables extend the existing `Config` struct.
- **Interface-driven design** — `TaskStore` and `SyncNotifier` are interfaces injected at startup in `cmd/ultrabridge/main.go`. `NoteStore`, `Processor`, and `SearchIndex` follow the same pattern.
- **No existing filesystem access** beyond credential files. `UB_NOTES_PATH` and `UB_DB_PATH` are new concepts with no prior pattern to follow.
- **MariaDB is owned by supernote-service.** UltraBridge only reads/writes its own tables (`t_schedule_task`, `u_user`). The new SQLite DB continues this separation — we never write to MariaDB.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Infrastructure — SQLite and new dependencies
**Goal:** Establish the SQLite DB, new config variables, and the go-sn dependency. No business logic yet — just the foundation everything else builds on.

**Components:**
- `go.mod` / `go.sum` — add `github.com/jdkruzr/go-sn` and a Go SQLite driver (`modernc.org/sqlite`, CGo-free)
- `internal/config/config.go` — add `DBPath`, `NotesPath`, `BackupPath`, `OCREnabled`, `OCROAPIURL`, `OCRAPIKey`, `OCRModel`, `OCRConcurrency`, `OCRMaxFileMB` fields and corresponding `UB_` env var loading
- `internal/notedb/` — SQLite connection pool, schema migration runner, table creation for all three table groups (`notes`, `jobs`, `note_content` + `note_fts`)

**Dependencies:** None (first phase)

**Done when:** `go build ./...` succeeds with new dependency; `go test ./internal/notedb/...` passes schema creation and migration idempotency; new config vars load correctly from env
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: notestore — file discovery and state tracking
**Goal:** Walk `UB_NOTES_PATH`, classify files, and maintain state in SQLite. The `NoteStore` interface is the contract for all later phases.

**Components:**
- `internal/notestore/model.go` — `NoteFile`, `ProcessingStatus` types
- `internal/notestore/store.go` — `NoteStore` interface + SQLite implementation (`List`, `Get`, `Scan`)
- `internal/notestore/scanner.go` — recursive directory walk, mtime-based change detection, sha256 fallback, upserts into `notes` table

**Dependencies:** Phase 1 (SQLite DB and config)

**Done when:** Tests cover `Scan` (new file discovered, changed file detected by mtime, unchanged file not re-hashed), `List` (directory contents correct), `Get` (single file state); all pass
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: File browser UI tab
**Goal:** Add the Files tab to the web UI with read-only directory navigation and status badges. No write actions yet.

**Components:**
- `internal/web/handler.go` — `handleFiles(w, r)` registered at `GET /files`; reads `?path=` param; calls `NoteStore.List`; renders template
- `internal/web/templates/index.html` — Files tab with breadcrumb navigation, file/folder list, type icons, status badge for `.note` files (unprocessed/queued/done/failed/skipped/unsupported)

**Dependencies:** Phase 2 (NoteStore)

**Done when:** Tests cover `GET /files` with valid path, path traversal rejection, missing `UB_NOTES_PATH`; template renders directory listing correctly in browser
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: processor — job queue and lifecycle
**Goal:** Background processor with start/stop control, job queue management, and watchdog. No OCR yet — jobs are enqueued and claimed but worker is a stub.

**Components:**
- `internal/processor/job.go` — `Job` type, status constants, status transitions
- `internal/processor/processor.go` — `Processor` interface + SQLite-backed implementation; `Start`/`Stop`; job claiming with `started_at` lock; `Enqueue`, `Skip`, `Unskip`
- `internal/processor/watchdog.go` — goroutine that reclaims `in_progress` jobs stuck >10 minutes

**Dependencies:** Phase 1 (SQLite), Phase 2 (NoteStore for path validation)

**Done when:** Tests cover enqueue, skip/unskip, status transitions, watchdog reclaim of stuck jobs; processor starts and stops cleanly; all pass
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: worker — backup, render, OCR, inject
**Goal:** The actual processing logic. Worker reads a `.note` file, optionally backs it up, extracts or re-recognizes text, and writes RECOGNTEXT back.

**Components:**
- `internal/processor/worker.go` — full job execution: backup check, `note.Load`, `note.ReadRecognText`, conditional render-to-JPEG (`note.RenderObjects`), vision API POST, `note.InjectRecognText`, file write-back
- `internal/processor/ocrclient.go` — HTTP client for the vision API; configurable URL/key/model; accepts JPEG bytes, returns text string

**Dependencies:** Phase 4 (processor lifecycle), go-sn (`note.Load`, `note.RenderObjects`, `note.InjectRecognText`)

**Done when:** Tests cover backup-before-write (backup absent → copy then proceed; backup present → skip copy; backup path unset → proceed without copy; backup copy failure → job fails), RECOGNTEXT extraction with no API call when OCR disabled, OCR path with mocked API client; all pass
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: search — FTS5 index and Search tab
**Goal:** Index recognized text into SQLite FTS5 and expose search through a new UI tab.

**Components:**
- `internal/search/model.go` — `NoteDocument`, `SearchQuery`, `SearchResult` types
- `internal/search/index.go` — `SearchIndex` interface + FTS5 implementation (`Index`, `Search`, `Delete`); bm25 ordering; snippet extraction
- Worker integration in `internal/processor/worker.go` — calls `SearchIndex.Index` after successful text extraction
- `internal/web/handler.go` — `handleSearch(w, r)` at `GET /search`
- `internal/web/templates/index.html` — Search tab with text input, result list (path, page, snippet)

**Dependencies:** Phase 5 (worker produces indexed content)

**Done when:** Tests cover index upsert, FTS5 query returns ranked results, delete removes content, snippet present in results; search handler returns results for indexed content; all pass
<!-- END_PHASE_6 -->

<!-- START_PHASE_7 -->
### Phase 7: file browser command & control
**Goal:** Add per-file write actions to the Files tab and expose processor controls in the UI.

**Components:**
- `internal/web/handler.go` — new POST routes: `/files/queue`, `/files/skip`, `/files/unskip`, `/files/force`; `GET /files/status` for processor state (JSON, polled by UI)
- `internal/web/templates/index.html` — per-file action buttons (queue/re-queue, skip, force-include); job history modal (attempts, timestamps, last error); global start/stop processor button with status indicator

**Dependencies:** Phase 3 (Files tab), Phase 4 (Processor), Phase 6 (SearchIndex for re-index on force)

**Done when:** Tests cover each POST route (queue, skip, unskip, force); status endpoint returns correct processor state; all pass
<!-- END_PHASE_7 -->

<!-- START_PHASE_8 -->
### Phase 8: file detection pipeline
**Goal:** Wire up automatic file detection so new and changed notes are queued without manual intervention.

**Components:**
- `internal/pipeline/pipeline.go` — `Pipeline` struct; owns `NoteStore`, `Processor`, `SearchIndex` lifecycles; started from `cmd/ultrabridge/main.go`
- `internal/pipeline/watcher.go` — fsnotify watcher on `UB_NOTES_PATH`; 2-second debounce per path; enqueues on CREATE/RENAME/WRITE
- `internal/pipeline/reconciler.go` — periodic full scan (15-minute interval); diffs filesystem against `notes` table; enqueues new or changed files
- `internal/pipeline/engineio.go` — listen for inbound Engine.IO events on existing `UB_SOCKETIO_URL` connection; enqueue affected paths (event names verified during implementation)
- `cmd/ultrabridge/main.go` — instantiate and start `Pipeline` alongside existing components

**Dependencies:** All prior phases

**Done when:** Tests cover watcher debounce (rapid writes produce one enqueue), reconciler detects new file, reconciler detects changed file (mtime bump); pipeline starts and stops without goroutine leaks; all pass
<!-- END_PHASE_8 -->

## Additional Considerations

**Engine.IO inbound events:** The specific event names emitted by supernote-service when a file is synced are unknown. Phase 8 includes an investigation step to snoop the WebSocket connection while syncing a file. If no usable events exist, the engineio.go component is omitted and fsnotify + reconciler provide full coverage.

**File size guard:** Files exceeding `UB_OCR_MAX_FILE_MB` are set to `skipped` with `skip_reason = "size_limit"`. The per-file force-include action in the Files tab overrides this for individual files.

**Implementation scoping:** This design has 8 phases — at the limit for a single implementation plan. If scope needs to be trimmed, Phase 7 (command & control UI) is the most separable and could move to a follow-on plan.
