# internal/web

Last verified: 2026-04-10

HTTP handler and HTML templates for the UltraBridge web UI.

## Handler contract

`NewHandler(store, notifier, noteStore, searchIndex, proc, scanner, syncProvider, booxStore, booxImporter, booxNotesPath, notesPathPrefix, noteDB, logger, broadcaster, embedder, embedStore, embedModel, retriever, chatHandler, chatStore, ragDisplay, runningConfig) *Handler`

- All domain dependencies (`noteStore`, `searchIndex`, `proc`, `scanner`, `notifier`, `syncProvider`) are **nil-safe** — passing nil disables the corresponding feature gracefully (no crash, renders an informative state).
- `booxStore` is **nil-safe** — when nil, Boox-specific routes return empty lists and the UI shows only Supernote notes.
- `booxImporter` is **nil-safe** — when nil, bulk import routes return an error response.
- `booxNotesPath` is a string path (may be empty if Boox is disabled).
- `notesPathPrefix` is the device file path prefix for rendering note page images in the API.
- `noteDB` is the shared SQLite DB for settings and notes (may be nil; when nil, config/sources API and MCP token routes are disabled).
- `embedder` is the RAG embedder implementation (nil-safe, feature disabled when nil).
- `embedStore` is the embedding store for backfill and vector search (nil-safe).
- `embedModel` is the embedding model name.
- `retriever` is the hybrid search retriever interface (nil-safe; when nil, JSON API endpoints are disabled).
- `chatHandler` / `chatStore` are chat subsystem dependencies (nil-safe; when nil, chat routes are disabled).
- `ragDisplay` is a `RAGDisplayConfig` struct with display URLs/models for the settings UI.
- `runningConfig` is the `*appconfig.Config` loaded at startup; used for drift detection (restart banner).
- `Handler` implements `http.Handler` via an internal `*http.ServeMux`.

## Routes

| Method | Path | Handler | Notes |
|--------|------|---------|-------|
| GET | `/setup` | `handleSetup` | First-run setup page |
| POST | `/setup/save` | `handleSetupSave` | Save initial credentials |
| GET | `/` | `handleIndex` | Task list |
| POST | `/tasks` | `handleCreateTask` | |
| POST | `/tasks/{id}/complete` | `handleCompleteTask` | |
| POST | `/tasks/bulk` | `handleBulkAction` | |
| POST | `/tasks/purge-completed` | `handlePurgeCompleted` | |
| GET | `/logs` | `handleLogs` (SSE) | Log stream |
| GET | `/settings` | `handleSettings` | Settings page (config + MCP tokens) |
| POST | `/settings/save` | `handleSettingsSave` | Save config changes |
| GET | `/files` | `handleFiles` | File browser; path traversal guarded |
| POST | `/files/queue` | `handleFilesQueue` | Enqueue file for OCR |
| POST | `/files/skip` | `handleFilesSkip` | Mark skipped (manual) |
| POST | `/files/unskip` | `handleFilesUnskip` | Remove manual skip |
| POST | `/files/force` | `handleFilesForce` | Unskip + enqueue (overrides size_limit) |
| GET | `/files/status` | `handleFilesStatus` | JSON: ProcessorStatus |
| GET | `/files/history` | `handleFilesHistory` | JSON: Job record for a path |
| GET | `/files/boox/render` | `handleBooxRender` | JPEG page image for Boox note |
| GET | `/files/boox/versions` | `handleBooxVersions` | JSON: []BooxVersion for archived versions |
| POST | `/files/import` | `handleFilesImport` | Bulk import from configured import path |
| POST | `/files/retry-failed` | `handleFilesRetryFailed` | Reset all failed Boox jobs to pending |
| POST | `/files/delete-note` | `handleFilesDeleteNote` | Delete single Boox note + jobs + content + cache |
| POST | `/files/delete-bulk` | `handleFilesDeleteBulk` | Delete multiple Boox notes |
| POST | `/files/migrate-imports` | `handleFilesMigrateImports` | Copy imported files to Boox notes directory |
| POST | `/files/scan` | `handleFilesScan` | Trigger immediate filesystem scan |
| POST | `/processor/start` | `handleProcessorStart` | |
| POST | `/processor/stop` | `handleProcessorStop` | |
| GET | `/search` | `handleSearch` | FTS5 keyword search |
| GET | `/sync/status` | `handleSyncStatus` | JSON: SyncStatus (adapter state, timestamps) |
| POST | `/sync/trigger` | `handleSyncTrigger` | Trigger immediate sync cycle |
| GET | `/api/search` | `handleAPISearch` | JSON: hybrid search results (requires retriever) |
| GET | `/api/notes/pages` | `handleAPIGetPages` | JSON: indexed page content for a note (requires retriever) |
| GET | `/api/notes/pages/image` | `handleAPIGetImage` | JPEG image for a note page (requires retriever) |
| POST | `/settings/mcp-tokens/create` | `handleMCPTokenCreate` | Create new MCP bearer token; redirect with one-time display (requires noteDB) |
| POST | `/settings/mcp-tokens/revoke` | `handleMCPTokenRevoke` | Revoke MCP token by hash (requires noteDB) |

## Interfaces

### BooxImporter
```go
type BooxImporter interface {
    ScanAndEnqueue(ctx context.Context, cfg ImportConfig, logger *slog.Logger) ImportResult
    MigrateImportedFiles(ctx context.Context, importPath, notesPath string, logger *slog.Logger) MigrateResult
}
```
Implemented by `booxpipeline.Importer`. Handles bulk import of .note and .pdf files from a configured import path.

### BooxStore (extended)
In addition to previously documented methods, `BooxStore` now includes:
- `RetryAllFailed(ctx) (int64, error)` — reset all failed jobs to pending; returns count reset
- `DeleteNote(ctx, path) error` — delete note row, associated jobs, content index entries, and rendered cache
- `SkipNote(ctx, path) error` — mark note's pending job as skipped
- `UnskipNote(ctx, path) error` — reset a skipped job to pending
- `GetQueueStatus(ctx) (QueueStatus, error)` — return counts of jobs by status

## JSON API Endpoints

### Search & Notes API (requires retriever)

- `GET /api/search?q=...&folder=...&device=...&from=...&to=...&limit=...` -- hybrid search using SearchRetriever
- `GET /api/notes/pages?path=...` -- fetch indexed content for a note (all pages)
- `GET /api/notes/pages/image?path=...&page=...` -- render JPEG image for a page

Conditional: only registered if `retriever` is non-nil.

### Config & Sources API (requires noteDB)

- `GET /api/config` -- returns RedactedConfig (secrets shown as "[set]"/"[not set]")
- `PUT /api/config` -- accepts JSON config update, returns SaveResult with changed keys and restart flag
- `GET /api/sources` -- list all source rows
- `POST /api/sources` -- add source (validates type, name, config_json)
- `PUT /api/sources/{id}` -- update source row
- `DELETE /api/sources/{id}` -- remove source row

Conditional: only registered if `noteDB` is non-nil.

Auth: All API routes use the same Basic Auth middleware as the web UI (authMW in main.go).

## Setup Mode

`SetupMiddleware(db, next)` -- HTTP middleware that redirects all requests to `/setup` when no credentials are configured (username + password_hash missing from settings table). Uses atomic flag for fast path after setup completes. Setup page (`/setup`) accepts initial username and password, saves bcrypt hash via appconfig, then allows normal access.

## Path traversal guard

`safeRelPath` validates any user-supplied `?path=` query parameter. Returns `"", false` for absolute paths or anything containing `..`. All file-browser routes call this before touching the filesystem.

## Template functions

Custom `template.FuncMap` functions registered in `NewHandler`:
- `formatDueTime(t time.Time) string`
- `formatCreated(t time.Time) string`
- `formatTimestamp(ms int64) string` — formats millisecond UTC unix timestamp to "2006-01-02 15:04"; returns "Never" if 0
- `fileTypeStr(ft notestore.FileType) string` — converts FileType to its string value for template conditionals
- `noteSource(path string) string` — returns "Boox" if path starts with booxNotesPath, else "Supernote"

## Template data

Shared data in `baseTemplateData`:
- `tasks` — list of tasks for the task list page
- `BooxNotesPath` — the Boox notes root directory path (may be empty if disabled); used by JavaScript to detect Boox notes

## MCP Token Management (Phase 3c)

Settings page includes an MCP Tokens card (rendered when noteDB is present):
- Lists all active tokens with label, hash prefix (first 8 chars), creation timestamp, and last-used timestamp
- One-time display of raw token after creation via `?new_token=` query parameter (redirect-after-POST pattern)
- Creates bearer tokens for MCP clients via POST `/settings/mcp-tokens/create` with form field `label`
- Revokes tokens via POST `/settings/mcp-tokens/revoke` with form field `token_hash`
- Both endpoints are **nil-safe**: only registered if `noteDB != nil`
- Handler methods: `handleMCPTokenCreate`, `handleMCPTokenRevoke`
- Uses `mcpauth.CreateToken`, `mcpauth.ListTokens`, `mcpauth.RevokeToken` (Phase 1)
- Data keys: `MCPTokensEnabled` (bool), `MCPTokens` ([]mcpauth.TokenInfo), `NewMCPToken` (raw token string, one-time flash)

## Error handling pattern

All `ExecuteTemplate` calls check and log the error (`h.logger.Error`). Since headers are already written at that point, `http.Error` is not called — logging is the only recovery path.

All POST handlers to processor methods (`Enqueue`, `Skip`, `Unskip`, `Start`, `Stop`) check and log errors via `h.logger.Error`.

## Tests

`handler_test.go` uses:
- `newMockTaskStore()` — in-memory task store
- `mockNotifier` — no-op SyncNotifier
- `mockNoteStore` — configurable file map per relPath
- `mockSearchIndex` — no-op SearchIndex
- `mockProcessor` — in-memory job map; tracks running state
- `mockScanner` — counts ScanNow calls
- `mockSyncProvider` — configurable SyncStatus; tracks TriggerSync call count
- `mockBooxStore` — implements BooxStore interface; returns configurable notes and versions; nil-safe (can be passed as nil to test non-Boox configuration); includes stub implementations of RetryAllFailed, DeleteNote, SkipNote, UnskipNote, GetQueueStatus
