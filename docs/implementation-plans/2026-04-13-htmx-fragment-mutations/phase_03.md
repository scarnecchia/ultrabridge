# HTMX Fragment Mutations — Phase 3: Task mutation handlers emit fragments

**Goal:** Rewrite all task-related POST handlers so HTMX requests get row-scoped HTML (or an empty body for bulk deletes and purge) instead of a full Tasks-tab re-render. Non-HTMX paths keep redirecting.

**Architecture:** Each HX-Request branch of a mutation handler fetches the affected task(s) via the service layer and emits `_task_row` fragments via `h.renderFragment`. Bulk delete and purge return empty bodies because there is no "updated row" to send — the client-side swap directives on the originating buttons handle DOM removal. Create is special-cased with `hx-swap="afterbegin"` so new rows prepend to the table body.

**Tech Stack:** Go 1.24, `html/template`, HTMX 2.x.

**Scope:** Phase 3 of 6. Requires Phases 1 and 2. No file-tab work here (Phases 4–5).

**Codebase verified:** 2026-04-13. Handler locations confirmed: `handleCompleteTask` at handler.go:403, `handleCreateTask` at 378, `handleBulkAction` at 421, `handlePurgeCompleted` at 434. TaskService (`internal/service/interfaces.go:86–94`) exposes `List`, `Create`, `Complete`, `Delete`, `PurgeCompleted`, `BulkComplete`, `BulkDelete` — **no `Get(ctx, id)` method exists yet**. The underlying TaskStore does have `Get`, so adding a thin wrapper on `taskService` is straightforward. `Create` already returns `(Task, error)` so Create handler does not need a follow-up fetch. `Complete`, `BulkComplete`, and the bulk variants return only `error`, so mutation handlers will need to call a new `Get(ctx, id)` to fetch the updated row.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### htmx-fragment-mutations.AC1: Task mutation responses are row-scoped
- **htmx-fragment-mutations.AC1.1 Success:** `POST /tasks/{id}/complete` with `HX-Request: true` returns a single `<tr id="task-{id}">` element and 200 OK.
- **htmx-fragment-mutations.AC1.2 Success:** `POST /tasks/{id}/complete` without `HX-Request` returns 303 redirect to `/` (backward compatible).
- **htmx-fragment-mutations.AC1.3 Success:** The returned row has `data-status="completed"` so the existing client-side `toggleCompleted()` filter continues to hide/show it correctly.
- **htmx-fragment-mutations.AC1.4 Success:** `POST /tasks/bulk` with `action=complete` returns one `<tr>` fragment per affected task, concatenated in the response body.
- **htmx-fragment-mutations.AC1.5 Success:** `POST /tasks/bulk` with `action=delete` returns an empty response body; the client-side buttons use `hx-swap="delete"` on each row's checkbox.
- **htmx-fragment-mutations.AC1.6 Success:** `POST /tasks` (create) with `HX-Request: true` returns a single new `<tr>` fragment for the created task, targeted at the task table's tbody with `hx-swap="afterbegin"`.
- **htmx-fragment-mutations.AC1.7 Success:** `POST /tasks/purge-completed` with `HX-Request: true` returns an empty body; the client removes all `[data-status="completed"]` rows (server tells client via a response header, or the client handles it via HTMX's extended events).
- **htmx-fragment-mutations.AC1.8 Failure:** `POST /tasks/{id}/complete` for a nonexistent task returns 404 (unchanged from current behavior).

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->

<!-- START_TASK_1 -->
### Task 1: Add `Get(ctx, id)` to `TaskService`

**Verifies:** Prerequisite for AC1.1, AC1.4 (no direct AC).

**Files:**
- Modify: `internal/service/interfaces.go` — add `Get(ctx context.Context, id string) (Task, error)` to the `TaskService` interface (declared at lines 86–94).
- Modify: `internal/service/task.go` — implement `Get` on the concrete `taskService` struct. The implementation wraps the underlying `TaskStore.Get(ctx, id)` (which already returns the internal task type) and maps to the service `Task` type via the existing `mapInternalTask` helper that `Create`/`List` already use.
- Modify: `internal/service/task_test.go` — add a unit test `TestTaskService_Get` covering:
  - Returns a populated `Task` with fields mapped correctly for a known ID.
  - Returns `sql.ErrNoRows` (or whatever sentinel the underlying `TaskStore.Get` returns — investigator confirmed this behavior exists; match it) when ID doesn't exist.

**Implementation:**

Follow the same structure as the existing `Create` method in `task.go`:

```go
func (s *taskService) Get(ctx context.Context, id string) (Task, error) {
    if s.store == nil {
        return Task{}, fmt.Errorf("task store not available")
    }
    t, err := s.store.Get(ctx, id)
    if err != nil {
        return Task{}, err
    }
    return mapInternalTask(*t), nil
}
```

(Exact type on `t` depends on what `TaskStore.Get` returns — the implementor should read `internal/service/task.go` and match the existing mapping pattern. If `mapInternalTask` takes a non-pointer `taskstore.Task` and `store.Get` returns `*taskstore.Task`, dereference as shown. If it's already a value type, drop the `*`.)

**Testing:**

Match the project's existing `task_test.go` conventions (table-driven tests with mock store). The test asserts behavior (returns correct Task, returns error for missing ID), not implementation (does not verify that `store.Get` was called a specific way).

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/service/ -run TestTaskService_Get -v`
Expected: Passes.

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Clean build — the web package will compile because nothing yet calls `Get`.

**Commit:** `feat(service): add TaskService.Get for single-task fetch`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Update `mockTaskStore` to satisfy the new interface

**Verifies:** Prerequisite (test infrastructure).

**Files:**
- Modify: `internal/web/handler_test.go` (or wherever `mockTaskStore` is declared — investigator noted lines 77–150 of `handler_test.go`, but if the mock is named differently for the service-layer tests, handle both).

**Implementation:**

The mocked task store used by web handler tests already has a `Get`-style accessor because it stores tasks in a map. If the test harness uses a mocked `TaskService` directly instead of a mocked `TaskStore`, add a `Get(ctx, id) (Task, error)` method to that mock that returns the matching entry from its in-memory map or an error if missing. Keep the mock nil-safe if other tests construct it via struct literals.

No production code changes.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: All existing tests still pass.

**Commit:** `test(web): update mock task store for TaskService.Get`
<!-- END_TASK_2 -->

<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-7) -->

<!-- START_TASK_3 -->
### Task 3: `handleCompleteTask` emits `_task_row` on HX-Request

**Verifies:** htmx-fragment-mutations.AC1.1, AC1.2, AC1.3, AC1.8

**Files:**
- Modify: `internal/web/handler.go:403–419` — replace the body of `handleCompleteTask`.

**Pre-step: Verify service-layer error passthrough.**

The rewrite preserves the existing `errors.Is(err, sql.ErrNoRows)` 404 path. That only works if `taskService.Complete` propagates `sql.ErrNoRows` untouched (no `fmt.Errorf("…: %w", err)` wrapping that obscures the sentinel — note that `%w` preserves `errors.Is` matching; only `%s`/`%v` or re-declared custom errors break it). Before writing the handler change, inspect `internal/service/task.go` `Complete` (and the underlying `TaskStore.Complete`) and confirm one of:
- Returns the store error directly (preserves `errors.Is` trivially), OR
- Wraps via `%w` (preserves `errors.Is` through `errors.Unwrap`), OR
- Re-declares as a non-wrapping error (would break AC1.8 — in which case add a service-level `ErrTaskNotFound` sentinel and update the handler to check for that instead).

If the third case holds, add a unit test in `internal/service/task_test.go` asserting that `errors.Is(err, sql.ErrNoRows)` (or the chosen sentinel) returns true when `Complete` is called with an unknown ID. This guards against future service-layer refactors quietly breaking the 404 contract.

**Implementation:**

New body structure:

```go
func (h *Handler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
    if h.tasks == nil {
        http.NotFound(w, r); return
    }
    taskID := r.PathValue("id")
    if err := h.tasks.Complete(r.Context(), taskID); err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            http.Error(w, "task not found", http.StatusNotFound); return
        }
        http.Error(w, "failed to complete task", http.StatusInternalServerError); return
    }
    if r.Header.Get("HX-Request") == "true" {
        t, err := h.tasks.Get(r.Context(), taskID)
        if err != nil {
            h.logger.Error("failed to fetch completed task for fragment render", "id", taskID, "error", err)
            http.Error(w, "failed to render row", http.StatusInternalServerError); return
        }
        h.renderFragment(w, r, "_task_row", t)
        return
    }
    http.Redirect(w, r, "/", http.StatusSeeOther)
}
```

(Exact error-sentinel and import handling as the file currently does — preserve the existing `errors.Is(err, sql.ErrNoRows)` check once the pre-step confirms it still works after Phase 3 Task 1 added `TaskService.Get`.)

**Testing:**

Add tests to `internal/web/handler_test.go`:
- **AC1.1 + AC1.3:** HX-Request POST to `/tasks/{id}/complete` returns 200 with body containing `id="task-{id}"` and `data-status="completed"`. Uses `strings.Contains` per project convention.
- **AC1.2:** non-HTMX POST returns 303 with `Location: /`.
- **AC1.8:** HX-Request POST for a nonexistent task returns 404.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleCompleteTask -v`
Expected: All three subtests pass.

**Commit:** `feat(web): handleCompleteTask emits _task_row fragment on HX-Request`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: `handleCreateTask` emits `_task_row` on HX-Request

**Verifies:** htmx-fragment-mutations.AC1.6

**Files:**
- Modify: `internal/web/handler.go:378–401`.
- Modify: `internal/web/templates/tasks.html:6` — the create form today uses `hx-target="#main-content"`. Change to `hx-target="#task-table tbody" hx-swap="afterbegin"` so the server's new `<tr>` response is prepended to the table body. Also add `hx-on::after-request="if (event.detail.successful) this.reset()"` so the form clears only on 2xx — validation errors (400/500) preserve the user's input. `event.detail.successful` is HTMX's boolean for whether the response was a success status code.

**Implementation:**

Creation already returns `(Task, error)` from `h.tasks.Create`, so the handler does not need a follow-up `Get`. On `HX-Request: true`, call `h.renderFragment(w, r, "_task_row", created)`.

Preserve the existing validation (title required, dueAt parsing) and HTTP error responses. These already return before the HX-Request branch.

**Testing:**

- **AC1.6:** HX-Request POST to `/tasks` with valid title returns 200 with body containing `id="task-{newID}"` and the submitted title. Non-HTMX returns 303.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleCreateTask -v`
Expected: Pass.

Manual browser check: create a task, observe the new row appear at the top of the table without a full-tab reload; form input clears.

**Commit:** `feat(web): handleCreateTask emits _task_row fragment on HX-Request`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: `handleBulkAction` emits fragments or empty body

**Verifies:** htmx-fragment-mutations.AC1.4, AC1.5

**Files:**
- Modify: `internal/web/handler.go:421–432`.
- Modify: `internal/web/templates/tasks.html:43–44` — the two bulk-action buttons. Replace `hx-target="#main-content"` on each with an explicit no-swap strategy that processes the response in client-side JS. HTMX's native multi-target capability is `hx-swap-oob`, which the design explicitly forbids (AC6.2), so we handle multi-row updates manually:

  - **Complete Selected:**
    ```
    hx-post="/tasks/bulk"
    hx-include=".task-checkbox:checked"
    hx-vals='{"action": "complete"}'
    hx-swap="none"
    hx-on::after-request="if(!event.detail.successful) return;
      const parser = new DOMParser();
      const doc = parser.parseFromString(event.detail.xhr.responseText, 'text/html');
      doc.querySelectorAll('tr[id^=&quot;task-&quot;]').forEach(newRow => {
        const existing = document.getElementById(newRow.id);
        if (existing) existing.replaceWith(newRow);
      });
      updateBulkActions();"
    ```
    The server emits one `<tr id="task-X">` per affected task (concatenated). Client-side JS parses those out of the response body and replaces the corresponding existing rows in-place via `replaceWith`. This achieves the semantics of OOB swaps without using OOB: the response is still "the affected rows," but the dispatch happens in the browser.

  - **Delete Selected:**
    ```
    hx-post="/tasks/bulk"
    hx-include=".task-checkbox:checked"
    hx-vals='{"action": "delete"}'
    hx-swap="none"
    hx-on::after-request="if(!event.detail.successful) return;
      document.querySelectorAll('.task-checkbox:checked').forEach(cb => cb.closest('tr').remove());
      updateBulkActions();"
    ```
    Server returns empty body. Client removes the rows whose checkboxes are checked. The `if(!event.detail.successful) return` guard prevents row removal on server error.

**Note on AC1.5 wording (design deviation):** The design says "client-side buttons use `hx-swap=\"delete\"` on each row's checkbox." `hx-swap="delete"` requires one target per swap, but bulk delete has many targets from a single request. The JS `.remove()` sweep above is the closest equivalent within the no-OOB constraint. This is an acceptable deviation from the design's literal wording; the spirit of AC1.5 (empty response body + client removal of selected rows) is preserved.

**Implementation:**

Handler behavior on HX-Request:
- `action=complete`: loop over affected IDs, fetch each via `h.tasks.Get`, render each via `h.renderFragment(w, r, "_task_row", t)` in sequence to the same `w`. HTMX concatenates these in the response body.
- `action=delete`: no body; set `w.WriteHeader(http.StatusOK)` and return.

Non-HTMX: keep the existing `http.Redirect(w, r, "/", http.StatusSeeOther)`.

**Testing:**

- **AC1.4:** HX-Request POST `/tasks/bulk` with `action=complete&task_ids=a&task_ids=b` returns 200 with body containing `id="task-a"` (via `strings.Contains`), `id="task-b"` (via `strings.Contains`), AND `strings.Count(body, "data-status=\"completed\"") == 2`. Note: use `strings.Count` not `strings.Contains` when asserting a specific occurrence count — `Contains` only reports presence.
- **AC1.5:** HX-Request POST `/tasks/bulk` with `action=delete` returns 200 with empty body.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandleBulkAction -v`

Manual browser check: select multiple tasks, click Complete Selected — rows switch to the completed badge without tab reload. Click Delete Selected — rows vanish.

**Commit:** `feat(web): handleBulkAction returns row fragments or empty body on HX-Request`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: `handlePurgeCompleted` emits empty body + client-side sweep

**Verifies:** htmx-fragment-mutations.AC1.7

**Files:**
- Modify: `internal/web/handler.go:434–438`.
- Modify: `internal/web/templates/tasks.html:94` — the purge form. Add `hx-on::after-request="document.querySelectorAll('#task-table tbody tr[data-status=\"completed\"]').forEach(r => r.remove());"` and remove the `hx-target="#main-content"`.

**Implementation:**

Handler on HX-Request: call `h.tasks.PurgeCompleted(ctx)`, then `w.WriteHeader(http.StatusOK)`. Non-HTMX: existing redirect to `/`.

The client-side sweep is simpler and more robust than emitting an `HX-Trigger` response header, given the existing client-side filter state. If a reader prefers `HX-Trigger`: either approach meets AC1.7; pick the one above for minimum client code surface.

**Testing:**

- **AC1.7:** HX-Request POST `/tasks/purge-completed` returns 200 with empty body. (Client-side DOM sweep is not unit-testable in Go — verify via manual browser check.)

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestHandlePurgeCompleted -v`

Manual browser check: complete one or more tasks (they remain hidden by default filter), toggle "Show completed" on, click Purge Completed — confirmed-completed rows disappear without full tab reload.

**Commit:** `feat(web): handlePurgeCompleted returns empty body; client sweeps completed rows`
<!-- END_TASK_6 -->

<!-- END_SUBCOMPONENT_B -->

<!-- START_TASK_7 -->
### Task 7: Remove now-unused full-tab re-render call paths

**Verifies:** Housekeeping for htmx-fragment-mutations.AC5 (no-regression enforced in Phase 6).

**Files:**
- Inspect: `internal/web/handler.go` — after Tasks 3–6 the HX-Request branches no longer call `h.handleIndex(w, r)`. Remove any now-unused imports or helper calls. Confirm via `go vet ./...` and `go build ./...`.

**Implementation:**

Purely subtractive. No behavior changes.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge vet ./...`
Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Both clean.

**Commit:** `chore(web): drop unused full-tab re-render paths in task handlers`
<!-- END_TASK_7 -->

---

## Phase Done When

- `TaskService.Get` exists and is covered by a unit test.
- `handleCompleteTask`, `handleCreateTask`, `handleBulkAction`, `handlePurgeCompleted` all emit fragment-scoped responses on HX-Request and redirect otherwise.
- New handler tests cover AC1.1–AC1.8.
- `go test ./... && go vet ./...` green.
- Manual browser verification of a task-completion click shows a ~100-byte row swap in DevTools Network tab (not a full-tab response).
- Seven commits landed.
