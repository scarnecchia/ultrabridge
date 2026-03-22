# Note Reprocessing Implementation Plan - Phase 2

**Goal:** Pipeline automatically re-queues done jobs when file content has changed, while skipping UB's own injection writes.

**Architecture:** Modify the `enqueue()` function's done-status guard in `internal/pipeline/pipeline.go` to compare stored hash against current file hash. If they differ (or no hash stored), re-enqueue with a 30s delay. If they match, skip (UB wrote the file).

**Tech Stack:** Go, SQLite (modernc.org/sqlite), standard library testing

**Scope:** 3 phases from original design (phase 2 of 3)

**Codebase verified:** 2026-03-22

**Key files:**
- `internal/pipeline/CLAUDE.md` — Pipeline domain contracts
- `internal/pipeline/pipeline_test.go` — Test patterns (openTestComponents, openTestComponentsWithDB, raw SQL for seeding)
- `internal/notestore/CLAUDE.md` — NoteStore interface contracts
- `internal/processor/CLAUDE.md` — Processor interface contracts

---

## Acceptance Criteria Coverage

This phase implements and tests:

### note-reprocessing.AC3: Hash-based change detection
- **note-reprocessing.AC3.1 Success:** File changed after processing (hash differs) -> job re-queued as pending with requeue delay
- **note-reprocessing.AC3.2 Success:** File unchanged after processing (hash matches) -> job stays done, not re-queued
- **note-reprocessing.AC3.3 Success:** File with no stored hash (NULL sha256) -> job re-queued (conservative)
- **note-reprocessing.AC3.4 Edge:** Rapid successive edits within delay window -> second enqueue is no-op (job already pending)

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
### Task 1: Modify enqueue() to compare hashes for done jobs

**Verifies:** note-reprocessing.AC3.1, note-reprocessing.AC3.2, note-reprocessing.AC3.3

**Files:**
- Modify: `internal/pipeline/pipeline.go:84-133` (enqueue function — replace done-status early return with hash comparison)
- Test: `internal/pipeline/pipeline_test.go` (add tests after existing tests)

**Implementation:**

Replace the done-status guard at lines 109-112 of `pipeline.go`:

Current code (lines 109-112):
```go
job, err := p.proc.GetJob(ctx, path)
if err == nil && job != nil && job.Status == processor.StatusDone {
    return
}
```

Replace with:
```go
job, err := p.proc.GetJob(ctx, path)
if err == nil && job != nil && job.Status == processor.StatusDone {
    // Compare stored hash with current file to distinguish UB's own write from a user edit.
    storedHash, hashErr := p.store.GetHash(ctx, path)
    if hashErr != nil {
        p.logger.Warn("pipeline: failed to get stored hash, skipping re-enqueue", "path", path, "err", hashErr)
        return
    }
    currentHash, hashErr := notestore.ComputeSHA256(path)
    if hashErr != nil {
        p.logger.Warn("pipeline: failed to compute file hash, skipping re-enqueue", "path", path, "err", hashErr)
        return
    }
    if storedHash != "" && storedHash == currentHash {
        return // hashes match — UB wrote this file, no re-processing needed
    }
    // Hash differs or no stored hash — user edited the file, re-queue with delay.
    if enqErr := p.proc.Enqueue(ctx, path, processor.WithRequeueAfter(30*time.Second)); enqErr != nil {
        p.logger.Warn("pipeline: re-enqueue after hash change failed", "path", path, "err", enqErr)
    } else {
        p.logger.Info("pipeline: re-queued changed file", "path", path, "storedHash", storedHash, "currentHash", currentHash)
    }
    return
}
```

Also add `"time"` to the import block if not already present. The `notestore` and `processor` packages are already imported in `pipeline.go`.

Key design decisions:
- GetHash error -> skip re-enqueue (conservative: if we can't read the hash, don't loop)
- ComputeSHA256 error -> skip re-enqueue (file may be temporarily unavailable during sync)
- storedHash=="" (NULL) AND currentHash exists -> treat as changed (conservative — one unnecessary re-process per legacy file, which also backfills the hash)
- storedHash matches currentHash -> skip (UB wrote this file)
- 30s delay absorbs rapid sync bursts
- The existing move-detection block (lines 114-128) only runs for non-done jobs, so no interaction

Also update the enqueue() doc comment (lines 84-91) to reflect the new behavior:
```go
// enqueue adds a path to the processor queue if it is a .note file.
// It first ensures the file exists in the notes table (FK constraint on jobs.note_path)
// by running a targeted upsert, then enqueues the job.
//
// Files whose last job is "done" are checked for content changes: the stored
// SHA-256 from job completion is compared against the current file. If they match
// (UB's own RECOGNTEXT injection), the file is skipped. If they differ (user edit
// on the device), the file is re-queued with a 30-second delay to debounce rapid syncs.
```

**Testing:**

Tests must verify each AC listed above:
- note-reprocessing.AC3.1: Create a file, reconcile to enqueue, mark job done with stored hash, modify file content (so hash changes), call enqueue again. Verify job status changed back to pending with requeue_after set.
- note-reprocessing.AC3.2: Create a file, reconcile, mark job done with stored hash matching current content. Call enqueue again. Verify job stays done (not re-queued).
- note-reprocessing.AC3.3: Create a file, reconcile, mark job done WITHOUT setting a hash (NULL sha256). Call enqueue. Verify job is re-queued (conservative behavior).

Follow existing pipeline test patterns: use `openTestComponentsWithDB(t)`, create Pipeline with `New(Config{...})`, seed job state via raw SQL on `db`, call `pl.enqueue(ctx, path)` directly.

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/pipeline/ -run TestEnqueue_HashChange -v
```

Expected: All three tests pass.

**Commit:** `feat(pipeline): add hash-based change detection for done jobs`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Test rapid successive edits (AC3.4)

**Verifies:** note-reprocessing.AC3.4

**Files:**
- Test: `internal/pipeline/pipeline_test.go` (add edge case test)

**Implementation:**

No new production code. This behavior falls out of existing Enqueue semantics: the ON CONFLICT clause only updates jobs with `status IN (done, failed, skipped)`. A job in `pending` status is not matched by ON CONFLICT, so the second enqueue is a no-op.

**Testing:**

- note-reprocessing.AC3.4: Create a file, reconcile, mark job done with hash. Modify file content. Call enqueue twice. Verify the job is pending after first enqueue. Verify second enqueue doesn't change anything (still one pending job, requeue_after unchanged from first enqueue).

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/pipeline/ -run TestEnqueue_RapidEdits -v
```

Expected: Test passes.

**Commit:** `test(pipeline): verify rapid edits within delay window are no-op`

<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->

<!-- START_TASK_3 -->
### Task 3: Run full test suite and verify existing tests still pass

**Verifies:** None (regression verification)

**Files:** None (no code changes)

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
go vet -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
```

Expected: All packages pass. No vet warnings. Existing pipeline tests (reconciler, watcher, move detection, conflict files) all still pass.

**Commit:** No commit needed — verification only.

<!-- END_TASK_3 -->
