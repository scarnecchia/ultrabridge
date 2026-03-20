# Human Test Plan: JIIX-Compatible RECOGNTEXT Injection

## Prerequisites

- UltraBridge built and running on dev instance with OCR enabled (`UB_OCR_ENABLED=true`)
- A Supernote device (A5X, A6X2, or Manta) connected to the same SPC instance
- At least one RTR-enabled `.note` file with handwritten content on the device
- At least one standard (non-RTR) `.note` file with handwritten content on the device
- Automated tests passing:
  - `go test -C /home/sysop/src/go-sn ./note/ -count=1`
  - `go test -C /home/sysop/src/ultrabridge/.worktrees/jiix-recogntext ./internal/processor/ -count=1`

## Phase 1: RTR Note Injection and Device Display (AC-HV1)

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the Supernote device, create a new note with "Real Time Recognition" enabled. Write several words of English text across at least 2 lines. Wait for the device to complete recognition (the recognition text view should show the device's own OCR result). | Note saved as RTR file with RECOGNSTATUS=1 on all pages. |
| 2 | Trigger SPC sync from the device. Verify the `.note` file appears in the UB notes path (check `UB_NOTES_PATH` directory). | File synced to server. |
| 3 | In the UB web UI, navigate to the Files tab. Confirm the RTR note appears. Click the note name and verify it shows `FILE_RECOGN_TYPE=1` and `RECOGNSTATUS=1` for all pages. | Note metadata confirms RTR with complete device recognition. |
| 4 | Verify UB's pipeline has detected the file and enqueued a processing job. Check the Processor tab in the web UI -- the job should appear as "pending" or "in_progress". | Job enqueued automatically or can be manually triggered. |
| 5 | Wait for the job to complete (status = "done" in web UI). Check the processor log stream for messages indicating JIIX injection occurred. | Log shows injection message for each page. Job status is "done". |
| 6 | Trigger SPC sync from the server side (or wait for automatic sync). Verify the modified `.note` file is synced back to the device. | Modified file transferred to device. |
| 7 | On the device, open the note. Tap the recognition text view toggle. Verify that text appears -- it should be the OCR result from UB's vision API, not the device's original recognition. | OCR text displayed in recognition view. Text is readable, not blank or garbled. |
| 8 | On the device, use the global search function. Search for a distinctive word that appeared in UB's OCR output. | Search returns the correct note and page. Highlight rectangle appears on the matching page. |
| 9 | After the sync in step 6, wait 5 minutes. Check whether the device has re-recognized the page by re-syncing back to the server and comparing the RECOGNTEXT block. | RECOGNTEXT should be identical to what UB injected. RECOGNSTATUS should remain 1. The device should NOT overwrite UB's injection. |

## Phase 2: Non-RTR Note Behavior

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the device, create a standard (non-RTR) note with handwritten text. Sync to server via SPC. | Standard `.note` file appears in `UB_NOTES_PATH`. |
| 2 | Wait for UB to process the note. Verify job completes as "done". | Job status is "done". |
| 3 | Compare the `.note` file bytes before and after processing (use `sha256sum` on the server). | File bytes are identical -- no modification for non-RTR notes. |
| 4 | In UB's Search tab, search for a word that should have been OCR'd from the note. | Search returns the correct note and page, confirming indexing worked without file modification. |

## Phase 3: SPC Sync Round-Trip Integrity (AC-HV2)

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the server, identify an RTR note that UB has already injected RECOGNTEXT into (from Phase 1). Record the SHA-256 hash of the file: `sha256sum /path/to/note.note`. | Hash recorded as "pre-sync hash". |
| 2 | Trigger a full SPC sync cycle: device syncs to cloud, then cloud syncs back to device. Wait for both directions to complete. | Sync completes in both directions without errors. |
| 3 | After the round-trip sync, compute the SHA-256 hash of the file again on the server. | Hash matches the pre-sync hash. If different, proceed to step 4. |
| 4 | (Only if hash changed) Use `go-sn` to extract the RECOGNTEXT block from the file. Compare the JSON structure before and after. Determine if the device overwrote UB's injection or if SPC modified file metadata. | If the device overwrote RECOGNTEXT, this indicates a RECOGNSTATUS gate failure. Report as a blocking issue. |

## Phase 4: Requeue Delay Observability

| Step | Action | Expected |
|------|--------|----------|
| 1 | On the device, create an RTR note but do NOT wait for device recognition to complete. Immediately sync to the server. | RTR note with RECOGNSTATUS!=1 on at least one page arrives on server. |
| 2 | Observe UB's processor log. The job should be requeued with a delay message. | Log shows "requeue" message with delay. Job status shows "pending" with a future requeue_after timestamp. |
| 3 | On the device, allow recognition to complete. Sync again. | Updated `.note` file with RECOGNSTATUS=1 arrives on server. |
| 4 | Wait for UB to re-process the job (after the requeue delay expires). Verify the job now completes as "done" and RECOGNTEXT is injected. | Job transitions from pending to in_progress to done. File is modified with injected JIIX. |

## End-to-End: Full Pipeline from Handwriting to Search

1. On a Supernote device, create a new RTR note. Write the sentence "The quick brown fox jumps" in clear handwriting.
2. Wait for device recognition to complete (recognition text view shows device OCR).
3. Sync to server via SPC.
4. Monitor UB processor -- job should be enqueued, processed, and completed as "done".
5. In UB's Search tab, search for "quick brown fox". Verify the search returns the note with the correct page number.
6. Sync the modified file back to the device.
7. On the device, open the note and view recognition text. Verify OCR output appears.
8. On the device, search for "quick". Verify the device's search finds the note.

## Traceability

| Criterion | Automated Test | Manual Step |
|-----------|---------------|-------------|
| AC-HV1 (Device display) | -- | Phase 1 steps 7-9 |
| AC-HV2 (SPC sync stability) | -- | Phase 3 steps 1-4 |
| AC4.1 (Non-RTR: no file mod) | TestWorker_NonRTR_NoFileModification | Phase 2 step 3 |
| AC5.1 (RECOGNSTATUS=1 -> inject) | TestWorker_RTR_WithRecognition | Phase 1 steps 4-5 |
| AC5.2 (RECOGNSTATUS!=1 -> requeue) | TestWorker_RTR_WithoutRecognition | Phase 4 steps 1-2 |
| AC7.1 (Non-RTR text in search) | TestWorker_NonRTR_NoFileModification | Phase 2 step 4 |
