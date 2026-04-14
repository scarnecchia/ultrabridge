# Test Requirements: HTMX Fragment Mutations

Derived from `docs/design-plans/2026-04-13-htmx-fragment-mutations.md` Acceptance Criteria, reconciled against the six phase implementation files. Each scoped AC maps to an automated test, a human verification step, or both.

Notes on design-vs-implementation deviations (applied below):
- **AC1.5 / AC2.3:** Design wording says `hx-swap="delete"` on each row; the implementation uses `hx-swap="none"` plus an `hx-on::after-request` JS sweep because per-row `hx-swap` from a single bulk request is not expressible without OOB (forbidden by AC6.2). Tests assert the implementation (empty 200 body + client-side sweep behavior), not the design's literal wording.
- **AC2.4:** Broad file actions return an empty body on HX-Request; tests assert only `200 OK` + empty body. Do NOT assert on tbody/row content — the downstream UI update is driven by the existing 5s `/files/status` poller, which is out of scope for this plan.
- **AC6.\*:** Explicit non-goals. No test requirements.

---

## htmx-fragment-mutations.AC1 — Task mutation responses are row-scoped

- **AC1.1** — Automated (unit, `internal/web/handler_test.go` `TestHandleCompleteTask`): HX-Request POST `/tasks/{id}/complete` returns 200 with body containing `id="task-{id}"` via `strings.Contains`.
- **AC1.2** — Automated (unit, `internal/web/handler_test.go` `TestHandleCompleteTask`): non-HTMX POST returns 303 with `Location: /`.
- **AC1.3** — Automated (unit, `internal/web/handler_test.go` `TestHandleCompleteTask`): HX-Request response body contains `data-status="completed"`. Human verification (browser): confirm `toggleCompleted()` actually hides the swapped row when the filter is active (DOM listener behavior on `htmx:afterSwap` is JS-only).
- **AC1.4** — Automated (unit, `internal/web/handler_test.go` `TestHandleBulkAction`): HX-Request POST `/tasks/bulk` with `action=complete&task_ids=a&task_ids=b` returns 200; assert `strings.Contains(body, "id=\"task-a\"")`, `strings.Contains(body, "id=\"task-b\"")`, and `strings.Count(body, "data-status=\"completed\"") == 2`.
- **AC1.5** — Automated (unit, `internal/web/handler_test.go` `TestHandleBulkAction`): HX-Request POST with `action=delete` returns 200 with empty body. Human verification (browser): selected rows are removed by the `hx-on::after-request` JS sweep (`.task-checkbox:checked → closest('tr').remove()`), and `updateBulkActions()` re-runs. **Implementation deviates from design wording (JS sweep vs per-row `hx-swap="delete"`) — test asserts implementation.**
- **AC1.6** — Automated (unit, `internal/web/handler_test.go` `TestHandleCreateTask`): HX-Request POST `/tasks` with valid title returns 200 with body containing `id="task-{newID}"` and the submitted title; non-HTMX returns 303. Human verification (browser): new row prepends to `#task-table tbody` via `hx-swap="afterbegin"`; form input clears on 2xx only.
- **AC1.7** — Automated (unit, `internal/web/handler_test.go` `TestHandlePurgeCompleted`): HX-Request POST `/tasks/purge-completed` returns 200 with empty body. Human verification (browser): `hx-on::after-request` sweep removes all `tr[data-status="completed"]` rows from `#task-table tbody` without full-tab reload.
- **AC1.8** — Automated (unit, `internal/web/handler_test.go` `TestHandleCompleteTask`): HX-Request POST for nonexistent task returns 404. Also covered upstream by `internal/service/task_test.go` verifying `errors.Is(err, sql.ErrNoRows)` propagates from `TaskService.Complete` (Phase 3 Task 3 pre-step).

---

## htmx-fragment-mutations.AC2 — File mutation responses are row-scoped

- **AC2.1** — Automated (unit, `internal/web/handler_test.go` `TestHandleFilesQueue`, `TestHandleFilesSkip`, `TestHandleFilesUnskip`, `TestHandleFilesForce`): HX-Request POST returns 200 with body containing `id="{fileRowID(path)}"` and the expected post-mutation `JobStatus` badge. Identifier produced by the same `fileRowID` helper used server-side.
- **AC2.2** — Automated (unit, `internal/web/handler_test.go` `TestHandleFilesDeleteNote`): HX-Request POST `/files/delete-note` returns 200 with empty body. Human verification (browser): history modal's delete form removes the target row (resolved via path → ID) without full-tab reload.
- **AC2.3** — Automated (unit, `internal/web/handler_test.go` `TestHandleFilesDeleteBulk`): HX-Request POST with multiple `paths` form values returns 200 with empty body. Human verification (browser): `hx-on::after-request` sweep removes all `.file-checkbox:checked` rows and `updateBulkDeleteBar()` re-runs. **Implementation deviates from design wording (JS sweep vs per-row `hx-swap="delete"`) — test asserts implementation.**
- **AC2.4** — Automated (unit, `internal/web/handler_test.go` `TestHandleBroadFileMutations`): parameterized over `/files/scan`, `/files/import`, `/files/retry-failed`, `/files/migrate-imports`, `/processor/start`, `/processor/stop`. HX-Request returns 200 with empty body; non-HTMX returns 303. **Assert only 200 OK + empty body — do NOT assert on response content or on subsequent UI state.** Downstream UI refresh is driven by the existing 5s `/files/status` poller (out of scope here). Human verification (browser): `hx-on::after-request="updateProcessorStatus()"` triggers a poller refresh within ~1s after the click.
- **AC2.5** — Automated (unit, `internal/web/handler_test.go` single-row and broad-mutation tests): non-HTMX POSTs return 303 with `Location: /files?path={escaped-back}` (query string preserved).
- **AC2.6** — Automated (unit, `internal/web/handler_test.go` `TestHandleFilesQueue` subtest for invalid path): POST with `path=../escape` returns 400 with no body render. Per Phase 5 Task 3 note, the test mirrors *current* (pre-refactor) validation behavior; if the current implementation does not actually 400 on traversal, that is a separate bug and the AC2.6 test should match the existing contract rather than introduce new validation.

---

## htmx-fragment-mutations.AC3 — Shared row templates

- **AC3.1** — Automated (static check inside `internal/web/handler_test.go` `TestTaskRowFragmentIdentity`): embedded `tasks.html` content does not contain an inline `<tr>` loop body; tbody loop invokes `{{template "_task_row" .}}`. Implicitly verified by the identity test passing — if inline markup were present, the substring assertions on `id="task-{id}"` would still pass but dual maintenance would be detectable via a separate assertion that `strings.Count(tasksHTML, "<tr")` equals the number of static header rows only. Practical test: substring assertion on `{{template "_task_row"` presence in the embedded template source, or snapshot diff.
- **AC3.2** — Automated (static check inside `internal/web/handler_test.go` `TestFileRowFragmentIdentity`): `files.html` loop invokes `{{template "_file_row" (makeFileRowCtx . $.relPath)}}`. Same substring/snapshot approach as AC3.1.
- **AC3.3** — Automated (unit, `internal/web/handler_test.go` `TestTaskRowFragmentIdentity`): for a known task, `h.renderFragment(w1, r, "_task_row", t)` and the full-tab GET `/` (HX-Request) produce bodies containing the same load-bearing tokens (`id="task-abc"`, `data-status="needsAction"`, task title, `hx-post="/tasks/abc/complete"`). Substring equivalence, not byte-for-byte (whitespace differences between template invocations are tolerated per Phase 2 Task 3 note).
- **AC3.4** — Automated (unit, `internal/web/handler_test.go` `TestFileRowFragmentIdentity`): two sub-tests (Supernote and Boox NoteFile fixtures) verify that fragment render and full-tab render emit the same `id`, source badge, filename, and `hx-target="closest tr"` attribute for each file.

---

## htmx-fragment-mutations.AC4 — Fragment-render infrastructure

- **AC4.1** — Automated (unit, `internal/web/handler_test.go` `TestRenderFragment`): `h.renderFragment(w, r, "_test_fixture_row", struct{ID, Label string}{...})` (Phase 1 fixture, later retargeted in Phase 6 to `_task_row`) writes body containing the expected template expansion; `Content-Type` starts with `text/html`; status 200.
- **AC4.2** — Automated (unit, `internal/web/handler_test.go` `TestRenderTemplate`): GET of a known tab template with and without `HX-Request: true`. Without header, body contains a layout-only marker (e.g., `<nav class="sidebar">`); with header, body does not contain that marker and does contain the tab heading. Regression guard against accidental edits to `renderTemplate`'s branching.
- **AC4.3** — Automated (implicit, same `TestRenderFragment` test): the fragment is resolvable because it lives under `internal/web/templates/` matched by the existing `templates/*.html` embed glob. No additional filesystem plumbing in the test; successful resolution is the evidence.

---

## htmx-fragment-mutations.AC5 — No regression

- **AC5.1** — Automated (whole-tree): `go test -C /home/sysop/src/ultrabridge ./...` passes. Executed as Phase 6 Task 4 gate.
- **AC5.2** — Human verification (curl): `curl -s http://localhost:8443/ | grep -c 'class="sidebar"'` returns 1; same for `/files`; the same URL with `-H 'HX-Request: true'` returns 0. Can be scripted as a smoke test but is not wired into `go test`; treat as manual per Phase 6 Task 2.
- **AC5.3** — Human verification (browser): after each HTMX swap on Tasks and Files tabs, confirm that `toggleCompleted()`, `updateBulkActions()`, `updateBulkDeleteBar()`, and the `setInterval`-driven `#global-status` updater continue to function. Not automatable in Go tests because these are DOM-level JS listener behaviors. Phase 6 Task 2 explicitly flags this as a required manual check.

---

## htmx-fragment-mutations.AC6 — Explicit non-goals

- **AC6.1, AC6.2, AC6.3, AC6.4, AC6.5** — No test requirements. These are scope boundaries, not behaviors. A test attempting to verify "we did NOT introduce OOB swaps" would be brittle and tautological; code review at PR time enforces the boundary.

---

## Coverage summary

ACs requiring **both automated + human verification**:

- **AC1.3** — Unit test asserts `data-status="completed"` present in response; human verifies `toggleCompleted()` DOM listener actually hides the swapped row on `htmx:afterSwap`.
- **AC1.5** — Unit test asserts empty 200 body; human verifies JS sweep removes checked rows and `updateBulkActions()` re-runs.
- **AC1.6** — Unit test asserts fragment in response; human verifies `hx-swap="afterbegin"` placement and form-reset-on-success JS.
- **AC1.7** — Unit test asserts empty 200 body; human verifies client-side DOM sweep removes all `[data-status="completed"]` rows.
- **AC2.2** — Unit test asserts empty 200 body; human verifies history-modal delete form resolves path-to-row-id and removes the row.
- **AC2.3** — Unit test asserts empty 200 body; human verifies JS sweep removes checked file rows and `updateBulkDeleteBar()` re-runs.
- **AC2.4** — Unit test asserts empty 200 body; human verifies immediate `updateProcessorStatus()` refresh and subsequent 5s poller reflects new state. **Tests do NOT assert on poller-driven UI state.**

ACs that are **human-verification only** (no automated coverage):

- **AC5.2** — Curl-based check for layout presence/absence (could be scripted, currently manual).
- **AC5.3** — JS listener behavior through fragment swaps.

ACs with **no test requirements**:

- **AC6.1 through AC6.5** — explicit non-goals.
