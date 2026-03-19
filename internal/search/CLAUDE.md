# Search

Last verified: 2026-03-19

## Purpose
Full-text search over note page content using SQLite FTS5.
Provides indexing and ranked query with BM25 scoring and snippet extraction.

## Contracts
- **Exposes**: `SearchIndex` interface (Index, Search, Delete, IndexPage), `NoteDocument`, `SearchQuery`, `SearchResult`.
- **Guarantees**: IndexPage satisfies `processor.Indexer` interface. Search returns BM25-ranked results with snippets. Empty query returns nil (no error). FTS5 input is escaped to prevent syntax injection.
- **Expects**: SQLite `*sql.DB` with `note_content` and `note_fts` tables (created by notedb).

## Dependencies
- **Uses**: `notedb` schema (note_content + note_fts tables)
- **Used by**: `processor` (via Indexer interface for page indexing), `web` (Search tab handler)
- **Boundary**: Read/write note_content only. No filesystem access.

## Key Decisions
- FTS5 content-sync via triggers (not external-content rebuild): automatic, no manual sync needed
- BM25 scoring with default weights; snippet from body_text column (index 3)
- Default limit 25 results per query
- User input wrapped in double-quotes to prevent FTS5 operator injection

## Invariants
- One content row per (note_path, page) pair; upsert on conflict
- FTS index stays in sync via INSERT/UPDATE/DELETE triggers on note_content
- Source field tracks provenance: "myScript" (device) or "api" (OCR)
