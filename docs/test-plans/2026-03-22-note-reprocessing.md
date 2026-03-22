# Note Reprocessing - Human Test Plan

## Prerequisites
- UltraBridge deployed with the note-reprocessing branch (`rebuild.sh`)
- Supernote device connected and syncing via Private Cloud
- `go test -C /home/sysop/src/ultrabridge ./...` passing
- `go vet -C /home/sysop/src/ultrabridge ./...` clean
- At least one .note file with existing OCR (status=done in the web UI)

## Phase 1: Hash Storage Verification

| Step | Action | Expected |
|------|--------|----------|
| 1 | Open the UltraBridge web UI, navigate to the Files tab | Files listing loads without errors |
| 2 | Pick a file that shows status "done" in the file list | File has been previously processed |
| 3 | Open a SQLite shell: `sqlite3 /path/to/ultrabridge.db "SELECT sha256 FROM notes WHERE path LIKE '%YourFile%'"` | The sha256 column contains a 64-character hex string (not NULL) |

## Phase 2: Re-Processing After Device Edit

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the Supernote device, open a previously-processed .note file and make a visible handwriting edit | File is modified on device |
| 2 | Trigger a sync from the device (or wait for automatic sync) | Private Cloud receives the updated file |
| 3 | Within 60 seconds, check the UltraBridge web UI processor status | The edited file should appear as "pending" in the job queue |
| 4 | Wait 30+ seconds for the requeue delay to expire | The processor should claim and begin processing the file (status changes to "in_progress") |
| 5 | Wait for processing to complete | File status returns to "done"; search index contains the new handwriting content |
| 6 | Search for a word from the new edit in the Search tab | The edited file appears in search results with the new content |

## Phase 3: Engine.IO FILE-SYN Detection

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the device, create a brand new .note file with some handwriting | New file created on device |
| 2 | Sync the device | Private Cloud sends FILE-SYN event to UltraBridge |
| 3 | Check UltraBridge logs for "extractNotePaths" or "enqueue" entries | Log shows the new file path was extracted from the FILE-SYN frame and enqueued |
| 4 | Wait for processing to complete | New file appears in Files tab with status "done" |

## End-to-End: Edit-Sync-Reprocess Cycle

**Purpose:** Validates the complete flow from device edit through hash-based change detection, delayed re-enqueue, re-processing, and search index update.

1. Identify a .note file that has status "done" and a stored sha256 in the database
2. On the Supernote device, open this file and write a distinctive word (e.g., "REPROCESS_TEST_2026")
3. Sync the device
4. Monitor UltraBridge logs: expect a log line containing "re-queued changed file" with the file path and differing storedHash/currentHash values
5. Wait 30+ seconds for the requeue delay
6. Monitor logs: expect the processor to claim the job and complete OCR
7. Search for "REPROCESS_TEST_2026" in the web UI Search tab
8. Confirm the search result shows the file with the newly written word

## End-to-End: Unchanged File Is Not Reprocessed

**Purpose:** Validates that the hash gate prevents unnecessary reprocessing when a sync event arrives but file content has not changed.

1. Identify a .note file with status "done"
2. Trigger a device sync without editing any files
3. Monitor UltraBridge logs for 60 seconds
4. Confirm NO "re-queued changed file" log line appears for the unchanged file
5. Verify the file's job status remains "done" in the web UI

## End-to-End: Rapid Edit Debounce

**Purpose:** Validates that multiple rapid edits during a sync window do not create duplicate processing jobs.

1. On the device, open a processed .note file
2. Make 3-4 quick edits in succession (write a stroke, erase, write again)
3. Sync the device
4. Check the web UI: the file should show exactly one "pending" job, not multiple
5. Wait for processing to complete: file returns to "done" with content reflecting the final edit

## Traceability

| Acceptance Criterion | Automated Test | Manual Step |
|----------------------|----------------|-------------|
| AC1.1 (stored hash) | `TestGetHash_WithStoredHash` | Phase 1, Step 3 |
| AC1.1 (roundtrip) | `TestGetHash_SetHash_Roundtrip` | Phase 1, Step 3 |
| AC1.2 (NULL hash) | `TestGetHash_WithNullHash` | -- (covered by AC3.3 automated test) |
| AC2.1 (no delay) | `TestEnqueue_NoOptions_RequeueAfterNull` | -- (data-layer only) |
| AC2.2 (with delay) | `TestEnqueue_WithRequeueAfter_SetsFutureTime` | Phase 2, Step 4 |
| AC2.3 (skip future) | `TestClaimNext_SkipsFutureRequeueAfter` | Phase 2, Step 4 |
| AC3.1 (hash differs) | `TestEnqueue_HashChange_FileChanged` | Phase 2, Steps 3-5 |
| AC3.2 (hash matches) | `TestEnqueue_HashChange_FileUnchanged` | E2E: Unchanged File |
| AC3.3 (NULL hash) | `TestEnqueue_HashChange_NoStoredHash` | -- (legacy migration scenario) |
| AC3.4 (rapid edits) | `TestEnqueue_RapidEdits` | E2E: Rapid Edit Debounce |
| AC4.1 (FILE-SYN parse) | `TestExtractNotePaths/AC4.1_*` | Phase 3, Step 3 |
| AC4.2 (multiple entries) | `TestExtractNotePaths/AC4.2_*` | -- (pure function) |
| AC4.3 (non-FILE-SYN) | `TestExtractNotePaths/AC4.3_*` | -- (pure function) |
| AC4.4 (non-.note filter) | `TestExtractNotePaths/AC4.4_*` | -- (pure function) |
| AC4.5 (malformed frame) | `TestExtractNotePaths/AC4.5_*` | -- (pure function) |
