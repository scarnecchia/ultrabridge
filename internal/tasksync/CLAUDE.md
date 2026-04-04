# Task Sync Engine

Last verified: 2026-04-04

## Purpose
Adapter-agnostic reconciliation engine for syncing tasks between UltraBridge's local store and external devices. Manages adapter registration, sync scheduling, conflict resolution, and sync state tracking.

## Contracts
- **Exposes**: `SyncEngine` (Start, Stop, TriggerSync, Status, RegisterAdapter, UnregisterAdapter), `DeviceAdapter` interface, `SyncMap` (sync state data access), types (RemoteTask, Change, SyncStatus, PushResult)
- **Guarantees**: UB-wins conflict resolution. Detects remote hard-deletes. Sync map tracks per-task remote ID mapping. Background loop with configurable interval + manual trigger.
- **Expects**: A `TaskStore` implementation and a `*sql.DB` with sync_state/task_sync_map tables.

## Dependencies
- **Uses**: `taskstore` (Task model, helpers), `database/sql` (sync tables)
- **Used by**: `cmd/ultrabridge` (startup), `web.Handler` (via SyncStatusProvider interface)
- **Boundary**: Does not import caldav, web, or vendor-specific packages. Adapters live in sub-packages.

## Key Decisions
- Single adapter at a time (extensible to multiple later)
- Follows processor Start/Stop/WithCancel pattern
- Sync map uses SQLite tables in the task DB
