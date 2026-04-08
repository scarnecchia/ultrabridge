# Design Plan Guidance

## Architectural Direction

**API-first design.** All new features should be designed as clean API
operations first, with the web UI as one consumer. This prepares for a
future split into:
- An API engine (Go backend) that does the work
- One or more front-end interfaces (web UI, MCP server, CLI, mobile)

Concretely:
- New functionality should live in domain packages with clear interfaces,
  not in HTTP handlers
- HTTP handlers should be thin wrappers that call domain logic and format
  responses
- Avoid embedding business logic in templates or JavaScript
- JSON API endpoints should be usable independently of the HTML UI
- SSE/WebSocket endpoints for real-time features should be cleanly
  separable from the HTML page that hosts them

This does not mean building the split now — just making choices that
don't make it harder later.

## Conventions

- Pure-Go SQLite (modernc.org/sqlite, no CGO)
- Config via UB_ prefixed env vars
- Single-user Basic Auth (bcrypt)
- Unicode symbols preferred over emoji/icon libraries in UI
- Soft deletes for task records (is_deleted = 'Y')
- Docker deployment via install.sh / rebuild.sh
