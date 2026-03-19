# Human Test Plan: Note Ingest and Search

Generated: 2026-03-19
Implementation plan: `docs/implementation-plans/2026-03-19-note-ingest-search/`

---

## Prerequisites

- UltraBridge built: `go build -C /home/sysop/src/ultrabridge/.worktrees/note-ingest-search ./cmd/ultrabridge/`
- All automated tests passing: `go test -C /home/sysop/src/ultrabridge/.worktrees/note-ingest-search ./...`
- Environment configured with `UB_NOTES_PATH` pointing to a directory containing at least 3 `.note` files (one with MyScript RECOGNTEXT, one with KEYWORD blocks, one large file > `UB_OCR_MAX_FILE_MB`)
- A PDF or other non-note file present in the notes directory
- Browser open to `http://<host>:<port>`
- For OCR tests: `UB_OCR_ENABLED=true`, `UB_OCR_API_URL`, `UB_OCR_API_KEY`, `UB_OCR_MODEL` configured with valid credentials

---

## Phase 1: File Browser (AC1)

| Step | Action | Expected |
|------|--------|----------|
| 1.1 | Navigate to `/files` | Page loads with 200 OK. Top-level directory listing shows all files and subdirectories from `UB_NOTES_PATH`. |
| 1.2 | Observe a `.note` file in the listing | File name displayed. Status badge shows "unprocessed" in gray styling. |
| 1.3 | Observe a `.pdf` file in the listing | File name displayed. Status badge shows "unsupported" in italic gray. No Actions buttons. |
| 1.4 | Observe a subdirectory | Directory name as a clickable link. No status badge shown. |
| 1.5 | Click a subdirectory link | Navigates to `/files?path=<subdir>`. Breadcrumb trail appears. Subdirectory contents listed. |
| 1.6 | Click a breadcrumb segment | Returns to the correct parent directory. |
| 1.7 | Enter `http://<host>:<port>/files?path=../../etc` in the address bar | Returns 400 Bad Request. No directory listing shown. |

---

## Phase 2: Processor Lifecycle (AC2)

| Step | Action | Expected |
|------|--------|----------|
| 2.1 | Navigate to `/files`. Observe the processor status indicator. | Status shows "Stopped". Pending and Active counts are 0. |
| 2.2 | Click the "▶ Start" button | Page refreshes. Status indicator changes to "Running". |
| 2.3 | Wait 10 seconds | Status indicator remains "Running". Pending count decrements if jobs exist. |
| 2.4 | Click the "⏹ Stop" button | Page refreshes. Status shows "Stopped". |
| 2.5 | Open `/files/status` directly in browser | Returns JSON: `{"Running":false,"Pending":0,"InFlight":0}` with `application/json` Content-Type. |

---

## Phase 3: Per-file Command and Control (AC7)

| Step | Action | Expected |
|------|--------|----------|
| 3.1 | Find a `.note` file showing "unprocessed". Click "Queue". | File shows "pending" badge in yellow. |
| 3.2 | Click "Skip" on a different unprocessed `.note` file. | File shows "skipped" badge. |
| 3.3 | Click "Unskip" on the file just skipped. | File returns to "pending" badge. |
| 3.4 | Start the processor. Wait for a queued file to reach "done". Click "Queue" on it. | File resets to "pending" (re-queue from done state). |
| 3.5 | If a file shows "skipped" with reason "size_limit", click "Force". | File changes to "pending", overriding the size limit skip. |
| 3.6 | Click "Details" on any processed file. | Modal appears showing: status, queued_at, started_at, finished_at, attempts, last_error. Click "Close" to dismiss. |

---

## Phase 4: Search (AC6)

| Step | Action | Expected |
|------|--------|----------|
| 4.1 | Ensure at least one `.note` is processed ("done"). Navigate to Search tab. | Search page loads with empty input and no results. |
| 4.2 | Enter a word known to be in the processed note. Click Search. | Results appear with file path, page number, and a text snippet. |
| 4.3 | Enter a term appearing in multiple notes. | Multiple results ordered by relevance (higher-frequency note ranks first). |
| 4.4 | Submit an empty search. | No results, no error. |
| 4.5 | Enter a term not in any indexed content. | "No results" message displayed. |

---

## Phase 5: Automatic File Detection (AC8)

| Step | Action | Expected |
|------|--------|----------|
| 5.1 | With processor running, copy a new `.note` file into `UB_NOTES_PATH`. | Within a few seconds, file appears in Files tab with "pending" badge (auto-detected and queued). |
| 5.2 | Modify a processed `.note` file (e.g., sync from device with new content). | Within a few seconds, file shows "pending" again (mtime change detected, re-queued). |
| 5.3 | Copy the same `.note` file 5 times in quick succession (< 2 seconds total). | Only one queue entry created. Check logs to confirm single enqueue event (debounce working). |

---

## End-to-End: Full OCR Pipeline (AC4.1 + AC4.2 + AC3.1)

**Purpose:** Validate the complete pipeline from detection through OCR to search.

**Setup:** `UB_OCR_ENABLED=true` with valid API credentials. `UB_BACKUP_PATH` set.

1. Start UltraBridge. Navigate to `/files`. Start the processor.
2. Copy a `.note` file containing handwritten content (no existing RECOGNTEXT) into `UB_NOTES_PATH`.
3. Observe: file transitions `pending → in_progress → done`.
4. Navigate to Search tab. Search for a word from the handwritten content.
5. **Expected:** Result found pointing to the correct file and page.
6. Check `UB_BACKUP_PATH`: a copy of the original `.note` exists before modification.
7. Open the modified `.note` on a Supernote device. **Expected:** Device opens without errors.

---

## End-to-End: MyScript-only Indexing (AC3.1 + AC6.1)

**Purpose:** Validate files with existing MyScript RECOGNTEXT are indexed without OCR.

**Setup:** `UB_OCR_ENABLED=false`.

1. Place a `.note` with existing MyScript RECOGNTEXT in `UB_NOTES_PATH`.
2. Start UltraBridge and processor. Queue the file.
3. Wait for "done" status.
4. Navigate to Search. Search for text known to be in the RECOGNTEXT.
5. **Expected:** Result found with correct file path.

---

## Human Verification Required

| Criterion | What to Verify |
|-----------|----------------|
| AC1.3 Badge colors | In Files tab, verify correct colors: gray=unprocessed, yellow=pending, blue=processing, green=done, red=failed, neutral=skipped, italic gray=unsupported. |
| AC2.5 Status polling | On Files tab, observe status line updates every 5 seconds without page reload. Enqueue a job; confirm Pending count increments in real time. |
| AC4.1/4.2 Real API | After full OCR pipeline, verify RECOGNTEXT is readable by go-sn and Supernote device can open the modified file. |
| AC7.6 History modal | Click "Details" on a processed file. Confirm modal shows all fields (attempts, timestamps, errors). Click "Close" to dismiss. |
| AC8.4 Debounce | During a real Supernote device sync, monitor logs. Confirm single enqueue event per file even during rapid inotify events. |
| AC3.3 KEYWORD search | Index a `.note` with KEYWORD annotations. Search for a keyword. Verify it appears in results (keywords are indexed for FTS5 search). |

---

## Traceability

| AC | Automated Test | Manual Step |
|----|---------------|-------------|
| AC1.1 | `TestHandleFiles_TopLevel` | 1.1 |
| AC1.2 | `TestHandleFiles_WithPath` | 1.5, 1.6 |
| AC1.3 | `TestHandleFiles_TopLevel` | 1.2, Human |
| AC1.4 | `TestHandleFiles_TopLevel` | 1.3 |
| AC1.5 | `TestHandleFiles_PathTraversal` | 1.7 |
| AC1.6 | `TestHandleFiles_NoteStoreNil` | — |
| AC2.1 | `TestProcessor_NotRunningByDefault` | 2.1 |
| AC2.2 | `TestProcessor_StartStop` | 2.2, 2.4 |
| AC2.3 | `TestProcessor_StopGraceful` | 2.4 |
| AC2.4 | `TestProcessor_PendingJobsPersist` | — |
| AC2.5 | `TestHandleFilesStatus` | 2.5, Human |
| AC3.1 | `TestWorker_MyScriptExtractionOnly` | E2E MyScript |
| AC3.2 | `TestWorker_EmptyBodyTextIndexed` | — |
| AC3.3 | `TestWorker_KeywordExtraction` | Human |
| AC4.1 | `TestWorker_OCREnabled` | E2E Full OCR, Human |
| AC4.2 | `TestWorker_OCREnabled` | E2E Full OCR |
| AC4.3 | `TestWorker_OCREnabled` | E2E Full OCR |
| AC4.4 | `TestWorker_OCRAPIError` | — |
| AC4.5 | `TestWorker_SizeLimit` | 3.5 |
| AC4.6 | `TestWatchdog_ReclaimsStuckJobs` | — |
| AC5.1 | `TestWorker_BackupCreated` | E2E Full OCR |
| AC5.2 | `TestWorker_BackupAlreadyExists` | — |
| AC5.3 | `TestWorker_BackupFails` | — |
| AC5.4 | `TestWorker_NoBackupPath` | — |
| AC6.1 | `TestSearch_IndexAndQuery` | 4.2 |
| AC6.2 | `TestSearch_IndexAndQuery` | 4.2 |
| AC6.3 | `TestSearch_Ordering` | 4.3 |
| AC6.4 | `TestSearch_Reindex` | — |
| AC6.5 | `TestSearch_EmptyQuery` | 4.4 |
| AC7.1 | `TestHandleFilesQueue` | 3.1 |
| AC7.2 | `TestHandleFilesRequeue` | 3.4 |
| AC7.3 | `TestHandleFilesSkip` | 3.2 |
| AC7.4 | `TestHandleFilesUnskip` | 3.3 |
| AC7.5 | `TestHandleFilesForce` | 3.5 |
| AC7.6 | `handleFilesHistory` (endpoint) | 3.6, Human |
| AC8.1 | `TestReconciler_NewAndUnchanged` | 5.1 |
| AC8.2 | `TestReconciler_ChangedFileRequeued` | 5.2 |
| AC8.3 | `TestReconciler_NewAndUnchanged` | — |
| AC8.4 | `TestWatcher_Debounce` | 5.3, Human |
| AC9.1 | `go test ./...` (build gate) | Prerequisites |
| AC9.2 | `TestLoad_PipelineDefaults` | — |
