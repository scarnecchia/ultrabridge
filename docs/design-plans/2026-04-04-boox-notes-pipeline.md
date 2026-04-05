# Boox Notes Pipeline Design

## Summary

UltraBridge is adding a second notes pipeline alongside the existing Supernote pipeline to support Boox e-ink devices. Boox devices can auto-export handwritten notes as `.note` files — ZIP archives containing protobuf metadata and binary stroke data — to a WebDAV server. This design describes a new WebDAV ingestion endpoint, a format-specific parser and stroke renderer, a job-based processing pipeline, and Web UI changes to present Boox notes alongside Supernote notes in a unified interface.

The approach is additive and deliberately parallel to the Supernote pipeline rather than an extension of it. The two pipelines share only what is genuinely common: the OCR client, the FTS5 full-text search index, and the indexing interface. Everything specific to Boox — the ZIP/protobuf parser, the stroke renderer, the WebDAV server, and the job queue — lives in new packages. This separation keeps the existing Supernote pipeline unchanged while allowing the Boox pipeline to handle its own ingestion model (device push via WebDAV rather than filesystem watch), its own rendering requirements (pressure-sensitive strokes from binary point data), and its own versioning semantics (overwrite-and-archive rather than backup-before-modify).

## Definition of Done

UltraBridge accepts Boox .note files via a WebDAV server endpoint (authenticated with existing UB Basic Auth credentials), parses them using the reverse-engineered Couchbase Lite + point file format, renders pages as visually faithful images (pressure-sensitive width, pen type simulation, colors, transforms), runs those images through VLM OCR, and indexes the extracted text in the existing FTS5 search index. Boox notes appear in the files list with a source indicator and their rendered pages are viewable in a details view. Users can search across both Supernote and Boox notes from a single search interface. When a note is re-uploaded, the previous version is archived, the new version is processed as current (re-rendered, re-OCR'd, re-indexed), and previous versions are retained for future history viewing.

## Acceptance Criteria

### boox-notes-pipeline.AC1: Parser reads Boox .note format
- **boox-notes-pipeline.AC1.1 Success:** Parser extracts note title from note/pb/note_info protobuf
- **boox-notes-pipeline.AC1.2 Success:** Parser extracts page list with dimensions from virtual/page/pb
- **boox-notes-pipeline.AC1.3 Success:** Parser deserializes ShapeInfoProtoList from nested shape ZIP, extracting shapeType, color, thickness, boundingRect, matrixValues
- **boox-notes-pipeline.AC1.4 Success:** Parser reads V1 point files: 76-byte header, xref table, 16-byte TinyPoint records (x, y, pressure, size, time)
- **boox-notes-pipeline.AC1.5 Success:** Parser correlates shapes to point data via matching UUIDs
- **boox-notes-pipeline.AC1.6 Failure:** Parser returns clear error for corrupt/truncated ZIP
- **boox-notes-pipeline.AC1.7 Failure:** Parser returns clear error for unsupported point file format version
- **boox-notes-pipeline.AC1.8 Edge:** Parser handles notes with zero shapes (blank pages)
- **boox-notes-pipeline.AC1.9 Edge:** Parser handles multi-page notes

### boox-notes-pipeline.AC2: Renderer produces visually faithful page images
- **boox-notes-pipeline.AC2.1 Success:** Renderer produces JPEG at page native resolution (e.g., 1860×2480)
- **boox-notes-pipeline.AC2.2 Success:** Stroke width varies with pressure values from point data
- **boox-notes-pipeline.AC2.3 Success:** Different pen types (pencil, fountain, brush, marker, calligraphy) produce visually distinct rendering
- **boox-notes-pipeline.AC2.4 Success:** Colors render correctly from ARGB packed int
- **boox-notes-pipeline.AC2.5 Success:** Affine transforms from matrixValues are applied to strokes
- **boox-notes-pipeline.AC2.6 Success:** Geometric shapes (circle, rectangle, line) render from bounding rect/vertices
- **boox-notes-pipeline.AC2.7 Edge:** Renderer handles shapes with empty point data gracefully (skip, no crash)
- **boox-notes-pipeline.AC2.8 Edge:** Renderer handles pages with >500 shapes without excessive memory or time

### boox-notes-pipeline.AC3: WebDAV server accepts uploads with auth and versioning
- **boox-notes-pipeline.AC3.1 Success:** WebDAV endpoint at /webdav/ accepts PUT of .note files with valid Basic Auth credentials
- **boox-notes-pipeline.AC3.2 Success:** Uploaded file written to UB_BOOX_NOTES_PATH preserving the device path structure (/onyx/{model}/{type}/{folder}/{name}.note)
- **boox-notes-pipeline.AC3.3 Success:** Re-upload of same path moves old file to .versions/ before writing new
- **boox-notes-pipeline.AC3.4 Success:** WebDAV PROPFIND/MKCOL work (Boox device can browse and create directories)
- **boox-notes-pipeline.AC3.5 Success:** Device model, note type (Notebooks/Reading Notes), and folder name extracted from upload path and stored in boox_notes metadata
- **boox-notes-pipeline.AC3.6 Failure:** PUT without valid credentials returns 401
- **boox-notes-pipeline.AC3.7 Failure:** Non-.note files accepted by WebDAV but not enqueued for processing
- **boox-notes-pipeline.AC3.8 Edge:** Concurrent uploads of different files don't corrupt each other

### boox-notes-pipeline.AC4: Processing pipeline runs end-to-end
- **boox-notes-pipeline.AC4.1 Success:** WebDAV upload triggers processing job automatically
- **boox-notes-pipeline.AC4.2 Success:** Job parses ZIP, renders all pages to cached JPEGs, OCRs each page, indexes text
- **boox-notes-pipeline.AC4.3 Success:** OCR'd text appears in note_content/note_fts tables with correct path and page numbers
- **boox-notes-pipeline.AC4.4 Success:** Re-upload triggers re-processing: old cache cleared, new pages rendered, re-OCR'd, re-indexed
- **boox-notes-pipeline.AC4.5 Failure:** Failed OCR marks job as failed, does not block future jobs
- **boox-notes-pipeline.AC4.6 Failure:** Corrupt .note file fails gracefully with error logged, job marked failed
- **boox-notes-pipeline.AC4.7 Edge:** Note with many pages (>10) processes all pages sequentially without timeout

### boox-notes-pipeline.AC5: Web UI shows Boox notes with source indicators
- **boox-notes-pipeline.AC5.1 Success:** Files list shows both Supernote and Boox notes with source badges
- **boox-notes-pipeline.AC5.2 Success:** Boox note detail view shows rendered page images with page navigation
- **boox-notes-pipeline.AC5.3 Success:** Version history accessible for Boox notes with re-uploaded versions
- **boox-notes-pipeline.AC5.4 Success:** Indexed content (OCR text) viewable for Boox notes via existing /files/content endpoint
- **boox-notes-pipeline.AC5.5 Edge:** Files list works correctly when Boox is enabled but no Boox notes exist yet

### boox-notes-pipeline.AC6: Unified search across note sources
- **boox-notes-pipeline.AC6.1 Success:** Search query returns results from both Supernote and Boox notes
- **boox-notes-pipeline.AC6.2 Success:** Each search result shows correct source badge (Boox or Supernote)
- **boox-notes-pipeline.AC6.3 Success:** Search ranking is consistent across sources (BM25 scoring unaffected by source)

### boox-notes-pipeline.AC7: Deployment configuration
- **boox-notes-pipeline.AC7.1 Success:** install.sh prompts for Boox support and configures correctly when enabled
- **boox-notes-pipeline.AC7.2 Success:** rebuild.sh --fresh clears Boox cache and jobs but preserves .versions/
- **boox-notes-pipeline.AC7.3 Success:** rebuild.sh --nuke clears all Boox data including .versions/
- **boox-notes-pipeline.AC7.4 Success:** UB_BOOX_ENABLED=false disables all Boox functionality (no WebDAV mount, no processing)

## Glossary

- **Boox / BOOX**: Brand of e-ink tablets made by Onyx International. Their Notes app can auto-export handwritten notebooks to a WebDAV server as `.note` files.
- **`.note` file (Boox)**: A ZIP archive exported by Boox devices containing protobuf metadata files and binary stroke point files for one notebook. Not the same format as Supernote `.note` files.
- **WebDAV**: A protocol extension to HTTP that allows clients to create, move, and manage files on a remote server. Boox devices use it as the transport for auto-exporting notes.
- **Protobuf / Protocol Buffers**: Google's binary serialization format. The Boox `.note` ZIP uses protobuf for structured metadata (note info, page dimensions, shape lists).
- **ShapeInfoProtoList**: The protobuf message type inside the nested shape ZIP within a Boox `.note` file. Each entry describes one shape: its type, color, thickness, bounding rectangle, and affine transform.
- **TinyPoint / point file**: A binary file inside the Boox `.note` ZIP that records raw input events for one shape: x, y, pressure, size, and timestamp in 16-byte records, preceded by a 76-byte header and an xref table.
- **FTS5**: SQLite's built-in full-text search extension. UltraBridge uses it for the `note_fts` virtual table that indexes OCR'd text from all note sources.
- **VLM OCR**: Vision Language Model-based optical character recognition. UltraBridge sends rendered page images to a VLM (via `OCRClient`) to extract text, rather than using a traditional OCR engine.
- **`fogleman/gg`**: A Go 2D graphics library used for rasterizing Boox strokes onto a canvas. Handles drawing, path rendering, and affine transforms.
- **Affine transform / matrixValues**: A matrix encoding rotation, scaling, shearing, and translation. Boox shapes carry a `matrixValues` array applied to stroke coordinates before rendering.
- **ARGB**: A 32-bit packed integer encoding Alpha, Red, Green, Blue color channels. Boox shape color is stored in this format.
- **`golang.org/x/net/webdav`**: The Go standard library extension package implementing the WebDAV server protocol. Used with a custom `FileSystem` backend.
- **PROPFIND / MKCOL / PUT**: WebDAV HTTP methods. PROPFIND retrieves directory/file metadata, MKCOL creates a directory, PUT uploads a file. Boox devices issue all three during auto-export.
- **`Indexer` interface**: A Go interface defined in `internal/processor/` with a single `IndexPage()` method. Both pipelines call it to write OCR results into the shared FTS5 index.
- **`OCRClient`**: A Go interface wrapping the VLM API call. Shared between both pipelines so they use the same API key and model configuration.
- **Version-on-overwrite**: The Boox versioning strategy: when a re-upload arrives, the current file is moved to `.versions/` before the new file is written. Contrasts with the Supernote pipeline's backup-before-modify approach.

## Architecture

Separate Boox notes pipeline running alongside the existing Supernote pipeline, sharing only the FTS5 search index and OCR client.

### Components

```
Boox Device                UltraBridge
┌──────────┐     PUT .note  ┌─────────────────────────────────────────┐
│ Notes App ├──────────────►│ WebDAV Handler (/webdav/)               │
│ (auto-    │   Basic Auth  │  └► Filesystem backend                  │
│  export)  │               │      └► Version old file if exists      │
└──────────┘               │      └► Write to UB_BOOX_NOTES_PATH     │
                           │      └► Enqueue processing job           │
                           │                                          │
                           │ Boox Processor                           │
                           │  ├► Parse ZIP (.note format)             │
                           │  ├► Render pages (fogleman/gg)           │
                           │  ├► OCR via shared VLM client            │
                           │  └► Index via shared Indexer             │
                           │                                          │
                           │ Shared Search Index (FTS5)               │
                           │  ├► note_content (Supernote + Boox)      │
                           │  └► note_fts (unified full-text search)  │
                           │                                          │
                           │ Web UI                                   │
                           │  ├► /files — merged list, source badges  │
                           │  ├► /files/boox/render — page images     │
                           │  └► /search — unified results            │
                           └─────────────────────────────────────────┘
```

### New Packages

- **`internal/booxnote/`** — Parser for the Boox .note ZIP format. Reads protobuf metadata (note info, pages, shapes) and binary point files. Pure library, no side effects.
- **`internal/booxrender/`** — Stroke renderer using `fogleman/gg`. Takes parsed page data, produces `image.Image`. Handles pressure-sensitive width, pen types, colors, affine transforms.
- **`internal/booxpipeline/`** — Processing pipeline: job queue, worker loop, orchestrates parse → render → OCR → index. Analogous to `internal/processor/` but Boox-specific.

### Modified Packages

- **`internal/web/`** — New routes for Boox file rendering and version history. Files list merges both sources with source indicators.
- **`internal/config/`** — New env vars: `UB_BOOX_ENABLED`, `UB_BOOX_NOTES_PATH`.
- **`internal/notedb/`** — New tables: `boox_notes`, `boox_jobs`.
- **`cmd/ultrabridge/`** — Wire Boox components when `UB_BOOX_ENABLED=true`.

### Data Flow

1. Boox device PUT `.note` file to `/webdav/{filename}`
2. WebDAV handler authenticates (Basic Auth), writes to `UB_BOOX_NOTES_PATH`
3. If file exists at that path, old file moved to `.versions/{filename}/{timestamp}.note`
4. WebDAV handler enqueues a Boox processing job
5. Boox processor claims job:
   a. Opens ZIP, parses protobuf note_info → extracts title, page list, device info
   b. For each page: parses shape protobuf + reads point files → renders to JPEG → caches at `.cache/{noteId}/page_{N}.jpg`
   c. Sends each JPEG to VLM OCR (shared `OCRClient`)
   d. Indexes OCR text via shared `Indexer` into `note_content`/`note_fts`
6. Web UI serves merged files list, Boox page images from cache, unified search results

### Shared Interfaces

The Boox pipeline reuses two interfaces from the existing codebase:

```go
// processor.Indexer — already defined in internal/processor/processor.go
type Indexer interface {
    IndexPage(ctx context.Context, path string, pageIdx int,
        source, bodyText, titleText, keywords string) error
}

// processor.OCRClient — already defined in internal/processor/ocrclient.go
// Boox pipeline uses the same OCR client instance and configuration.
```

### WebDAV Server

Uses `golang.org/x/net/webdav` with a custom `FileSystem` backend. Mounted at `/webdav/` on the existing HTTP listener (same port, same auth). The backend implements:

- `OpenFile` / `Stat` / `ReadDir` for PROPFIND (directory listing)
- `Create` for PUT (file upload) — triggers versioning + job enqueue
- `RemoveAll` for DELETE
- `Mkdir` for MKCOL (directory creation)

Boox devices expect a spec-compliant WebDAV server for auto-export to work reliably.

### Upload Path Structure

Boox devices export notes with this path convention:
```
/onyx/{device_model}/{Notebooks|Reading Notes}/{folder_name}/{notebook_name}.note
```

The WebDAV backend preserves this structure on disk and extracts metadata from the path components: device model, note type (Notebooks vs Reading Notes), and folder name. These are stored in `boox_notes` for display and filtering.

### Boox .note File Format

The exported `.note` file is a **ZIP archive** containing:

```
{noteId}/
  note/pb/note_info              — protobuf: title, pen settings, page list, device info
  virtual/doc/pb/{docId}         — protobuf: document structure
  virtual/page/pb/{pageId}       — protobuf: page dimensions (e.g., 1860×2480)
  pageModel/pb/{pageModelId}     — protobuf: layer info
  shape/{pageId}#...zip          — nested ZIP of protobuf ShapeInfoProtoList
  point/{pageId}/{shapeId}#points — V1 point files (76-byte header, xref, 16-byte TinyPoints)
  template/json/...              — page template/background
  tag/pb/...                     — tags
  resource/pb/...                — resource references
  extra/pb/extra                 — extra metadata
```

No Couchbase Lite databases, no Fleece encoding. Parsing requires only `archive/zip`, `google.golang.org/protobuf`, and custom binary point file reader.

### Stroke Rendering

`fogleman/gg` renders strokes to JPEG images at the page's native resolution.

For each scribble-type shape (types 2, 3, 4, 5, 15, 21, 22, 47, 60, 61):
- Apply affine transform from shape's `matrixValues`
- Set color from ARGB packed int
- Render connected line segments with pressure-driven width variation
- Per-type visual treatment (pencil: thin/uniform, fountain: strong pressure response, brush: wide range, marker: semi-transparent/wide, calligraphy: angle-sensitive)

Geometric shapes rendered from bounding rect/vertices. Text shapes rendered at specified position (best-effort font matching).

### Note Versioning

Filesystem-based versioning:
- Current file at canonical path: `{UB_BOOX_NOTES_PATH}/{filename}.note`
- Previous versions at: `{UB_BOOX_NOTES_PATH}/.versions/{filename}/{timestamp}.note`
- `boox_notes.version` column increments on each upload
- Rendered page cache cleared and regenerated on update
- `note_content` rows replaced via existing upsert-on-conflict

## Existing Patterns

### Patterns Followed

- **Job queue model:** `boox_jobs` follows the same status lifecycle as `jobs` (pending → in_progress → done/failed/skipped) with single-worker atomic claim
- **Indexer interface:** Boox pipeline calls the same `IndexPage()` method, writing to the same `note_content`/`note_fts` tables
- **OCR client reuse:** Same `OCRClient` instance, same API key, same VLM model
- **Config pattern:** New env vars follow `UB_` prefix convention (`UB_BOOX_ENABLED`, `UB_BOOX_NOTES_PATH`)
- **Web handler pattern:** New routes follow existing `/files/...` prefix with format-specific sub-paths
- **Feature gating:** `UB_BOOX_ENABLED` follows the `UB_SN_SYNC_ENABLED` pattern — disabled by default, all Boox code gated behind it
- **Script updates:** install.sh/rebuild.sh updated for Boox configuration, following the interactive prompt pattern

### Divergences

- **Separate pipeline:** Unlike extending the existing processor, Boox gets its own pipeline package. Justified because Boox processing has no RECOGNTEXT extraction/injection, no SPC catalog sync, and adds versioning — these differences would require extensive branching in the shared processor.
- **Filesystem versioning:** Supernote pipeline uses backup-before-modify. Boox uses version-on-overwrite since the source of truth is the device (re-uploads replace the file), not UltraBridge.
- **WebDAV ingestion:** New ingestion path alongside fsnotify/Engine.IO. The WebDAV handler directly enqueues jobs rather than going through the pipeline watcher.

## Implementation Phases

<!-- START_PHASE_1 -->
### Phase 1: Boox .note Parser
**Goal:** Pure-Go library that reads the Boox .note ZIP format and produces structured data

**Components:**
- Protobuf schema reconstruction in `internal/booxnote/proto/` — `ShapeInfoProto`, note info, page info messages generated from BOOX_STROKE_FORMAT.md and sample data analysis
- ZIP reader and format parser in `internal/booxnote/` — opens ZIP, navigates directory structure, deserializes protobuf metadata
- Point file reader in `internal/booxnote/` — reads V1 point files (76-byte header, xref table, 16-byte TinyPoint records)
- Shape-to-point correlation — matches shape UUIDs from protobuf to point data via xref entries

**Dependencies:** None (first phase)

**Done when:** Parser loads the sample `.note` file, extracts title/page dimensions/shape metadata/stroke points, all tests pass. Covers `boox-notes-pipeline.AC1.*`.
<!-- END_PHASE_1 -->

<!-- START_PHASE_2 -->
### Phase 2: Stroke Renderer
**Goal:** Render parsed Boox pages to visually faithful JPEG images

**Components:**
- Page renderer in `internal/booxrender/` — creates canvas at page dimensions, iterates shapes in z-order, renders strokes/geometry/text
- Pressure-sensitive stroke rendering — variable line width driven by TinyPoint pressure values and shape thickness
- Pen type simulation — per-shapeType visual treatment (pencil, fountain, brush, marker, calligraphy)
- Color and transform support — ARGB color decoding, affine transform application from matrixValues

**Dependencies:** Phase 1 (parser provides structured data)

**Done when:** Renderer produces readable page images from the sample `.note` file with pressure variation, pen types, and colors visible. Covers `boox-notes-pipeline.AC2.*`.
<!-- END_PHASE_2 -->

<!-- START_PHASE_3 -->
### Phase 3: WebDAV Server
**Goal:** WebDAV endpoint that accepts Boox .note uploads with auth and versioning

**Components:**
- WebDAV handler in `internal/webdav/` — `golang.org/x/net/webdav` with custom `FileSystem` backend
- Filesystem backend — writes to `UB_BOOX_NOTES_PATH`, version-on-overwrite to `.versions/` subdirectory
- HTTP mux integration in `cmd/ultrabridge/` — mount at `/webdav/` on existing listener, Basic Auth middleware
- Config additions in `internal/config/` — `UB_BOOX_ENABLED`, `UB_BOOX_NOTES_PATH`

**Dependencies:** None (can be built in parallel with Phase 1-2, but listed after for logical ordering)

**Done when:** Boox device can configure WebDAV target, authenticate, upload `.note` files, re-upload triggers versioning. Covers `boox-notes-pipeline.AC3.*`.
<!-- END_PHASE_3 -->

<!-- START_PHASE_4 -->
### Phase 4: Boox Processing Pipeline
**Goal:** Job queue that orchestrates parse → render → OCR → index for Boox notes

**Components:**
- Schema additions in `internal/notedb/` — `boox_notes` table (path, title, device_model, note_type, folder, page_count, file_hash, version, created_at, updated_at) and `boox_jobs` table
- Pipeline processor in `internal/booxpipeline/` — worker loop, job claiming, status management (mirrors `internal/processor/` patterns)
- Processing stages — parse ZIP, render pages to cache, OCR via shared `OCRClient`, index via shared `Indexer`
- WebDAV integration — WebDAV handler enqueues job after successful write
- Re-processing on update — clear cached renders, re-parse, re-render, re-OCR, re-index

**Dependencies:** Phase 1 (parser), Phase 2 (renderer), Phase 3 (WebDAV triggers jobs)

**Done when:** End-to-end flow works: upload `.note` via WebDAV → pages rendered → OCR'd → text appears in `note_content`. Re-upload replaces content. Covers `boox-notes-pipeline.AC4.*`.
<!-- END_PHASE_4 -->

<!-- START_PHASE_5 -->
### Phase 5: Web UI Integration
**Goal:** Boox notes visible in files list with source indicators and page image viewing

**Components:**
- Files list merge in `internal/web/` — query both Supernote notestore and `boox_notes` table, add source badge (Boox/Supernote) to each entry
- Boox page render endpoint — `GET /files/boox/render?path={abs}&page={N}` serves cached JPEG
- Boox version history endpoint — `GET /files/boox/versions?path={abs}` returns archived versions list
- Template updates in `internal/web/templates/` — source indicator styling, Boox details view with page navigation

**Dependencies:** Phase 4 (pipeline populates data and renders)

**Done when:** Boox notes appear in files list with "Boox" badge, rendered pages viewable, version history accessible. Covers `boox-notes-pipeline.AC5.*`.
<!-- END_PHASE_5 -->

<!-- START_PHASE_6 -->
### Phase 6: Unified Search
**Goal:** Search results include both Supernote and Boox notes with source indicators

**Components:**
- Search result source detection in `internal/web/` — determine source from note path prefix (`UB_NOTES_PATH` vs `UB_BOOX_NOTES_PATH`)
- Search result template updates — source badge on each result
- No changes to `internal/search/` — FTS5 index already contains both sources via shared `Indexer`

**Dependencies:** Phase 4 (Boox content indexed), Phase 5 (source indicator pattern established)

**Done when:** Search query returns results from both Supernote and Boox notes, each with correct source badge. Covers `boox-notes-pipeline.AC6.*`.
<!-- END_PHASE_6 -->

<!-- START_PHASE_7 -->
### Phase 7: Deployment Configuration
**Goal:** install.sh/rebuild.sh support Boox configuration, Docker volume setup

**Components:**
- install.sh updates — "Enable Boox note support?" prompt, env var setup, volume mount for Boox notes directory
- rebuild.sh updates — `--fresh` clears Boox rendered cache and jobs (preserves `.versions/`), `--nuke` clears everything
- Docker Compose — volume mount for `UB_BOOX_NOTES_PATH`
- Documentation — Boox device WebDAV configuration instructions in README or inline help

**Dependencies:** Phase 3-6 (all Boox functionality exists)

**Done when:** Fresh install with Boox enabled works end-to-end. `--fresh` and `--nuke` handle Boox data correctly. Covers `boox-notes-pipeline.AC7.*`.
<!-- END_PHASE_7 -->

## Additional Considerations

**Pen type fidelity is iterative.** The initial renderer implements pressure-based width variation for all pen types. Per-type visual treatments (fountain pen taper, marker transparency, calligraphy angle sensitivity) are refined by comparing rendered output against device display. The OCR pipeline doesn't depend on visual fidelity — legible strokes are sufficient for text recognition.

**Multi-page notes.** The sample file has one page, but Boox notes can have many. The parser/renderer handle arbitrary page counts. Each page is rendered and OCR'd independently.

**Template backgrounds.** The `.note` format includes template JSON (e.g., lined paper backgrounds). The initial renderer uses a white background. Template rendering can be added later without changing the pipeline architecture.

**Point file format versions.** The current implementation handles V1 point files. If Boox firmware introduces new versions, the parser should detect the version from the header and fail gracefully with a clear error rather than producing corrupt output.
