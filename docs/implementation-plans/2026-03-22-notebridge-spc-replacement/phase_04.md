# NoteBridge Phase 4: OCR Pipeline Integration

**Goal:** .note files uploaded via sync are automatically OCR'd with RECOGNTEXT injection, and the tablet receives the injected version on next sync.

**Architecture:** Transfer UltraBridge's proven pipeline (~70% unchanged). Replace MariaDB catalog updater with syncdb update + event publishing. Event bus (from Phase 3) triggers pipeline on file uploads. Pipeline: event listener (primary) + fsnotify + reconciler (backup).

**Tech Stack:** Go 1.24, go-sn (RECOGNTEXT injection), Anthropic/OpenAI vision API, FTS5, fsnotify

**Scope:** Phase 4 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC4: OCR Pipeline + RECOGNTEXT Injection
- **AC4.1 Success:** .note file uploaded via sync → OCR runs → RECOGNTEXT injected → syncdb updated with new MD5
- **AC4.2 Success:** Next tablet sync sees updated MD5 → downloads injected version (no CONFLICT)
- **AC4.3 Success:** RTR notes (FILE_RECOGN_TYPE=1) are OCR'd and indexed but NOT modified
- **AC4.4 Success:** Re-processing: user edits note → uploads new version → hash mismatch detected → re-queued with 30s delay
- **AC4.5 Success:** FTS5 search returns OCR'd content from injected RECOGNTEXT
- **AC4.6 Edge:** Backup created before any file modification

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: Port Pipeline Packages

<!-- START_TASK_1 -->
### Task 1: Port notestore, search, and notedb packages

**Files:**
- Create: `/home/sysop/src/notebridge/internal/notestore/store.go` (from UB)
- Create: `/home/sysop/src/notebridge/internal/notestore/model.go` (from UB)
- Create: `/home/sysop/src/notebridge/internal/notestore/scanner.go` (from UB)
- Create: `/home/sysop/src/notebridge/internal/search/index.go` (from UB)
- Create: `/home/sysop/src/notebridge/internal/search/model.go` (from UB)

**Implementation:**

Copy these files from UltraBridge (`/home/sysop/src/ultrabridge/`) with the following changes:

1. **Module path**: Change all imports from `github.com/sysop/ultrabridge/` to `github.com/sysop/notebridge/`
2. **notedb reference**: In UltraBridge, notestore and search both accept `*sql.DB` opened by the `notedb` package. In NoteBridge, the notes pipeline tables are part of the unified syncdb schema (Phase 1, Task 5 already defines them). The packages should continue to accept `*sql.DB` — they don't care which schema opener was used.

The notestore package provides:
- `NoteStore` — file inventory (scan, list, get), SHA-256 hashing, job transfer for moved/renamed files
- `Scanner` — filesystem walk with upsert, orphan pruning
- `ComputeSHA256` — hash computation for change detection

The search package provides:
- `SearchIndex` — FTS5 indexing (IndexPage) and search (Search with BM25 scoring)
- Implements `processor.Indexer` interface

Add go-sn dependency:
```bash
go get github.com/jdkruzr/go-sn
```

Add fsnotify dependency:
```bash
go get github.com/fsnotify/fsnotify
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port notestore and search packages from UltraBridge`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Port processor package (minus catalog.go)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/processor/job.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/processor/processor.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/processor/worker.go` (from UB, MODIFIED)
- Create: `/home/sysop/src/notebridge/internal/processor/ocrclient.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/processor/watchdog.go` (from UB, unchanged)

**Implementation:**

Copy all processor files EXCEPT `catalog.go` from UltraBridge. Change module imports.

**Modification to worker.go — Replace CatalogUpdater with AfterInject hook:**

In UltraBridge's WorkerConfig:
```go
CatalogUpdater CatalogUpdater  // removes this
```

In NoteBridge's WorkerConfig, replace with:
```go
AfterInject func(ctx context.Context, path string, md5 string, size int64) error
```

In `processJob()`, after computing SHA-256 of the injected file, call the hook:
```go
if s.cfg.AfterInject != nil {
    if err := s.cfg.AfterInject(ctx, job.NotePath, newMD5, newSize); err != nil {
        s.logger.Warn("post-injection hook failed", "path", job.NotePath, "err", err)
        // Best-effort: don't fail the job
    }
}
```

This hook will be wired in main.go to:
1. Update syncdb.files row with new MD5 and size (so tablet sees the updated version)
2. Publish FileModifiedEvent (so Socket.IO pushes ServerMessage to connected tablets)

**Do NOT copy catalog.go** — it contains MariaDB-specific SPC catalog sync code.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port processor package, replace CatalogUpdater with AfterInject hook`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Port pipeline package

**Files:**
- Create: `/home/sysop/src/notebridge/internal/pipeline/pipeline.go` (from UB, MODIFIED)
- Create: `/home/sysop/src/notebridge/internal/pipeline/watcher.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/pipeline/reconciler.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/pipeline/engineio.go` (from UB, MODIFIED — keep `extractNotePaths` only)

**Implementation:**

Copy pipeline files from UltraBridge. Change module imports.

**Modification to engineio.go — Strip SPC connection logic, keep frame parser:**

UltraBridge's `engineio.go` contains two things:
1. `runEngineIOListener` — goroutine that reads from `p.events` channel (SPC-specific, connects to SPC's Engine.IO endpoint)
2. `extractNotePaths` — pure function that parses Socket.IO frames and extracts .note file paths

In NoteBridge, **remove `runEngineIOListener`** entirely. The event bus replaces inbound SPC sync events. **Keep `extractNotePaths`** — it's a pure frame parsing utility reused by `engineio_test.go` (Phase 4 Task 5) and potentially by the Socket.IO handler (Phase 3) for parsing ClientMessage frames from the tablet.

**Modification to pipeline.go — Add event bus listener as primary detection:**

UltraBridge's pipeline uses three detection strategies:
1. fsnotify watcher (primary)
2. Reconciler (15-min full scan, backup)
3. Engine.IO listener (SPC sync events)

NoteBridge changes the priority:
1. **Event bus listener (PRIMARY)** — subscribes to `events.FileUploaded`, enqueues .note files
2. fsnotify watcher (backup — catches direct filesystem modifications)
3. Reconciler (backup — catches anything missed)

Remove any pipeline code that starts `runEngineIOListener` — it is no longer needed.

Add `EventBus` field to pipeline Config. In `Start()`, subscribe to FileUploaded events:

```go
if cfg.EventBus != nil {
    cfg.EventBus.Subscribe(events.FileUploaded, func(e events.Event) {
        if notestore.ClassifyFileType(e.Path) == notestore.FileTypeNote {
            p.enqueue(ctx, e.Path)
        }
    })
}
```

This replaces UltraBridge's reliance on fsnotify as the primary trigger for synced files. fsnotify and reconciler remain as backup paths.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: port pipeline with event bus as primary file detection`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-6) -->
## Subcomponent B: Wire Pipeline + Post-Injection Sync

<!-- START_TASK_4 -->
### Task 4: Wire AfterInject to update syncdb and publish event

**Files:**
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go` — wire pipeline, processor, AfterInject hook

**Implementation:**

In main.go, after creating the sync server, event bus, and syncdb store:

1. Create notestore.NoteStore (backed by syncdb, pointing at blob storage path)
2. Create search.SearchIndex (backed by syncdb)
3. Create OCRClient (if OCR enabled via config)
4. Create processor with WorkerConfig:
   ```go
   worker := processor.NewWorker(processor.WorkerConfig{
       OCREnabled: cfg.OCREnabled,
       BackupPath: cfg.BackupPath,
       MaxFileMB:  cfg.OCRMaxFileMB,
       OCRClient:  ocrClient,
       Indexer:    searchIndex,
       AfterInject: func(ctx context.Context, path, md5 string, size int64) error {
           // 1. Find the file entry in syncdb by matching the storage path
           fileEntry, err := syncStore.GetFileByStorageKey(ctx, userID, storageKeyFromPath(path))
           if err != nil || fileEntry == nil {
               return fmt.Errorf("file not found in syncdb: %s", path)
           }
           // 2. Update syncdb with new MD5 and size
           if err := syncStore.UpdateFileMD5(ctx, fileEntry.ID, md5, size); err != nil {
               return err
           }
           // 3. Publish FileModifiedEvent for Socket.IO notification
           eventBus.Publish(ctx, events.Event{
               Type:   events.FileModified,
               FileID: fileEntry.ID,
               UserID: fileEntry.UserID,
               Path:   path,
           })
           return nil
       },
   })
   ```
5. Create pipeline with event bus, watcher, reconciler:
   ```go
   pipe := pipeline.New(pipeline.Config{
       NotesPath:  cfg.StoragePath,
       Processor:  proc,
       NoteStore:  noteStore,
       EventBus:   eventBus,
       Logger:     logger,
   })
   pipe.Start(ctx)
   defer pipe.Close()
   ```

**New syncdb method (not in Phase 2 — add here):**

Add `GetFileByStorageKey` to `internal/syncdb/store.go`:
```go
func (s *Store) GetFileByStorageKey(ctx context.Context, userID int64, storageKey string) (*FileEntry, error)
```
Query: `SELECT id, user_id, directory_id, file_name, inner_name, storage_key, md5, size, is_folder, is_active, created_at, updated_at FROM files WHERE user_id = ? AND storage_key = ? AND is_active = 'Y'`

Also add `UpdateFileMD5` method:
```go
func (s *Store) UpdateFileMD5(ctx context.Context, fileID int64, md5 string, size int64) error
```
Query: `UPDATE files SET md5 = ?, size = ?, updated_at = ? WHERE id = ?`

Add helper `storageKeyFromPath(absPath string) string` that converts absolute blob path to relative storage key.

Add config fields for OCR:
- `OCREnabled` (NB_OCR_ENABLED, default: "false")
- `OCRAPIURL` (NB_OCR_API_URL)
- `OCRAPIKey` (NB_OCR_API_KEY)
- `OCRModel` (NB_OCR_MODEL)
- `OCRConcurrency` (NB_OCR_CONCURRENCY, default: 1)
- `OCRMaxFileMB` (NB_OCR_MAX_FILE_MB, default: 50)
- `OCRFormat` (NB_OCR_FORMAT, default: "anthropic")

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire OCR pipeline with post-injection syncdb update`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Port pipeline and processor tests

**Files:**
- Create: `/home/sysop/src/notebridge/internal/processor/processor_test.go` (from UB, adapted)
- Create: `/home/sysop/src/notebridge/internal/pipeline/engineio_test.go` (from UB, unchanged)
- Create: `/home/sysop/src/notebridge/internal/search/index_test.go` (from UB, adapted)

**Implementation:**

Copy test files from UltraBridge, change module imports.

Key adaptations:
- Tests should use syncdb.Open(":memory:") instead of notedb.Open(":memory:") for database setup
- Remove any tests that reference CatalogUpdater or MariaDB
- Test helpers (openTestProcessor, seedNotesRow) adapted to use syncdb's unified schema

The existing UltraBridge tests cover:
- Job queue: enqueue, claim, complete, fail, skip, unskip, requeue-with-delay
- Watchdog: stuck job reclamation after timeout
- Worker: backup before modify, OCR client mocking, indexing
- Pipeline: Engine.IO frame parsing, FILE-SYN/DOWNLOADFILE filtering
- Search: FTS5 indexing and BM25 search

These tests transfer with minimal changes since the interfaces are the same.

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/processor/ ./internal/pipeline/ ./internal/search/
```

**Commit:** `test: port pipeline and processor tests from UltraBridge`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Post-injection integration test (CONFLICT-free flow)

**Verifies:** AC4.1 (upload → OCR → inject → syncdb updated), AC4.2 (next sync downloads injected version), AC4.3 (RTR notes not modified), AC4.4 (re-processing on hash change), AC4.5 (FTS5 search works), AC4.6 (backup before modification)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/handlers_pipeline_test.go`

**Testing:**

Integration tests that exercise the full flow: upload via sync API → pipeline detection → processor → injection → syncdb update → download reflects injection.

These tests need a real OCR client or mock. Since OCR calls external APIs, use a mock OCR client that returns fixed text. The processor.WorkerConfig supports injecting an OCRClient — create a `mockOCRClient` for tests.

Test helper: extend `setupTestServer` to include pipeline, processor, notestore, search, and event bus wiring. Use a very small test `.note` file (can be a minimal valid .note from go-sn's test fixtures or a synthetically created one).

**Test cases:**

- AC4.1 upload → inject → syncdb update:
  1. Upload a Standard .note file via sync API
  2. Wait for pipeline to detect and process (poll job status or use channel)
  3. Verify syncdb file entry has updated MD5 (different from upload MD5)
  4. Verify job status is "done"

- AC4.2 CONFLICT-free download:
  1. After injection completes, list_folder_v3
  2. Assert content_hash matches post-injection MD5 (not original)
  3. Download file via signed URL
  4. Verify downloaded file contains RECOGNTEXT (parse with go-sn)

- AC4.3 RTR notes not modified:
  1. Upload an RTR .note file (FILE_RECOGN_TYPE=1)
  2. Wait for processing
  3. Verify job status is "done" (or "skipped" for injection, but indexed)
  4. Verify file content unchanged (MD5 same as upload)
  5. Verify FTS5 index contains OCR'd text (indexed but file not modified)

- AC4.4 re-processing:
  1. Upload and process a .note file
  2. Upload a new version of same file (different content, same path)
  3. Verify pipeline detects hash mismatch
  4. Verify job is re-queued with 30s delay (check requeue_after in DB)

- AC4.5 FTS5 search:
  1. After injection completes, call search with the OCR'd text
  2. Verify search returns the processed note with relevant snippet

- AC4.6 backup:
  1. After injection, verify backup file exists in backup directory
  2. Verify backup content matches original (pre-injection) file

Note: Some tests may need to mock the OCR client to avoid external API calls. The mock returns fixed text like "test handwriting content" for any image.

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestPipeline
```

Expected: All tests pass.

**Commit:** `test: add post-injection integration tests for CONFLICT-free flow`
<!-- END_TASK_6 -->
<!-- END_SUBCOMPONENT_B -->
