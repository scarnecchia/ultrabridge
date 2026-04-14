# HTMX Fragment Mutations Design

## Summary

This design refactors the mutation response paths in UltraBridge's web layer so that HTMX-driven POST requests (task completions, file queuing, deletions, bulk actions, etc.) return only the minimal HTML needed to update the affected table row, rather than re-rendering the entire Tasks or Files tab. Today, when a user clicks "Complete" on a task, the server rebuilds and returns kilobytes of full-tab HTML including filter controls, counts, and every other row; after this change, it returns roughly 80-200 bytes of `<tr>` HTML for just that one row, and HTMX swaps it in place.

The mechanism works by introducing two new Go HTML template files, `_task_row.html` and `_file_row.html`, which define the row markup as named template blocks. Both the initial full-page render (which loops over all rows) and individual mutation responses invoke the same template block, guaranteeing the HTML is never duplicated or allowed to drift. A new `renderFragment` helper in `handler.go` executes these named blocks directly against a single row's data, bypassing the layout shell and the existing tab-level `HX-Request` branching. The existing five-second status pollers and all JavaScript-driven counter updates are explicitly left in place; no out-of-band (OOB) swap infrastructure is added.

## Definition of Done

- Clicking a task-list action (complete, bulk complete, bulk delete, create, purge) with HTMX enabled triggers a response containing only the affected row fragment(s), not a re-render of the entire Tasks tab.
- Clicking a file-list action (queue, skip, unskip, force, delete-note, delete-bulk, scan, import, retry-failed) with HTMX enabled triggers a response containing only the affected row fragment(s) or an empty body for deletions, not a re-render of the entire Files tab.
- Initial page render and HTMX-driven mutations produce the same row HTML by invoking a shared row-level template (`_task_row.html`, `_file_row.html`). No duplicated row markup between `tasks.html`/`files.html` and mutation responses.
- Non-HTMX (full page reload) behavior is unchanged: GETs return the full shell, POSTs redirect as before.
- Counts (bulk-action bar, global status, folder breadcrumbs) continue to update via the existing 5-second `/files/status` and `/sync/status` pollers. No out-of-band (OOB) swap infrastructure is introduced in this design.
- The Settings tab is out of scope for this design.

## Acceptance Criteria

### htmx-fragment-mutations.AC1: Task mutation responses are row-scoped
- **htmx-fragment-mutations.AC1.1 Success:** `POST /tasks/{id}/complete` with `HX-Request: true` returns a single `<tr id="task-{id}">` element and 200 OK.
- **htmx-fragment-mutations.AC1.2 Success:** `POST /tasks/{id}/complete` without `HX-Request` returns 303 redirect to `/` (backward compatible).
- **htmx-fragment-mutations.AC1.3 Success:** The returned row has `data-status="completed"` so the existing client-side `toggleCompleted()` filter continues to hide/show it correctly.
- **htmx-fragment-mutations.AC1.4 Success:** `POST /tasks/bulk` with `action=complete` returns one `<tr>` fragment per affected task, concatenated in the response body.
- **htmx-fragment-mutations.AC1.5 Success:** `POST /tasks/bulk` with `action=delete` returns an empty response body; the client-side buttons use `hx-swap="delete"` on each row's checkbox.
- **htmx-fragment-mutations.AC1.6 Success:** `POST /tasks` (create) with `HX-Request: true` returns a single new `<tr>` fragment for the created task, targeted at the task table's tbody with `hx-swap="afterbegin"`.
- **htmx-fragment-mutations.AC1.7 Success:** `POST /tasks/purge-completed` with `HX-Request: true` returns an empty body; the client removes all `[data-status="completed"]` rows (server tells client via a response header, or the client handles it via HTMX's extended events).
- **htmx-fragment-mutations.AC1.8 Failure:** `POST /tasks/{id}/complete` for a nonexistent task returns 404 (unchanged from current behavior).

### htmx-fragment-mutations.AC2: File mutation responses are row-scoped
- **htmx-fragment-mutations.AC2.1 Success:** `POST /files/queue`, `/files/skip`, `/files/unskip`, `/files/force` each return a single `<tr id="file-{sanitized-path}">` fragment for the affected file when `HX-Request: true`.
- **htmx-fragment-mutations.AC2.2 Success:** `POST /files/delete-note` with `HX-Request: true` returns an empty body and 200 OK; the button uses `hx-swap="delete" hx-target="closest tr"`.
- **htmx-fragment-mutations.AC2.3 Success:** `POST /files/delete-bulk` with `HX-Request: true` returns an empty body; client deletes selected rows via per-row `hx-swap`.
- **htmx-fragment-mutations.AC2.4 Success:** `POST /files/scan`, `/files/import`, `/files/retry-failed`, `/files/migrate-imports` are "broad" actions whose effect is visible only after the next poller tick; with `HX-Request: true` they return an empty body (or the unchanged current tbody, see phase-3 design decision) and rely on the 5s status poller to reflect the new state.
- **htmx-fragment-mutations.AC2.5 Success:** Non-HTMX variants still redirect to `/files` with the original query string (folder, page, sort preserved).
- **htmx-fragment-mutations.AC2.6 Failure:** Invalid `path` (fails `safeRelPath`) continues to return 400 with no rendering.

### htmx-fragment-mutations.AC3: Shared row templates
- **htmx-fragment-mutations.AC3.1:** `tasks.html` contains no inline `<tr>` markup for task rows; it invokes `{{template "_task_row" .}}` for each task in its loop.
- **htmx-fragment-mutations.AC3.2:** `files.html` contains no inline `<tr>` markup for file rows; it invokes `{{template "_file_row" .}}` for each file in its loop.
- **htmx-fragment-mutations.AC3.3:** Rendering `_task_row` for a task via the new fragment-render path produces byte-identical HTML to the same task rendered inside `tasks.html`.
- **htmx-fragment-mutations.AC3.4:** Same byte-identical guarantee holds for `_file_row` across initial and mutation paths.

### htmx-fragment-mutations.AC4: Fragment-render infrastructure
- **htmx-fragment-mutations.AC4.1:** A new `Handler` method (provisionally `renderFragment(w, r, name, data)`) renders a named sub-fragment (e.g., `_task_row`, `_file_row`) without the layout shell, regardless of `HX-Request`.
- **htmx-fragment-mutations.AC4.2:** The existing `renderTemplate` continues to branch on `HX-Request` for tab-level templates — its behavior is not regressed.
- **htmx-fragment-mutations.AC4.3:** Fragments are loaded from the same `embed.FS` as tab templates; no new filesystem reads.

### htmx-fragment-mutations.AC5: No regression
- **htmx-fragment-mutations.AC5.1:** All existing tests in `internal/web/` continue to pass.
- **htmx-fragment-mutations.AC5.2:** A non-HTMX `curl /` and `curl /files` still return the full HTML shell with navigation sidebar.
- **htmx-fragment-mutations.AC5.3:** Inline JavaScript in `layout.html` that depends on `#task-table`, `#files-table`, `.task-checkbox`, `.file-checkbox`, `#bulk-actions`, and `#bulk-delete-bar` continues to function after HTMX swaps.

### htmx-fragment-mutations.AC6: Explicit non-goals (scope boundary)
- **htmx-fragment-mutations.AC6.1:** The 5-second `/files/status` and `/sync/status` pollers are NOT converted to HTMX native polling or OOB swaps in this design.
- **htmx-fragment-mutations.AC6.2:** Bulk-action counters (`#selected-count`, `#bulk-delete-count`) continue to update via inline JavaScript. They are NOT emitted as OOB swaps from the server.
- **htmx-fragment-mutations.AC6.3:** The global status bar (`#global-status`) continues to update via `setInterval` + `fetch`. It is NOT emitted as OOB swaps.
- **htmx-fragment-mutations.AC6.4:** Chat and Logs tabs are untouched.
- **htmx-fragment-mutations.AC6.5:** Settings tab and its forms are untouched.

## Glossary

- **HTMX**: A JavaScript library that adds AJAX, CSS transitions, and partial-page updates to HTML elements via declarative attributes (`hx-post`, `hx-target`, `hx-swap`, etc.), without writing JavaScript for individual interactions.
- **Fragment**: In this document, a self-contained piece of server-rendered HTML representing a single table row (`<tr>...</tr>`), returned by a mutation endpoint instead of a full tab or page.
- **`hx-swap`**: An HTMX attribute that controls how the server response is inserted into the DOM. Values used here include `outerHTML` (replace the target element), `delete` (remove the target with no response body needed), and `afterbegin` (prepend inside the target).
- **`hx-target`**: An HTMX attribute that selects which DOM element to update with the server's response. `closest tr` means the nearest ancestor `<tr>`, scoping the swap to the row that initiated the request.
- **`HX-Request` header**: An HTTP request header set automatically by HTMX on every request it initiates. Handlers use its presence to distinguish an HTMX partial-update request from a traditional full-page browser request.
- **OOB (out-of-band) swap**: An HTMX mechanism (`hx-swap-oob`) that allows a single server response to update multiple, non-contiguous parts of the DOM simultaneously. This design explicitly avoids introducing OOB swaps.
- **`renderTemplate`**: The existing helper in `handler.go` that renders a named tab-level template, branching on `HX-Request` to emit either the full layout shell or just the tab's content block.
- **`renderFragment`**: The new helper introduced by this design that executes a named sub-template block (e.g., `_task_row`) directly against a single data value, always bypassing the layout shell and the `HX-Request` branch.
- **`embed.FS`** / **`go:embed`**: A Go standard library mechanism that compiles static files (here, `.html` templates) directly into the binary at build time. Fragments live in the same embedded filesystem as existing tab templates.
- **Service layer**: The package at `internal/service/` that encapsulates business logic (task completion, file queuing, etc.) and is called by HTTP handlers. Handlers use it to fetch the updated single-row record after a mutation before rendering the fragment.
- **`{{define}}`** / **`{{template}}`**: Go `html/template` directives. `{{define "_task_row"}}` declares a named block; `{{template "_task_row" .}}` invokes it, allowing the same markup to be called from both the full-tab loop and a standalone fragment response.
- **`htmx:afterSwap`**: A custom DOM event fired by HTMX after it completes a DOM update. The existing `toggleCompleted()` listener is wired to this event so client-side row filtering re-runs after each HTMX swap.
- **`hx-swap="afterbegin"`**: An HTMX swap mode that prepends the response as the first child of the target element; used for the task-create handler to insert the new row at the top of the table body.
- **`HX-Trigger` response header**: An HTMX mechanism allowing the server to emit a named client-side event via a response header, which JavaScript can listen for. Mentioned as one option for the purge-completed handler to instruct the client to remove all completed rows.
- **`safeRelPath`**: A server-side path validation function that rejects file path inputs not conforming to safe relative-path rules, used to guard file mutation endpoints against path traversal.

## Architecture

Mutation handlers in `internal/web/handler.go` currently follow a uniform pattern: mutate via the service layer, then `if HX-Request → call the GET handler → re-render entire tab fragment`. This design replaces the "re-render whole tab" branch with a per-row fragment response, so a task-completion click returns ~80 bytes of `<tr>` HTML instead of kilobytes of rebuilt tab.

The key architectural moves:

1. **Row-level templates become the unit of shared HTML.** `_task_row.html` and `_file_row.html` are introduced as Go template definitions (not standalone `ExecuteTemplate` targets, but `{{define "_task_row"}}...{{end}}` blocks) embedded alongside existing tab templates. `tasks.html` and `files.html` stop having inline `<tr>` markup and instead loop with `{{template "_task_row" .}}`. Any server code that wants to emit a single row invokes the same template, guaranteeing no drift.

2. **`renderTemplate` grows a sibling, `renderFragment`.** The existing helper (`handler.go:265`) stays as-is for tab-level rendering. A new method renders a named template block against a data value and writes it directly to the response, skipping the layout shell and the `HX-Request` branch. Mutation handlers call this new method when `HX-Request` is set.

3. **Mutation handlers split their response paths.** For HTMX requests they emit either (a) one fragment, (b) a concatenation of fragments (bulk), or (c) an empty body (deletions and broad actions). Non-HTMX paths continue to redirect.

4. **HTMX button attributes move from tab-wide targets to row-scoped targets.** Today all buttons use `hx-target="#main-content"` (and accept the wasteful full-tab swap). After this design, `_task_row`'s Complete button uses `hx-target="closest tr" hx-swap="outerHTML"`, the Delete button uses `hx-swap="delete"`, and so on. Create uses `hx-target="#task-table tbody" hx-swap="afterbegin"`.

5. **Client-side filtering and counting stay.** `toggleCompleted()` runs on `htmx:afterSwap` as well as `DOMContentLoaded` (layout.html:503 already wires this) and continues to work because rows still carry `data-status`. Counter updates continue via the existing `change` event listener on checkboxes, which fires after HTMX swaps because swapped elements inherit the event delegation.

Data flow for a task completion:

```
Click "Complete"
  → HTMX POST /tasks/{id}/complete
  → handleCompleteTask: h.tasks.Complete(ctx, id)
  → if HX-Request:
      t := h.tasks.Get(ctx, id)
      h.renderFragment(w, "_task_row", t)
  → HTMX replaces <tr id="task-{id}"> with response
  → htmx:afterSwap event fires
  → toggleCompleted() re-runs, hides the row if filter is active
```

No DB query for the full task list. No render of the bulk-action bar, filter controls, or purge button. ~1 SQL SELECT instead of ~2.

## Existing Patterns

Investigation of `internal/web/handler.go` (commit d441772) confirms:

- `renderTemplate(w, r, name, data)` at line 265 already implements the HX-Request branch for tab-level rendering. The parse-and-execute flow clones the root template, reads `templates/{name}.html` from `templateFS`, parses it as a `"content"` template, then executes either `"content"` (HTMX) or `"layout.html"` (full page). This design extends that pattern, not replaces it.
- Templates are embedded via `go:embed` into `templateFS`. The new row fragments will live alongside tab fragments (`templates/_task_row.html`, `templates/_file_row.html`) and be embedded the same way.
- Mutation handlers already uniformly check `HX-Request` (lines 399, 417, 430, 436, 467, 473, 479, 485, 491, 497, 504, 510, 529, 535, 541, 547, 580, 589) — this design keeps that branching structure but swaps the branch body.
- Service layer (`internal/service/`) already exposes the primitives needed: `tasks.Get(ctx, id)` for single-row fetches after mutation. For file mutations, `notes.Get(ctx, path)` or similar is needed; investigation in Phase 4 will confirm.
- Tests in `handler_test.go` use table-driven patterns with a mock service layer. New tests follow the same structure.

No divergence from existing patterns. This design is strictly additive: a new render helper, new template files, and refactored handler bodies.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Fragment-render infrastructure

**Goal:** Add the ability to render a named template block without the layout shell, so mutation handlers can emit row fragments.

**Components:**
- `renderFragment(w, r, name, data)` method in `internal/web/handler.go` — looks up an already-parsed template block by name, executes against `data`, writes response. Reuses the `h.tmpl` root. Does not branch on `HX-Request`; always emits fragment only.
- Root template parsing in `NewHandler` updated to include `templates/_*.html` files alongside existing tab templates, so block definitions register at startup.
- Unit test for `renderFragment` in `internal/web/handler_test.go` covering: fragment emits expected HTML against a test data struct, returns 200, emits correct Content-Type.

**Dependencies:** None (infrastructure).

**Covers ACs:** htmx-fragment-mutations.AC4.1, AC4.2, AC4.3.

**Done when:** Unit test for `renderFragment` passes; existing `renderTemplate` tests still pass; `go test ./internal/web/` green.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Task row fragment

**Goal:** Extract task-row HTML into a shared fragment and prove the pattern on the Tasks tab before tackling Files.

**Components:**
- `internal/web/templates/_task_row.html` — `{{define "_task_row"}}<tr id="task-{{.ID}}" data-status="{{.Status}}">...</tr>{{end}}`. Includes Complete button with `hx-post="/tasks/{{.ID}}/complete" hx-target="closest tr" hx-swap="outerHTML"`; Delete uses `hx-swap="delete"`.
- `internal/web/templates/tasks.html` refactored: `<tbody>` loop body replaced with `{{template "_task_row" .}}`.
- Visual/HTML regression test: assert that a full-tab render and a single-row fragment render produce the same `<tr>` HTML for the same task (per AC3.3).

**Dependencies:** Phase 1.

**Covers ACs:** htmx-fragment-mutations.AC3.1, AC3.3.

**Done when:** Initial page render of `/` looks unchanged in a browser; `go test ./internal/web/` green including new identity test.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: Task mutation handlers emit fragments

**Goal:** Rewrite all task-related POST handlers to return row fragments on HTMX requests.

**Components:**
- `handleCompleteTask` (handler.go:403): on HX-Request, fetch updated task and render `_task_row`.
- `handleCreateTask` (handler.go:378): on HX-Request, fetch created task and render `_task_row`; client-side button uses `hx-target="#task-table tbody" hx-swap="afterbegin"`.
- `handleBulkAction` (handler.go:421): on HX-Request with `action=complete`, render a `_task_row` for each affected ID, concatenated; with `action=delete`, return empty body.
- `handlePurgeCompleted` (handler.go:434): on HX-Request, return empty body with a response header (e.g., `HX-Trigger: purged-completed`) that client JS listens to for removing completed rows, OR simpler: button uses `hx-target="#task-table tbody" hx-swap="innerHTML"` with a server-rendered empty-or-non-completed tbody — design decision to make during implementation.
- Handler tests for each mutation, asserting HTMX response is fragment-only and non-HTMX response redirects.

**Dependencies:** Phase 2.

**Covers ACs:** htmx-fragment-mutations.AC1.1 through AC1.8.

**Done when:** All new handler tests pass; manual verification in browser confirms a task completion click produces a single-row swap (DevTools Network tab shows ~100-byte response); non-HTMX `curl -X POST` still returns 303.
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: File row fragment

**Goal:** Mirror Phase 2 for the Files tab — extract row HTML to `_file_row.html`, refactor `files.html` to use it.

**Components:**
- `internal/web/templates/_file_row.html` — handles both Supernote and Boox rows (current template has conditional rendering based on source badge, job status, etc.). Fragment includes the per-row action buttons with row-scoped HTMX targets.
- `internal/web/templates/files.html` refactored to invoke the fragment in its loop.
- Identity test: full-tab render and fragment render produce byte-identical HTML per file.

**Dependencies:** Phase 1 (infrastructure). Independent of Phase 3.

**Covers ACs:** htmx-fragment-mutations.AC3.2, AC3.4.

**Done when:** Initial render of `/files` unchanged in a browser across Supernote-only, Boox-only, and mixed configurations; tests green.
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: File mutation handlers emit fragments

**Goal:** Rewrite all file-related POST handlers to return row fragments or empty bodies on HTMX requests.

**Components:**
- Single-row mutations (`handleFilesQueue`, `handleFilesSkip`, `handleFilesUnskip`, `handleFilesForce`): fetch updated file record, render `_file_row`.
- Deletion mutations (`handleFilesDeleteNote`, `handleFilesDeleteBulk`): return empty body with 200 OK; buttons use `hx-swap="delete"`.
- Broad mutations (`handleFilesScan`, `handleFilesImport`, `handleFilesRetryFailed`, `handleFilesMigrateImports`, `handleProcessorStart`, `handleProcessorStop`): return empty body; rely on existing 5s poller for UI update (consistent with AC6 non-goals).
- New service-layer method if missing: `notes.GetFileRow(ctx, path)` returning the same struct used by the Files tab row rendering.
- Handler tests for each mutation variant.

**Dependencies:** Phase 4.

**Covers ACs:** htmx-fragment-mutations.AC2.1 through AC2.6.

**Done when:** All new handler tests pass; browser verification: queueing a file produces a ~200-byte row swap, deleting a note removes its row without full-tab rebuild, non-HTMX paths still redirect with preserved query strings.
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: Regression sweep and cleanup

**Goal:** Confirm no regression, document the pattern, remove dead code.

**Components:**
- Run the full `internal/web/` test suite and `go vet ./...`.
- Manual browser verification of Tasks and Files tabs: initial render, each mutation, non-HTMX via `curl`, DevTools Network-tab inspection to confirm fragment responses are small.
- Update `internal/web/CLAUDE.md` to document the fragment-render pattern, the `_*.html` template convention, and the DoD/scope boundary (so future work on Settings/Chat/Logs knows this pattern exists).
- Remove any now-dead code paths (e.g., if any full-tab re-render helper is now unused).

**Dependencies:** Phases 3 and 5.

**Covers ACs:** htmx-fragment-mutations.AC5.1, AC5.2, AC5.3.

**Done when:** All tests pass; manual checks confirm AC5; CLAUDE.md updated; commit ready.
<!-- END_PHASE_6 -->

## Additional Considerations

**Path as DOM ID:** File paths contain characters that are not valid in HTML `id` attributes (`/`, `.`, spaces). The implementation must sanitize paths to stable IDs — likely `file-{sha1(path)[:12]}` or a URL-encoded variant. The chosen scheme is an implementation detail; this design requires only that initial-render and mutation responses produce the same ID for the same path.

**Purge-completed UX:** `handlePurgeCompleted` is the trickiest handler because it affects an unbounded set of rows. Two viable shapes surfaced in the body; the final choice is deferred to Phase 3 implementation because both are shallow and the right answer depends on seeing how `htmx:afterSwap` interacts with the existing `toggleCompleted()` listener.

**Future work enabled by this design:** Once fragment rendering exists, converting the pollers to HTMX-native polling (`hx-trigger="every 5s"` pointed at a server-rendered `_global_status.html` fragment) becomes a one-template, one-handler change. That is explicitly out of scope here (AC6) but is the natural follow-up that completes Phase 3 of the decoupled-architecture roadmap.

**Settings tab:** Settings forms have a different interaction pattern (whole-form save, not per-row mutation). Applying the fragment pattern to Settings would require a different decomposition (`_auth_card.html`, `_rag_card.html`, etc.) and is deferred.
