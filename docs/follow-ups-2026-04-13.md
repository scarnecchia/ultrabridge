# Follow-ups — 2026-04-13

Items surfaced during the HTMX fragment mutations implementation (PR
[#11](https://github.com/jdkruzr/ultrabridge/pull/11)) plus pre-existing
issues that were flagged but deliberately left out of that branch's
scope. Grouped by category and tagged with severity + source so this
doc can be triaged as a punch list.

## Pre-existing, unrelated to HTMX branch

### 1. ~~`TestSyncEngine_RemoteHardDelete` failing in `internal/tasksync/`~~ (FIXED 2026-04-28, commit `70184dd`)
- **Resolution:** Test was correct; engine had a real bug. `processRemoteTask` returned early on unchanged-ETag remote tasks without bumping `sync_map.LastPulled`, so any task we pushed-then-pulled-back-unchanged stayed at `LastPulled=0`. The hard-delete loop in `reconcile` explicitly skips entries with `LastPulled==0`, which meant remote hard-deletes for those tasks were silently suppressed forever. Fix bumps `LastPulled` to "now" whenever the task is observed in a Pull, regardless of ETag match, restoring the invariant the hard-delete detector assumes.
- **User-visible impact before fix:** "I deleted a task on my device and it came back" — exactly the failure mode the original report predicted.

## Documentation drift from the prior decoupled-architecture refactor

### 2. ~~`internal/service/` has no domain CLAUDE.md~~ (FIXED 2026-04-28)
- **Resolution:** Wrote `internal/service/CLAUDE.md` (90 lines) covering the four service interfaces, the Store/pipeline interfaces they depend on, key decisions (`interface{}` cross-domain returns, `TaskPatch` semantics), and current invariants. Includes a gotcha pointing at follow-up #17 (Supernote-side RetryFailed gap).

### 3. ~~Root `CLAUDE.md` Project Structure omits `internal/service/`~~ (FIXED 2026-04-28)
- **Resolution:** Added a new "Service Layer" section to the Project Structure listing, between "RAG & Chat" and "Web UI & API". Bumped the file's "Last verified" date to 2026-04-28.

### 4. ~~`internal/web/CLAUDE.md` handler-signature section is stale~~ (FIXED 2026-04-14)
- **Resolution:** Rewritten during Files-tab split cleanup (step 5). The Handler contract section now matches `NewHandler` in `internal/web/handler.go`: 9 args (tasks, notes, search, config, noteDB, notesPathPrefix, booxNotesPath, logger, broadcaster). Items 2 and 3 (service/ domain CLAUDE.md and root Project Structure mention) remain open.

## Follow-ups surfaced by HTMX branch reviewers (deliberately deferred)

### 5. `renderFragment` partial-write risk
- **Source:** Phase 1 reviewer, Minor 3 — deferred as "acceptable to defer; flagging only."
- **Severity:** Low — `html/template.ExecuteTemplate` can write partial output before returning an error. Currently the response is 200 OK with truncated HTML + a log line; HTMX clients swap the truncated fragment into the DOM.
- **Fix shape:** Buffer the render via `bytes.Buffer` first, then `io.Copy(w, buf)` only on success. Affects both `renderFragment` and `renderTemplate`.

### 6. Modal delete-note form's fragile `hx-on` selector
- **Source:** Phase 5 reviewer, Minor 1 — "acceptable as-is."
- **Severity:** Low — `document.querySelectorAll('input.file-checkbox').find(cb => cb.value === path)` becomes a no-op if the row was removed by other activity (pagination, concurrent refresh). No crash; just silent fail.
- **Fix shape:** When `showHistory(path)` opens the modal, stash the originating row's `id` on the form (e.g. `data-row-id` attribute), and have the `hx-on:htmx:after-request` read that id directly.

### 7. Empty-state placeholder doesn't reappear after last-task delete/purge
- **Source:** Phase 3 reviewer (informational), reconfirmed.
- **Severity:** Low UX — if a user deletes all their tasks via bulk delete or purge, the "No tasks yet" placeholder doesn't regrow client-side; they see an empty tbody until they navigate away and back. Same shape applies to files if the last file in a view is deleted.
- **Fix shape:** Extend the bulk-delete/purge `hx-on:htmx:after-request` handlers to check if the tbody is now empty and inject the placeholder row. Or: server-side emit the placeholder in the response body and swap it in when the row count is about to hit zero.

### 8. AC2.6 path-traversal audit
- **Source:** Phase 5 plan; non-coverage permitted; Phase 5 and final reviewers flagged.
- **Severity:** Potentially important — file mutation handlers (queue/skip/unskip/force/delete-note) don't call `safeRelPath` at the handler layer. Current behavior relies on service-layer validation that predates the HTMX branch. Nobody has verified whether `path=../escape` silently succeeds or not.
- **Fix shape:** Write a targeted test exercising each mutation endpoint with `path=../escape`. If they succeed, add `safeRelPath` gates at the handler layer (or push into service). If they fail appropriately, document the behavior.

## Test quality touch-ups (HTMX branch reviewers — explicitly "optional / non-defects")

### 9. Duplicate `fileRowID` formula across two test files
- **Source:** Phase 5 reviewer, Minor 2.
- **Severity:** Cosmetic — same sha1→hex formula lives in `handler_test.go` as `fileRowIDFor` and inside `TestFileRowFragmentIdentity`'s closure. Comments acknowledge the duplication.
- **Fix shape:** Extract to a single package-level test helper, OR expose an exported `FileRowID` from production and have both test sites reference it.

### 10. Confusing test fixture id `"task-hx"`
- **Source:** Phase 3 reviewer, Minor 2.
- **Severity:** Cosmetic — `TestPostCompleteTaskHXReturnsRow` uses `TaskID: "task-hx"`, which renders as `id="task-task-hx"` — double prefix confuses failure-mode debugging.
- **Fix shape:** Change the fixture id to `"abc123"` or similar non-prefixed value.

### 11. `mockNoteService.Enqueue` doesn't distinguish `force=true`
- **Source:** Phase 5 reviewer, Minor 3.
- **Severity:** Cosmetic — mock sets `JobStatus = "pending"` regardless of `force`. Works for current tests but may mislead future authors extending the mock for state-transition coverage.
- **Fix shape:** Either document the simplification in the mock or make Enqueue clear any skipped flag when `force=true`.

### 12. `TestRenderTemplate` uses layout-coupled string markers
- **Source:** Phase 1 reviewer, Minor 4 — "Ship as-is; flag for future hardening. Fix: None required."
- **Severity:** Cosmetic — asserts on `<nav class="sidebar">` and `Create New Task` literal strings, which would break silently if the layout or tasks-tab heading is restyled.
- **Fix shape:** Add comment markers in the templates (e.g. `<!-- MARKER_LAYOUT -->`) that the tests can assert on without coupling to human-readable strings.

## Unrelated noise observed during testing

### 13. `/favicon.ico` 404 on every page load
- **Source:** Playwright runs during HTMX branch verification.
- **Severity:** Cosmetic console noise. Browsers auto-request `/favicon.ico`; server returns 404.
- **Fix shape:** Add a favicon file to the embedded static assets, OR register a route returning 204 No Content.

## CalDAV client troubleshooting notes

### 14. 2Do on Mac is a poor choice for troubleshooting CalDAV sync behavior
- **Source:** 2026-04-13 debugging session after the PROPPATCH `displayname` fix landed (`23a49d3`).
- **Severity:** Informational / testing guidance, not a bug in our server.
- **Context:** After web-UI mutations (e.g. completing a task in the UltraBridge UI), 2Do Mac can take up to its background polling interval (minutes) to reflect the change. This manifested as "I completed two tasks on the web, only one synced." Investigation showed both server-side `.ics` files had `STATUS:COMPLETED` and fresh `LAST-MODIFIED` timestamps immediately; 2Do simply hadn't polled for the second one yet. Hitting 2Do's manual sync button pulled the missing update.
- **Why it matters:** CalDAV is intrinsically pull-based. There is no standards-compliant way for UltraBridge to push "hey, something changed" to a CalDAV client — the closest existing mechanism on our side is the socket.io `STARTSYNC` we use for the device-side pipeline, which 2Do does not speak. Any debugging of "my change didn't propagate" that uses 2Do as the downstream observer will see this latency and misread it as a bug.
- **Recommendation for debugging:**
  - Prefer DAVx5 on Android (exposes "sync now" and detailed logs) or Thunderbird Lightning (tight manual-sync feedback loop + request logs via DevTools-equivalents).
  - Or `curl` directly against `/caldav/user/calendars/tasks/<id>.ics` to confirm server state — the authoritative truth is the `.ics` body we serve.
  - Use 2Do only after confirming server-side state via one of the above.
- **Fix shape:** None required — this is a documentation / test-methodology note. Could be mirrored into `internal/caldav/CLAUDE.md` as a "Testing" subsection callout if we find we're reaching for it often.

## Web UI regressions

### 15. Folder navigation on `/files` — URL updates but content doesn't swap
- **Source:** 2026-04-13 debugging session after the service-worker fix landed (`0c91a8c`).
- **Severity:** UX annoyance, not functionality-breaking. A hard refresh (Cmd-R / Ctrl-R) loads the correct content at the new URL, so the data is always reachable — the HTMX-driven in-place navigation is just broken for the subset of links that do `hx-get` + `hx-push-url="true"` to a same-path different-query URL (e.g. `/files` → `/files?path=Moffitt`).
- **Repro:** Navigate to `/files`. Click a folder row (Moffitt, Personal, etc.). Observe: address bar updates to `/files?path=<name>`; `#main-content` innerHTML stays on the root listing. Hard refresh → correct content loads.
- **What's been ruled out:**
  - Server-side: `GET /files?path=Moffitt` with `HX-Request: true` returns a clean 200 with a 10KB `<div id="files" …>…` body; verified both via curl and via `fetch()` from inside the browser.
  - Service worker: still reproduces after the SW v3 rewrite that no longer intercepts dynamic GETs.
  - HTMX's event pipeline: `htmx:beforeRequest` → `htmx:beforeSwap` → `htmx:afterSwap` → `htmx:afterRequest` all fire cleanly with the right status/target/body — but `target.innerHTML.length` is **identical** (36161 chars) before and after the swap. HTMX believes it swapped; the DOM disagrees.
- **Smoking-gun differential:** `htmx.ajax('GET', '/files?path=Moffitt', { target: '#main-content', swap: 'innerHTML' })` invoked programmatically **does** swap correctly (36149 → 10039 chars, `[global-status, files, SCRIPT]` → `[files, SCRIPT]`). The only material delta between the programmatic path and the anchor-click path is `hx-push-url="true"` on the anchor. So HTMX's `hx-push-url` codepath in 1.9.10 is either short-circuiting the innerHTML replacement or replacing something that immediately gets reverted by the history-snapshot machinery.
- **Bundled HTMX version:** 1.9.10 (per `internal/web/static/htmx.min.js`).
- **Fix avenues, from least to most invasive:**
  1. Remove `hx-push-url="true"` from folder links and breadcrumbs in `_file_row.html` and `files.html`. Add a tiny `hx-on:htmx:after-swap="history.pushState({}, '', event.detail.pathInfo.requestPath)"` to manually push the URL after a successful swap. Decouples the two operations HTMX is fusing.
  2. Wrap the files-tab content in `hx-boost="true"` and drop the per-link `hx-get`/`hx-push-url` attributes. HTMX's boost pathway for normal anchors may handle this case differently.
  3. Upgrade HTMX to 2.x. The 2.x codebase has substantial changes to the history pipeline and a known batch of push-url-related fixes — our code already assumes 2.x-friendly syntax in places (`hx-on:htmx:*`), so upgrading is directionally correct anyway. Bigger surface area to validate.
- **Not in scope for a quick fix:** task-tab navigation, settings tab, etc., all use `hx-push-url="true"` on the top-level nav links and work correctly. The bug seems specific to "same path, different query string" URL changes. Fix avenue #1 is the narrowest intervention and can land without touching those other paths.
- **Workaround until fixed:** users hit refresh after clicking a folder. Annoying; not data-loss.

---

### 16. Boox note deletion doesn't remove the underlying source file
- **Source:** 2026-04-14 while exercising human test plan steps D1/D3.
- **Severity:** Data-hygiene / user-expectation mismatch. Not a
  regression from the HTMX branch — this is how `DeleteNote` has
  always behaved.
- **Current behavior** (`internal/service/note.go:363` →
  `internal/booxpipeline/store.go:324`): the `/files/delete-note` and
  `/files/delete-bulk` endpoints drop the `boox_notes` row, all
  `boox_jobs` rows, the `note_content` FTS5 entry, and the rendered
  JPEG cache dir under `{booxCachePath}/{noteID}/`. They do **not**
  touch the `.pdf` / `.note` file on the backing WebDAV/Boox notes
  path.
- **Observable consequence:** after "Delete," the row disappears and
  search results drop, but the file is still on disk and will be
  re-enqueued on the next filesystem scan / WebDAV upload. Repeated
  deletes become a whack-a-mole, and the user has no UI affordance
  for actually reclaiming space.
- **Desired behavior:** the delete path should also move the source
  file to an archive/trash directory (preferred) rather than hard-
  deleting it — gives us undo, and a separate "Empty archive" action
  can do the final `os.Remove` when the user is confident. Needs a
  config key for the archive path (or reuse `.versions/` with a new
  subtree), a small UI note near the delete buttons explaining the
  semantics, and probably a CalDAV-style soft-delete column so a
  future scan won't re-discover archived files.
- **Out of scope for this follow-up:** deciding between hard-delete
  vs archive-then-purge. Start with archive-then-purge.

---

### 18. ~~Legacy-import rows with `device_model=".."` in boox_notes~~ (FIXED 2026-04-28)
- **Resolution:** All 60 affected rows shared the flat path shape
  `/mnt/supernote/boox-notes/<Device>/Notebooks/<file>` with no
  user folder, and the bad extractor had stuffed the actual device
  name into `folder` while leaving `device_model='..'` and
  `note_type='onyx'`. One-shot SQL migration (run against the
  production DB after a backup):
  ```sql
  UPDATE boox_notes
  SET device_model = folder,
      note_type    = 'Notebooks',
      folder       = ''
  WHERE device_model = '..';
  ```
  Verified afterward: 0 rows remaining with `device_model='..'`;
  60 rows redistributed across the legitimate device labels
  (NoteMax +48, Go7 +9, plus single-row touch-ups to Go103,
  NoteAir5C, NoteAir4C). The code path that produced this was
  already corrected; only the legacy data needed cleanup.

### 17. "Retry Failed" has no Supernote-side implementation
- **Source:** 2026-04-14 during Files-tab split (step 4). Surfaced
  when auditing which broad-mutation buttons belonged on which tab.
- **Severity:** Low — cosmetic gap. The button was silently no-op'ing
  against SN jobs before the split; after the split we simply removed
  it from the SN tab, which makes the gap invisible but doesn't fix
  it.
- **Current behavior** (`internal/service/note.go:438`): `RetryFailed`
  iterates only `booxStore.RetryAllFailed`. No equivalent call exists
  for the Supernote processor / `notes` table, so SN jobs stuck in
  `failed` status can only be retried one-at-a-time via per-row
  Queue/Force actions (which work because `Enqueue` routes by path
  prefix).
- **Fix shape:** Extend `RetryFailed` to also reset all SN-side
  failed jobs. Either expose a `RetryAllFailed` on the Supernote
  `processor.Processor` interface, or touch the notes-table rows
  directly via `notestore.NoteStore`. Restore the "Retry Failed"
  button on `files_supernote.html` once there's a real
  implementation behind it.

---

## Suggested triage order

1. **Investigate item 1** — `TestSyncEngine_RemoteHardDelete` has been failing for weeks. Biggest unknown.
2. **Ship items 2–4 together** — documentation catch-up from the decoupled-architecture refactor. Low-risk, high-reader-value.
3. **Audit item 8** — potentially security-relevant; should be confirmed before the current non-coverage becomes load-bearing.
4. **Everything else** as polish, opportunistically.
