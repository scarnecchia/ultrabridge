# Test Requirements: Note Reprocessing

Maps each acceptance criterion from the [design](../design-plans/2026-03-22-note-reprocessing.md) to automated tests or human verification. Every criterion is covered.

## Phase 1: NoteStore GetHash and Enqueue Delay

### note-reprocessing.AC1: GetHash and data layer

| AC | Description | Test Type | Test File | Verifies |
|----|-------------|-----------|-----------|----------|
| AC1.1 | GetHash returns stored SHA-256 for a file with a hash | Unit | `internal/notestore/store_test.go` (`TestGetHash`) | Seed a notes row with a known sha256 value via raw SQL. Call `GetHash`. Assert returned string equals the seeded hash and error is nil. |
| AC1.1 (roundtrip) | GetHash returns hash previously written by SetHash | Unit | `internal/notestore/store_test.go` (`TestGetHash`) | Call `SetHash` with a known digest, then `GetHash`. Assert the returned value matches. Confirms the two methods compose correctly. |
| AC1.2 | GetHash returns empty string for a file with NULL sha256 | Unit | `internal/notestore/store_test.go` (`TestGetHash`) | Seed a notes row without setting sha256 (column defaults to NULL). Call `GetHash`. Assert returned string is `""` and error is nil. |

**Rationale:** Both criteria are pure data-layer operations against an in-memory SQLite database. No external dependencies, no filesystem. Unit tests with `openTestStore(t)` are sufficient and fast.

### note-reprocessing.AC2: Enqueue with delay

| AC | Description | Test Type | Test File | Verifies |
|----|-------------|-----------|-----------|----------|
| AC2.1 | Enqueue(ctx, path) with no options sets requeue_after to NULL | Unit | `internal/processor/processor_test.go` (`TestEnqueue`) | Call `Enqueue(ctx, path)` with no options. Query the jobs row directly with raw SQL. Assert `requeue_after IS NULL`. |
| AC2.2 | Enqueue(ctx, path, WithRequeueAfter(30s)) sets requeue_after to now+30s | Unit | `internal/processor/processor_test.go` (`TestEnqueue`) | Call `Enqueue(ctx, path, WithRequeueAfter(30*time.Second))`. Query the jobs row. Assert `requeue_after` is within 2 seconds of `now + 30s` (tolerance for test execution time). |
| AC2.3 | claimNext skips jobs with future requeue_after | Unit | `internal/processor/processor_test.go` (existing `TestClaimNext_SkipsFutureRequeueAfter`, `TestClaimNext_ClaimsPastRequeueAfter`) | **No new test needed.** Existing tests already cover this behavior. Run full processor test suite to confirm no regression from the Enqueue signature change. |

**Rationale:** AC2.1 and AC2.2 test SQL output of the modified Enqueue method -- pure data-layer, unit-testable with `openTestProcessor(t)` and `seedNotesRow`. AC2.3 is a regression check; the existing tests remain valid because the claimNext logic is unchanged.

## Phase 2: Hash-Based Change Detection in Pipeline

### note-reprocessing.AC3: Hash-based change detection

| AC | Description | Test Type | Test File | Verifies |
|----|-------------|-----------|-----------|----------|
| AC3.1 | File changed after processing (hash differs) -- job re-queued as pending with requeue delay | Integration | `internal/pipeline/pipeline_test.go` (`TestEnqueue_HashChange`) | Create a real .note file in a temp dir. Reconcile to enqueue. Mark job done and set stored hash via raw SQL. Modify file content (so `ComputeSHA256` returns a different hash). Call `enqueue()` again. Assert job status is `pending` and `requeue_after` is non-NULL. |
| AC3.2 | File unchanged after processing (hash matches) -- job stays done | Integration | `internal/pipeline/pipeline_test.go` (`TestEnqueue_HashChange`) | Create a real .note file. Reconcile, mark done, set stored hash to the file's actual current SHA-256. Call `enqueue()`. Assert job status remains `done`. |
| AC3.3 | File with no stored hash (NULL sha256) -- job re-queued | Integration | `internal/pipeline/pipeline_test.go` (`TestEnqueue_HashChange`) | Create a file, reconcile, mark done WITHOUT setting sha256 (NULL). Call `enqueue()`. Assert job is re-queued as `pending` with `requeue_after` set. This validates the conservative fallback for legacy files. |
| AC3.4 | Rapid successive edits within delay window -- second enqueue is no-op | Integration | `internal/pipeline/pipeline_test.go` (`TestEnqueue_RapidEdits`) | Create a file, reconcile, mark done with hash. Modify content. Call `enqueue()` twice. After first call, assert job is `pending`. After second call, assert job is still `pending` with the same `requeue_after` value (ON CONFLICT clause skips pending jobs). |

**Rationale:** These tests require real filesystem interaction (`ComputeSHA256` reads the file) and real SQLite. This makes them integration tests, matching the existing `openTestComponentsWithDB` pattern in `pipeline_test.go`.

## Phase 3: Engine.IO FILE-SYN Parser

### note-reprocessing.AC4: Engine.IO FILE-SYN parser

| AC | Description | Test Type | Test File | Verifies |
|----|-------------|-----------|-----------|----------|
| AC4.1 | Valid FILE-SYN/DOWNLOADFILE frame with .note file -- returns resolved absolute path | Unit | `internal/pipeline/engineio_test.go` (`TestExtractNotePaths`) | Build a valid `42["ServerMessage","..."]` frame with msgType=FILE-SYN, one .note entry. Assert returns `[]string{filepath.Join(notesPath, "Note/MyNote.note")}`. Repeat with msgType=DOWNLOADFILE. |
| AC4.2 | Multiple entries in data array -- returns all matching .note paths | Unit | `internal/pipeline/engineio_test.go` (`TestExtractNotePaths`) | Frame with 3 data entries: 2 .note files, 1 .pdf. Assert returns exactly 2 paths. |
| AC4.3 | Non-FILE-SYN msgType -- returns nil | Unit | `internal/pipeline/engineio_test.go` (`TestExtractNotePaths`) | Frame with msgType="SOME-OTHER-TYPE". Assert returns nil. Also test msgType="STARTSYNC" to confirm STARTSYNC events do not enqueue files. |
| AC4.4 | Non-.note file in DOWNLOADFILE -- filtered out | Unit | `internal/pipeline/engineio_test.go` (`TestExtractNotePaths`) | Frame with only non-.note files (.pdf, .epub). Assert returns nil (empty after filtering). |
| AC4.5 | Malformed frame -- returns nil, no panic | Unit | `internal/pipeline/engineio_test.go` (`TestExtractNotePaths`) | Table-driven sub-tests: empty input, missing `42` prefix, truncated JSON, valid outer array but invalid inner payload, valid JSON but missing data field. Assert each returns nil without panicking. |

**Rationale:** `extractNotePaths` is a pure function (bytes in, paths out) with no I/O, database access, or filesystem interaction. All five ACs are testable with unit tests using a `buildFrame` helper.

## Cross-Cutting: Regression Verification

Each phase concludes with a full test suite run (`go test ./...` and `go vet ./...`). This guards against regressions in existing pipeline, processor, notestore, and web handler mock tests.

## Human Verification

No acceptance criteria require human verification. All criteria are testable through automated unit or integration tests because:

1. **AC1 and AC2** are pure data-layer operations (SQL in, values out)
2. **AC3** uses real files and real SQLite -- no external services needed
3. **AC4** is a pure function with deterministic input/output

The end-to-end flow (device edit -> sync -> re-OCR) is validated by the composition of AC3 tests (hash gate) + AC2 tests (delayed enqueue) + existing worker tests (claim and process).

## Summary

| Phase | ACs | Automated Tests | Human Verification |
|-------|-----|----------------|--------------------|
| 1 | AC1.1, AC1.2, AC2.1, AC2.2, AC2.3 | 5 (3 notestore unit + 2 processor unit + existing regression) | 0 |
| 2 | AC3.1, AC3.2, AC3.3, AC3.4 | 4 pipeline integration tests | 0 |
| 3 | AC4.1, AC4.2, AC4.3, AC4.4, AC4.5 | 11+ table-driven unit test cases in 5 logical groups | 0 |
| **Total** | **14 ACs** | **20+ test cases** | **0** |
