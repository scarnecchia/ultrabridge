# Sync Notifier

Last verified: 2026-03-19

## Purpose
Pushes STARTSYNC messages to the Supernote service via Engine.IO v3 WebSocket,
triggering the device to pull updated tasks from the DB.

## Contracts
- **Exposes**: `Notifier` (Connect, Notify, Events, Close). Satisfies `caldav.SyncNotifier` interface.
- **Guarantees**: Auto-reconnects on disconnect. Notify is best-effort (returns error but callers should not fail writes). Thread-safe: write mutex serializes all WebSocket sends.
- **Expects**: Valid Engine.IO v3 WebSocket URL (ws://host:port/socket.io/).

## Dependencies
- **Uses**: `gorilla/websocket`
- **Used by**: `caldav.Backend`, `web.Handler` (via `caldav.SyncNotifier`), `pipeline` (via `Events()` channel)
- **Boundary**: No DB access, no HTTP serving

## Key Decisions
- Engine.IO v3 (not v4): matches Supernote service's socket.io version
- Graceful degradation: sync failure never blocks task CRUD operations
- Separate write mutex: prevents concurrent WebSocket write panics

## Invariants
- STARTSYNC payload matches the format the Supernote service expects (ServerMessage event with FILE-SYN msgType)
- Pong responses sent for every ping to keep connection alive
- Connection loss sets conn to nil; Notify returns error when disconnected

## Gotchas
- NewNotifier panics on invalid URL (fail-fast at startup, not at runtime)
- Connect is non-blocking; spawns goroutine for reconnect loop
- Events() channel is buffered (16); drops messages if consumer is slow
