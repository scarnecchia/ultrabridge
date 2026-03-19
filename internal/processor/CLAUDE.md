# Processor

Last verified: 2026-03-19

## Purpose
Background OCR job queue. Processes .note files through a pipeline of backup,
text extraction, optional vision-API OCR, RECOGNTEXT injection, and search indexing.

## Contracts
- **Exposes**: `Processor` interface (Start, Stop, Status, Enqueue, Skip, Unskip, GetJob), `Job` model, `ProcessorStatus`, `OCRClient`, `Indexer` interface.
- **Guarantees**: Single worker loop claims jobs atomically. Watchdog reclaims stuck jobs (>10 min in_progress). Backup before any file modification. Graceful shutdown waits for current job.
- **Expects**: SQLite `*sql.DB` with `notes` and `jobs` tables. `WorkerConfig` with optional OCRClient and Indexer.

## Dependencies
- **Uses**: `notedb` schema, `go-sn/note` (parse/render/inject), vision API (Anthropic/OpenRouter)
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

## Invariants
- Job statuses: pending -> in_progress -> done|failed|skipped
- Backup is created exactly once per note (idempotent check via `backup_path` column)
- Size guard skips files exceeding `MaxFileMB` (status=skipped, reason=size_limit)
- Only .note files are processable (enforced by pipeline enqueue filter)

## Gotchas
- `Indexer` interface defined here (not in search) to avoid circular import
- Both OCR formats use `Authorization: Bearer` — no header difference from the caller's perspective
- Worker polls every 5s when queue is empty; watchdog runs every 2 min
