# RAG Retrieval Pipeline Design

## Summary
<!-- TO BE GENERATED after body is written -->

## Definition of Done

Build a RAG system over UltraBridge's OCR'd note content, consisting of four components:

1. **Embedding pipeline** — nomic-embed-text-v1.5 via Ollama on the UltraBridge host (.52). Generates embeddings after OCR indexing in both Supernote and Boox workers. Stored as BLOBs in a `note_embeddings` SQLite table. Backfills existing content via manual trigger with automatic backfill on startup. Ollama hosts the embedding model only (no text generation).

2. **Hybrid retriever** — combines FTS5 keyword search and vector cosine similarity with rank fusion. Supports metadata filtering (date range, device model, folder) from the start. Exposed as a clean domain interface in `internal/rag/`, designed API-first for future frontend split.

3. **MCP server** — separate `cmd/ub-mcp` binary connecting to UltraBridge's API for data. Exposes `search_notes`, `get_note_pages`, `get_note_image` tools for Claude. Supports both stdio transport (Claude Desktop / Claude Code) and HTTP SSE transport (claude.ai web) via the official MCP Go SDK (v1.5.0+).

4. **Local chat tab** — web UI tab using the same retriever, sending assembled context to a text generation model (e.g., Qwen3.5) running on the shared RTX 5060 Ti via vLLM. Streamed responses via SSE.

**Out of scope:** Replacing the existing OCR pipeline, changing the FTS5 schema, building the API/frontend split (but all new code designed to make it easy later per project design guidance).

## Acceptance Criteria
<!-- TO BE GENERATED and validated before glossary -->

## Glossary
<!-- TO BE GENERATED after body is written -->
