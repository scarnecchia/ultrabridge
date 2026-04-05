# Boox Notes Pipeline — Phase 5: Web UI Integration

**Goal:** Boox notes visible in files list with source indicators, page image viewing, and version history.

**Architecture:** Extend the existing `internal/web/` handler with a `BooxStore` interface for querying Boox notes. The files list handler merges Supernote and Boox entries, each with a `Source` field. New endpoints serve cached Boox page images and version history. The single `index.html` template gets source badge styling and Boox-specific details view logic.

**Tech Stack:** Go `html/template`, `net/http`, existing `internal/web/` patterns.

**Scope:** 7 phases from original design (phase 5 of 7)

**Codebase verified:** 2026-04-05

**Reference files:**
- Web handler: `/home/jtd/ultrabridge/internal/web/handler.go`
- Files list handler: `/home/jtd/ultrabridge/internal/web/handler.go:367-405`
- Render endpoint: `/home/jtd/ultrabridge/internal/web/handler.go:561-619`
- Content endpoint: `/home/jtd/ultrabridge/internal/web/handler.go:541-559`
- Template: `/home/jtd/ultrabridge/internal/web/templates/index.html`
- NoteFile model: `/home/jtd/ultrabridge/internal/notestore/model.go:36-45`
- Handler construction: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go:241`
- Web CLAUDE.md: `/home/jtd/ultrabridge/internal/web/CLAUDE.md`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC5: Web UI shows Boox notes with source indicators
- **boox-notes-pipeline.AC5.1 Success:** Files list shows both Supernote and Boox notes with source badges
- **boox-notes-pipeline.AC5.2 Success:** Boox note detail view shows rendered page images with page navigation
- **boox-notes-pipeline.AC5.3 Success:** Version history accessible for Boox notes with re-uploaded versions
- **boox-notes-pipeline.AC5.4 Success:** Indexed content (OCR text) viewable for Boox notes via existing /files/content endpoint
- **boox-notes-pipeline.AC5.5 Edge:** Files list works correctly when Boox is enabled but no Boox notes exist yet

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add BooxStore interface and extend NoteFile with Source

**Verifies:** boox-notes-pipeline.AC5.1, boox-notes-pipeline.AC5.5

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notestore/model.go` (add Source field to NoteFile)
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add BooxStore interface and inject it)
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go` (pass BooxStore to web handler)

**Implementation:**

Do NOT modify `NoteFile` in `notestore/model.go`. Source detection is handled uniformly via a `noteSource` template function in the web handler that derives the source from the note path prefix (comparing against `booxNotesPath`). This avoids modifying the existing struct and keeps a single source-detection mechanism.

Define a `BooxStore` interface in `/home/jtd/ultrabridge/internal/web/handler.go`. The types `BooxNoteEntry` and `BooxVersion` are defined in the `booxpipeline` package (which owns the data) and referenced by the web layer via the interface:

```go
import "github.com/sysop/ultrabridge/internal/booxpipeline"

// BooxStore provides Boox note data to the web handler.
// Types are defined in booxpipeline package to avoid circular imports.
type BooxStore interface {
    ListNotes(ctx context.Context) ([]booxpipeline.BooxNoteEntry, error)
    GetVersions(ctx context.Context, path string) ([]booxpipeline.BooxVersion, error)
    GetNoteID(ctx context.Context, path string) (string, error) // returns note_id for cache path resolution
}
```

In `/home/jtd/ultrabridge/internal/booxpipeline/store.go` (created in Phase 4), add the shared types:

```go
// BooxNoteEntry is a summary for web display.
type BooxNoteEntry struct {
    Path        string
    Title       string
    DeviceModel string
    NoteType    string
    Folder      string
    PageCount   int
    Version     int
    NoteID      string // top-level directory name from ZIP, used for cache paths
    UpdatedAt   int64  // unix millis
    JobStatus   string // latest job status
}

// BooxVersion represents an archived version of a Boox note.
type BooxVersion struct {
    Path      string
    Timestamp string // formatted timestamp from filename
    SizeBytes int64
}
```

This avoids circular imports: `web` imports `booxpipeline` for types, and `booxpipeline` never imports `web`.

Add `booxStore BooxStore` field to the Handler struct and update `NewHandler` to accept it (add parameter after `broadcaster`). The `booxStore` should be nil-safe (nil when Boox is disabled).

Update `cmd/ultrabridge/main.go` to pass the boox pipeline store when constructing the web handler. If Boox is disabled, pass nil.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors. Existing functionality unaffected (Source field defaults to empty string, treated as "supernote").

**Commit:** `feat(web): add BooxStore interface and Source field to NoteFile`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Merge Boox notes into files list

**Verifies:** boox-notes-pipeline.AC5.1, boox-notes-pipeline.AC5.5

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (extend handleFiles to merge Boox entries)
- Modify: `/home/jtd/ultrabridge/internal/booxpipeline/store.go` (implement BooxStore interface)

**Implementation:**

In `handleFiles` (handler.go:367-405), after the existing `noteStore.List()` call, add Boox notes if booxStore is non-nil:

```go
// Merge Boox notes into file list (only at root level).
if h.booxStore != nil && relPath == "" {
    booxNotes, err := h.booxStore.ListNotes(ctx)
    if err != nil {
        h.logger.Error("list boox notes", "error", err)
    }
    for _, bn := range booxNotes {
        files = append(files, notestore.NoteFile{
            Path:      bn.Path,
            RelPath:   bn.Title, // display title instead of path
            Name:      bn.Title,
            IsDir:     false,
            FileType:  notestore.FileTypeNote,
            JobStatus: bn.JobStatus,
            Source:    "boox",
        })
    }
}
```

In `internal/booxpipeline/store.go`, add `ListNotes` and `GetVersions` methods that satisfy the `web.BooxStore` interface:

```go
func (s *Store) ListNotes(ctx context.Context) ([]BooxNoteEntry, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT bn.path, bn.title, bn.device_model, bn.note_type, bn.folder,
               bn.page_count, bn.version, bn.note_id, bn.updated_at,
               COALESCE((SELECT status FROM boox_jobs WHERE note_path = bn.path
                        ORDER BY id DESC LIMIT 1), '') as job_status
        FROM boox_notes bn
        ORDER BY bn.updated_at DESC`)
    // ... scan rows into []BooxNoteEntry
}

func (s *Store) GetVersions(ctx context.Context, path string) ([]BooxVersion, error) {
    // Derive the version directory from the note path.
    // Version files live at {root}/.versions/{relDir}/{nameNoExt}/{timestamp}.note
    relPath, _ := filepath.Rel(s.notesRoot, path)
    relDir := filepath.Dir(relPath)
    baseName := filepath.Base(relPath)
    nameNoExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
    versionDir := filepath.Join(s.notesRoot, ".versions", relDir, nameNoExt)

    entries, err := os.ReadDir(versionDir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil // no versions yet
        }
        return nil, err
    }

    var versions []BooxVersion
    for _, e := range entries {
        if e.IsDir() {
            continue
        }
        info, err := e.Info()
        if err != nil {
            continue
        }
        // Parse timestamp from filename: e.g., "20260404T120000.note"
        name := e.Name()
        ts := strings.TrimSuffix(name, filepath.Ext(name))
        versions = append(versions, BooxVersion{
            Path:      filepath.Join(versionDir, name),
            Timestamp: ts,
            SizeBytes: info.Size(),
        })
    }
    return versions, nil
}
```

The Store needs a `notesRoot` field (set from config) to derive version paths. Add this to the Store struct created in Phase 4.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors.

**Commit:** `feat(web): merge Boox notes into files list with source field`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
<!-- START_TASK_3 -->
### Task 3: Add Boox render and version history endpoints

**Verifies:** boox-notes-pipeline.AC5.2, boox-notes-pipeline.AC5.3, boox-notes-pipeline.AC5.4

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (add new routes and handlers)

**Implementation:**

Add two new routes in the Handler's mux setup (in NewHandler, where routes are registered):

```go
mux.HandleFunc("GET /files/boox/render", h.handleBooxRender)
mux.HandleFunc("GET /files/boox/versions", h.handleBooxVersions)
```

`handleBooxRender` — serves cached JPEG page images:

```go
func (h *Handler) handleBooxRender(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Query().Get("path")
    pageStr := r.URL.Query().Get("page")
    page, _ := strconv.Atoi(pageStr)

    // Look up note_id from boox_notes table to construct cache path.
    // The cache is at {BooxNotesPath}/.cache/{noteId}/page_{N}.jpg
    noteID, err := h.booxStore.GetNoteID(r.Context(), path)
    if err != nil || noteID == "" {
        http.Error(w, "Note not found", http.StatusNotFound)
        return
    }
    cachePath := filepath.Join(h.booxCachePath, noteID, fmt.Sprintf("page_%d.jpg", page))

    data, err := os.ReadFile(cachePath)
    if err != nil {
        http.Error(w, "Page not rendered yet", http.StatusNotFound)
        return
    }

    w.Header().Set("Content-Type", "image/jpeg")
    w.Header().Set("Cache-Control", "public, max-age=300")
    w.Write(data)
}
```

`handleBooxVersions` — returns list of archived versions:

```go
func (h *Handler) handleBooxVersions(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Query().Get("path")
    if h.booxStore == nil {
        json.NewEncoder(w).Encode([]interface{}{})
        return
    }
    versions, err := h.booxStore.GetVersions(r.Context(), path)
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(versions)
}
```

AC5.4 (indexed content viewable) is already satisfied — the existing `/files/content` endpoint queries `note_content` which contains Boox OCR results indexed by the shared Indexer in Phase 4. No changes needed to that endpoint.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors.

**Commit:** `feat(web): add Boox page render and version history endpoints`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Template updates for source badges and Boox details view

**Verifies:** boox-notes-pipeline.AC5.1, boox-notes-pipeline.AC5.2

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go` (register `noteSource` template function)
- Modify: `/home/jtd/ultrabridge/internal/web/templates/index.html`

**Implementation:**

First, register the `noteSource` template function in the `funcMap` in `NewHandler`. This function determines the device source from a note path and is used in both the files list and search templates:

```go
funcMap["noteSource"] = func(path string) string {
    if h.booxStore != nil && strings.HasPrefix(path, h.booxNotesPath) {
        return "Boox"
    }
    return "Supernote"
}
```

Store `booxNotesPath` as a field on Handler (set from config in constructor).

In the files table rows (around lines 360-411 of `index.html`), add source badge after the file name:

```html
<!-- In the file name column, after the file name text -->
{{$src := noteSource .Path}}
{{if eq $src "Boox"}}
  <span class="badge badge-boox" title="Boox">B</span>
{{end}}
```

The `noteSource` template function is registered in `NewHandler` (added in Phase 6 Task 2) and derives the source from the note path prefix. It should be registered early enough to be used in both the files list and search templates.

Add CSS for the Boox badge (in the `<style>` section):

```css
.badge-boox {
    display: inline-block;
    padding: 1px 5px;
    font-size: 10px;
    font-weight: bold;
    color: #fff;
    background-color: #6b7280;
    border-radius: 3px;
    margin-left: 6px;
    vertical-align: middle;
}
```

In the `showHistory()` JavaScript function (around line 668), add Boox-specific rendering logic. When the file source is "boox", fetch rendered pages from `/files/boox/render` instead of `/files/render`, and add a version history section that fetches from `/files/boox/versions`:

```javascript
// In the showHistory function, detect source and adjust render URL:
const isBoox = file.source === 'boox';
const renderUrl = isBoox
    ? `/files/boox/render?path=${encodeURIComponent(path)}&page=${idx}`
    : `/files/render?path=${encodeURIComponent(path)}&page=${idx}`;
```

For version history, add a section in the modal that fetches `/files/boox/versions?path=...` and displays the list of archived versions with timestamps and sizes.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors. Template compiles correctly.

**Commit:** `feat(web): add Boox source badges and details view to templates`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-5) -->
<!-- START_TASK_5 -->
### Task 5: Tests for Web UI integration — all AC5 criteria

**Verifies:** boox-notes-pipeline.AC5.1, boox-notes-pipeline.AC5.2, boox-notes-pipeline.AC5.3, boox-notes-pipeline.AC5.4, boox-notes-pipeline.AC5.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/web/boox_test.go`

**Testing:**

Follow project testing patterns. Use `httptest.NewServer` with the web handler, mock BooxStore, and `t.TempDir()` for cached page images.

Define `mockBooxStore` implementing the `BooxStore` interface:
```go
type mockBooxStore struct {
    notes    []BooxNoteEntry
    versions []BooxVersion
}
```

Tests:

- **boox-notes-pipeline.AC5.1:** `TestFilesPage_ShowsBothSources` — create handler with mock BooxStore returning 2 Boox notes and mock NoteStore returning 2 Supernote notes. GET /files, verify response HTML contains both sets of entries and contains the "badge-boox" class for Boox entries.

- **boox-notes-pipeline.AC5.2:** `TestBooxRender_ServesCache` — write a test JPEG to `{tmpDir}/.cache/{noteId}/page_0.jpg`. GET /files/boox/render?path=...&page=0, verify 200 response with Content-Type image/jpeg and correct body.

- **boox-notes-pipeline.AC5.3:** `TestBooxVersions_ReturnsList` — mock BooxStore.GetVersions returns 3 versions. GET /files/boox/versions?path=..., verify JSON response contains 3 entries with timestamps.

- **boox-notes-pipeline.AC5.4:** `TestBooxContent_ViaExistingEndpoint` — this is implicitly tested by the existing `/files/content` endpoint since Boox OCR results go into the same `note_content` table. No new test needed; verify by ensuring the endpoint works with a note_content row that has a Boox note path.

- **boox-notes-pipeline.AC5.5:** `TestFilesPage_NoBooxNotes` — create handler with mock BooxStore returning empty list. GET /files, verify page renders without error and shows only Supernote entries.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/web/ -v -run TestBoox
```

Expected: All tests pass.

**Commit:** `test(web): add Web UI integration tests covering AC5.1-AC5.5`
<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_C -->
