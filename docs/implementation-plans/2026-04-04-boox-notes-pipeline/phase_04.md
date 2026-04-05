# Boox Notes Pipeline — Phase 4: Boox Processing Pipeline

**Goal:** Job queue that orchestrates parse → render → OCR → index for Boox notes, triggered by WebDAV uploads.

**Architecture:** New package `internal/booxpipeline/` mirroring the existing `internal/processor/` patterns: atomic job claiming, single worker loop, 5-second poll interval, watchdog for stuck jobs. Schema additions in `internal/notedb/schema.go` for `boox_notes` and `boox_jobs` tables. WebDAV handler enqueues jobs after upload. Rendered pages cached to disk at `{BooxNotesPath}/.cache/{noteId}/page_{N}.jpg`.

**Tech Stack:** `database/sql` (SQLite job queue), `image/jpeg` (JPEG encoding), existing `processor.OCRClient` and `processor.Indexer` interfaces.

**Scope:** 7 phases from original design (phase 4 of 7)

**Codebase verified:** 2026-04-04

**Reference files:**
- Job queue pattern: `/home/jtd/ultrabridge/internal/processor/processor.go:206-295` (claimNext, run loop)
- Worker pipeline: `/home/jtd/ultrabridge/internal/processor/worker.go:63-215`
- Job model: `/home/jtd/ultrabridge/internal/processor/job.go`
- Schema migrations: `/home/jtd/ultrabridge/internal/notedb/schema.go`
- DB open: `/home/jtd/ultrabridge/internal/notedb/db.go:11-30`
- Indexer interface: `/home/jtd/ultrabridge/internal/processor/processor.go:13-19`
- OCRClient: `/home/jtd/ultrabridge/internal/processor/ocrclient.go:26-51`
- Processor wiring in main: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go:158-189`

---

## Acceptance Criteria Coverage

This phase implements and tests:

### boox-notes-pipeline.AC4: Processing pipeline runs end-to-end
- **boox-notes-pipeline.AC4.1 Success:** WebDAV upload triggers processing job automatically
- **boox-notes-pipeline.AC4.2 Success:** Job parses ZIP, renders all pages to cached JPEGs, OCRs each page, indexes text
- **boox-notes-pipeline.AC4.3 Success:** OCR'd text appears in note_content/note_fts tables with correct path and page numbers
- **boox-notes-pipeline.AC4.4 Success:** Re-upload triggers re-processing: old cache cleared, new pages rendered, re-OCR'd, re-indexed
- **boox-notes-pipeline.AC4.5 Failure:** Failed OCR marks job as failed, does not block future jobs
- **boox-notes-pipeline.AC4.6 Failure:** Corrupt .note file fails gracefully with error logged, job marked failed
- **boox-notes-pipeline.AC4.7 Edge:** Note with many pages (>10) processes all pages sequentially without timeout

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Add boox_notes and boox_jobs schema to notedb

**Files:**
- Modify: `/home/jtd/ultrabridge/internal/notedb/schema.go` (append new migration statements)

**Implementation:**

Append the following statements to the existing `migrations` slice in `/home/jtd/ultrabridge/internal/notedb/schema.go` (after the last existing statement). Uses `CREATE TABLE IF NOT EXISTS` for idempotency, same as all existing migrations.

```sql
-- boox_notes: metadata for each known Boox .note file
CREATE TABLE IF NOT EXISTS boox_notes (
    path TEXT PRIMARY KEY,           -- absolute filesystem path
    note_id TEXT NOT NULL DEFAULT '',     -- top-level directory name from ZIP (used for cache paths)
    title TEXT NOT NULL DEFAULT '',
    device_model TEXT NOT NULL DEFAULT '',
    note_type TEXT NOT NULL DEFAULT '',   -- 'Notebooks' or 'Reading Notes'
    folder TEXT NOT NULL DEFAULT '',
    page_count INTEGER NOT NULL DEFAULT 0,
    file_hash TEXT NOT NULL DEFAULT '',   -- SHA-256 of current file
    version INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT 0,  -- unix millis
    updated_at INTEGER NOT NULL DEFAULT 0   -- unix millis
);

-- boox_jobs: processing job queue for Boox notes
CREATE TABLE IF NOT EXISTS boox_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    note_path TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    skip_reason TEXT NOT NULL DEFAULT '',
    ocr_source TEXT NOT NULL DEFAULT '',
    api_model TEXT NOT NULL DEFAULT '',
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    queued_at INTEGER NOT NULL DEFAULT 0,
    started_at INTEGER NOT NULL DEFAULT 0,
    finished_at INTEGER NOT NULL DEFAULT 0,
    requeue_after INTEGER,               -- unix timestamp, NULL = no delay
    FOREIGN KEY (note_path) REFERENCES boox_notes(path)
);
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/notedb/ -v
```

Expected: Existing tests still pass (schema is additive, IF NOT EXISTS is safe).

**Commit:** `feat(notedb): add boox_notes and boox_jobs schema tables`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create boox_notes store (CRUD operations)

**Verifies:** boox-notes-pipeline.AC4.1 (partial — enqueue requires note row)

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxpipeline/store.go`

**Implementation:**

Create a store for boox_notes and boox_jobs operations. Mirrors the patterns in `internal/processor/processor.go` and `internal/notestore/store.go`.

Key operations:
- `UpsertNote(ctx, path, title, deviceModel, noteType, folder, pageCount, fileHash)` — INSERT OR REPLACE into boox_notes, incrementing version on conflict
- `EnqueueJob(ctx, notePath)` — First ensures a `boox_notes` row exists for the path (INSERT OR IGNORE with minimal defaults), then INSERT into boox_jobs with status=pending, queued_at=now. This satisfies the FK constraint since the WebDAV callback calls Enqueue before the worker has parsed the note metadata. The worker's `UpsertNote` call later fills in full metadata (title, pages, etc.).
- `ClaimNextJob(ctx)` — atomic UPDATE...WHERE...SELECT pattern from processor.go:206-236
- `CompleteJob(ctx, jobID, ocrSource, apiModel)` — set status=done, finished_at
- `FailJob(ctx, jobID, errMsg)` — set status=failed, last_error, finished_at
- `GetNote(ctx, path)` — read boox_notes row
- Note: `ClearNoteContent` is NOT a method on Store — content deletion is handled by the `ContentDeleter` interface in WorkerConfig (satisfied by `search.Store.Delete`). This ensures FTS5 triggers fire correctly.

```go
package booxpipeline

import (
    "context"
    "database/sql"
    "fmt"
    "time"
)

type Store struct {
    db *sql.DB
}

func NewStore(db *sql.DB) *Store {
    return &Store{db: db}
}

type BooxNote struct {
    Path        string
    Title       string
    DeviceModel string
    NoteType    string
    Folder      string
    PageCount   int
    FileHash    string
    Version     int
}

type BooxJob struct {
    ID           int64
    NotePath     string
    Status       string
    SkipReason   string
    OCRSource    string
    APIModel     string
    Attempts     int
    LastError    string
    QueuedAt     int64
    StartedAt    int64
    FinishedAt   int64
    RequeueAfter *int64
}
```

Implement `UpsertNote` with version increment:
```go
func (s *Store) UpsertNote(ctx context.Context, path, noteID, title, deviceModel, noteType, folder string, pageCount int, fileHash string) error {
    now := time.Now().UnixMilli()
    _, err := s.db.ExecContext(ctx, `
        INSERT INTO boox_notes (path, note_id, title, device_model, note_type, folder, page_count, file_hash, version, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
        ON CONFLICT(path) DO UPDATE SET
            note_id = excluded.note_id,
            title = excluded.title,
            device_model = excluded.device_model,
            note_type = excluded.note_type,
            folder = excluded.folder,
            page_count = excluded.page_count,
            file_hash = excluded.file_hash,
            version = version + 1,
            updated_at = excluded.updated_at`,
        path, noteID, title, deviceModel, noteType, folder, pageCount, fileHash, now, now,
    )
    return err
}
```

Implement `ClaimNextJob` using SQLite's `RETURNING` clause (available since SQLite 3.35+, which modernc.org/sqlite supports) to atomically claim and read the job in a single statement, avoiding the race condition of timestamp-based lookup:

```go
func (s *Store) ClaimNextJob(ctx context.Context) (*BooxJob, error) {
    now := time.Now().Unix()
    var job BooxJob
    err := s.db.QueryRowContext(ctx, `
        UPDATE boox_jobs SET status = 'in_progress', started_at = ?
        WHERE id = (SELECT id FROM boox_jobs WHERE status = 'pending'
            AND (requeue_after IS NULL OR requeue_after <= ?)
            ORDER BY queued_at ASC LIMIT 1)
        RETURNING id, note_path, status, attempts, last_error, queued_at, started_at`,
        now, now,
    ).Scan(&job.ID, &job.NotePath, &job.Status, &job.Attempts, &job.LastError, &job.QueuedAt, &job.StartedAt)
    if err == sql.ErrNoRows {
        return nil, nil // no jobs available
    }
    if err != nil {
        return nil, fmt.Errorf("claim boox job: %w", err)
    }
    return &job, nil
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxpipeline/
```

Expected: Builds without errors.

**Commit:** `feat(booxpipeline): add boox_notes/boox_jobs store with atomic job claiming`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 3-4) -->
<!-- START_TASK_3 -->
### Task 3: Create Boox processing worker

**Verifies:** boox-notes-pipeline.AC4.2, boox-notes-pipeline.AC4.5, boox-notes-pipeline.AC4.6

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxpipeline/worker.go`

**Implementation:**

The worker executes a claimed job: parse → render → OCR → index. Mirrors `internal/processor/worker.go` structure but uses Boox-specific parser and renderer.

```go
package booxpipeline

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
    "image/jpeg"
    "io"
    "os"
    "path/filepath"
    "strings"

    "github.com/sysop/ultrabridge/internal/booxnote"
    "github.com/sysop/ultrabridge/internal/booxrender"
    "github.com/sysop/ultrabridge/internal/processor"
    ubwebdav "github.com/sysop/ultrabridge/internal/webdav"
)

// OCRer abstracts the OCR capability. processor.OCRClient satisfies this interface.
type OCRer interface {
    Recognize(ctx context.Context, jpegData []byte) (string, error)
}

// ContentDeleter removes indexed content for a note path. search.Store satisfies this.
type ContentDeleter interface {
    Delete(ctx context.Context, path string) error
}

// WorkerConfig configures the Boox processing worker.
type WorkerConfig struct {
    Indexer        processor.Indexer
    ContentDeleter ContentDeleter // for clearing old content on re-process
    OCR            OCRer          // nil = OCR disabled
    CachePath      string         // base dir for rendered page cache
}

func (p *Processor) executeJob(ctx context.Context, job *BooxJob) error {
    notePath := job.NotePath

    // 1. Open and parse the .note file.
    f, err := os.Open(notePath)
    if err != nil {
        return fmt.Errorf("open note: %w", err)
    }
    defer f.Close()

    info, err := f.Stat()
    if err != nil {
        return fmt.Errorf("stat note: %w", err)
    }

    note, err := booxnote.Open(f, info.Size())
    if err != nil {
        return fmt.Errorf("parse note: %w", err)
    }

    // 2. Compute file hash for dedup.
    f.Seek(0, io.SeekStart)
    h := sha256.New()
    io.Copy(h, f)
    fileHash := hex.EncodeToString(h.Sum(nil))

    // 3. Extract path metadata.
    relPath, _ := filepath.Rel(p.notesPath, notePath)
    pm := ubwebdav.ExtractPathMetadata(relPath)

    // 4. Update boox_notes row (note.NoteID is the top-level directory name from the ZIP).
    if err := p.store.UpsertNote(ctx, notePath, note.NoteID, note.Title, pm.DeviceModel, pm.NoteType, pm.Folder, len(note.Pages), fileHash); err != nil {
        return fmt.Errorf("upsert note: %w", err)
    }

    // 5. Clear old cached renders and indexed content for re-processing.
    cacheDir := filepath.Join(p.cfg.CachePath, note.NoteID)
    os.RemoveAll(cacheDir)
    os.MkdirAll(cacheDir, 0755)
    // Use ContentDeleter to clear old indexed content (ensures FTS5 triggers fire correctly).
    if p.cfg.ContentDeleter != nil {
        p.cfg.ContentDeleter.Delete(ctx, notePath)
    }

    // 6. Render, OCR, and index each page.
    for i, page := range note.Pages {
        // Render to image.
        img, err := booxrender.RenderPage(page)
        if err != nil {
            return fmt.Errorf("render page %d: %w", i, err)
        }

        // Encode to JPEG.
        var buf bytes.Buffer
        if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
            return fmt.Errorf("encode page %d: %w", i, err)
        }

        // Cache rendered JPEG.
        cachePath := filepath.Join(cacheDir, fmt.Sprintf("page_%d.jpg", i))
        if err := os.WriteFile(cachePath, buf.Bytes(), 0644); err != nil {
            return fmt.Errorf("cache page %d: %w", i, err)
        }

        // OCR if client available.
        var ocrText string
        if p.cfg.OCR != nil {
            text, err := p.cfg.OCR.Recognize(ctx, buf.Bytes())
            if err != nil {
                return fmt.Errorf("ocr page %d: %w", i, err)
            }
            ocrText = text
        }

        // Index OCR'd text via shared Indexer.
        titleText := ""
        keywords := ""
        if i == 0 {
            titleText = note.Title
        }
        if err := p.cfg.Indexer.IndexPage(ctx, notePath, i, "api", ocrText, titleText, keywords); err != nil {
            return fmt.Errorf("index page %d: %w", i, err)
        }
    }

    return nil
}
```

Error handling follows the processor pattern: errors returned from `executeJob` cause the job to be marked failed; the worker loop continues to the next job.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxpipeline/
```

Expected: Builds without errors.

**Commit:** `feat(booxpipeline): add processing worker (parse → render → OCR → index)`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Create Processor with worker loop and watchdog

**Verifies:** boox-notes-pipeline.AC4.1, boox-notes-pipeline.AC4.4, boox-notes-pipeline.AC4.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxpipeline/processor.go`

**Implementation:**

Mirror the processor lifecycle from `internal/processor/processor.go:274-295` (run loop) and watchdog pattern.

```go
package booxpipeline

import (
    "context"
    "database/sql"
    "log/slog"
    "time"
)

// Processor manages the Boox notes processing pipeline.
type Processor struct {
    store     *Store
    cfg       WorkerConfig
    notesPath string
    logger    *slog.Logger
    cancel    context.CancelFunc
    done      chan struct{}
}

// New creates a new Boox processor.
func New(db *sql.DB, notesPath string, cfg WorkerConfig, logger *slog.Logger) *Processor {
    return &Processor{
        store:     NewStore(db),
        cfg:       cfg,
        notesPath: notesPath,
        logger:    logger,
        done:      make(chan struct{}),
    }
}

// Enqueue adds a .note file to the processing queue.
func (p *Processor) Enqueue(ctx context.Context, absPath string) error {
    return p.store.EnqueueJob(ctx, absPath)
}

// Start begins the worker loop and watchdog.
func (p *Processor) Start(ctx context.Context) error {
    ctx, p.cancel = context.WithCancel(ctx)
    go p.run(ctx)
    go p.watchdog(ctx)
    return nil
}

// Stop signals shutdown and waits for the worker to finish.
func (p *Processor) Stop() {
    if p.cancel != nil {
        p.cancel()
    }
    <-p.done
}

func (p *Processor) run(ctx context.Context) {
    defer close(p.done)
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        job, err := p.store.ClaimNextJob(ctx)
        if err != nil {
            p.logger.Error("claim boox job", "error", err)
        }
        if job == nil {
            select {
            case <-ctx.Done():
                return
            case <-time.After(5 * time.Second):
            }
            continue
        }

        p.processJob(ctx, job)
    }
}

func (p *Processor) processJob(ctx context.Context, job *BooxJob) {
    p.logger.Info("processing boox note", "path", job.NotePath, "job_id", job.ID)

    if err := p.executeJob(ctx, job); err != nil {
        p.logger.Error("boox job failed", "job_id", job.ID, "error", err)
        p.store.FailJob(ctx, job.ID, err.Error())
        return
    }

    ocrSource := "api"
    if p.cfg.OCR == nil {
        ocrSource = ""
    }
    p.store.CompleteJob(ctx, job.ID, ocrSource, "")
    p.logger.Info("boox note processed", "path", job.NotePath, "job_id", job.ID)
}

// watchdog reclaims stuck jobs (in_progress for >10 minutes).
func (p *Processor) watchdog(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            p.store.ReclaimStuckJobs(ctx, 10*time.Minute)
        }
    }
}
```

Add `ReclaimStuckJobs` to the store:
```go
func (s *Store) ReclaimStuckJobs(ctx context.Context, timeout time.Duration) {
    cutoff := time.Now().Add(-timeout).Unix()
    s.db.ExecContext(ctx, `
        UPDATE boox_jobs SET status = 'pending', attempts = attempts + 1
        WHERE status = 'in_progress' AND started_at < ?`, cutoff)
}
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./internal/booxpipeline/
```

Expected: Builds without errors.

**Commit:** `feat(booxpipeline): add Processor with worker loop, watchdog, and job lifecycle`
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_SUBCOMPONENT_C (tasks 5-6) -->
<!-- START_TASK_5 -->
### Task 5: Wire Boox processor into main.go and WebDAV

**Verifies:** boox-notes-pipeline.AC4.1

**Files:**
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go` (wire Boox processor, connect WebDAV callback)

**Implementation:**

In `/home/jtd/ultrabridge/cmd/ultrabridge/main.go`, after the existing processor wiring (around line 189), add Boox processor setup:

```go
    // Wire Boox pipeline if enabled
    var booxProc *booxpipeline.Processor
    if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
        booxCfg := booxpipeline.WorkerConfig{
            Indexer:        si,  // shared search.Store (same as Supernote)
            ContentDeleter: si,  // search.Store also satisfies ContentDeleter
            CachePath:      filepath.Join(cfg.BooxNotesPath, ".cache"),
        }
        if cfg.OCREnabled && cfg.OCRAPIURL != "" {
            booxCfg.OCR = processor.NewOCRClient(cfg.OCRAPIURL, cfg.OCRAPIKey, cfg.OCRModel, cfg.OCRFormat)
        }
        booxProc = booxpipeline.New(noteDB, cfg.BooxNotesPath, booxCfg, logger)
        if err := booxProc.Start(context.Background()); err != nil {
            logger.Warn("boox processor start failed", "err", err)
        } else {
            defer booxProc.Stop()
        }
    }
```

Update the WebDAV handler wiring (from Phase 3) to use the processor's Enqueue:

```go
    if cfg.BooxEnabled && cfg.BooxNotesPath != "" {
        davHandler := ubwebdav.NewHandler(cfg.BooxNotesPath, func(absPath string) {
            logger.Info("boox note uploaded", "path", absPath)
            if booxProc != nil {
                if err := booxProc.Enqueue(context.Background(), absPath); err != nil {
                    logger.Error("enqueue boox job", "error", err, "path", absPath)
                }
            }
        })
        mux.Handle("/webdav/", authMW.Wrap(davHandler))
        logger.Info("boox webdav enabled", "path", cfg.BooxNotesPath)
    }
```

Add import:
```go
    "github.com/sysop/ultrabridge/internal/booxpipeline"
```

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge build ./cmd/ultrabridge/
```

Expected: Builds without errors.

**Commit:** `feat(main): wire Boox processor with WebDAV upload trigger`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Tests for Boox pipeline — all AC4 criteria

**Verifies:** boox-notes-pipeline.AC4.1, boox-notes-pipeline.AC4.2, boox-notes-pipeline.AC4.3, boox-notes-pipeline.AC4.4, boox-notes-pipeline.AC4.5, boox-notes-pipeline.AC4.6, boox-notes-pipeline.AC4.7

**Files:**
- Create: `/home/jtd/ultrabridge/internal/booxpipeline/store_test.go`
- Create: `/home/jtd/ultrabridge/internal/booxpipeline/processor_test.go`

**Testing:**

Follow project patterns: in-memory SQLite (`:memory:`), standard `testing` package, `t.TempDir()` for filesystem, inline mocks for OCRClient/Indexer.

`store_test.go` tests:

- **boox-notes-pipeline.AC4.1:** `TestEnqueueJob` — open in-memory DB with notedb.Open, call UpsertNote then EnqueueJob, query boox_jobs to verify row exists with status=pending.
- `TestClaimNextJob_Atomic` — enqueue two jobs, call ClaimNextJob twice, verify each returns a different job in queued order.
- `TestClaimNextJob_Empty` — no jobs enqueued, call ClaimNextJob, verify returns nil, nil.
- **boox-notes-pipeline.AC4.4:** `TestUpsertNote_VersionIncrement` — call UpsertNote for same path twice, verify version=2 on second call.

`processor_test.go` tests — these require a mock OCR server and mock indexer:

Define test helpers following the `internal/processor/worker_test.go` pattern:
- `mockIndexer` struct with `calls []indexCall` slice to record IndexPage calls
- `mockOCRServer` using `httptest.NewServer` that returns canned text
- `openTestProcessor(t)` helper that wires everything with in-memory DB and `t.TempDir()`

Tests:

- **boox-notes-pipeline.AC4.2:** `TestProcessor_EndToEnd` — create a synthetic .note file (using test helpers from Phase 1) in t.TempDir(), enqueue via Enqueue(), start processor, wait for job completion. Verify: (1) cached JPEGs exist in `.cache/` directory, (2) mockIndexer received IndexPage calls for each page, (3) boox_jobs status = done.

- **boox-notes-pipeline.AC4.3:** `TestProcessor_IndexesContent` — same as above but verify mockIndexer received correct path, pageIdx, source="api", and non-empty bodyText (from mock OCR response).

- **boox-notes-pipeline.AC4.4:** `TestProcessor_ReprocessOnReupload` — process a note, then call Enqueue again for same path. Verify: old cache cleared and new cache created, mockIndexer received new calls.

- **boox-notes-pipeline.AC4.5:** `TestProcessor_OCRFailure` — mock OCR server returns 500 error. Verify job marked as failed, processor continues (doesn't panic or hang).

- **boox-notes-pipeline.AC4.6:** `TestProcessor_CorruptNote` — enqueue a path to a file with garbage content (not a valid ZIP). Verify job marked as failed with descriptive error, processor continues.

- **boox-notes-pipeline.AC4.7:** `TestProcessor_ManyPages` — create a synthetic .note with 12 pages. Process and verify all 12 pages rendered, OCR'd, and indexed without timeout.

**Verification:**

Run:
```bash
go -C /home/jtd/ultrabridge test ./internal/booxpipeline/ -v -timeout 60s
```

Expected: All tests pass.

**Commit:** `test(booxpipeline): add pipeline tests covering AC4.1-AC4.7`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_C -->
