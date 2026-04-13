# HTMX Fragment Mutations — Phase 6: Regression sweep and cleanup

**Goal:** Confirm no regressions introduced by Phases 1–5, remove the temporary Phase 1 test fixture, update `internal/web/CLAUDE.md` to document the new fragment-render pattern, and leave the codebase in a state where the next engineer who touches the web package knows the convention.

**Architecture:** This is a wrap-up phase. No new code paths. It removes a transient file and updates contracts documentation.

**Tech Stack:** N/A — documentation and verification only.

**Scope:** Phase 6 of 6. Requires Phases 1–5 complete.

**Codebase verified:** 2026-04-13. The temporary `_test_fixture_row.html` introduced in Phase 1 is used only by Phase 1 tests; after `_task_row.html` and `_file_row.html` exist, those real fragments can serve as the test targets for `renderFragment` and the fixture becomes dead weight. `internal/web/CLAUDE.md` exists and documents the handler contract, routes, interfaces, and test mocks — it does not yet mention `renderFragment` or the `_*.html` fragment convention.

---

## Acceptance Criteria Coverage

This phase verifies (not implements new code for):

### htmx-fragment-mutations.AC5: No regression
- **htmx-fragment-mutations.AC5.1:** All existing tests in `internal/web/` continue to pass.
- **htmx-fragment-mutations.AC5.2:** A non-HTMX `curl /` and `curl /files` still return the full HTML shell with navigation sidebar.
- **htmx-fragment-mutations.AC5.3:** Inline JavaScript in `layout.html` that depends on `#task-table`, `#files-table`, `.task-checkbox`, `.file-checkbox`, `#bulk-actions`, and `#bulk-delete-bar` continues to function after HTMX swaps.

---

<!-- START_TASK_1 -->
### Task 1: Retarget `renderFragment` tests to real fragments; remove fixture

**Files:**
- Modify: `internal/web/handler_test.go` — the Phase 1 `TestRenderFragment` test(s) currently exercise `_test_fixture_row`. Update them to exercise `_task_row` (with a minimal `service.Task` input) and `_file_row` (with a minimal `fileRowCtx` input). Both templates now exist in the codebase.
- Delete: `internal/web/templates/_test_fixture_row.html`.

**Implementation:**

The updated Phase 1 tests are effectively redundant with the identity tests from Phase 2 and Phase 4, but keep them as a minimal, focused proof that `renderFragment` itself works — a unit test of the helper, not of any specific fragment. The fixture file was a bootstrap device; now that real fragments exist, point the unit test at one of them.

**Non-trivial test data:** The real `_task_row` references `{{.ID}}`, `{{.Status}}`, `{{.Title}}`, `{{.CompletedAt | formatCreated}}`, `{{.DueAt}}` (conditionally via `formatDueTime`), `{{.Detail}}` (with `hasPrefix`/`trimPrefix`), `{{.Links}}` (with `taskLink`), plus calls into the FuncMap helpers `formatDueTime`, `formatCreated`, and `taskLink`. Constructing a `service.Task` for the test therefore needs realistic values (non-zero `CreatedAt`, valid status string, a meaningful title) or the template will either panic or emit malformed HTML. Minimal viable fixture:

```go
task := service.Task{
    ID:        "test-123",
    Title:     "Phase-6 regression fixture",
    Status:    service.StatusNeedsAction,
    CreatedAt: time.Now().UTC(),
}
```

Leave `DueAt`, `CompletedAt`, `Detail`, `Links` nil — the template handles missing optional fields. Assert `strings.Contains(body, "test-123")` and `strings.Contains(body, "Phase-6 regression fixture")` — enough to prove the template executed without asserting on every branch.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: All tests pass. Specifically the Phase 1 tests pass against the real `_task_row` template.

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Clean. No references to the deleted fixture remain.

**Commit:** `chore(web): retarget renderFragment tests and remove Phase 1 fixture`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Verify AC5 regression coverage

**Files:** None modified. Verification only.

**Implementation:**

Manually verify each AC5 criterion:

- **AC5.1:** `go -C /home/sysop/src/ultrabridge test ./...` — all tests across all packages pass.
- **AC5.2:** Start the server locally and run:
  - `curl -s http://localhost:8443/ | grep -c 'class="sidebar"'` → expect 1.
  - `curl -s http://localhost:8443/files | grep -c 'class="sidebar"'` → expect 1.
  - `curl -s -H 'HX-Request: true' http://localhost:8443/ | grep -c 'class="sidebar"'` → expect 0 (fragment response, no layout).
- **AC5.3:** Browser-level check. Navigate between tabs (Tasks → Files → back), execute a task completion and a file queue, and confirm:
  - Task checkbox → bulk-action bar still shows/hides correctly.
  - File checkbox → bulk-delete bar still shows/hides correctly.
  - `toggleCompleted()` still hides completed tasks on DOMContentLoaded and after any HTMX swap.
  - Global status bar still updates on the 5s poll.

If any step fails, open the browser console and fix before marking the phase complete. Do not skip AC5.3 — the design explicitly committed to not introducing OOB swaps for counts, which means these JS listeners must continue to work through fragment swaps.

**Verification:**

Document the checks in a short scratch note; no commit needed unless something was broken and fixed (which would be treated as a bug-fix commit, not a regression-prevention commit).

**Commit (only if a fix was needed):** `fix(web): [describe regression]`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Update `internal/web/CLAUDE.md` with the fragment-render pattern

**Files:**
- Modify: `internal/web/CLAUDE.md` — add a new section (near the "Handler contract" section, or immediately after "Template data") titled **"Fragment rendering"**. Document:
  - The `renderFragment(w, r, name, data)` method: when to use it, how it differs from `renderTemplate`.
  - The `_*.html` template convention: files named `_<name>.html` define a `{{define "_<name>"}}…{{end}}` block, are picked up by the existing `templates/*.html` embed glob, and are invoked from tab templates via `{{template "_<name>" .}}` and from mutation handlers via `h.renderFragment`.
  - The `fileRowID` template helper and its purpose (path → stable DOM id).
  - The `fileRowCtx` struct used to pass both the file and the surrounding `RelPath` into `_file_row`.
  - The "Minimal" scope decision: counts and global status stay poller-driven; no OOB swaps in this implementation. Future OOB work is deferred.
  - Update the "Last verified" date at the top of the file to the Phase-6 completion date.

Also update `/home/sysop/src/ultrabridge/CLAUDE.md` top-level file if the web-package description (currently mentioning "HTML UI" and per-tab routes) would benefit from a short note about fragment-based mutation responses. Keep it brief; the detailed contract lives in `internal/web/CLAUDE.md`.

**Implementation:**

Documentation only. Match the existing CLAUDE.md tone and style. Use concrete code snippets sparingly and only where they reduce ambiguity.

**Verification:**

Read the updated CLAUDE.md as if encountering this codebase for the first time: does it explain the fragment pattern well enough that the next engineer can add a new mutation handler (e.g., for a new kind of task action) without re-deriving the whole design? If not, revise.

**Commit:** `docs(web): document fragment-render pattern and _*.html convention`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Final whole-tree verification

**Files:** None modified. Verification only.

**Implementation:**

Execute the project's canonical gates in order:

```bash
go -C /home/sysop/src/ultrabridge vet ./...
go -C /home/sysop/src/ultrabridge test ./...
go -C /home/sysop/src/ultrabridge build ./cmd/ultrabridge/
go -C /home/sysop/src/ultrabridge build ./cmd/ub-mcp/
```

All four must succeed. If any fails, return to the relevant earlier phase and fix the root cause — do not paper over at this stage.

**Verification:**

All commands exit 0 with no warnings relevant to this work.

**Commit:** None for verification; only a "Phase 6 complete" summary if the human operator chooses to record it.
<!-- END_TASK_4 -->

---

## Phase Done When

- `_test_fixture_row.html` is deleted; Phase 1 tests retargeted to a real fragment.
- `internal/web/CLAUDE.md` documents `renderFragment`, the `_*.html` convention, `fileRowID`, and the Minimal scope decision.
- `go vet ./... && go test ./... && go build ./cmd/ultrabridge/ && go build ./cmd/ub-mcp/` all succeed.
- Manual browser verification of AC5.2 (curl fragment vs. full-page) and AC5.3 (JS listeners intact) pass.
- Between 1 and 3 commits landed (Task 1 for sure, Task 3 for sure, Task 2 only if a fix was needed).
