# Changelog

## v0.5.0 — 2026-04-05

First public release.

### Features

**CalDAV Task Sync**
- Full CalDAV VTODO collection at `/caldav/tasks/`
- Compatible with DAVx5, GNOME Evolution, Apple Reminders, 2Do, and other CalDAV clients
- Bidirectional sync with Supernote device via SPC REST API
- SQLite-backed task store (works standalone without MariaDB)

**Supernote Notes Pipeline**
- Automatic `.note` file discovery via fsnotify watcher + 15-minute reconciler
- Handwritten text extraction from MyScript RECOGNTEXT
- Optional vision-API OCR (Anthropic, OpenRouter, vLLM/Ollama)
- JIIX RECOGNTEXT injection back into `.note` files for on-device display
- Backup before modification
- SPC catalog sync after file changes

**Boox Notes Pipeline**
- WebDAV server at `/webdav/` for Boox device uploads
- Parses Boox `.note` ZIP format (protobuf metadata, nested shape ZIPs, V1 binary point files)
- Renders pages with pressure-sensitive strokes, 10 pen types, geometric shapes, affine transforms
- OCR via shared vision API
- Version-on-overwrite: old files archived to `.versions/` with nanosecond timestamps
- Device model, note type, and folder extracted from upload path

**Red Ink To-Do Extraction**
- Optional second OCR pass on Boox notes looking for red handwriting
- Red text automatically created as CalDAV tasks
- Duplicate detection against both incomplete and completed tasks
- Configurable prompt via Settings tab

**Unified Search**
- FTS5 full-text search across both Supernote and Boox notes
- Source badges (SN / B) on search results
- Folder filter dropdown
- BM25 ranking consistent across sources

**Web UI**
- Five tabs: Tasks, Files, Search, Logs, Settings
- Source badges distinguish Supernote and Boox notes throughout
- Rendered Boox page viewing with version history
- Per-pipeline OCR prompt configuration
- Live WebSocket log streaming with level filter
- Scan Now, Purge Completed, and bulk task actions

**Deployment**
- Interactive `install.sh` with auto-detection of Supernote Private Cloud
- Standalone mode for Boox-only users (no SPC/MariaDB required)
- `rebuild.sh` with `--fresh` (preserves versions) and `--nuke` (clears all)
- Polling health checks with progress reporting

### Technical Details

- Pure Go, single binary, Docker deployment
- SQLite (WAL mode, pure-Go via modernc.org/sqlite) for tasks, notes pipeline, and settings
- 145+ automated tests across 8 packages
- Protobuf wire-format parsing tolerant of non-UTF-8 device firmware output
- Shared `Indexer` interface for unified search across both pipelines
