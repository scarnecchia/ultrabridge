# RAG Embedding Infrastructure

Last verified: 2026-04-08

## Purpose
Generates and stores vector embeddings for note content, enabling semantic search.
Separates embedding concerns (inference, storage, retrieval) from OCR and indexing.

## Contracts
- **Exposes**: `Embedder` interface (Embed method), `EmbedStore` interface (Save method), `Store` struct (LoadAll, AllEmbeddings, UnembeddedPages, Save), `OllamaEmbedder` implementation, `Backfill` function
- **Guarantees**: In-memory cache loaded on startup reflects all DB embeddings. Context cancellation stops backfill gracefully. Embeddings persisted atomically (upsert). AllEmbeddings returns a snapshot copy (safe for concurrent read).
- **Expects**: SQLite DB with `note_embeddings` table (created by notedb migrations). Ollama server at configured URL with embedding model available. Caller respects context cancellation in long-running operations (Backfill).

## Dependencies
- **Uses**: `notedb` schema (note_embeddings, note_content tables via SQL), Ollama HTTP `/api/embed` endpoint
- **Used by**: `processor` (worker embeds OCR'd text), `booxpipeline` (worker embeds Boox note text), `web` handler (manual backfill trigger)
- **Boundary**: Do NOT import from processor, booxpipeline, or web (would create circular deps). Both client packages implement interfaces, not the reverse.

## Key Decisions
- **In-memory cache on startup**: Avoids repeated DB queries during retrieval; trade-off is memory usage. Cache refreshed on Save atomically.
- **Two-phase backfill**: Lazy backfill via Backfill() on startup covers unembedded pages; manual trigger via web UI allows on-demand re-embedding after model upgrades.
- **Interface-based design**: Embedder and EmbedStore are interfaces so worker configs accept mocks in tests. Store implements EmbedStore, OllamaEmbedder implements Embedder.
- **Context cancellation**: Backfill respects ctx.Err() in its loop — allows graceful shutdown during long runs.

## Invariants
- In-memory cache kept in sync with DB via atomic upsert in Save()
- Embeddings stored as float32 arrays, serialized as little-endian byte blobs
- Each (note_path, page) tuple has at most one embedding row
- Backfill skips pages with empty body_text (no embedding needed)
- AllEmbeddings() returns deep copy (modifications don't affect cache)

## Key Files
- `embedder.go` — Embedder and EmbedStore interfaces, OllamaEmbedder HTTP client
- `store.go` — Store struct: LoadAll, AllEmbeddings, UnembeddedPages, Save CRUD
- `backfill.go` — Backfill function: iterates unembedded pages, calls Embed+Save, respects cancellation

## Gotchas
- Ollama timeout is 30s; very large documents may timeout mid-embedding
- Float32 serialization is little-endian; endianness matters for cross-platform DB copies
- AllEmbeddings() includes ALL in-cache embeddings (could be large if cache is GBs); caller responsible for memory
- Backfill continues on individual page errors (logged, not fatal); one failing embedding doesn't stop the backfill loop
