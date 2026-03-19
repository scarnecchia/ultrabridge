# Note Database

Last verified: 2026-03-19

## Purpose
Opens and migrates the SQLite database used by the notes pipeline.
Centralizes schema ownership so all pipeline packages share one DB connection.

## Contracts
- **Exposes**: `Open(ctx, path) (*sql.DB, error)` -- opens/creates SQLite DB, applies migrations, returns pool.
- **Guarantees**: WAL mode and foreign keys enabled. Schema is idempotent (safe to call on existing DB). MaxOpenConns=1 (SQLite single-writer).
- **Expects**: Writable filesystem path. Context for cancellation.

## Dependencies
- **Uses**: `modernc.org/sqlite` (pure-Go, no CGO)
- **Used by**: `cmd/ultrabridge` (startup), indirectly by `notestore`, `processor`, `search`
- **Boundary**: Only owns schema DDL. No CRUD logic -- that lives in domain packages.

## Key Decisions
- Pure-Go SQLite (`modernc.org/sqlite`): avoids CGO dependency for cross-compilation
- Single `*sql.DB` shared across all pipeline packages via dependency injection

## Invariants
- `notes.path` is PRIMARY KEY (absolute filesystem path)
- `jobs.note_path` has FK to `notes.path` -- notes row must exist before job insert
- `note_fts` is a contentless FTS5 table synced via triggers on `note_content`
- `note_content` has UNIQUE(note_path, page) -- one content row per page per note

## Schema Tables
- `notes` -- file metadata (path, type, size, mtime, backup state)
- `jobs` -- processing queue (status, attempts, timestamps)
- `note_content` -- extracted text per page (title, body, keywords, source)
- `note_fts` -- FTS5 virtual table for full-text search
