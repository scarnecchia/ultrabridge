# UltraBridge CalDAV

Last verified: 2026-03-17

Go sidecar service providing CalDAV task sync and a web UI for Supernote Private Cloud.
Reads/writes the Supernote MariaDB directly, pushes STARTSYNC via Engine.IO to trigger device sync.

## Bash Commands: No `cd &&` Compounds

**NEVER** use `cd /path && command` compound bash statements. This triggers a Claude Code bug where the permission prompt fires on `cd` instead of the actual command.

Instead: `git -C /path`, `go -C /path build`, or absolute paths.

## Project Structure

- `cmd/ultrabridge/` -- entry point, wires all components
- `internal/caldav/` -- CalDAV backend (go-webdav), VTODO conversion (see domain CLAUDE.md)
- `internal/taskstore/` -- task CRUD against t_schedule_task, field mapping (see domain CLAUDE.md)
- `internal/sync/` -- Engine.IO v3 notifier for STARTSYNC push (see domain CLAUDE.md)
- `internal/auth/` -- Basic Auth middleware (bcrypt)
- `internal/config/` -- env vars (UB_ prefix) + .dbenv file loading
- `internal/db/` -- MariaDB pool + single-user discovery
- `internal/logging/` -- structured slog, file rotation, syslog, WebSocket broadcast
- `internal/web/` -- HTML task list, create/complete, SSE log stream
- `tests/` -- integration tests (require real DB)

## Build & Test

```bash
go build -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./cmd/ultrabridge/
go test -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./...
go vet -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./...
```

## Conventions

- Module: `github.com/sysop/ultrabridge`
- DB timestamps: millisecond UTC unix timestamps, 0 = unset
- IDs: MD5(title + timestamp) for task IDs (matches Supernote device convention)
- Supernote quirk: `completed_time` holds creation time; `last_modified` holds actual completion time
- Soft deletes only: `is_deleted = 'Y'`, never hard delete
- Config: all env vars prefixed `UB_`, DB creds from shared `.dbenv` file
- Auth: single-user Basic Auth, password stored as bcrypt hash
