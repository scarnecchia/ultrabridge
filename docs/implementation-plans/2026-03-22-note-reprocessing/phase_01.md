# Note Reprocessing Implementation Plan - Phase 1

**Goal:** Add data layer methods for hash retrieval and delayed re-enqueue.

**Architecture:** Extend NoteStore with GetHash to read stored SHA-256. Extend Processor.Enqueue with variadic options to support requeue_after delay. Both changes are backward-compatible.

**Tech Stack:** Go, SQLite (modernc.org/sqlite), standard library testing

**Scope:** 3 phases from original design (phase 1 of 3)

**Codebase verified:** 2026-03-22

**Key files:**
- `internal/notestore/CLAUDE.md` — NoteStore domain contracts
- `internal/processor/CLAUDE.md` — Processor domain contracts
- `internal/notestore/store_test.go` — Testing patterns (real in-memory SQLite, t.Helper, t.Cleanup, table-driven)
- `internal/processor/processor_test.go` — Processor test patterns (seedNotesRow helper, raw SQL assertions)

---

## Acceptance Criteria Coverage

This phase implements and tests:

### note-reprocessing.AC1: GetHash and data layer
- **note-reprocessing.AC1.1 Success:** GetHash returns stored SHA-256 for a file with a hash
- **note-reprocessing.AC1.2 Success:** GetHash returns empty string for a file with NULL sha256

### note-reprocessing.AC2: Enqueue with delay
- **note-reprocessing.AC2.1 Success:** Enqueue(ctx, path) with no options sets requeue_after to NULL (backward compatible)
- **note-reprocessing.AC2.2 Success:** Enqueue(ctx, path, WithRequeueAfter(30s)) sets requeue_after to now+30s
- **note-reprocessing.AC2.3 Success:** claimNext skips jobs with future requeue_after (already works, verify no regression)

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
### Task 1: Add GetHash to NoteStore interface and Store implementation

**Verifies:** note-reprocessing.AC1.1, note-reprocessing.AC1.2

**Files:**
- Modify: `internal/notestore/store.go:22-47` (NoteStore interface — add GetHash method)
- Modify: `internal/notestore/store.go` (Store struct — add GetHash implementation after SetHash at line 168)
- Test: `internal/notestore/store_test.go` (add tests after TestSetHash at line 300)

**Implementation:**

Add `GetHash` to the `NoteStore` interface:

```go
// GetHash returns the stored SHA-256 hex digest for the file at path.
// Returns empty string if no hash is stored (NULL sha256).
GetHash(ctx context.Context, path string) (string, error)
```

Add the implementation on `Store`:

```go
// GetHash returns the stored SHA-256 hex digest for the file at path.
// Returns empty string with nil error if the hash is NULL.
func (s *Store) GetHash(ctx context.Context, path string) (string, error) {
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT sha256 FROM notes WHERE path=?", path).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get hash %s: %w", path, err)
	}
	if !hash.Valid {
		return "", nil
	}
	return hash.String, nil
}
```

Key design decisions:
- Uses `sql.NullString` to distinguish NULL from empty string
- Returns `ErrNotFound` if the path doesn't exist in notes (consistent with `Get`)
- NULL sha256 returns empty string with nil error (not an error condition — file just hasn't been hashed yet)

**Testing:**

Tests must verify each AC listed above:
- note-reprocessing.AC1.1: Seed a notes row with a known sha256 value, call GetHash, verify it returns the stored hash
- note-reprocessing.AC1.2: Seed a notes row without setting sha256 (defaults to NULL), call GetHash, verify it returns empty string with nil error

Follow existing test patterns in `store_test.go`: use `openTestStore(t)`, seed rows with raw SQL via `s.db.ExecContext`, assert with `if got != want { t.Errorf(...) }`.

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/notestore/ -run TestGetHash -v
```

Expected: Both tests pass.

**Commit:** `feat(notestore): add GetHash method for SHA-256 retrieval`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Verify GetHash integration with existing SetHash

**Verifies:** note-reprocessing.AC1.1 (roundtrip confirmation)

**Files:**
- Test: `internal/notestore/store_test.go` (add roundtrip test)

**Implementation:**

No new production code. Add a test that exercises the SetHash -> GetHash roundtrip:

**Testing:**

- note-reprocessing.AC1.1 roundtrip: Seed a notes row, call SetHash with a known digest, then call GetHash and verify the returned value matches. This confirms the two methods work together correctly.

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/notestore/ -run TestGetHash -v
```

Expected: All GetHash tests pass.

**Commit:** `test(notestore): add GetHash/SetHash roundtrip test`

<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->

<!-- START_TASK_3 -->
### Task 3: Add EnqueueOption type and modify Enqueue signature

**Verifies:** note-reprocessing.AC2.1, note-reprocessing.AC2.2

**Files:**
- Modify: `internal/processor/processor.go:42-51` (Processor interface — change Enqueue signature)
- Modify: `internal/processor/processor.go:115-129` (Store.Enqueue implementation — apply options)
- Modify: `internal/web/handler_test.go:169` (mockProcessor.Enqueue — update signature to accept `...processor.EnqueueOption`)
- Test: `internal/processor/processor_test.go` (add tests)

**Implementation:**

Add the option types before the Processor interface (around line 40):

```go
// EnqueueOption configures optional behavior for Enqueue.
type EnqueueOption func(*enqueueConfig)

type enqueueConfig struct {
	requeueAfter *time.Duration
}

// WithRequeueAfter sets a delay before the re-enqueued job can be claimed.
// claimNext will skip the job until the delay has elapsed.
func WithRequeueAfter(d time.Duration) EnqueueOption {
	return func(c *enqueueConfig) {
		c.requeueAfter = &d
	}
}
```

Update the Processor interface:

```go
Enqueue(ctx context.Context, path string, opts ...EnqueueOption) error
```

Update the Store.Enqueue implementation:

```go
func (s *Store) Enqueue(ctx context.Context, path string, opts ...EnqueueOption) error {
	var cfg enqueueConfig
	for _, o := range opts {
		o(&cfg)
	}

	now := time.Now()
	var requeueAfter any
	if cfg.requeueAfter != nil {
		requeueAfter = now.Add(*cfg.requeueAfter).Unix()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (note_path, status, queued_at, requeue_after)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(note_path) DO UPDATE SET status=excluded.status, queued_at=excluded.queued_at, requeue_after=excluded.requeue_after
		WHERE status IN (?, ?, ?)`,
		path, StatusPending, now.Unix(), requeueAfter,
		StatusDone, StatusFailed, StatusSkipped,
	)
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", path, err)
	}
	return nil
}
```

Key design decisions:
- Variadic options: existing callers pass no options, behavior unchanged
- `requeueAfter` passed as `any` — when nil, SQLite stores NULL; when set, stores unix timestamp
- The ON CONFLICT clause now propagates `excluded.requeue_after` instead of hardcoding NULL, so new enqueues without options insert NULL (same as before) while re-enqueues with delay insert the timestamp. For the no-option case, `requeueAfter` is nil, so the INSERT value is NULL, and `excluded.requeue_after` propagates NULL — identical to the current hardcoded `requeue_after=NULL`.
- `enqueueConfig` is unexported — only `EnqueueOption` and `WithRequeueAfter` are public
- Uses a single `time.Now()` call for both `queued_at` and `requeue_after` calculation to avoid sub-second drift
- **Mock update required:** `internal/web/handler_test.go:169` has a `mockProcessor` that implements the `Processor` interface. Update its `Enqueue` signature to: `func (m *mockProcessor) Enqueue(_ context.Context, path string, _ ...processor.EnqueueOption) error` — the mock ignores options, just accepts them

**Testing:**

Tests must verify each AC listed above:
- note-reprocessing.AC2.1: Call `Enqueue(ctx, path)` with no options, query the jobs row directly, verify `requeue_after IS NULL`
- note-reprocessing.AC2.2: Call `Enqueue(ctx, path, WithRequeueAfter(30*time.Second))`, query the jobs row, verify `requeue_after` is approximately `now + 30s` (within 2-second tolerance)

Follow existing patterns: use `openTestProcessor(t)`, `seedNotesRow(t, s, path)`, raw SQL assertions.

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/processor/ -run TestEnqueue -v
```

Expected: Both new tests pass.

**Commit:** `feat(processor): add EnqueueOption with WithRequeueAfter support`

<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Verify backward compatibility and claimNext regression

**Verifies:** note-reprocessing.AC2.1, note-reprocessing.AC2.3

**Files:**
- Test: `internal/processor/processor_test.go` (add re-enqueue backward compat test)

**Implementation:**

No new production code. Add tests confirming:

**Testing:**

- note-reprocessing.AC2.1 backward compat: Enqueue a job with no options, then re-enqueue the same path (simulating done -> re-enqueue). Verify requeue_after remains NULL after re-enqueue without options.
- note-reprocessing.AC2.3 regression: This is already covered by the existing `TestClaimNext_SkipsFutureRequeueAfter` and `TestClaimNext_ClaimsPastRequeueAfter` tests. Run the full processor test suite to confirm no regression.

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./internal/processor/ -v
```

Expected: All processor tests pass (existing + new).

**Commit:** `test(processor): verify Enqueue backward compatibility and claimNext regression`

<!-- END_TASK_4 -->

<!-- END_SUBCOMPONENT_B -->

<!-- START_TASK_5 -->
### Task 5: Run full test suite

**Verifies:** None (integration verification)

**Files:** None (no code changes)

**Verification:**

```bash
go test -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
go vet -C /home/sysop/src/ultrabridge/.worktrees/note-reprocessing ./...
```

Expected: All packages pass. No vet warnings.

**Commit:** No commit needed — verification only.

<!-- END_TASK_5 -->
