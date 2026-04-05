# Boox Pipeline

Last verified: 2026-04-05

## Purpose
Processing pipeline for Boox notes. Orchestrates parse → render → OCR → index workflow
for .note files uploaded via WebDAV, triggered by file uploads.

## Contracts
- **Exposes**: `Store` (UpsertNote, EnqueueJob, ClaimNextJob, CompleteJob, FailJob, GetNote, ReclaimStuckJobs), `BooxNote` and `BooxJob` models, `Processor` interface (Start, Stop, Enqueue), `WorkerConfig` with Indexer, ContentDeleter, OCR interfaces.
- **Guarantees**: Atomic job claiming via SQLite RETURNING. Watchdog reclaims stuck jobs (>10 min in_progress). Graceful shutdown waits for current job. Content deletion uses ContentDeleter interface to ensure FTS5 triggers fire. OCR failure fails the job but does not block subsequent jobs.
- **Expects**: SQLite `*sql.DB` with `boox_notes` and `boox_jobs` tables (created by notedb schema migrations). `WorkerConfig` with Indexer and optional OCR, ContentDeleter.

## Dependencies
- **Uses**: `notedb` schema (boox_notes, boox_jobs tables), `booxnote` (ZIP parser), `booxrender` (page renderer), `processor.Indexer` interface (shared with Supernote processor), optional `processor.OCRClient` (vision API), `search.Store.Delete()` for content deletion
- **Used by**: `cmd/ultrabridge` (wiring in main), `webdav` handler (Enqueue callback on upload)
- **Boundary**: Does not own file discovery — WebDAV handler passes paths directly.

## Key Decisions
- Separate from Supernote processor: Boox notes use different parser/renderer (booxnote/booxrender vs go-sn), no RECOGNTEXT injection, different storage format
- Shared Indexer: uses same `processor.Indexer` interface and note_content/note_fts tables as Supernote for unified search
- Atomic job claiming: SQLite RETURNING clause (SQLite 3.35+) for single-statement claim, avoiding race conditions
- Content deletion via interface: `ContentDeleter` allows search.Store to maintain FTS5 triggers on re-process
- Cache lifecycle: old cached JPEGs removed on re-process via `os.RemoveAll` before new renders
- OCR source tracking: "api" if OCR enabled, empty string if OCR disabled (no "myScript" equivalent for Boox)

## Invariants
- Job statuses: pending -> in_progress -> done|failed|skipped (or back to pending via ReclaimStuckJobs)
- boox_notes.path is PRIMARY KEY (absolute filesystem path)
- boox_jobs.note_path has FK to boox_notes.path — note row must exist before job insert
- EnqueueJob auto-creates minimal boox_notes row (INSERT OR IGNORE) to satisfy FK before worker parses metadata
- Version incremented on UpsertNote conflict (second call for same path increments version)
- ReclaimStuckJobs only operates on in_progress jobs (prevents status regression)
- Worker polls every 5s when queue is empty; watchdog runs every 1 minute

## Gotchas
- `Indexer` interface defined in processor package; `ContentDeleter` defined locally in worker.go (to avoid circular imports)
- Rendered pages cached to disk at `{CachePath}/{noteID}/page_{N}.jpg` (not in SQLite)
- boox_notes.updated_at uses millisecond UTC unix timestamps (consistent with notedb convention)
- boox_jobs timestamps use seconds (queued_at, started_at, finished_at for compatibility with watchdog timeout)
