# WebDAV Server

Last verified: 2026-04-05

## Purpose
Exposes a WebDAV endpoint for Boox .note file uploads with Basic Auth and automatic versioning of overwritten files.

## Contracts
- **Exposes**: `FS` (implements `golang.org/x/net/webdav.FileSystem`), `NewHandler` (HTTP handler), `ExtractPathMetadata` (path parser), `OnNoteUpload` (callback type), `PathMetadata` (struct)
- **Guarantees**: Writes preserve device path structure. On overwrite, old files move to `.versions/{dir}/{name}/{timestamp}.ext`. .note files trigger `OnNoteUpload` callback on successful close. Directory operations (MKCOL, PROPFIND) work. All requests require valid Basic Auth (handled by wrapping caller).
- **Expects**: A valid writable filesystem root (configured via the Boox source settings). Caller wraps handler with `auth.Middleware` before mounting at `/webdav/`.

## Dependencies
- **Uses**: `golang.org/x/net/webdav` (protocol handler, FileSystem interface, LockSystem)
- **Used by**: `cmd/ultrabridge` (HTTP mount behind `auth.Middleware`)
- **Boundary**: Does not import `config`, `logging`, `auth`, or other internal packages

## Key Types
- **FS**: Custom FileSystem backend wrapping OS filesystem with versioning hooks. Implements `webdav.FileSystem` interface (OpenFile, Mkdir, Stat, RemoveAll, Rename).
- **hookFile**: Wraps `*os.File` to intercept Close() calls and trigger `OnNoteUpload` callback for .note files only.
- **PathMetadata**: Struct with fields DeviceModel, NoteType, Folder, NoteName extracted from Boox device path convention.
- **OnNoteUpload**: Callback function signature `func(absPath string)` called after .note file write completes successfully.

## Versioning Semantics
- Triggered when: OpenFile called with `os.O_CREATE | os.O_TRUNC` flags on existing file
- Archive path: `{root}/.versions/{reldir}/{basename_no_ext}/{timestamp}.{ext}`
- Timestamp format: `20060102T150405.000000000` (nanosecond precision to prevent collisions)
- Old file moved atomically via `os.Rename`; if collision, new version silently replaces old

## Upload Hook Contract
- Called only on successful file Close() after a write
- Only for files matching `*.note` (case-insensitive)
- Path is absolute filesystem path, not WebDAV relative path
- Called after all write data flushed and file closed
- Errors in hook do not propagate to client

## Mount Path
- Mounted at `/webdav/` on the HTTP mux
- Mounted behind `auth.Middleware` for Basic Auth enforcement
- WebDAV prefix: `/webdav/` (used by `webdav.Handler` for PROPFIND, etc.)
- Device path convention expected: `/onyx/{device_model}/{Notebooks|Reading Notes}/{folder}/{name}.note`

## Gotchas
- No path traversal checks needed: `filepath.Clean` and `filepath.FromSlash` sanitize paths
- Parent directories auto-created on OpenFile with `os.O_CREATE`
- LockSystem: Uses in-memory `webdav.NewMemLS()` (no persistence across restarts)
- Callback is not guaranteed to run if Close() fails; failures logged externally
