# HTMX Fragment Mutations — Phase 5: File mutation handlers emit fragments

**Goal:** Rewrite all file-related POST handlers so HTMX requests get either the affected `_file_row` or an empty body. Non-HTMX paths keep redirecting with preserved query strings.

**Architecture:** Single-row mutations (queue, skip, unskip, force) fetch the updated file via a new `NoteService.GetFile` method and render `_file_row`. Deletion mutations return empty bodies and rely on client-side `hx-swap="delete"` to remove rows. Broad mutations (scan, import, retry-failed, migrate-imports, processor start/stop) return empty bodies and defer UI update to the existing 5-second `/files/status` poller — this is the explicit non-goal recorded as AC6 in the design.

**Tech Stack:** Go 1.24, `html/template`, HTMX 2.x.

**Scope:** Phase 5 of 6. Requires Phases 1 and 4. Independent of Phases 2 and 3.

**Codebase verified:** 2026-04-13 (re-verified against handler.go on branch `arch/decoupled-architecture` at commit d441772 after initial draft had stale line numbers from an older revision). Actual handler locations:

| Handler | Line |
|---|---|
| `handleProcessorStart` | 465 |
| `handleProcessorStop` | 471 |
| `handleFilesScan` | 477 |
| `handleFilesImport` | 483 |
| `handleFilesRetryFailed` | 489 |
| `handleFilesDeleteNote` | 495 |
| `handleFilesDeleteBulk` | 501 |
| `handleFilesMigrateImports` | 508 |
| `handleFilesQueue` | 527 |
| `handleFilesSkip` | 533 |
| `handleFilesUnskip` | 539 |
| `handleFilesForce` | 545 |

`NoteService` (`internal/service/interfaces.go:96–124`) does NOT have a `GetFile(ctx, path) (NoteFile, error)` method — `GetNoteDetails` returns `interface{}` and is unsuitable for row rendering. This phase adds the missing service method as a prerequisite. `NoteService.ListFiles` returns `([]NoteFile, int, error)`; the underlying store likely has a single-file lookup, but the cheapest and safest approach is to add a new typed `GetFile` method that wraps whatever the store exposes.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### htmx-fragment-mutations.AC2: File mutation responses are row-scoped
- **htmx-fragment-mutations.AC2.1 Success:** `POST /files/queue`, `/files/skip`, `/files/unskip`, `/files/force` each return a single `<tr id="file-{sanitized-path}">` fragment for the affected file when `HX-Request: true`.
- **htmx-fragment-mutations.AC2.2 Success:** `POST /files/delete-note` with `HX-Request: true` returns an empty body and 200 OK; the button uses `hx-swap="delete" hx-target="closest tr"`.
- **htmx-fragment-mutations.AC2.3 Success:** `POST /files/delete-bulk` with `HX-Request: true` returns an empty body; client deletes selected rows via per-row `hx-swap`.
- **htmx-fragment-mutations.AC2.4 Success:** `POST /files/scan`, `/files/import`, `/files/retry-failed`, `/files/migrate-imports` are "broad" actions whose effect is visible only after the next poller tick; with `HX-Request: true` they return an empty body (or the unchanged current tbody, see phase-3 design decision) and rely on the 5s status poller to reflect the new state.
- **htmx-fragment-mutations.AC2.5 Success:** Non-HTMX variants still redirect to `/files` with the original query string (folder, page, sort preserved).
- **htmx-fragment-mutations.AC2.6 Failure:** Invalid `path` (fails `safeRelPath`) continues to return 400 with no rendering.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
### Task 1: Add `GetFile(ctx, path)` to `NoteService`

**Verifies:** Prerequisite for AC2.1.

**Files:**
- Modify: `internal/service/interfaces.go` — add `GetFile(ctx context.Context, path string) (NoteFile, error)` to `NoteService` (lines 96–124). Place near `ListFiles` for readability.
- Modify: `internal/service/note.go` — implement `GetFile` on the concrete `noteService` struct. Use the underlying store method the investigator identifies; if no direct single-file store method exists, call the existing `ListFiles`-equivalent on the store with an exact-path filter or iterate (prefer a direct lookup if the store offers it). Map the store type to service `NoteFile` using the existing `mapBooxFile` / `mapSupernoteFile` helpers (note.go uses these in `ListFiles`).
- Modify: `internal/service/note_test.go` — add `TestNoteService_GetFile` covering:
  - Returns a populated `NoteFile` with `Source`, `JobStatus`, `Name`, `Path` populated for a known path.
  - Returns `sql.ErrNoRows` (or the store's not-found sentinel — match it) when path doesn't exist.
  - Both Supernote and Boox code paths covered.

**Implementation:**

Before writing the implementation, the task-implementor must inspect `internal/service/note.go` to see how `ListFiles` fetches and maps data today. The new `GetFile` follows the same pattern but for one file. If the underlying stores (`notes.Store` for Supernote, `booxpipeline.Store` for Boox) expose single-file getters, prefer those. If they don't, a filtered `ListFiles` call with `perPage=1` and a path match is acceptable.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/service/ -run TestNoteService_GetFile -v`
Expected: Passes.

**Commit:** `feat(service): add NoteService.GetFile for single-file fetch`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Update mock note store for `GetFile`

**Verifies:** Prerequisite (test infrastructure).

**Files:**
- Modify: `internal/web/handler_test.go` — `mockNoteStore` (investigator: lines 153–199) — add `GetFile` returning the matching entry from its in-memory map or an error.

**Implementation:**

Pure test helper. No production code changes. Keep the mock nil-safe and consistent with the existing pattern.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: All existing tests still pass.

**Commit:** `test(web): update mockNoteStore for NoteService.GetFile`
<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-5) -->

<!-- START_TASK_3 -->
### Task 3: Single-row file mutations emit `_file_row`

**Verifies:** htmx-fragment-mutations.AC2.1, AC2.5, AC2.6

**Files:**
- Modify: `internal/web/handler.go:527` (`handleFilesQueue`), `:533` (`handleFilesSkip`), `:539` (`handleFilesUnskip`), `:545` (`handleFilesForce`). All four share the same shape today (mutate, then on HX-Request re-render the Files tab, else redirect with preserved back query).

**Implementation:**

New shape (illustrated for `handleFilesQueue`; the others are identical modulo method name):

```go
func (h *Handler) handleFilesQueue(w http.ResponseWriter, r *http.Request) {
    path, ok := safeRelPath(r.FormValue("path"))
    if !ok { http.Error(w, "invalid path", http.StatusBadRequest); return }
    if err := h.notes.Enqueue(r.Context(), path, false); err != nil {
        http.Error(w, "failed to enqueue", http.StatusInternalServerError); return
    }
    if r.Header.Get("HX-Request") == "true" {
        f, err := h.notes.GetFile(r.Context(), path)
        if err != nil {
            h.logger.Error("failed to fetch file for fragment render", "path", path, "error", err)
            http.Error(w, "failed to render row", http.StatusInternalServerError); return
        }
        h.renderFragment(w, r, "_file_row", fileRowCtx{File: f, RelPath: r.FormValue("back")})
        return
    }
    http.Redirect(w, r, "/files?path="+url.QueryEscape(r.FormValue("back")), http.StatusSeeOther)
}
```

Apply the same pattern to Skip, Unskip, Force.

**On `safeRelPath` (AC2.6 scope clarification):** Today's `handleFilesQueue` (handler.go:527–531) accepts `path` via `FormValue` and does NOT call `safeRelPath` before calling `h.notes.Enqueue`. The validation likely happens deeper in the notestore layer — the investigator should confirm. AC2.6 in the design reads: "Invalid `path` (fails `safeRelPath`) continues to return 400 with no rendering." The word *continues* implies existing behavior — this phase does NOT need to add `safeRelPath` as a new gate.

**Chosen approach:** Preserve current validation behavior. Do NOT add `safeRelPath` calls to the four mutation handlers in this task. Whatever error the service layer currently returns on an invalid path becomes a 500 (same as today). If a test reveals that the current behavior does NOT return 400 for `path=../escape`, open a separate ticket — do not fix in this phase. The AC2.6 test case should mirror current behavior: whatever the existing `handleFilesQueue` returns for an invalid path today is what the rewritten handler must also return.

If the implementor finds that current behavior silently succeeds on path traversal (a real security concern), surface it to the human operator before proceeding; that is a bug fix deserving its own commit and test, not a silent inclusion in this refactor.

**Testing:**

Add tests to `handler_test.go` for each of the four handlers. For `handleFilesQueue` specifically:
- **AC2.1:** HX-Request POST returns 200 with body containing `id="{fileRowID(path)}"` and the expected `JobStatus` reflecting the post-enqueue state (e.g., `badge-pending` for queued).
- **AC2.5:** Non-HTMX POST returns 303 with `Location: /files?path={escaped-back}`.
- **AC2.6:** POST with `path=../escape` returns 400.

Repeat for Skip, Unskip, Force (varying the expected post-mutation JobStatus).

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleFiles -v`
Expected: All four handler tests pass.

**Commit:** `feat(web): single-row file handlers emit _file_row fragment on HX-Request`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Deletion mutations return empty body

**Verifies:** htmx-fragment-mutations.AC2.2, AC2.3

**Files:**
- Modify: `internal/web/handler.go:495` (`handleFilesDeleteNote`) and `:501` (`handleFilesDeleteBulk`).
- Modify: `internal/web/templates/_file_row.html` — the Details button (line 109 in original files.html, now in the fragment) opens the history modal, which in turn has a delete form. That form already lives in `layout.html:460`. Update the form's HTMX attributes there so the delete emits empty body + `hx-target="#{fileRowID path}" hx-swap="delete"`.
- Modify: `internal/web/templates/files.html` — the bulk-delete `<form>` at line 49 and its submit button at 119–121. Change: the form no longer needs `hx-target`; the submit button adds `hx-on::after-request="document.querySelectorAll('.file-checkbox:checked').forEach(cb => cb.closest('tr').remove()); updateBulkDeleteBar();"` to sweep the checked rows after a successful response.

**Implementation:**

Handler on HX-Request for `handleFilesDeleteNote`: call `h.notes.DeleteNote(ctx, path)`, then `w.WriteHeader(http.StatusOK)` and return. Non-HTMX: existing redirect.

Handler on HX-Request for `handleFilesDeleteBulk`: call `h.notes.BulkDelete(ctx, paths)`, then empty 200. Non-HTMX: existing redirect.

**Partial-failure semantics (carrying forward existing behavior):** If `h.notes.BulkDelete` succeeds for some paths and fails for others, the current implementation returns the aggregate error and the client receives 500 — rows remain in the DOM. The new empty-200 path has the same behavior when fully successful and the same 500 behavior when it errors. If `BulkDelete` is changed in the future to support partial success (return a list of successfully-deleted paths), this phase's client-side sweep (`.task-checkbox:checked` → remove) would need to become "remove only the rows the server confirmed." That is out of scope for this design. Today's behavior is all-or-nothing and this phase preserves that.

**Design deviation note:** Design AC2.3 says "client deletes selected rows via per-row `hx-swap`". Per-row `hx-swap` on a single-request bulk action is not expressible without OOB (forbidden by AC6.2), so this phase uses `hx-on::after-request` JS sweep instead. Same spirit, different mechanism.

The layout.html delete form is inside the history modal, which is opened via JavaScript (`showHistory()`). The form's `hx-target` needs to resolve dynamically — a hard-coded `#{fileRowID}` won't work because the ID depends on the path currently displayed in the modal. Two workable approaches:
- (a) The `showHistory` JS function, when opening the modal, updates the form's `hx-target` attribute to point at the correct row ID via `document.getElementById('delete-note-form').setAttribute('hx-target', '#' + hashPath(path))`. `hashPath` is a small JS SHA-1 helper or a `fetch('/api/v1/file-id?path=...')` call. Simpler: include the row's ID as a `data-row-id` attribute on the row (already true: `id="{{fileRowID}}"` → the JS reads from the row), and `showHistory` stashes it on the form.
- (b) The delete form emits no body and uses `hx-on::after-request` to remove the row element whose path matches. Requires the form to know the current path (it already does via the hidden input at layout.html:461).

**Recommendation:** (b) — keep the form decoupled from ID generation, use client-side code to find and remove the row. The `hx-on::after-request` reads the hidden `path` input, computes/finds the matching row via `document.querySelector(`tr[id="${fileRowID(path)}"]`)`, and removes it. Since `fileRowID` is server-side (Go), a small JS equivalent is needed. Easier: have the server set the form's target via a `hx-target` attribute stamped by `showHistory` using the target row's `id` (read from the row the user clicked — the "Details" button already knows which row it's in, just propagate that ID into `showHistory`'s argument list).

Pick whichever the implementor finds cleaner; both satisfy AC2.2.

**Testing:**

- **AC2.2:** HX-Request POST `/files/delete-note?path=...` returns 200 with empty body. (DOM sweep itself is JS-only; manual verification.)
- **AC2.3:** HX-Request POST `/files/delete-bulk` with multiple `paths` form values returns 200 with empty body.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleFilesDelete -v`
Expected: Tests pass.

Manual browser check: open details on a file, click Delete — modal closes, row vanishes without tab reload. Select multiple files, click Delete Selected — selected rows vanish.

**Commit:** `feat(web): deletion handlers return empty body; client sweeps rows`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Broad mutations return empty body, rely on poller

**Verifies:** htmx-fragment-mutations.AC2.4

**Files:**
- Modify: `internal/web/handler.go:489` (`handleFilesRetryFailed`), `:477` (`handleFilesScan`), `:483` (`handleFilesImport`), `:508` (`handleFilesMigrateImports`), `:465` (`handleProcessorStart`), `:471` (`handleProcessorStop`).
- Modify: `internal/web/templates/files.html:12–31` — update the toolbar forms: remove `hx-target="#main-content"` (no target needed) and add a subtle visual confirmation via `hx-on::after-request="updateProcessorStatus();"` (already a function in layout.html) so the poller refreshes immediately rather than waiting the full 5s.

**Implementation:**

Each broad handler: perform its mutation via the service layer, then:

```go
if r.Header.Get("HX-Request") == "true" {
    w.WriteHeader(http.StatusOK)
    return
}
http.Redirect(w, r, "/files", http.StatusSeeOther)
```

No fragment rendering. No row fetch. The existing 5-second `updateProcessorStatus` poller in layout.html already reflects queue/processing state changes, and the new `hx-on::after-request="updateProcessorStatus()"` forces an immediate refresh so users see the impact of their click right away.

**Testing:**

One parameterized test (`TestHandleBroadFileMutations`) covering all six handlers with a common pattern: HX-Request POST returns 200 with empty body; non-HTMX POST returns 303 redirect.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleBroadFileMutations -v`
Expected: All six cases pass.

Manual browser check: clicking Scan Now / Start / Stop no longer full-tab-reloads; the status bar refreshes within ~1s.

**Commit:** `feat(web): broad file handlers return empty body; poller refreshes immediately`
<!-- END_TASK_5 -->

<!-- END_SUBCOMPONENT_B -->

<!-- START_TASK_6 -->
### Task 6: Remove unused full-tab re-render paths

**Verifies:** Housekeeping.

**Files:**
- Inspect: `internal/web/handler.go` — confirm no HX-Request branch in a file-mutation handler still calls `h.handleFiles(w, r)`. Remove any helpers that exist solely to support the old pattern.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge vet ./...`
Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Both clean.

**Commit:** `chore(web): drop unused full-tab re-render paths in file handlers`
<!-- END_TASK_6 -->

---

## Phase Done When

- `NoteService.GetFile` exists and is covered by a unit test.
- All 11 file mutation handlers (queue, skip, unskip, force, delete-note, delete-bulk, scan, import, retry-failed, migrate-imports, processor start/stop) emit fragment-scoped responses on HX-Request per AC2.
- Button `hx-target` attributes in `_file_row.html` and `files.html` point at rows or nothing (never `#main-content`) for mutation actions.
- New handler tests cover AC2.1 through AC2.6.
- `go test ./... && go vet ./...` green.
- Manual browser verification: queueing a file produces a ~200-byte row swap; Scan Now refreshes the status bar without tab reload; preserves query strings on non-HTMX redirects.
- Six commits landed.
