# HTMX Fragment Mutations — Phase 1: Fragment-render infrastructure

**Goal:** Add a `renderFragment` method on the web `Handler` that executes a named template block against a data value without the layout shell, so later phases can emit row-level HTML from mutation handlers.

**Architecture:** Extend `internal/web/handler.go`. `renderFragment` parallels the existing `renderTemplate` (handler.go:265) but never emits the layout — it looks up a block already registered in `h.tmpl` (the root `*template.Template` built in `NewHandler`) and executes it directly. Row fragments registered in later phases (`_task_row.html`, `_file_row.html`) are picked up automatically because the existing embed directive uses glob `templates/*.html`.

**Tech Stack:** Go 1.24, `html/template`, `embed.FS`.

**Scope:** Phase 1 of 6. Infrastructure only — no new templates, no handler rewrites, no AC1/AC2/AC3 work here.

**Codebase verified:** 2026-04-13 via codebase-investigator. Key findings: `renderTemplate` at handler.go:265–311 uses `h.tmpl.Clone()` + fragment-specific parse; embed glob is `templates/*.html` and captures underscore-prefixed files; no `_*.html` templates exist today; test pattern uses `strings.Contains` for HTML body assertions.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### htmx-fragment-mutations.AC4: Fragment-render infrastructure
- **htmx-fragment-mutations.AC4.1:** A new `Handler` method (provisionally `renderFragment(w, r, name, data)`) renders a named sub-fragment (e.g., `_task_row`, `_file_row`) without the layout shell, regardless of `HX-Request`.
- **htmx-fragment-mutations.AC4.2:** The existing `renderTemplate` continues to branch on `HX-Request` for tab-level templates — its behavior is not regressed.
- **htmx-fragment-mutations.AC4.3:** Fragments are loaded from the same `embed.FS` as tab templates; no new filesystem reads.

---

<!-- START_TASK_1 -->
### Task 1: Add an in-test fragment fixture for `renderFragment` tests

**Files:**
- Modify: `internal/web/handler_test.go` (or a new `sw_test.go`-style helper) — define a small Go string constant containing a test-only template block, e.g.:

```go
const testFixtureRowTmpl = `{{define "_test_fixture_row"}}<tr id="fixture-{{.ID}}">{{.Label}}</tr>{{end}}`
```

The test that exercises `renderFragment` parses this string into the handler's template via `h.tmpl, _ = h.tmpl.Parse(testFixtureRowTmpl)` before calling `renderFragment`. No new file under `internal/web/templates/`.

**Implementation:**

Keeping the fixture inside the test file (rather than as a separate embedded `.html`) has two benefits:
- **No intermediate-phase artefact on disk.** If someone branches off after Phase 1 to ship, they don't carry a dev-only template into production binaries.
- **No Phase 6 cleanup task.** Phase 6 simply retargets the unit test to a real fragment and the in-test constant can be deleted in the same diff.

**Naming convention caveat:** The file glob used by the root template parser is `templates/*.html` (confirmed at handler.go:165). Real fragments in Phases 2 and 4 MUST use `{{define "_name"}}…{{end}}` wrappers AND filenames starting with an underscore. The filename prefix is a project convention signalling "fragment, not standalone page." The `{{define}}` wrapper is load-bearing: a bare `<tr>…</tr>` file would be parsed under the file's base name (`_task_row`) but Go's `html/template` treats the outer file content as the default body of that template, so `ExecuteTemplate(w, "_task_row", data)` would work either way. However, `renderTemplate` (handler.go:299) dynamically defines a template named `"content"` on a cloned tree — **no fragment file may contain `{{define "content"}}` or it would collide with that dynamic slot**. Using the underscore prefix both in the filename and in the define name prevents this class of collision by construction.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Clean. Constant is unused until Task 2's tests reference it.

**Commit:** (folded into Task 2 — no standalone commit for a string constant)
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Add `renderFragment` method to `Handler`

**Verifies:** htmx-fragment-mutations.AC4.1, htmx-fragment-mutations.AC4.3

**Files:**
- Modify: `internal/web/handler.go` — add a new method `renderFragment(w http.ResponseWriter, r *http.Request, name string, data any)` alongside the existing `renderTemplate` (currently at lines 265–311). Place `renderFragment` immediately after `renderTemplate` in the file.

**Implementation:**

`renderFragment` must:

1. Set `Content-Type: text/html; charset=utf-8` (same as `renderTemplate`).
2. Execute the named template against `data` using `h.tmpl.ExecuteTemplate(w, name, data)`.
3. On `ExecuteTemplate` error, call `h.logger.Error("failed to execute fragment", "name", name, "error", err)` — do NOT call `http.Error` because headers are already flushed once execution starts (matches the existing error-handling pattern documented in `internal/web/CLAUDE.md`).
4. **Do NOT** clone `h.tmpl`, **do NOT** read from `templateFS`, and **do NOT** branch on `HX-Request`. The fragment must already be registered as a block in `h.tmpl` at parse time (which happens in `NewHandler` via `ParseFS(templateFS, "templates/*.html")`).

This is intentionally simpler than `renderTemplate`: no per-request parse step because fragments never need to be re-parsed. The existing `renderTemplate` needs to clone + parse because it redefines the `"content"` block per request; `renderFragment` just looks up a stable, already-parsed block.

No changes to `NewHandler`, `h.tmpl` initialization, or the embed directive. The `templates/*.html` glob already captures `_test_fixture_row.html` and every future `_*.html`.

**Testing:**

Add tests to `internal/web/handler_test.go` (or a new `renderfragment_test.go` in the same package — investigator noted the package is flat, no subdirs for tests). Tests must verify each listed AC:

- **AC4.1:** Calling `h.renderFragment(w, r, "_test_fixture_row", struct{ID, Label string}{"123", "hello"})` writes a response whose body contains `<tr id="fixture-123">hello</tr>` (use `strings.Contains` per project convention).
- **AC4.3:** A second test confirms the fragment is picked up via the same embed.FS by verifying no custom filesystem plumbing was added. This can be a simple "the test above works because the file lives in `internal/web/templates/` and the existing embed directive handles it" observation plus a comment. It does not need its own separate test case — the passing AC4.1 test is the evidence.
- Content-Type assertion: `w.Header().Get("Content-Type")` starts with `text/html`.

Follow the existing test setup pattern: `newTestHandler()` (defined in `testutil_test.go`) produces a `*Handler` with mocked dependencies. Construct `w := httptest.NewRecorder()` and a dummy `r := httptest.NewRequest(http.MethodGet, "/", nil)`; the request value is not used by `renderFragment` but the signature requires it for consistency with `renderTemplate`.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestRenderFragment -v`
Expected: All new tests pass.

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Full web-package suite still green (AC4.2 regression check).

Run: `go -C /home/sysop/src/ultrabridge vet ./...`
Expected: Clean.

**Commit:** `feat(web): add renderFragment helper for row-level HTMX responses`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Guard against regression of `renderTemplate`

**Verifies:** htmx-fragment-mutations.AC4.2

**Files:**
- Modify: `internal/web/handler_test.go` (or add a focused test file) — add a test that exercises `renderTemplate` for a known tab template (e.g., `tasks`) both with and without `HX-Request: true`, asserting:
  - Without `HX-Request`: response body contains the layout marker (e.g., `<nav class="sidebar">` which only appears in `layout.html`).
  - With `HX-Request: true`: response body does NOT contain the layout marker and DOES contain a tab-specific marker (e.g., the Tasks tab's `<h2>` heading).

**Implementation:**

No production code changes. This task adds coverage that would have caught a regression in `renderTemplate`'s `HX-Request` branching if someone accidentally broke it while editing adjacent code.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -v -run TestRenderTemplate`
Expected: Both subtests pass.

**Commit:** `test(web): add regression guard for renderTemplate HX-Request branching`
<!-- END_TASK_3 -->

---

## Phase Done When

- `renderFragment` method exists in `internal/web/handler.go`.
- New tests in `internal/web/` cover AC4.1 and AC4.2.
- Full `go test ./internal/web/` + `go vet ./...` + `go build ./...` succeed.
- Three commits landed on current branch.
