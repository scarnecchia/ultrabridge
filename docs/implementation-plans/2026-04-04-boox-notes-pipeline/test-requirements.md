# Test Requirements: Boox Notes Pipeline

Every acceptance criterion from the design plan is mapped below to either an automated test or a documented human verification procedure.

## Automated Tests

| AC ID | Description | Test Type | Test File | Phase |
|-------|------------|-----------|-----------|-------|
| AC1.1 | Parser extracts note title from note/pb/note_info protobuf | Unit | `internal/booxnote/note_test.go` (`TestOpen_ExtractsTitle`) | 1 |
| AC1.2 | Parser extracts page list with dimensions from virtual/page/pb | Unit | `internal/booxnote/note_test.go` (`TestOpen_ExtractsPageDimensions`) | 1 |
| AC1.3 | Parser deserializes ShapeInfoProtoList from nested shape ZIP | Unit | `internal/booxnote/note_test.go` (`TestOpen_DeserializesShapes`) | 1 |
| AC1.4 | Parser reads V1 point files: 76-byte header, xref table, 16-byte TinyPoint records | Unit | `internal/booxnote/point_test.go` (`TestParsePointFile_V1`) | 1 |
| AC1.5 | Parser correlates shapes to point data via matching UUIDs | Unit | `internal/booxnote/note_test.go` (`TestOpen_CorrelatesShapesToPoints`) | 1 |
| AC1.6 | Parser returns clear error for corrupt/truncated ZIP | Unit | `internal/booxnote/note_test.go` (`TestOpen_CorruptZIP`) | 1 |
| AC1.7 | Parser returns clear error for unsupported point file format version | Unit | `internal/booxnote/point_test.go` (`TestParsePointFile_UnsupportedVersion`) | 1 |
| AC1.8 | Parser handles notes with zero shapes (blank pages) | Unit | `internal/booxnote/note_test.go` (`TestOpen_BlankPage`) | 1 |
| AC1.9 | Parser handles multi-page notes | Unit | `internal/booxnote/note_test.go` (`TestOpen_MultiplePages`) | 1 |
| AC2.1 | Renderer produces JPEG at page native resolution | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_Resolution`) | 2 |
| AC2.2 | Stroke width varies with pressure values from point data | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_PressureVariation`) | 2 |
| AC2.4 | Colors render correctly from ARGB packed int | Unit | `internal/booxrender/color_test.go` (`TestDecodeARGB`) | 2 |
| AC2.5 | Affine transforms from matrixValues applied to strokes | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_AffineTransform`) | 2 |
| AC2.6 | Geometric shapes render from bounding rect/vertices | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_GeometricShapes`) | 2 |
| AC2.7 | Renderer handles shapes with empty point data gracefully | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_EmptyPoints`) | 2 |
| AC2.8 | Renderer handles pages with >500 shapes without excessive memory or time | Unit | `internal/booxrender/render_test.go` (`TestRenderPage_ManyShapes`) | 2 |
| AC3.1 | WebDAV endpoint accepts PUT of .note files with valid Basic Auth | Integration | `internal/webdav/handler_test.go` (`TestHandler_PUT_WithAuth`) | 3 |
| AC3.2 | Uploaded file written to UB_BOOX_NOTES_PATH preserving device path structure | Unit | `internal/webdav/fs_test.go` (`TestFS_OpenFile_WritesCorrectPath`) | 3 |
| AC3.3 | Re-upload of same path moves old file to .versions/ before writing new | Unit | `internal/webdav/fs_test.go` (`TestFS_OpenFile_VersionsOnOverwrite`) | 3 |
| AC3.4 | WebDAV PROPFIND/MKCOL work (device can browse and create directories) | Unit | `internal/webdav/fs_test.go` (`TestFS_Mkdir_and_Stat`) | 3 |
| AC3.5 | Device model, note type, folder name extracted from upload path | Unit | `internal/webdav/handler_test.go` (`TestExtractPathMetadata`) | 3 |
| AC3.6 | PUT without valid credentials returns 401 | Integration | `internal/webdav/handler_test.go` (`TestHandler_PUT_NoAuth`) | 3 |
| AC3.7 | Non-.note files accepted by WebDAV but not enqueued for processing | Unit | `internal/webdav/fs_test.go` (`TestFS_OpenFile_NonNoteNoCallback`) | 3 |
| AC3.8 | Concurrent uploads of different files don't corrupt each other | Unit | `internal/webdav/fs_test.go` (`TestFS_ConcurrentUploads`) | 3 |
| AC4.1 | WebDAV upload triggers processing job automatically | Integration | `internal/booxpipeline/store_test.go` (`TestEnqueueJob`) | 4 |
| AC4.2 | Job parses ZIP, renders all pages to cached JPEGs, OCRs each page, indexes text | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_EndToEnd`) | 4 |
| AC4.3 | OCR'd text appears in note_content/note_fts with correct path and page numbers | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_IndexesContent`) | 4 |
| AC4.4 | Re-upload triggers re-processing: old cache cleared, new pages rendered | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_ReprocessOnReupload`) | 4 |
| AC4.5 | Failed OCR marks job as failed, does not block future jobs | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_OCRFailure`) | 4 |
| AC4.6 | Corrupt .note file fails gracefully with error logged, job marked failed | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_CorruptNote`) | 4 |
| AC4.7 | Note with many pages (>10) processes all pages sequentially without timeout | Integration | `internal/booxpipeline/processor_test.go` (`TestProcessor_ManyPages`) | 4 |
| AC5.1 | Files list shows both Supernote and Boox notes with source badges | Integration | `internal/web/boox_test.go` (`TestFilesPage_ShowsBothSources`) | 5 |
| AC5.2 | Boox note detail view shows rendered page images with page navigation | Integration | `internal/web/boox_test.go` (`TestBooxRender_ServesCache`) | 5 |
| AC5.3 | Version history accessible for Boox notes with re-uploaded versions | Integration | `internal/web/boox_test.go` (`TestBooxVersions_ReturnsList`) | 5 |
| AC5.5 | Files list works correctly when Boox is enabled but no Boox notes exist | Integration | `internal/web/boox_test.go` (`TestFilesPage_NoBooxNotes`) | 5 |
| AC6.1 | Search query returns results from both Supernote and Boox notes | Integration | `internal/search/index_test.go` (`TestSearch_ReturnsMultipleSources`) | 6 |
| AC6.2 | Each search result shows correct source badge (Boox or Supernote) | Integration | `internal/web/search_test.go` (`TestSearchPage_SourceBadges`) | 6 |
| AC6.3 | Search ranking consistent across sources (BM25 unaffected by source) | Integration | `internal/search/index_test.go` (`TestSearch_BM25ConsistentAcrossSources`) | 6 |

## Human Verification

| AC ID | Description | Why Not Automated | Verification Approach |
|-------|------------|-------------------|----------------------|
| AC2.3 | Different pen types (pencil, fountain, brush, marker, calligraphy) produce visually distinct rendering | Visual fidelity is subjective; automated tests can verify non-blank output per pen type but cannot assess whether the rendered strokes look like their real-world pen counterparts. | Render a test page with identical stroke data for each pen type. Visually compare output images side-by-side against Boox device screenshots. Automated test (`TestRenderPage_PenTypes` in `internal/booxrender/render_test.go`) confirms each pen type produces a non-blank image and that marker (type 15) has semi-transparent pixels, but final visual fidelity is judged by the developer. |
| AC5.2 (partial) | Boox note detail view page navigation UX | Automated test verifies the render endpoint serves cached JPEGs; the page navigation UI (prev/next buttons, layout) requires browser interaction to confirm correctness. | Load the files detail view in a browser with a processed Boox note. Click through pages using navigation controls. Verify page images load, page numbers update, and navigation works at boundaries (first page, last page). |
| AC5.4 | Indexed content (OCR text) viewable for Boox notes via existing /files/content endpoint | The existing `/files/content` endpoint already works and is tested elsewhere; the Boox-specific aspect (that Boox OCR results land in `note_content` via the shared Indexer) is verified by AC4.3's automated test. No new HTTP test adds value. | Confirm during integration that a processed Boox note's OCR text appears when visiting `/files/content?path={boox_note_path}` in a browser. AC4.3's automated test verifies the data reaches `note_content`; this human step confirms the existing endpoint renders it. |
| AC7.1 | install.sh prompts for Boox support and configures correctly when enabled | Script prompts involve interactive terminal I/O that cannot be reliably automated in a unit test. Shellcheck validates syntax but not interactive flow. | Run `install.sh` interactively on a test host. When prompted "Enable Boox device uploads via WebDAV?", answer "y". Provide a notes path. Verify `.ultrabridge.env` contains `UB_BOOX_ENABLED=true` and `UB_BOOX_NOTES_PATH`. Verify `docker-compose.override.yml` includes the Boox volume mount. Also test unattended mode: run with `--unattended` and `UB_BOOX_ENABLED=true` env var set. Run `shellcheck install.sh` to validate syntax. |
| AC7.2 | rebuild.sh --fresh clears Boox cache and jobs but preserves .versions/ | Filesystem cleanup behavior depends on real directory structure on the deployment host. Automated tests would need to mock the full deployment layout. | Create test directories: `{BOOX_PATH}/.cache/test/page_0.jpg` and `{BOOX_PATH}/.versions/test/20260404T120000.note`. Run `rebuild.sh --fresh`. Verify `.cache/` is deleted. Verify `.versions/` and its contents are preserved. Verify the SQLite database (containing `boox_jobs`) is recreated empty. |
| AC7.3 | rebuild.sh --nuke clears all Boox data including .versions/ | Same as AC7.2: depends on real deployment filesystem layout. | Create test directories for both `.cache/` and `.versions/`. Run `rebuild.sh --nuke`. Verify both `.cache/` and `.versions/` are deleted. Verify the SQLite database is deleted. |
| AC7.4 | UB_BOOX_ENABLED=false disables all Boox functionality | Requires running the full application binary and observing startup behavior. Could theoretically be an e2e test but the overhead of spinning up the full server for a negative test is not justified. | Build and run UltraBridge with `UB_BOOX_ENABLED=false`. Verify startup logs do NOT contain "boox webdav enabled". Send a request to `/webdav/` and verify 404 (not mounted). Verify no Boox processing occurs (no `boox_jobs` table activity). |
