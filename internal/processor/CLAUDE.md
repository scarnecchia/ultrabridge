# Processor

Last verified: 2026-03-20

## Purpose
Background OCR job queue. Processes .note files through a pipeline of backup,
text extraction, optional vision-API OCR, RECOGNTEXT injection, and search indexing.

## Contracts
- **Exposes**: `Processor` interface (Start, Stop, Status, Enqueue, Skip, Unskip, GetJob), `Job` model (includes `RequeueAfter`), `ProcessorStatus`, `OCRClient`, `Indexer` interface, `Requeue(ctx, jobID, delay)`, `CatalogUpdater` interface, `NewSPCCatalog`.
- **Guarantees**: Single worker loop claims jobs atomically. Watchdog reclaims stuck jobs (>10 min in_progress). Backup before any file modification. Graceful shutdown waits for current job. Requeued jobs respect delay before re-claim. SPC catalog updates are best-effort (logged, never fail the job).
- **Expects**: SQLite `*sql.DB` with `notes` and `jobs` tables (including `requeue_after` column). `WorkerConfig` with optional OCRClient, Indexer, and CatalogUpdater.

## Dependencies
- **Uses**: `notedb` schema, `go-sn/note` (parse/render/inject), vision API (Anthropic/OpenRouter), SPC MariaDB (f_user_file, f_file_action, f_capacity -- via CatalogUpdater)
- **Used by**: `pipeline` (Enqueue), `web` (Start/Stop/Status/Skip/Unskip/GetJob for C&C routes)
- **Boundary**: Does not own file discovery -- that is `pipeline`'s responsibility.

## Key Decisions
- SQLite job queue (not external broker): simplicity, single-process deployment
- OCR via vision API — two formats supported:
  - `OCRFormatAnthropic` (`anthropic`): Anthropic Messages API `/v1/messages` — used with direct Anthropic API or OpenRouter
  - `OCRFormatOpenAI` (`openai`): OpenAI Chat Completions `/v1/chat/completions` — used with vLLM, Ollama, or any OpenAI-compatible endpoint
  - Configured via `UB_OCR_FORMAT`; defaults to `anthropic`
- Two-source indexing: "myScript" (existing RECOGNTEXT) indexed first, then "api" (OCR result) overwrites
- File reloaded after each page injection: .note format offsets shift when RECOGNTEXT is written
- RTR gate: only notes with `FILE_RECOGN_TYPE=1` get RECOGNTEXT injection; non-RTR notes are OCR'd and indexed but not modified
- RECOGNSTATUS gate: RTR notes are requeued (5 min delay, up to 12 attempts / 1 hour) until device recognition is complete on all pages
- JIIX format: injection uses `BuildRecognText` (JIIX v3 "Raw Content" with word-level bounding boxes in mm) to produce device-compatible RECOGNTEXT that survives SPC sync
- SPC catalog sync after successful injection: updates f_user_file (size, md5, update_time), inserts f_file_action audit row, adjusts f_capacity quota delta. Each step independent -- one failure does not block others.

## Invariants
- Job statuses: pending -> in_progress -> done|failed|skipped (requeue sets back to pending with delay)
- Requeue only operates on in_progress jobs (prevents status regression)
- Backup is created exactly once per note (idempotent check via `backup_path` column)
- Size guard skips files exceeding `MaxFileMB` (status=skipped, reason=size_limit)
- Only .note files are processable (enforced by pipeline enqueue filter)

## Gotchas
- `Indexer` and `CatalogUpdater` interfaces defined here (not in their impl packages) to avoid circular imports
- Both OCR formats use `Authorization: Bearer` — no header difference from the caller's perspective
- Worker polls every 5s when queue is empty; watchdog runs every 2 min
