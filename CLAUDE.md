# UltraBridge CalDAV

Go sidecar service providing CalDAV/WebDAV integration with Supernote Private Cloud.

## Bash Commands: No `cd &&` Compounds

**NEVER** use `cd /path && command` compound bash statements. This triggers a Claude Code bug (anthropics/claude-code#28240) where the permission prompt fires on `cd` instead of the actual command, making it impossible to whitelist and breaking workflow.

Instead:
- Use tool-specific directory flags: `git -C /path`, `go -C /path build`
- Use absolute paths: `/path/to/file` instead of `cd /path && cat file`
- Run commands independently in separate bash calls

## Project Structure

- `cmd/ultrabridge/` — main entry point
- `internal/config/` — environment + .dbenv config loading
- `internal/db/` — MariaDB connection and user discovery
- `internal/taskstore/` — task CRUD and field mapping
- `docs/` — design and implementation plans

## Build & Test

```bash
go build -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./cmd/ultrabridge/
go test -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./...
go vet -C /home/sysop/src/ultrabridge/.worktrees/ultrabridge-caldav ./...
```

## Conventions

- Module: `github.com/sysop/ultrabridge`
- DB timestamps: millisecond UTC unix timestamps, 0 = unset
- IDs: Snowflake IDs stored as int64
- Supernote quirk: `completed_time` holds creation time; `last_modified` holds actual completion time
