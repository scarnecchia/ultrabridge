# Task Database

Last verified: 2026-04-04

## Purpose
Opens and migrates the SQLite database used for task storage.
Implements the `caldav.TaskStore` interface for CalDAV and web UI task operations.

## Contracts
- **Exposes**: `Open(ctx, path) (*sql.DB, error)` -- opens/creates SQLite DB, applies migrations, returns pool. `NewStore(db) *Store` -- creates TaskStore implementation.
- **Guarantees**: WAL mode and foreign keys enabled. Schema is idempotent (safe to call on existing DB). MaxOpenConns=1 (SQLite single-writer). Implements all 6 `caldav.TaskStore` methods. Uses `taskstore.ErrNotFound` sentinel for missing tasks.
- **Expects**: Writable filesystem path. Context for cancellation.

## Dependencies
- **Uses**: `modernc.org/sqlite` (pure-Go, no CGO), `taskstore` (Task model, ErrNotFound, mapping helpers)
- **Used by**: `cmd/ultrabridge` (startup), indirectly by `caldav.Backend`, `web.Handler` via `caldav.TaskStore` interface
- **Boundary**: Owns schema DDL and CRUD. Does not own iCal conversion (that's `caldav/vtodo.go`).

## Key Decisions
- Single-user: no user_id column (one SQLite DB per UltraBridge instance)
- Reuses `taskstore.Task` model: no new type, CalDAV/web code unchanged
- Default values match existing `taskstore.Store` behavior (GenerateTaskID, CompletedTime=now, etc.)

## Invariants
- Timestamps are always millisecond UTC unix (0 = unset)
- `completed_time` holds **creation** time (Supernote quirk preserved for compatibility)
- `is_deleted` is "Y" or "N", never NULL
- Soft deletes only: Delete sets is_deleted='Y', never removes rows
- `ical_blob` column exists but is unused until Phase 2
