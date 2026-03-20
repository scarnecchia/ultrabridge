# Content Hash Deduplication Design

## Summary

UltraBridge processes Supernote `.note` files through a pipeline that extracts text, runs OCR, and indexes content for search. Currently, if a file is moved or renamed on the device, the reconciler treats it as new content and re-queues it for full processing — wasting time and potentially duplicating work. This design adds SHA-256 content hashing to distinguish "same file, new location" from "genuinely new or changed file."

The approach wires up two already-stubbed but never-called pieces of infrastructure: the `sha256` column in the `notes` table and the `ComputeSHA256` function in `notestore/scanner.go`. At job completion, the worker stores the hash of the final file state. At enqueue time, before starting any work, the pipeline checks whether a completed job already exists for that exact content at a different path; if so, it transfers the job record to the new path and returns without re-processing. Hash computation is gated behind the existing mtime-change check, so files that haven't changed are never hashed.

## Definition of Done

- When a `.note` file is moved or renamed to a new path and its content is unchanged, UltraBridge detects this via SHA-256 content hash at enqueue time and skips re-processing, transferring the existing completed job record to the new path.
- When a `.note` file's mtime changes but content is unchanged (e.g., accidental `touch`), UltraBridge skips re-enqueueing if a completed job exists for that content.
- After a job completes (whether OCR was applied or not), the SHA-256 hash of the final file is stored in `notes.sha256` for future deduplication.

## Acceptance Criteria

### content-hash-dedup.AC1: Move/rename detection skips re-processing
- **content-hash-dedup.AC1.1 Success:** File moved to a new path with unchanged content → job record transferred to new path, file not re-enqueued
- **content-hash-dedup.AC1.2 Success:** File renamed (same directory, different name) → same behavior as move
- **content-hash-dedup.AC1.3 Success:** New path's `notes.sha256` is set to the file's hash after transfer
- **content-hash-dedup.AC1.4 Failure:** File moved AND content changed → hash mismatch, file enqueued for processing normally

### content-hash-dedup.AC2: Mtime-only changes don't trigger re-processing
- **content-hash-dedup.AC2.1 Success:** File `touch`ed (mtime changed, content unchanged) with existing done job → not re-enqueued (done-status guard fires before hash check; no regression)

### content-hash-dedup.AC3: Worker stores hash on completion
- **content-hash-dedup.AC3.1 Success:** Job completes with OCR applied → `notes.sha256` set to SHA-256 of the final (post-injection) file
- **content-hash-dedup.AC3.2 Success:** Job completes without OCR (myScript-only or OCR disabled) → `notes.sha256` set to SHA-256 of the original file
- **content-hash-dedup.AC3.3 Failure:** Job fails → `notes.sha256` not written (no hash stored for failed jobs)

### content-hash-dedup.AC4: Job transfer integrity
- **content-hash-dedup.AC4.1 Success:** Transferred job retains `ocr_source`, `api_model`, `status=done` from original
- **content-hash-dedup.AC4.2 Success:** Old path's job record is gone after transfer (UPDATE moved it, not copied)

## Glossary

- **SHA-256**: A cryptographic hash function that produces a fixed-length fingerprint of a file's bytes. Two files with identical content produce the same hash; any change to the bytes produces a different hash.
- **mtime**: The filesystem "last modified" timestamp on a file. The pipeline uses mtime changes as a cheap first signal that a file may need reprocessing before doing anything more expensive.
- **reconciler**: The component that periodically scans the notes directory and compares what's on disk against what's in the database, enqueuing new or changed files.
- **`pruneOrphans`**: A reconciler pass that deletes database rows for files that no longer exist at their recorded path.
- **done-status guard**: An existing early-return check in `pipeline.enqueue()` that skips re-enqueuing a file if it already has a completed job, preventing redundant work.
- **job transfer**: Moving a job record from one file path to another via `UPDATE jobs SET note_path = new_path WHERE note_path = old_path`, rather than creating a new job entry.
- **myScript**: Supernote's on-device handwriting recognition engine. Text it produces is stored in the `.note` file under a `RECOGNTEXT` record, distinct from text produced by an external OCR API call.
- **`NoteStore` interface**: The Go interface in `internal/notestore/store.go` that defines all database operations on the notes and jobs tables. All pipeline and worker code interacts with the database through this abstraction.

## Architecture

The `notes` table already has a `sha256 TEXT` column (never populated) and `ComputeSHA256()` is already defined and tested in `notestore/scanner.go` (never called). This design wires up the existing stub.

Content-only SHA-256 hashing (no filename component) means moves and renames are detected equally — if bytes match, it's the same content regardless of where the file lives.

**Two integration points:**

1. **`pipeline.enqueue()`** — after the existing done-status guard, compute SHA-256 of the new/changed file and query `notes.sha256` for a match. If a match exists with a completed job at a different path, transfer the job record to the new path and skip enqueue. If no match, proceed to enqueue normally.

2. **`processJob()` in the worker** — at successful completion (all pages processed), compute SHA-256 of the final file state and store it in `notes.sha256`. This runs whether or not OCR was applied: myScript-only files also get their hash recorded.

**Data flow for a move:**

```
Reconciler detects new path → pipeline.enqueue()
  → UpsertFile (notes row created for new path)
  → done-status guard (no job for new path, falls through)
  → ComputeSHA256(new path)
  → LookupByHash: finds old path with matching sha256 + done job
  → UPDATE jobs SET note_path = new_path WHERE note_path = old_path
  → UPDATE notes SET sha256 = hash WHERE path = new_path
  → return (no enqueue)
Old path notes row → pruned by next reconciler orphan pass
```

## Existing Patterns

Investigation found that `ComputeSHA256` in `internal/notestore/scanner.go` was stubbed as "called lazily by the worker before first modification" but never implemented. The `notes.sha256` column exists in the schema at `internal/notedb/schema.go` but is never written. This design completes what was clearly intended.

`NoteStore` interface in `internal/notestore/store.go` is the existing contract for DB operations on the notes table. Two new methods are added to this interface. The pattern follows existing methods: context-first arguments, explicit error returns, `ErrNotFound` sentinel for misses.

`pipeline.enqueue()` in `internal/pipeline/pipeline.go` is the existing choke point for all file enqueue decisions. The hash check is inserted there, following the existing guard pattern (check condition → return early if no work needed).

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: NoteStore methods and hash storage in worker

**Goal:** Populate `notes.sha256` after job completion, and expose the two new NoteStore methods.

**Components:**
- `internal/notestore/store.go` — add `SetHash(ctx, path, hash string) error` and `LookupByHash(ctx, hash string) (path, jobStatus string, found bool, err error)` to the `NoteStore` interface
- `internal/notestore/notestore.go` (or equivalent implementation file) — implement both methods:
  - `SetHash`: `UPDATE notes SET sha256=? WHERE path=?`
  - `LookupByHash`: `SELECT n.path, j.status FROM notes n JOIN jobs j ON j.note_path=n.path WHERE n.sha256=? LIMIT 1`
- `internal/processor/worker.go` — at the end of `processJob()`, before `markDone()`: call `ComputeSHA256(job.NotePath)` and `store.SetHash()`
- `internal/notestore/store_test.go` — tests for `SetHash` and `LookupByHash`
- `internal/processor/worker_test.go` — verify `notes.sha256` is populated after a successful job

**Dependencies:** None (first phase)

**Done when:** `content-hash-dedup.AC3.1`, `content-hash-dedup.AC3.2` pass — `notes.sha256` is set after job completion, `LookupByHash` returns the correct record
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Hash-based skip and job transfer in pipeline.enqueue

**Goal:** At enqueue time, detect moved/renamed files via hash and transfer the job rather than re-processing.

**Components:**
- `internal/pipeline/pipeline.go` — extend `enqueue()`: after the done-status guard, call `ComputeSHA256`, call `store.LookupByHash`, and if a done job is found at a different path, call `store.TransferJob(ctx, oldPath, newPath)` then `store.SetHash` and return
- `internal/notestore/store.go` — add `TransferJob(ctx, oldPath, newPath string) error` to `NoteStore` interface: `UPDATE jobs SET note_path=? WHERE note_path=?` (single statement, relies on FK cascade)
- `internal/notestore/notestore.go` — implement `TransferJob`
- `internal/notestore/store_test.go` — tests for `TransferJob`
- `internal/pipeline/pipeline_test.go` — integration test: file present at path A with done job and sha256, reconciler detects path B with same content → job transferred, path B not enqueued

**Dependencies:** Phase 1 (`LookupByHash`, `SetHash`)

**Done when:** `content-hash-dedup.AC1.1`, `content-hash-dedup.AC1.2`, `content-hash-dedup.AC2.1` pass — move/rename detection works end-to-end, job transferred, no re-enqueue
<!-- END_PHASE_2 -->

## Additional Considerations

**Hash cost:** SHA-256 is only computed for files that passed the mtime-change gate (i.e., files whose mtime differs from what's in the DB). Unchanged files are never hashed. For a typical `.note` file (1–10 MB), SHA-256 takes single-digit milliseconds — negligible.

**Stale jobs pointing to moved files:** Between the move and the next reconciler pass, the old path's notes/jobs rows remain in the DB. `pruneOrphans` cleans these up within 15 minutes. No special handling needed — if `TransferJob` has already moved the job record, the subsequent `DELETE FROM jobs WHERE note_path=old_path` in `pruneOrphans` is a no-op (0 rows affected).

**Hash collision (semantic, not cryptographic):** Two `.note` files with identical bytes are essentially impossible in practice — `.note` files contain per-file UUIDs, device identifiers, and timestamps in their headers. Not worth designing around.

**Files with no OCR:** If OCR is disabled or a file has no strokes to render, the worker still runs (myScript extraction, indexing) but does not modify the file. The stored hash in this case is the hash of the original file. Future moves of these files are still detected correctly.
