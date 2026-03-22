# Note Reprocessing Design

## Summary

UltraBridge's notes pipeline processes `.note` files by running OCR and injecting the resulting text back into each file. Currently, once a file is marked `done`, the pipeline ignores all future filesystem events for it — a deliberate guard against re-processing UltraBridge's own RECOGNTEXT injection writes. This design removes the blanket skip and replaces it with a precise change detector: the SHA-256 hash stored on each job completion is compared against the current file's hash to distinguish "UB wrote this" (hashes match, skip) from "the user edited this on the device" (hashes differ, re-queue). Re-queuing is deferred by a short delay to absorb rapid sync bursts.

Two additional pieces complete the design. First, the `Enqueue` method gains a variadic option parameter so callers can specify a `requeue_after` delay without breaking existing callers that pass no options. Second, the `extractNotePaths` stub in the Engine.IO listener is replaced with a real parser for `FILE-SYN`/`DOWNLOADFILE` frames — the event type SPC broadcasts when its file catalog changes — feeding those paths through the same `enqueue()` flow where the hash gate prevents loops.

## Definition of Done
- When a user edits a previously-processed .note file on their Supernote and syncs, UltraBridge automatically re-runs OCR and re-injects RECOGNTEXT without manual intervention
- UB's own injection writes do NOT trigger re-processing (no infinite loop), distinguished by SHA-256 hash comparison
- The `extractNotePaths` stub in `pipeline/engineio.go` is replaced with a real parser that extracts file paths from `FILE-SYN`/`DOWNLOADFILE` Engine.IO events
- Re-processing uses a short delay to debounce rapid sync activity
- All changes are covered by tests

## Acceptance Criteria

### note-reprocessing.AC1: GetHash and data layer
- **note-reprocessing.AC1.1 Success:** GetHash returns stored SHA-256 for a file with a hash
- **note-reprocessing.AC1.2 Success:** GetHash returns empty string for a file with NULL sha256

### note-reprocessing.AC2: Enqueue with delay
- **note-reprocessing.AC2.1 Success:** Enqueue(ctx, path) with no options sets requeue_after to NULL (backward compatible)
- **note-reprocessing.AC2.2 Success:** Enqueue(ctx, path, WithRequeueAfter(30s)) sets requeue_after to now+30s
- **note-reprocessing.AC2.3 Success:** claimNext skips jobs with future requeue_after (already works, verify no regression)

### note-reprocessing.AC3: Hash-based change detection
- **note-reprocessing.AC3.1 Success:** File changed after processing (hash differs) → job re-queued as pending with requeue delay
- **note-reprocessing.AC3.2 Success:** File unchanged after processing (hash matches) → job stays done, not re-queued
- **note-reprocessing.AC3.3 Success:** File with no stored hash (NULL sha256) → job re-queued (conservative)
- **note-reprocessing.AC3.4 Edge:** Rapid successive edits within delay window → second enqueue is no-op (job already pending)

### note-reprocessing.AC4: Engine.IO FILE-SYN parser
- **note-reprocessing.AC4.1 Success:** Valid FILE-SYN/DOWNLOADFILE frame with .note file → returns resolved absolute path
- **note-reprocessing.AC4.2 Success:** Multiple entries in data array → returns all matching .note paths
- **note-reprocessing.AC4.3 Failure:** Non-FILE-SYN msgType → returns nil
- **note-reprocessing.AC4.4 Failure:** Non-.note file in DOWNLOADFILE → filtered out
- **note-reprocessing.AC4.5 Failure:** Malformed frame (bad JSON, truncated, missing fields) → returns nil, no panic

## Glossary

- **SPC (Supernote Private Cloud)**: The self-hosted cloud software that syncs files between the Supernote device and a local server over Wi-Fi.
- **RECOGNTEXT**: A structured field inside a `.note` file that stores handwriting recognition text. UltraBridge injects OCR results here in JIIX v3 format.
- **Engine.IO**: The transport protocol (underlying Socket.IO) that SPC uses for real-time communication. UB connects as a client to send STARTSYNC and receive events.
- **FILE-SYN / DOWNLOADFILE**: Engine.IO event types broadcast by SPC when its file catalog changes. The `42["ServerMessage",{...}]` frame contains file metadata including name, path, md5, and size.
- **fsnotify**: Go library for cross-platform filesystem event watching. UB uses it to detect new or modified `.note` files in real time.
- **`enqueue()`**: The pipeline-internal function (unexported) that gates all file path submissions: filters non-note files, checks job status and hash, runs move detection, and calls `Processor.Enqueue`.
- **`requeue_after`**: A nullable timestamp column on the `jobs` table. `claimNext` skips any job whose `requeue_after` is in the future, implementing the debounce delay.
- **content-hash-dedup**: The prior design (`2026-03-20-content-hash-dedup.md`) that introduced SHA-256 storage at job completion and hash-based move/rename detection. This design reuses the same stored hash for change detection.
- **reconciler**: The pipeline component that walks the filesystem every 15 minutes to catch files missed by fsnotify.

## Architecture

The pipeline currently skips re-enqueuing any file whose job status is `done`, under the assumption that the mtime change was caused by UB's own RECOGNTEXT injection. This is correct for UB's writes but incorrect when the user edits the note on the device and syncs a genuinely changed file.

The fix uses the SHA-256 hash already stored after injection (by the content-hash-dedup design) as a change detector. When `enqueue()` sees a `done` job, it compares the current file's hash against the stored hash. If they match, UB wrote the file — skip. If they differ, the user changed it — re-queue with a debounce delay.

Separately, the `extractNotePaths` stub in `pipeline/engineio.go` is replaced with a real parser for `FILE-SYN`/`DOWNLOADFILE` Engine.IO events. These events are broadcast by SPC when its catalog changes. The parser extracts note file paths and feeds them through the same `enqueue()` flow, where the hash gate prevents loops.

**Three integration points:**

1. **`pipeline.enqueue()`** — the done-status guard gains a hash comparison. New `GetHash(ctx, path)` method on `NoteStore` retrieves the stored hash. If hashes differ (or no hash stored), call `Enqueue` with a requeue delay instead of returning.

2. **`Processor.Enqueue()`** — gains a variadic `EnqueueOption` parameter. `WithRequeueAfter(duration)` sets `requeue_after` on the re-enqueued job so `claimNext` skips it until the delay expires. Existing callers pass no options and behave identically to today.

3. **`pipeline/engineio.go`** — `extractNotePaths` parses Engine.IO frames, extracts paths from `FILE-SYN`/`DOWNLOADFILE` payloads, and resolves them to absolute filesystem paths. `runEngineIOListener` passes the pipeline's `notesPath` to the parser.

**Data flow for a user edit:**

```
Device: user edits note, syncs
  → SPC writes file to disk
  → fsnotify fires
  → pipeline.enqueue(ctx, path)
  → UpsertFile (refreshes mtime/size)
  → GetJob returns "done"
  → GetHash returns post-injection hash
  → ComputeSHA256 returns current file hash
  → Hashes differ → Enqueue(ctx, path, WithRequeueAfter(30s))
  → Job transitions: done → pending (with requeue_after)
  → After 30s: worker claims, runs full OCR pipeline
  → New hash stored on completion
```

**Data flow for UB's own write (loop prevention):**

```
Worker: injects RECOGNTEXT, writes file
  → fsnotify fires
  → pipeline.enqueue(ctx, path)
  → GetJob returns "done"
  → GetHash returns post-injection hash
  → ComputeSHA256 returns current file hash
  → Hashes match → return (no re-processing)
```

## Existing Patterns

This design builds directly on the content-hash-dedup design (`2026-03-20-content-hash-dedup.md`), which wired up SHA-256 hashing at job completion and hash-based move detection at enqueue time. That design stored the hash but only used it for move/rename detection. This design uses the same stored hash for change detection.

The `EnqueueOption` variadic pattern is new to this codebase. The existing `Enqueue` method takes `(ctx, path)` and uses `ON CONFLICT DO UPDATE` with a `WHERE status IN (done, failed, skipped)` clause. The option pattern extends this without breaking existing callers — `Enqueue(ctx, path)` continues to work identically, while `Enqueue(ctx, path, WithRequeueAfter(30*time.Second))` sets the delay.

The Engine.IO parser follows the functional core pattern: `extractNotePaths` is a pure function (bytes in, paths out) with no I/O. The `runEngineIOListener` goroutine is the imperative shell that reads from the channel and calls `enqueue`.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: NoteStore GetHash and Enqueue Delay

**Goal:** Add the data layer methods needed by the pipeline: hash retrieval and delayed re-enqueue.

**Components:**
- `GetHash(ctx, path) (string, error)` method on `NoteStore` interface and `Store` implementation in `internal/notestore/store.go` — reads `sha256` from `notes` table
- `EnqueueOption` type, `WithRequeueAfter(d time.Duration) EnqueueOption` constructor in `internal/processor/processor.go`
- Modified `Enqueue` method signature: `Enqueue(ctx context.Context, path string, opts ...EnqueueOption) error` — applies `requeue_after` when option present, NULL otherwise
- Updated `Processor` interface in `internal/processor/processor.go`
- Updated mock implementations in test files

**Dependencies:** None (first phase)

**Done when:** `GetHash` returns stored hash or empty string. `Enqueue` with `WithRequeueAfter` sets `requeue_after` column. `Enqueue` without options behaves identically to current. Tests pass for `note-reprocessing.AC1.1`, `note-reprocessing.AC1.2`, `note-reprocessing.AC2.1`, `note-reprocessing.AC2.2`, `note-reprocessing.AC2.3`.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Hash-Based Change Detection in Pipeline

**Goal:** Pipeline automatically re-queues done jobs when file content has changed.

**Components:**
- Modified `enqueue()` in `internal/pipeline/pipeline.go` — after done-status check, calls `GetHash` and `ComputeSHA256`, compares, calls `Enqueue` with delay if hashes differ
- Handles edge case: no stored hash (NULL sha256) treated as "changed" to be conservative

**Dependencies:** Phase 1 (GetHash, EnqueueOption)

**Done when:** Editing a previously-processed file and triggering enqueue causes re-processing. UB's own injection writes do not trigger re-processing. Tests pass for `note-reprocessing.AC3.1`, `note-reprocessing.AC3.2`, `note-reprocessing.AC3.3`, `note-reprocessing.AC3.4`.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: Engine.IO FILE-SYN Parser

**Goal:** Replace `extractNotePaths` stub with a real parser for SPC events.

**Components:**
- `extractNotePaths(msg []byte, notesPath string) []string` in `internal/pipeline/engineio.go` — parses `42["ServerMessage",{...}]` frames, extracts paths from `FILE-SYN`/`DOWNLOADFILE` data entries, resolves to absolute paths
- Modified `runEngineIOListener` to pass `notesPath` to parser
- Modified `Pipeline` struct if needed to store `notesPath` for the listener (already available as `p.notesPath`)

**Dependencies:** Phase 2 (hash gate must exist before Engine.IO paths flow through enqueue)

**Done when:** Valid `FILE-SYN`/`DOWNLOADFILE` frames produce resolved paths. Non-matching frames return nil. Malformed input returns nil without panicking. Tests pass for `note-reprocessing.AC4.1` through `note-reprocessing.AC4.5`.
<!-- END_PHASE_3 -->

## Additional Considerations

**Rapid successive edits:** If the user edits, syncs, edits again, and syncs again within the 30s requeue delay window, the second `enqueue` call hits a `pending` job (not `done`). The `ON CONFLICT WHERE status IN (done, failed, skipped)` clause prevents the update — the second edit is silently ignored until the first re-processing completes. After that job finishes and stores a new hash, the reconciler's 15-minute scan (or the next fsnotify event) will catch the second edit if the hash still differs.

**Missing hash (NULL sha256):** Files processed before the content-hash-dedup feature have no stored hash. Treating NULL as "changed" ensures these files get re-queued on their first edit, which also backfills their hash. This is conservative but correct — one unnecessary re-processing per legacy file.

**Engine.IO event scope:** Investigation of adb logcat confirmed that SPC currently only broadcasts `FILE-SYN`/`DOWNLOADFILE` events for catalog changes initiated by UB (not for device uploads). The parser is still valuable: it completes the stub, enables logging of SPC events for observability, and will automatically handle device-upload events if SPC adds them in the future.
