# internal/web

Last verified: 2026-04-04

HTTP handler and HTML templates for the UltraBridge web UI.

## Handler contract

`NewHandler(store, notifier, noteStore, searchIndex, proc, scanner, syncProvider, logger, broadcaster) *Handler`

- All domain dependencies (`noteStore`, `searchIndex`, `proc`, `scanner`, `notifier`, `syncProvider`) are **nil-safe** — passing nil disables the corresponding feature gracefully (no crash, renders an informative state).
- `Handler` implements `http.Handler` via an internal `*http.ServeMux`.

## Routes

| Method | Path | Handler | Notes |
|--------|------|---------|-------|
| GET | `/` | `handleIndex` | Task list |
| POST | `/tasks/create` | `handleCreateTask` | |
| POST | `/tasks/complete` | `handleCompleteTask` | |
| POST | `/tasks/bulk` | `handleBulkAction` | |
| GET | `/logs` | `handleLogs` (SSE) | Log stream |
| GET | `/files` | `handleFiles` | File browser; path traversal guarded |
| POST | `/files/queue` | `handleFilesQueue` | Enqueue file for OCR |
| POST | `/files/skip` | `handleFilesSkip` | Mark skipped (manual) |
| POST | `/files/unskip` | `handleFilesUnskip` | Remove manual skip |
| POST | `/files/force` | `handleFilesForce` | Unskip + enqueue (overrides size_limit) |
| GET | `/files/status` | `handleFilesStatus` | JSON: ProcessorStatus |
| GET | `/files/history` | `handleFilesHistory` | JSON: Job record for a path |
| POST | `/files/scan` | `handleFilesScan` | Trigger immediate filesystem scan |
| POST | `/processor/start` | `handleProcessorStart` | |
| POST | `/processor/stop` | `handleProcessorStop` | |
| GET | `/search` | `handleSearch` | FTS5 keyword search |
| GET | `/sync/status` | `handleSyncStatus` | JSON: SyncStatus (adapter state, timestamps) |
| POST | `/sync/trigger` | `handleSyncTrigger` | Trigger immediate sync cycle |

## Path traversal guard

`safeRelPath` validates any user-supplied `?path=` query parameter. Returns `"", false` for absolute paths or anything containing `..`. All file-browser routes call this before touching the filesystem.

## Template functions

Custom `template.FuncMap` functions registered in `NewHandler`:
- `formatDueTime(t time.Time) string`
- `formatCreated(t time.Time) string`
- `fileTypeStr(ft notestore.FileType) string` — converts FileType to its string value for template conditionals

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
