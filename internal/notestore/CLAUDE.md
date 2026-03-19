# Note Store

Last verified: 2026-03-19

## Purpose
Maintains file inventory in the SQLite `notes` table and provides directory
listing for the web UI Files tab. Bridges the filesystem with the database.

## Contracts
- **Exposes**: `NoteStore` interface (Scan, List, Get, UpsertFile), `NoteFile` model, `FileType` classification, `ComputeSHA256`.
- **Guarantees**: Scan returns only new-or-changed paths (mtime comparison). List returns directories from live FS + files from DB with latest job status. UpsertFile satisfies the `jobs.note_path` FK before enqueue.
- **Expects**: SQLite `*sql.DB` with `notes` and `jobs` tables. Absolute `notesPath` root directory.

## Dependencies
- **Uses**: `notedb` schema (notes + jobs tables)
- **Used by**: `pipeline` (Scan for reconciliation, UpsertFile before enqueue), `web` (List/Get for Files tab)
- **Boundary**: No processing logic. Does not read .note file contents.

## Key Decisions
- Directories are NOT stored in the notes table -- only listed live from FS in List()
- mtime-based change detection in Scan (not content hashing) for speed
- `ErrNotFound` sentinel for Get() miss, distinct from DB errors

## Invariants
- Every file in the notes table has an absolute path as PK and a relative path for display
- FileType is classified by extension: .note, .pdf, .epub, or "other"
- Scan walks the entire tree; returns changed paths for the pipeline to enqueue
