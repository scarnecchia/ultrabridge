# HTMX Fragment Mutations — Human Test Plan

Generated from the test-analyst pass on 2026-04-13. This plan covers the
acceptance criteria that require human-observable behavior in a browser —
DOM-level JS listeners, HTMX swaps, poller refreshes, and layout vs
fragment distinction. All other ACs are covered by automated tests; see
the traceability table at the bottom.

## Verification progress

Steps verified in the 2026-04-13 live session (either via Playwright
during implementation or by the human operator post-merge):

- **A2, A4** — per-row task complete, task create (Playwright + operator)
- **B1, B2, B3** — bulk task select, bulk complete, bulk delete (Playwright)
- **C2** — file Queue on a `done` file (Playwright)
- **D2** — file checkbox → bulk-delete bar shows/hides (Playwright)
- **E1–E3** — curl layout vs fragment assertions (curl)
- **F1, F2, F4** — Tasks + Files checkbox bars, clean console (Playwright)
- **A5** — reload after create persists the task (operator)
- **F3** — processor badge continues updating every ~5s (operator)

Outstanding: A6, C3–C6, D1, D3–D6, destructive items (B4/B5/D1/D3),
and both end-to-end scenarios.

## Prerequisites

- UltraBridge running locally at `http://localhost:8443/` (Basic Auth
  credentials available).
- `go test -C /home/sysop/src/ultrabridge ./internal/web/` reports `ok`
  (expected PASS at HEAD `85d22b8`).
- A test browser (Chrome). Open DevTools Console and Network panels
  for every step.
- Test data:
  - At least 3 tasks: 1 in `needsAction`, 1 in `completed`, 1 you
    will create during the test.
  - At least 3 files visible under `/files`: 1 with job status
    `done`, 1 with `unprocessed`, 1 reachable via subdirectory
    navigation. Have a Boox note available too if a Boox source is
    configured (sourcing optional but exercises AC3.4 Boox branch
    live).
- Note the existing 5-second `/files/status` poller: any "downstream
  UI refresh" expectations rely on this poller, not on the mutation
  response itself.

## Phase A: Tasks tab — single-row mutations

| Step | Action | Expected |
|------|--------|----------|
| A1 | Navigate to `http://localhost:8443/`. Open DevTools Console. | Tasks list renders. Console clean. Sidebar `<nav class="sidebar">` visible. |
| A2 | In Network panel, filter for `complete`. Click the Complete button on a `needsAction` task (call it `T-A`). | Single XHR `POST /tasks/T-A/complete`, 200 OK. Response Preview shows a single `<tr id="task-T-A" data-status="completed" …>`, no `<nav class="sidebar">`. The row in the DOM updates in place (no full-tab reload — URL and scroll position unchanged; no flash of layout). |
| A3 | If "Show completed" is off (default), the row from A2 should be hidden via `toggleCompleted()`. Toggle it on → row reappears with completed styling. | The newly-completed row from A2 is controlled by `toggleCompleted()`. Confirms DOM listener still functions after fragment swap (AC1.3 human portion). |
| A4 | Type a new task title `Manual smoke A4` into the create form and submit. | XHR `POST /tasks` 200 OK. Response is a single `<tr id="task-…">` containing `Manual smoke A4`. New row appears at the **top** of the table (`hx-swap="afterbegin"` on `#task-table tbody`). Form input clears. |
| A5 | Reload the page (`Cmd/Ctrl+R`). | Created task persists; its position depends on default sort, but it must still exist (verifies the create wasn't merely DOM-only). |
| A6 | Submit the create form with an empty title. | XHR `POST /tasks` returns 400. Form input is **not** cleared. No new row appears. |

## Phase B: Tasks tab — bulk mutations

| Step | Action | Expected |
|------|--------|----------|
| B1 | Select two `needsAction` tasks (`T-B1`, `T-B2`) via checkboxes. | A bulk-actions toolbar appears showing "2 selected" (verifies `updateBulkActions()` runs on checkbox change). |
| B2 | Click "Complete Selected" in the bulk toolbar. | XHR `POST /tasks/bulk` body contains `action=complete&task_ids=T-B1&task_ids=T-B2`. Response 200, body contains exactly two `<tr>` fragments with `data-status="completed"`. Both rows update in place; bulk toolbar resets to "0 selected". |
| B3 | Select two tasks. Click "Delete Selected" in the bulk toolbar (confirm any prompt). | XHR `POST /tasks/bulk` body contains `action=delete`. Response 200 with **empty body**. The two selected `<tr>` rows are removed from the DOM by `hx-on:htmx:after-request` sweep. Bulk toolbar resets to "0 selected". (AC1.5 human portion.) |
| B4 | With at least one `completed` task present, click "Purge Completed". | XHR `POST /tasks/purge-completed`, 200 OK, **empty body**. All rows with `data-status="completed"` disappear from the DOM. No full-tab reload. (AC1.7 human portion.) |
| B5 | Reload the page. | Purged tasks remain absent (soft-deleted in DB). |

## Phase C: Files tab — single-row mutations

| Step | Action | Expected |
|------|--------|----------|
| C1 | Navigate to `/files`. Console clean. Sidebar visible. | File table renders with badges. |
| C2 | On a file with `done` status, click "Queue". | XHR `POST /files/queue` 200, response body is a single `<tr id="file-<12-hex-chars>" …>` containing the `badge-pending` class. Row updates in place. |
| C3 | On a file with no job, click "Skip". | XHR `POST /files/skip` 200, response is a single `<tr>` with `badge-skipped` class. Row updates in place. |
| C4 | On a file currently `skipped`, click "Unskip". | XHR `POST /files/unskip` 200, response `<tr>` now shows `badge-unprocessed`. |
| C5 | On a file with `done` status, click "Force". | XHR `POST /files/force` 200, response `<tr>` now shows `badge-pending`. |
| C6 | Non-HX redirect path check: `curl -i -u user:pass -d "path=<some-file>&back=subdir" http://localhost:8443/files/queue`. | `HTTP/1.1 303 See Other` with `Location: /files?path=subdir`. |

## Phase D: Files tab — delete and broad mutations

| Step | Action | Expected |
|------|--------|----------|
| D1 | Click "Details" on a file row to open the history modal. Click Delete; confirm. | XHR `POST /files/delete-note` 200, **empty body**. The target row is removed from the DOM (modal JS resolves path → `file-<hash>` id and removes it). Modal closes. No full-tab reload. (AC2.2 human portion.) |
| D2 | Select two files via checkboxes. | "Bulk delete" bar appears (verifies `updateBulkDeleteBar()` runs). |
| D3 | Click bulk delete; confirm. | XHR `POST /files/delete-bulk` body has `paths=…&paths=…`. Response 200, **empty body**. Both rows are removed by JS sweep. Bulk-delete bar resets/hides. (AC2.3 human portion.) |
| D4 | Click "Scan Now". Watch Network panel for ~6 seconds. | XHR `POST /files/scan` 200, **empty body**. Within ~1 second, `updateProcessorStatus()` triggers an extra `GET /files/status`. The 5s poller continues firing `GET /files/status` afterward. |
| D5 | Repeat D4 for: Import, Retry Failed, Migrate Imports, Processor Start, Processor Stop. | Each: HX POST 200, empty body, immediate `updateProcessorStatus()` refresh, then continued 5s polling. |
| D6 | Non-HX redirect: `curl -i -u user:pass -X POST http://localhost:8443/files/scan`. | `HTTP/1.1 303 See Other` with `Location: /files`. |

## Phase E: Layout vs fragment regression (AC5.2 re-spot-check)

| Step | Action | Expected |
|------|--------|----------|
| E1 | `curl -s -u user:pass http://localhost:8443/ \| grep -c 'class="sidebar"'` | `1` |
| E2 | `curl -s -u user:pass -H 'HX-Request: true' http://localhost:8443/ \| grep -c 'class="sidebar"'` | `0` |
| E3 | Same two for `/files`. | `1` and `0` respectively. |

## Phase F: JS-listener integrity after fragment swaps (AC5.3)

| Step | Action | Expected |
|------|--------|----------|
| F1 | On Tasks tab after performing A2, B2, B3, B4 in sequence, verify the bulk-actions counter still updates when toggling any checkbox. | Counter increments/decrements correctly without reload. |
| F2 | On Files tab after C2–C5 and D1–D5, verify the bulk-delete bar still appears/hides when toggling file checkboxes. | Bar reacts correctly. |
| F3 | Confirm the global status bar / processor badge continues updating every ~5s. | Badge text/class refreshes per poller; no console errors. |
| F4 | Console panel across F1–F3. | No JS errors, no `htmx:swapError`, no uncaught promise rejections. |

## End-to-End Scenario 1: "Task lifecycle without leaving the page"

Validates AC1.1, AC1.3, AC1.4, AC1.5, AC1.6, AC1.7 in concert and
confirms no full-tab reload happens at any step.

1. Navigate to `/`. Note current URL and scroll position.
2. Create a task `E2E-Task-1`. Verify it appears at top.
3. Create `E2E-Task-2` and `E2E-Task-3`.
4. Click Complete on `E2E-Task-1`. Verify in-place row update.
5. Select `E2E-Task-2` and `E2E-Task-3`; bulk Complete. Verify both rows update.
6. Bulk Delete `E2E-Task-3`. Verify row disappears.
7. Click Purge Completed. Verify `E2E-Task-1` and `E2E-Task-2` rows disappear.
8. Throughout: URL must remain `/`; browser back/forward stack should
   not have grown by more than the initial navigation; console clean.

## End-to-End Scenario 2: "File mutate-and-delete flow"

Validates AC2.1, AC2.2, AC2.3, AC2.4, AC2.5 cohesion with the polling
refresh.

1. Navigate to `/files?path=<some subdir>`. Copy URL.
2. Click Skip on file F1. Verify badge → `badge-skipped`.
3. Click Unskip on F1. Verify badge → `badge-unprocessed`.
4. Click Queue on F1. Verify badge → `badge-pending`.
5. Click "Scan Now". Verify `updateProcessorStatus()` ticks; queue
   depth in header changes within 5s if scan finds anything.
6. Select F1 and another file F2 via checkboxes. Bulk delete; confirm.
7. Both rows disappear; bulk-delete bar resets.
8. Reload page; deleted notes remain absent.
9. Throughout: URL stays at the original `/files?path=…`; never
   bounces to `/files` without the query string (would indicate
   accidental non-HX redirect path).

## Traceability

| Criterion | Automated Test | Manual Step |
|-----------|----------------|-------------|
| AC1.1 | `TestPostCompleteTaskHXReturnsRow` | A2 |
| AC1.2 | `TestPostCompleteTaskUpdatesStatus` | A2 (via curl if needed) |
| AC1.3 | `TestPostCompleteTaskHXReturnsRow` | A3 |
| AC1.4 | `TestBulkCompleteHXReturnsRowFragments` | B2 |
| AC1.5 | `TestBulkDeleteHXReturnsEmptyBody` | B3 |
| AC1.6 | `TestPostCreateTaskHXReturnsRow` | A4, A6 |
| AC1.7 | `TestPurgeCompletedHXReturnsEmptyBody` + `TestPurgeCompletedNonHXRedirects` | B4 |
| AC1.8 | `TestPostCompleteTaskNotFound` | — |
| AC2.1 | `TestHandleFilesSingleRowMutations` HX subtests | C2–C5 |
| AC2.2 | `TestHandleFilesDeleteNoteHXEmptyBody` | D1 |
| AC2.3 | `TestHandleFilesDeleteBulkHXEmptyBody` | D2, D3 |
| AC2.4 | `TestHandleBroadFileMutations` | D4, D5 |
| AC2.5 | `TestHandleFilesSingleRowMutations` non-HX subtests | C6, D6 |
| AC2.6 | (acknowledged non-coverage; pre-existing service-layer validation) | — |
| AC3.1 | `TestTaskRowFragmentIdentity` + `tasks.html:60` shared invocation | (covered in A2/B2 visually) |
| AC3.2 | `TestFileRowFragmentIdentity` + `files.html:65` shared invocation | (covered in C2/C3 visually; Boox-source branch covered if a Boox file is used) |
| AC3.3 | `TestTaskRowFragmentIdentity` | — |
| AC3.4 | `TestFileRowFragmentIdentity` (Supernote + Boox) | — |
| AC4.1 | `TestRenderFragmentAC41` | — |
| AC4.2 | `TestRenderTemplate` | E1–E3 |
| AC4.3 | `TestRenderFragmentAC41` (implicit via embed.FS) | — |
| AC5.1 | `go test ./...` | (re-run before merge) |
| AC5.2 | — | E1–E3 |
| AC5.3 | — | F1–F4 |
| AC6.1–AC6.5 | (non-goals — no test) | (PR-time code review boundary) |
