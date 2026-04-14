# HTMX Fragment Mutations — Phase 2: Task row fragment

**Goal:** Extract the `<tr>` markup for a task row out of `tasks.html` into a shared `_task_row.html` template block, then have `tasks.html` loop using `{{template "_task_row" .}}`. Update the per-row HTMX attributes so the row targets itself (not the whole tab). No handler changes in this phase.

**Architecture:** Single source of truth for task-row HTML. `_task_row.html` defines `{{define "_task_row"}}<tr id="task-{{.ID}}" …>…</tr>{{end}}` with the action buttons pointing at the row via `hx-target="closest tr" hx-swap="outerHTML"`. `tasks.html` invokes it for each task in its loop. The resulting initial page render is byte-identical in content to today's render modulo the new `id="task-{ID}"` attribute and updated per-row HTMX targets.

**Tech Stack:** Go 1.24, `html/template`.

**Scope:** Phase 2 of 6. Templates only — no Go handler changes. Requires Phase 1 (renderFragment) to be complete.

**Codebase verified:** 2026-04-13. `tasks.html` currently renders rows inline at lines 59–90 with the loop body `<tr data-status="{{.Status}}" {{if eq .Status "completed"}}style="display: none;"{{end}}>…`. No `id` attribute is present today. The Complete button at line 86 uses `hx-target="#main-content"`, which this phase changes. The bulk-action buttons at 43–44 also use `hx-target="#main-content"` — those remain unchanged in Phase 2 (Phase 3 will update them when the handlers learn to emit fragments).

---

## Acceptance Criteria Coverage

This phase implements and tests:

### htmx-fragment-mutations.AC3: Shared row templates
- **htmx-fragment-mutations.AC3.1:** `tasks.html` contains no inline `<tr>` markup for task rows; it invokes `{{template "_task_row" .}}` for each task in its loop.
- **htmx-fragment-mutations.AC3.3:** Rendering `_task_row` for a task via the new fragment-render path produces byte-identical HTML to the same task rendered inside `tasks.html`.

Explicit non-coverage in this phase: AC3.2 / AC3.4 are Phase 4 (file rows). AC1.* are Phase 3 (task mutation handlers). AC5.1–5.3 (no-regression) are continuously enforced but formally Phase 6.

---

<!-- START_TASK_1 -->
### Task 1: Create `_task_row.html` fragment

**Verifies:** htmx-fragment-mutations.AC3.1 (together with Task 2)

**Files:**
- Create: `internal/web/templates/_task_row.html`

**Implementation:**

The fragment defines a single named template block, `_task_row`, whose content is the `<tr>…</tr>` currently inline at `tasks.html:60–89`. Translate that markup into the define block with these mandatory changes:

1. Add `id="task-{{.ID}}"` as the first attribute on the `<tr>`. This is the stable DOM ID mutation responses will target.
2. Keep `data-status="{{.Status}}"` exactly as it is today so the client-side `toggleCompleted()` filter in `layout.html` continues to work unchanged.
3. Keep the `{{if eq .Status "completed"}}style="display: none;"{{end}}` inline style. (Client-side `toggleCompleted()` re-applies visibility on DOMContentLoaded + `htmx:afterSwap`; the server-rendered inline style is a belt-and-suspenders default for the initial render before JS executes.)
4. Change the Complete button's HTMX attributes. Today: `hx-post="/tasks/{{.ID}}/complete" hx-target="#main-content"`. After: `hx-post="/tasks/{{.ID}}/complete" hx-target="closest tr" hx-swap="outerHTML"`.
5. All other markup (checkbox, title, detail/links rendering with `hasPrefix`/`trimPrefix`/`taskLink`, status badge, timestamps) is preserved verbatim.

Do not add any script tags inside the fragment.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Builds cleanly. The template is embedded and parsed at `NewHandler` startup via the `templates/*.html` glob (verified during investigation); no glob change required.

**Commit:** `feat(web): extract _task_row fragment from tasks.html`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Refactor `tasks.html` to invoke `_task_row`

**Verifies:** htmx-fragment-mutations.AC3.1

**Files:**
- Modify: `internal/web/templates/tasks.html` — replace lines 60–89 (the inline `<tr>…</tr>` body inside `{{range .tasks}}`) with a single line: `{{template "_task_row" .}}`.

**Implementation:**

After this change the loop at tasks.html:59–90 reads:

```
{{range .tasks}}
{{template "_task_row" .}}
{{end}}
```

No other edits to `tasks.html` in this phase. The following leftover `hx-target="#main-content"` attributes are rewritten in later phases, each listed with its specific task:
- Create Task form (line 6) → rewritten by **Phase 3 Task 4** (uses `hx-target="#task-table tbody" hx-swap="afterbegin"`).
- Bulk Complete/Delete buttons (lines 43–44) → rewritten by **Phase 3 Task 5** (uses `hx-swap="none"` plus `hx-on::after-request` JS).
- Purge Completed form (line 94) → rewritten by **Phase 3 Task 6** (uses empty body plus client-side DOM sweep).

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: All existing tests pass, specifically `TestRoutes` and any handler tests that GET `/` — their body assertions use `strings.Contains` for task titles, which remain present in the rendered HTML.

Run the server locally and visit `/`:
- Tasks appear in the same order, same columns, same visual layout as before.
- Inspect element on a task row: the `<tr>` has a new `id="task-{ID}"` attribute.
- Click "Complete" on an incomplete task. The button posts to `/tasks/{ID}/complete` — in this phase the handler still re-renders the whole tab (Phase 3 changes that), so the swap result is a layout-less full-tab replacement. This is acceptable Phase-2 intermediate state; the test to confirm surgical row swap is in Phase 3.

**Commit:** `refactor(web): invoke _task_row fragment from tasks.html loop`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Identity test — full-tab render contains `_task_row` output

**Verifies:** htmx-fragment-mutations.AC3.3

**Files:**
- Modify: `internal/web/handler_test.go` — add a test named `TestTaskRowFragmentIdentity` that:
  1. Constructs a `*Handler` via `newTestHandler()` with a mocked task store containing one known task (e.g., `{ID: "abc", Title: "Fixture Row", Status: "needsAction"}`).
  2. Calls `h.renderFragment(w1, r, "_task_row", task)` for a recorder `w1`.
  3. Issues an HTMX GET to `/` (HX-Request: true) via the handler's mux into a second recorder `w2`, which renders the `tasks.html` fragment containing the same task inside `{{range .tasks}}{{template "_task_row" .}}{{end}}`.
  4. Asserts that `w2.Body` contains every `strings.Contains`-level substring that `w1.Body` contains: `id="task-abc"`, `data-status="needsAction"`, `Fixture Row`, and `hx-post="/tasks/abc/complete"`.

**Implementation:**

The test need not do a literal byte-diff (whitespace between templates may differ after Go's `html/template` normalization). Substring equivalence on all the load-bearing tokens above is sufficient to prove the two paths produce the same logical row. This matches the project's existing `strings.Contains` HTML-assertion pattern (verified during investigation).

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestTaskRowFragmentIdentity -v`
Expected: Passes.

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Full package green.

**Commit:** `test(web): verify _task_row fragment matches tasks.html render`
<!-- END_TASK_3 -->

---

## Phase Done When

- `internal/web/templates/_task_row.html` exists and defines `_task_row`.
- `tasks.html` loop body is `{{template "_task_row" .}}`.
- `TestTaskRowFragmentIdentity` passes; full `go test ./internal/web/` green.
- Manual browser check: Tasks tab renders identically; rows now have `id="task-{ID}"`.
- Three commits landed.
