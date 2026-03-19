# Pipeline

Last verified: 2026-03-19

## Purpose
Owns file detection: discovers new/changed .note files and feeds them to the
processor queue. Three detection strategies run concurrently.

## Contracts
- **Exposes**: `Pipeline` (New, Start, Close), `Config` struct.
- **Guarantees**: Initial reconciliation scan on startup. Continuous fsnotify watching with 2-second per-path debounce. Periodic reconciliation every 15 minutes. Only .note files are enqueued.
- **Expects**: `notestore.NoteStore` for scanning/upserting, `processor.Processor` for enqueueing, optional Engine.IO events channel.

## Dependencies
- **Uses**: `notestore` (Scan, UpsertFile), `processor` (Enqueue), `fsnotify`, `sync.Notifier.Events()`
- **Used by**: `cmd/ultrabridge` (startup wiring)
- **Boundary**: No direct DB access. No file content reading.

## Detection Strategies
1. **Watcher** (fsnotify): real-time CREATE/WRITE/RENAME events, 2s debounce, recursive directory watching
2. **Reconciler**: full Scan() every 15 min to catch missed events
3. **Engine.IO listener**: inbound events from Supernote service (stub -- awaiting live traffic investigation)

## Key Decisions
- UpsertFile before Enqueue: satisfies jobs.note_path FK constraint
- Debounce per-path (not global): prevents duplicate enqueues during multi-write saves
- Chmod events strictly filtered: Linux inotify sends Chmod alone on attribute changes

## Invariants
- Only files with `.note` extension are enqueued (ClassifyFileType filter)
- Watcher adds new subdirectories dynamically on CREATE events
- Close() blocks until all goroutines exit cleanly
