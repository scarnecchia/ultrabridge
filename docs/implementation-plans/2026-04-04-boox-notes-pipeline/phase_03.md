# Boox Notes Pipeline — Phase 3: WebDAV Server

**Goal:** WebDAV endpoint that accepts Boox .note file uploads with Basic Auth and version-on-overwrite semantics.

**Architecture:** New package `internal/webdav/` wrapping `golang.org/x/net/webdav` with a custom `FileSystem` backend. The backend writes to `UB_BOOX_NOTES_PATH`, preserving the Boox device path structure. On re-upload, old files are moved to `.versions/`. A callback hook triggers processing after upload. Mounted at `/webdav/` on the existing HTTP mux behind the existing `auth.Middleware`.

**Tech Stack:** `golang.org/x/net/webdav` (WebDAV protocol handler), `os` (filesystem operations), `path/filepath` (path manipulation).

**Scope:** 7 phases from original design (phase 3 of 7)

**Codebase verified:** 2026-04-04

**Reference files:**
- Config pattern: `/home/jtd/ultrabridge/internal/config/config.go:11-69` (struct), `:71-109` (Load)
- Auth middleware: `/home/jtd/ultrabridge/internal/auth/auth.go` (Wrap pattern)
- HTTP mux wiring: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go:222-243`
- Feature gating pattern: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go:235` (`if cfg.WebEnabled`)

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC3: WebDAV server accepts uploads with auth and versioning
- **boox-notes-pipeline.AC3.1 Success:** WebDAV endpoint at /webdav/ accepts PUT of .note files with valid Basic Auth credentials
- **boox-notes-pipeline.AC3.2 Success:** Uploaded file written to UB_BOOX_NOTES_PATH preserving the device path structure (/onyx/{model}/{type}/{folder}/{name}.note)
- **boox-notes-pipeline.AC3.3 Success:** Re-upload of same path moves old file to .versions/ before writing new
- **boox-notes-pipeline.AC3.4 Success:** WebDAV PROPFIND/MKCOL work (Boox device can browse and create directories)
- **boox-notes-pipeline.AC3.5 Success:** Device model, note type (Notebooks/Reading Notes), and folder name extracted from upload path and stored in boox_notes metadata
- **boox-notes-pipeline.AC3.6 Failure:** PUT without valid credentials returns 401
- **boox-notes-pipeline.AC3.7 Failure:** Non-.note files accepted by WebDAV but not enqueued for processing
- **boox-notes-pipeline.AC3.8 Edge:** Concurrent uploads of different files don't corrupt each other

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add golang.org/x/net dependency and config fields

**Files:**
- Modify: `/home/jtd/ultrabridge/go.mod` (add golang.org/x/net)
- Modify: `/home/jtd/ultrabridge/internal/config/config.go` (add Boox config fields)

**Step 1: Add dependency**

Run:
```bash
go -C /home/jtd/ultrabridge get golang.org/x/net
```

**Step 2: Add config fields**

In `/home/jtd/ultrabridge/internal/config/config.go`, add to the Config struct (after the `OCRFormat` field, around line 68):

```go
    // Boox notes pipeline
    BooxEnabled   bool
    BooxNotesPath string
```

In the `Load()` function (after line 109, after `cfg.OCRFormat`):

```go
    cfg.BooxEnabled   = envBoolOrDefault("UB_BOOX_ENABLED", false)
    cfg.BooxNotesPath = os.Getenv("UB_BOOX_NOTES_PATH")
```

**Step 3: Verify build**

Run:
```bash
go -C /home/jtd/ultrabridge build ./...
```

Expected: Builds without errors.

**Commit:** `chore(config): add UB_BOOX_ENABLED and UB_BOOX_NOTES_PATH config fields`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create custom FileSystem backend with versioning

**Verifies:** boox-notes-pipeline.AC3.2, boox-notes-pipeline.AC3.3, boox-notes-pipeline.AC3.4, boox-notes-pipeline.AC3.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/webdav/fs.go`
- Create: `/home/jtd/ultrabridge/internal/webdav/file.go`

**Implementation:**

The custom FileSystem wraps `webdav.Dir` (the built-in OS filesystem backend) and adds:
1. Version-on-overwrite: before creating a file that already exists, move old file to `.versions/{relpath}/{timestamp}.note`
2. Upload callback: when a `.note` file is closed after writing, call an `OnNoteUpload` callback

`fs.go` — custom FileSystem:

```go
package webdav

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "time"

    "golang.org/x/net/webdav"
)

// OnNoteUpload is called after a .note file is successfully uploaded.
// absPath is the absolute filesystem path to the written file.
type OnNoteUpload func(absPath string)

// FS implements webdav.FileSystem with version-on-overwrite and upload hooks.
type FS struct {
    root         string
    onNoteUpload OnNoteUpload
}

// NewFS creates a new Boox WebDAV filesystem rooted at the given directory.
func NewFS(root string, onUpload OnNoteUpload) *FS {
    return &FS{root: root, onNoteUpload: onUpload}
}

func (fs *FS) resolve(name string) string {
    // Sanitize: clean path, remove leading slash, prevent traversal.
    name = filepath.Clean("/" + name)
    return filepath.Join(fs.root, filepath.FromSlash(name))
}

func (fs *FS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
    return os.MkdirAll(fs.resolve(name), perm)
}

func (fs *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
    return os.Stat(fs.resolve(name))
}

func (fs *FS) RemoveAll(ctx context.Context, name string) error {
    return os.RemoveAll(fs.resolve(name))
}

func (fs *FS) Rename(ctx context.Context, oldName, newName string) error {
    return os.Rename(fs.resolve(oldName), fs.resolve(newName))
}

func (fs *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
    absPath := fs.resolve(name)

    // Version-on-overwrite: if creating/truncating and file already exists, archive it.
    if flag&(os.O_CREATE|os.O_TRUNC) != 0 {
        if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
            if err := fs.archiveVersion(name, absPath); err != nil {
                return nil, fmt.Errorf("archive version: %w", err)
            }
        }
    }

    // Ensure parent directory exists.
    if flag&os.O_CREATE != 0 {
        if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
            return nil, err
        }
    }

    f, err := os.OpenFile(absPath, flag, perm)
    if err != nil {
        return nil, err
    }

    // Wrap with hook for .note file upload detection.
    isWrite := flag&(os.O_WRONLY|os.O_RDWR) != 0
    isNote := strings.HasSuffix(strings.ToLower(name), ".note")

    return &hookFile{
        File:       f,
        absPath:    absPath,
        triggerHook: isWrite && isNote,
        onClose:    fs.onNoteUpload,
    }, nil
}

// archiveVersion moves the existing file to .versions/{relpath}/{timestamp}.note
func (fs *FS) archiveVersion(name, absPath string) error {
    // Construct version directory: {root}/.versions/{relpath}/
    relDir := filepath.Dir(name)
    baseName := filepath.Base(name)
    ext := filepath.Ext(baseName)
    nameNoExt := strings.TrimSuffix(baseName, ext)

    versionDir := filepath.Join(fs.root, ".versions", relDir, nameNoExt)
    if err := os.MkdirAll(versionDir, 0755); err != nil {
        return err
    }

    timestamp := time.Now().UTC().Format("20060102T150405")
    versionPath := filepath.Join(versionDir, timestamp+ext)

    return os.Rename(absPath, versionPath)
}
```

`file.go` — hook file wrapper:

```go
package webdav

import "os"

// hookFile wraps os.File to trigger an upload callback on Close.
type hookFile struct {
    *os.File
    absPath     string
    triggerHook bool
    onClose     OnNoteUpload
}

func (hf *hookFile) Close() error {
    err := hf.File.Close()
    if err == nil && hf.triggerHook && hf.onClose != nil {
        hf.onClose(hf.absPath)
    }
    return err
}
```

Note: `*os.File` already implements all methods required by `webdav.File` (Read, Write, Seek, Readdir, Stat, Close).

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/webdav/
```

Expected: Builds without errors.

**Commit:** `feat(webdav): add custom FileSystem backend with version-on-overwrite and upload hooks`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
<!-- START_TASK_3 -->
### Task 3: Create WebDAV handler and path metadata extraction

**Verifies:** boox-notes-pipeline.AC3.1, boox-notes-pipeline.AC3.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/webdav/handler.go`

**Implementation:**

Create the handler that wraps `webdav.Handler` and provides path metadata extraction.

```go
package webdav

import (
    "net/http"
    "strings"

    "golang.org/x/net/webdav"
)

// PathMetadata extracted from the Boox upload path structure:
// /onyx/{device_model}/{Notebooks|Reading Notes}/{folder_name}/{notebook_name}.note
type PathMetadata struct {
    DeviceModel string // e.g., "Tab_Ultra_C_Pro"
    NoteType    string // "Notebooks" or "Reading Notes"
    Folder      string // folder name
    NoteName    string // notebook name (without .note extension)
}

// ExtractPathMetadata parses Boox device path convention from a relative path.
func ExtractPathMetadata(relPath string) PathMetadata {
    // Expected: onyx/{model}/{type}/{folder}/{name}.note
    parts := strings.Split(strings.TrimPrefix(relPath, "/"), "/")
    var pm PathMetadata
    // parts[0] = "onyx", parts[1] = model, parts[2] = type, parts[3] = folder, parts[4] = name.note
    if len(parts) >= 2 {
        pm.DeviceModel = parts[1]
    }
    if len(parts) >= 3 {
        pm.NoteType = parts[2]
    }
    if len(parts) >= 4 {
        pm.Folder = parts[3]
    }
    if len(parts) >= 5 {
        name := parts[len(parts)-1]
        if idx := strings.LastIndex(name, "."); idx > 0 {
            pm.NoteName = name[:idx]
        } else {
            pm.NoteName = name
        }
    }
    return pm
}

// NewHandler creates a WebDAV handler for Boox note uploads.
// rootDir is the absolute path to UB_BOOX_NOTES_PATH.
// onUpload is called after each .note file is written.
func NewHandler(rootDir string, onUpload OnNoteUpload) http.Handler {
    fs := NewFS(rootDir, onUpload)

    return &webdav.Handler{
        Prefix:     "/webdav",
        FileSystem: fs,
        LockSystem: webdav.NewMemLS(),
    }
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/webdav/
```

Expected: Builds without errors.

**Commit:** `feat(webdav): add WebDAV handler with path metadata extraction`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Wire WebDAV handler into main.go

**Verifies:** boox-notes-pipeline.AC3.1, boox-notes-pipeline.AC3.6

**Files:**
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go` (add conditional WebDAV mounting)

**Implementation:**

In `/home/jtd/ultrabridge/cmd/ultrabridge/main.go`, add WebDAV handler mounting after the CalDAV handler block (after line 232, before the Web UI block):

```go
    // Wire Boox WebDAV server if enabled
    if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
        davHandler := ubwebdav.NewHandler(cfg.BooxNotesPath, func(absPath string) {
            logger.Info("boox note uploaded", "path", absPath)
            // Job enqueuing will be wired in Phase 4
        })
        mux.Handle("/webdav/", authMW.Wrap(davHandler))
        logger.Info("boox webdav enabled", "path", cfg.BooxNotesPath)
    }
```

Add the import for the new package:

```go
    ubwebdav "github.com/sysop/ultrabridge/internal/webdav"
```

The `authMW.Wrap(davHandler)` ensures all WebDAV requests require Basic Auth — same pattern as CalDAV at line 227.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./cmd/ultrabridge/
```

Expected: Builds without errors.

**Commit:** `feat(main): wire Boox WebDAV handler behind auth middleware`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-5) -->
<!-- START_TASK_5 -->
### Task 5: Tests for WebDAV server — all AC3 criteria

**Verifies:** boox-notes-pipeline.AC3.1, boox-notes-pipeline.AC3.2, boox-notes-pipeline.AC3.3, boox-notes-pipeline.AC3.4, boox-notes-pipeline.AC3.5, boox-notes-pipeline.AC3.6, boox-notes-pipeline.AC3.7, boox-notes-pipeline.AC3.8

**Files:**
- Create: `/home/jtd/ultrabridge/internal/webdav/fs_test.go`
- Create: `/home/jtd/ultrabridge/internal/webdav/handler_test.go`

**Testing:**

Follow project testing patterns: standard `testing` package, manual assertions, `t.TempDir()` for filesystem operations, `httptest.NewServer` for HTTP testing.

`fs_test.go` tests (FileSystem backend):

- **boox-notes-pipeline.AC3.2:** `TestFS_OpenFile_WritesCorrectPath` — create FS with `t.TempDir()` root, open and write a file at path `onyx/TabUltra/Notebooks/Work/meeting.note`, verify the file exists at `{root}/onyx/TabUltra/Notebooks/Work/meeting.note` with correct content.

- **boox-notes-pipeline.AC3.3:** `TestFS_OpenFile_VersionsOnOverwrite` — write `test.note`, then write same path again. Verify: (1) original content moved to `.versions/` directory, (2) new content at original path, (3) version filename contains timestamp.

- **boox-notes-pipeline.AC3.4:** `TestFS_Mkdir_and_Stat` — call `Mkdir` to create a directory, call `Stat` to verify it exists and `IsDir()` returns true. This proves PROPFIND/MKCOL will work since the WebDAV handler delegates to these methods.

- **boox-notes-pipeline.AC3.7:** `TestFS_OpenFile_NonNoteNoCallback` — create FS with upload callback that records calls. Write a `.txt` file. Verify callback was NOT called. Write a `.note` file. Verify callback WAS called with correct path.

- **boox-notes-pipeline.AC3.8:** `TestFS_ConcurrentUploads` — use `sync.WaitGroup` to upload 10 different `.note` files concurrently. Verify all files exist with correct content and the upload callback was called 10 times.

`handler_test.go` tests (HTTP-level):

- **boox-notes-pipeline.AC3.1:** `TestHandler_PUT_WithAuth` — create handler with `httptest.NewServer`, send PUT with Basic Auth header, verify 201 Created response.

- **boox-notes-pipeline.AC3.5:** `TestExtractPathMetadata` — table-driven test:
  - Input `/onyx/Tab_Ultra_C_Pro/Notebooks/Work/meeting.note` → DeviceModel="Tab_Ultra_C_Pro", NoteType="Notebooks", Folder="Work", NoteName="meeting"
  - Input `/onyx/NoteAir5C/Reading Notes/Physics/chapter1.note` → DeviceModel="NoteAir5C", NoteType="Reading Notes", Folder="Physics", NoteName="chapter1"
  - Input `/short.note` → all fields empty or partial

- **boox-notes-pipeline.AC3.6:** `TestHandler_PUT_NoAuth` — create handler wrapped with `authMW.Wrap()` using a known password hash. Send PUT with invalid credentials (wrong password). Verify 401 Unauthorized response with `WWW-Authenticate: Basic` header.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/webdav/ -v
```

Expected: All tests pass.

**Commit:** `test(webdav): add WebDAV server tests covering AC3.1-AC3.8`
<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_C -->
