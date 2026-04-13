# HTMX Fragment Mutations — Phase 4: File row fragment

**Goal:** Extract the `<tr>` markup for a file row out of `files.html` into `_file_row.html`, then have `files.html` loop with `{{template "_file_row" .}}`. Per-row buttons target the row. No file-handler changes in this phase.

**Architecture:** `_file_row.html` defines a single `_file_row` block that handles both Supernote and Boox rows (the existing template already branches on `noteSource`/`JobStatus` inline — the fragment carries that logic forward). File paths contain characters that aren't valid in HTML `id` attributes, so Phase 4 introduces a template FuncMap helper `fileRowID` that returns a stable DOM-safe identifier derived from the path.

**Tech Stack:** Go 1.24, `html/template`, `crypto/sha1` (stdlib).

**Scope:** Phase 4 of 6. Requires Phase 1. Independent of Phases 2 and 3 — file work can ship before task work or after.

**Codebase verified:** 2026-04-13. `files.html` renders rows inline at lines 64–113. Row structure includes: checkbox, emoji + name (directory vs file), source badge (Boox/SN derived via `noteSource` template func), optional `DeviceInfo`, file type, size, timestamps, job-status badge (8 variants), and an Actions cell with 2–4 buttons conditional on `JobStatus`. Directory rows have a different shape (no checkbox, no status, no action buttons). No `id` attribute on any `<tr>` today. The `NoteService` (`internal/service/interfaces.go:96–124`) has `ListFiles` returning `[]NoteFile`; the `NoteFile` struct (lines 38–51) carries everything the row needs. No `_file_row.html` exists today.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### htmx-fragment-mutations.AC3: Shared row templates
- **htmx-fragment-mutations.AC3.2:** `files.html` contains no inline `<tr>` markup for file rows; it invokes `{{template "_file_row" .}}` for each file in its loop.
- **htmx-fragment-mutations.AC3.4:** Same byte-identical guarantee holds for `_file_row` across initial and mutation paths.

Explicit non-coverage: AC2.* (file mutation handler behavior) is Phase 5.

---

<!-- START_TASK_1 -->
### Task 1: Add `fileRowID` template FuncMap helper

**Verifies:** Prerequisite for AC3.2.

**Files:**
- Modify: `internal/web/handler.go` — locate the existing `template.FuncMap` registration (the investigator identified it around the `NewHandler` template parsing, approximately line 160–170, with existing entries `formatDueTime`, `formatCreated`, `formatTimestamp`, `fileTypeStr`, `noteSource`). Add:
  - `fileRowID(path string) string` — returns `"file-" + hex(sha1(path))[:12]`. Deterministic: the same path always produces the same ID. Stable across handler restarts.

**Implementation:**

```go
"fileRowID": func(path string) string {
    sum := sha1.Sum([]byte(path))
    return "file-" + hex.EncodeToString(sum[:])[:12]
},
```

Add `crypto/sha1` and `encoding/hex` imports if not already present in `handler.go`.

No unit test for this helper on its own — its behavior is covered by the fragment identity test in Task 3 (which checks that the initial render's `id="file-..."` matches what `renderFragment` would produce for the same path).

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Clean build.

**Commit:** `feat(web): add fileRowID template helper for stable DOM ids`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `_file_row.html` fragment

**Verifies:** htmx-fragment-mutations.AC3.2 (together with Task 3)

**Files:**
- Create: `internal/web/templates/_file_row.html`

> **Cross-phase note:** `_file_row.html` is created here with per-row Queue/Skip/Unskip/Force buttons. **Phase 5 Task 3** uses this file unchanged for its handler wiring. **Phase 5 Task 4** may edit this file (or nearby markup in `layout.html` and `files.html`) to wire up the Delete affordance — the delete form itself lives in the history modal in `layout.html:460`, not in the row. Any later edit to `_file_row.html` during Phase 5 should be additive (new attributes on existing buttons), not structural.

**Implementation:**

The fragment defines a single `_file_row` block. Translate the inline `<tr>…</tr>` body currently at `files.html:65–113` into the define block with these changes:

1. **Add `id="{{ fileRowID .Path }}"` as the first attribute on the `<tr>`.** This is the stable DOM ID that mutation responses (Phase 5) will target.
2. **Per-row buttons target the row.** Change every button in the Actions cell (Queue, Unskip, Force, Skip) from `hx-target="#main-content"` to `hx-target="closest tr" hx-swap="outerHTML"`. The Details button (line 109) is pure JavaScript (`onclick="showHistory(...)"`) and does not change.
3. **The bulk-delete checkbox gets a row-scoped target.** The checkbox at line 66 keeps its form-wide semantics — it's submitted as part of the bulk-delete form in `files.html`. No change to the checkbox itself. The enclosing form and submit button belong to `files.html` and update in Phase 5.
4. **Keep all the conditional logic intact:** directory-vs-file rendering, source badge (Boox/SN), job-status badge (8 variants), button visibility rules per `JobStatus`.
5. **Preserve `DeviceInfo` rendering** (optional `<span class="device-info">`).
6. **The template's `$` context inside the fragment:** the existing inline row uses `$.relPath` to build the `back=` query-string parameter on per-row button URLs (line 101, 104, 105, 107). Inside a `{{template "_file_row" .}}` invocation, `$` refers to the data passed to `_file_row` (a single `NoteFile`), not to the outer template's data. Two options: (a) pass a struct `{File NoteFile; RelPath string}` to the fragment, (b) keep the `back=` parameter but source it from somewhere else (e.g., remove the `back=` parameter and have the handler read the Referer header, or compute it from `r.URL.Query().Get("path")` in the handler).

**Recommendation:** Option (a) — define a small `fileRowCtx` struct in `handler.go`:

```go
type fileRowCtx struct {
    File    service.NoteFile
    RelPath string
}
```

The fields and the struct type are intentionally **unexported**. Go's `html/template` uses reflection to access struct fields from within templates, and that reflection works on unexported types so long as the template is in the same package as the type. Because `handler.go` both defines `fileRowCtx` and registers the FuncMap + parses the templates, the fragment can reference `{{.File.Path}}` without any export. Keep it unexported to signal "internal helper, not API."

The loop in `files.html` (updated in Task 3) wraps each file: `{{template "_file_row" (makeFileRowCtx . $.relPath)}}` using a new `makeFileRowCtx` FuncMap helper. Phase 5's mutation handlers will use the same struct when rendering a single row via `renderFragment`.

Add the `makeFileRowCtx` FuncMap helper in this task alongside `fileRowID`:

```go
"makeFileRowCtx": func(f service.NoteFile, relPath string) fileRowCtx {
    return fileRowCtx{File: f, RelPath: relPath}
},
```

Inside `_file_row.html`, reference fields as `{{.File.Path}}`, `{{.File.Name}}`, `{{.RelPath}}` etc.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge build ./...`
Expected: Builds — file is embedded but no template invokes it yet.

**Commit:** `feat(web): extract _file_row fragment from files.html`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Refactor `files.html` loop to invoke `_file_row`

**Verifies:** htmx-fragment-mutations.AC3.2

**Files:**
- Modify: `internal/web/templates/files.html` — replace lines 65–113 (the inline `<tr>…</tr>` body inside `{{range .files}}`) with `{{template "_file_row" (makeFileRowCtx . $.relPath)}}`.

**Implementation:**

After this change the loop reads:

```
{{range .files}}
{{template "_file_row" (makeFileRowCtx . $.relPath)}}
{{end}}
```

Other elements of `files.html` (tab-level toolbar at 12–31, breadcrumbs, pagination controls, bulk-delete form) are unchanged in this phase. Phase 5 updates their HTMX attributes alongside the handlers.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Existing tests still pass. `TestRoutes` and any files-tab GET test exercise this path via substring assertions on filenames.

Run the server locally and visit `/files`:
- File rows appear in the same order, same columns, same visual layout.
- Inspect element: each non-directory `<tr>` has `id="file-{12hex}"`.
- Directory rows (no checkbox, no status) render correctly.
- Boox and Supernote badges render correctly on mixed-source listings.

**Commit:** `refactor(web): invoke _file_row fragment from files.html loop`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Identity test — full-tab render contains `_file_row` output

**Verifies:** htmx-fragment-mutations.AC3.4

**Files:**
- Modify: `internal/web/handler_test.go` — add `TestFileRowFragmentIdentity`.

**Implementation:**

Construct a `*Handler` via `newTestHandler()` with a mock `NoteService` (or mock `NoteStore` per existing pattern — the investigator noted `mockNoteStore` at handler_test.go:153–199) returning a single known file, e.g., `service.NoteFile{Path: "/notes/foo.note", Name: "foo.note", FileType: "note", JobStatus: "done", Source: "supernote"}`.

Assertions:
- `strings.Contains` check on the full-tab GET `/files` response for: the exact `id` string `fileRowID("/notes/foo.note")` evaluated in-test (compute via the same sha1 approach, or expose `fileRowID` from the `web` package as `FileRowID` for test reuse — prefer the second for readability).
- Same string present in a direct `h.renderFragment(w, r, "_file_row", ctx)` call where `ctx` is a `fileRowCtx{File: file, RelPath: ""}`.
- Name ("foo.note") present in both.
- `hx-target="closest tr"` present in both (if the file's JobStatus supports the Queue button, which `"done"` does).

Repeat a second sub-test for a Boox file (different source badge) to confirm the conditional branches render the same in both paths.

**Verification:**

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/ -run TestFileRowFragmentIdentity -v`
Expected: Both sub-tests pass.

Run: `go -C /home/sysop/src/ultrabridge test ./internal/web/`
Expected: Full package green.

**Commit:** `test(web): verify _file_row fragment matches files.html render`
<!-- END_TASK_4 -->

---

## Phase Done When

- `internal/web/templates/_file_row.html` exists and defines `_file_row`.
- `files.html` loop invokes the fragment.
- `fileRowID` and `makeFileRowCtx` template helpers registered.
- `TestFileRowFragmentIdentity` passes.
- `go test ./... && go vet ./...` green.
- Manual browser check at `/files` across Supernote-only, Boox-only, and mixed configurations renders identically.
- Four commits landed.
