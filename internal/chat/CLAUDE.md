# Chat Subsystem

Last verified: 2026-04-08

## Purpose
RAG-powered chat interface. Stores conversation sessions, retrieves relevant note context
via hybrid search, streams responses from a vLLM-compatible API.

## Contracts
- **Exposes**: `Store` (CreateSession, ListSessions, AddMessage, GetMessages, DeleteSession), `Handler` (HandleAsk), `Session` and `Message` models.
- **Guarantees**: Session deletion is transactional (messages + session in one tx). HandleAsk streams SSE events from vLLM response. Context from hybrid search injected into system prompt before sending to LLM.
- **Expects**: SQLite DB with `chat_sessions` and `chat_messages` tables (created by notedb migrations). vLLM-compatible API at configured URL (OpenAI chat completions format with streaming). `rag.SearchRetriever` for context retrieval.

## Dependencies
- **Uses**: `notedb` schema (chat_sessions, chat_messages tables), `rag.SearchRetriever` interface (hybrid search for context), vLLM HTTP API (OpenAI-compatible `/v1/chat/completions` with `stream: true`)
- **Used by**: `cmd/ultrabridge` (wiring), `web` handler (registers chat routes)
- **Boundary**: Does not own RAG retrieval logic -- uses SearchRetriever interface. Does not own schema -- that lives in notedb.

## Key Decisions
- SSE streaming proxy: handler reads vLLM SSE stream and forwards chunks to browser, allowing real-time token display
- RAG context injection: top search results prepended as system message context before user question
- Session-based persistence: messages stored per-session for conversation history and continuity
- Millisecond timestamps: consistent with notedb convention

## Invariants
- Session deletion removes all messages first, then session (transactional)
- Message roles: "user", "assistant", "system"
- AddMessage updates session's updated_at timestamp (keeps sessions sorted by recency)
- Store timestamps use millisecond UTC unix timestamps (consistent with notedb)

## Key Files
- `store.go` -- Session/Message CRUD against SQLite chat tables
- `handler.go` -- HandleAsk: retrieval -> prompt assembly -> vLLM streaming -> SSE proxy

## Gotchas
- Handler depends on rag.SearchRetriever (not the concrete Retriever) for testability
- vLLM streaming uses OpenAI-compatible SSE format (`data: {"choices": [...]}`)
