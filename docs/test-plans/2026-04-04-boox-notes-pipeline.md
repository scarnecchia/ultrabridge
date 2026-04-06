# Human Test Plan: Boox Notes Pipeline

## Prerequisites
- UltraBridge deployed on test host via `install.sh`
- Boox device (Tab Ultra C Pro, NoteAir, or similar) on same network
- All automated tests passing: `go test ./...`
- Environment: `UB_BOOX_ENABLED=true`, `UB_BOOX_NOTES_PATH` configured, OCR API key set

---

## Phase 1: Visual Rendering Fidelity (AC2.3)

| Step | Action | Expected |
|------|--------|----------|
| 1 | On Boox device, create note "pen-test" | Note created |
| 2 | Draw horizontal stroke with each pen: pencil, fountain, brush, marker, calligraphy | Distinct strokes on device |
| 3 | Screenshot the Boox device showing all strokes | Reference saved |
| 4 | Upload via WebDAV sync | Processing job triggered |
| 5 | Wait for job status "done" in `/files` | Job completes |
| 6 | View rendered pages in UltraBridge UI | Page images displayed |
| 7 | Compare rendered output with device screenshot | Pencil: thin. Fountain: varying width. Brush: thick. Marker: semi-transparent. Calligraphy: angle-dependent. Each visually distinct. |

## Phase 2: Page Navigation UX (AC5.2 partial)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create 5-page note on Boox with distinct content per page | 5 pages identifiable |
| 2 | Upload and wait for processing | Job status "done" |
| 3 | Click the note in `/files` | Detail view shows page 1 |
| 4 | Click Next repeatedly to page 5 | Each page loads correctly |
| 5 | Click Next on page 5 (boundary) | No navigation / disabled |
| 6 | Click Previous back to page 1 | Pages decrement correctly |
| 7 | Click Previous on page 1 (boundary) | No navigation / disabled |

## Phase 3: Content Endpoint (AC5.4)

| Step | Action | Expected |
|------|--------|----------|
| 1 | Ensure a Boox note with English handwriting is processed | OCR text indexed |
| 2 | Visit `/files/content?path={boox_note_path}` | HTTP 200 |
| 3 | Verify OCR text is present | Recognized text visible with page numbers |

## Phase 4: Deployment Scripts

### AC7.1 — install.sh Boox Configuration

| Step | Action | Expected |
|------|--------|----------|
| 1 | Run `shellcheck install.sh` | No errors |
| 2 | Run `./install.sh` interactively | Prompts appear |
| 3 | Answer "y" to Boox WebDAV prompt | Notes path prompt appears |
| 4 | Provide notes path | Script continues |
| 5 | Inspect `.ultrabridge.env` | Contains `UB_BOOX_ENABLED=true` and `UB_BOOX_NOTES_PATH` |
| 6 | Inspect `docker-compose.override.yml` | Includes Boox volume mount |
| 7 | Test unattended: `UB_BOOX_ENABLED=true UB_BOOX_NOTES_PATH=/mnt/boox ./install.sh --unattended` | Same results as interactive |

### AC7.2 — --fresh Preserves .versions/

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create: `{BOOX_PATH}/.cache/test/page_0.jpg` | Cache exists |
| 2 | Create: `{BOOX_PATH}/.versions/test/20260404T120000.note` | Version exists |
| 3 | Run `./rebuild.sh --fresh` | Completes |
| 4 | Check `.cache/` | Deleted |
| 5 | Check `.versions/` | Preserved with content |
| 6 | Check SQLite DB | `boox_jobs` table empty (recreated) |

### AC7.3 — --nuke Clears Everything

| Step | Action | Expected |
|------|--------|----------|
| 1 | Recreate `.cache/` and `.versions/` test data | Both populated |
| 2 | Run `./rebuild.sh --nuke` | Completes |
| 3 | Check `.cache/` | Deleted |
| 4 | Check `.versions/` | Deleted |

### AC7.4 — Disabled Mode

| Step | Action | Expected |
|------|--------|----------|
| 1 | Set `UB_BOOX_ENABLED=false`, rebuild | Container starts |
| 2 | Check startup logs | No "boox webdav enabled" message |
| 3 | `curl -u user:pass http://host:port/webdav/` | 404 Not Found |
| 4 | Check `/files` in browser | Only Supernote notes, no Boox badges |

## End-to-End: Full Upload-to-Search Pipeline

| Step | Action | Expected |
|------|--------|----------|
| 1 | Create Boox note with unique phrase "quantum butterfly migration" | Note created |
| 2 | Configure Boox WebDAV sync to UltraBridge | Sync settings saved |
| 3 | Trigger sync from device | Upload succeeds |
| 4 | Check `/files` for note with "B" badge, status → "done" | Note visible, processed |
| 5 | Click note to view rendered pages | Handwriting recognizable |
| 6 | Search `/search?q=quantum+butterfly` | Returns Boox note with snippet and badge |
| 7 | Visit `/files/content?path={path}` | OCR text includes phrase |
| 8 | Re-upload after edit, check `/files/boox/versions` | Version archived, re-processed |

## End-to-End: Mixed Source Search

| Step | Action | Expected |
|------|--------|----------|
| 1 | Ensure both Supernote and Boox notes contain "meeting" | Both processed |
| 2 | Search `/search?q=meeting` | Both sources appear with correct badges |
| 3 | Verify ranking by content relevance, not source | Higher occurrence ranks first |

---

## Traceability

| AC | Automated | Manual |
|----|-----------|--------|
| AC1.1-AC1.9 | All covered | -- |
| AC2.1-AC2.2, AC2.4-AC2.8 | All covered | -- |
| AC2.3 | `TestRenderPage_PenTypes` (non-blank) | Phase 1 visual comparison |
| AC3.1-AC3.8 | All covered | -- |
| AC4.1-AC4.7 | All covered | -- |
| AC5.1 | `TestFilesPage_ShowsBothSources` | -- |
| AC5.2 | `TestBooxRender_ServesCache` | Phase 2 navigation UX |
| AC5.3 | `TestBooxVersions_ReturnsList` | -- |
| AC5.4 | Covered by AC4.3 | Phase 3 browser verification |
| AC5.5 | `TestFilesPage_NoBooxNotes` | -- |
| AC6.1-AC6.3 | All covered | E2E mixed search |
| AC7.1-AC7.4 | -- | Phase 4 steps |

## Post-Plan Features (added during integration testing)

The following features were added after the initial 7-phase plan and have their own test coverage.

### Automated Tests

| Feature | Tests | Location |
|---------|-------|----------|
| Wrapper message unwrapping | `TestOpen_WrappedNoteInfo`, `TestUnwrapField1` | `booxnote/format_test.go` |
| Wrapped pageNameList JSON | `TestOpen_WrappedPageNameList` | `booxnote/format_test.go` |
| Rect-format pageSize | `TestOpen_RectPageSize`, `TestParsePageSize_AllFormats` | `booxnote/format_test.go` |
| Directory entries in point files | `TestOpen_DirectoryEntriesInPointFiles` | `booxnote/format_test.go` |
| Settings page render | `TestSettingsPage_Renders`, `_InactiveSections`, `_ActiveSections` | `web/routes_test.go` |
| Settings save (OCR prompts, todo toggle) | `TestSettingsSave_BooxOCRPrompt`, `_SupernoteOCRPrompt` | `web/routes_test.go` |
| Logs page | `TestLogsPage_Renders` | `web/routes_test.go` |
| Boox job history routing | `TestFilesHistory_BooxRoute`, `_SupernoteRoute` | `web/routes_test.go` |
| Purge completed tasks | `TestPurgeCompleted_Redirects`, `_DeletesCompletedTasks` | `web/routes_test.go` |
| Folder search filter | `TestSearchPage_FolderFilter`, `_FolderDropdown` | `web/routes_test.go` |
| Todo JSON parsing | `TestParseTodoResponse_*` (7 tests) | `booxpipeline/todo_test.go` |
| Red ink to task creation | `TestCreateTasksFromTodos_*` (7 tests) | `booxpipeline/todotask_test.go` |

### Human Verification: Red Ink To-Do Extraction

| Step | Action | Expected |
|------|--------|----------|
| 1 | In Settings > Boox, set Red Ink To-Do Extraction to "Enabled" | Saved successfully |
| 2 | On Boox device, create a note with black handwriting and some red handwriting | Both colors visible |
| 3 | Upload via WebDAV sync | Processing completes (job status "done") |
| 4 | Check Tasks tab | New task(s) created matching the red text, with detail "From Boox red ink: {path}" |
| 5 | Click the detail link on the task | Navigates to Files tab with note detail modal open |
| 6 | Upload the same note again | No duplicate tasks created |
| 7 | Complete one of the red ink tasks, then re-upload | Completed task not re-created |

### Human Verification: Settings Tab

| Step | Action | Expected |
|------|--------|----------|
| 1 | Navigate to /settings | General, Supernote, Boox sections visible |
| 2 | Edit the Boox OCR prompt, click Save | Redirects back, prompt preserved |
| 3 | Requeue a Boox note | OCR uses the custom prompt |
| 4 | Disable Supernote pipeline (unset UB_NOTES_PATH), restart | Supernote section grayed out with explanation |

### Human Verification: Other New Features

| Feature | Steps |
|---------|-------|
| Purge Completed | Complete a task, click "✗ Purge Completed", confirm. Task disappears. |
| Folder Search | Search for a term, select a folder from dropdown. Results filtered to that folder only. |
| Scan Now | Click "⟳ Scan Now" on Files tab. Deleted files removed from list after scan. |
