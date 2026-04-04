# CalDAV Backend

Last verified: 2026-04-04

## Purpose
Exposes Supernote tasks as a standard CalDAV VTODO collection so any CalDAV client
(Apple Reminders, Thunderbird, DAVx5) can read and write tasks.

## Contracts
- **Exposes**: `Backend` (implements `gocaldav.Backend`), `TaskStore` interface, `SyncNotifier` interface
- **Guarantees**: Single fixed collection at `{prefix}/tasks/`. Only VTODO supported (VEVENT rejected). Writes trigger sync notification (graceful degradation if notifier down). ETags computed from mutable fields.
- **Expects**: A `TaskStore` implementation and a `SyncNotifier`. Caller sets HTTP prefix.

## Dependencies
- **Uses**: `taskstore` (via `TaskStore` interface), `sync` (via `SyncNotifier` interface)
- **Used by**: `cmd/ultrabridge` (HTTP mount), `web` (reuses `TaskStore` and `SyncNotifier` interfaces)
- **Boundary**: Does not import `config`, `db`, `auth`, or `logging`

## Key Decisions
- Single collection: Supernote has one flat task list, no sub-calendars
- TaskStore as interface: decouples from SQL, enables test doubles
- DueTimeMode config: "preserve" keeps time component, "date_only" strips it
- iCal blob overlay: TaskToVTODO checks ICalBlob first; if present, deserializes and overlays DB-authoritative fields (UID, SUMMARY, STATUS, DUE, DTSTAMP, LAST-MODIFIED, PERCENT-COMPLETE). Falls back to field-only build on corrupt blob.
- VTODOToTask serializes the full iCal calendar to ICalBlob for round-trip fidelity of non-modeled properties (DESCRIPTION, PRIORITY, RRULE, X-props, etc.)

## Invariants
- UID in VTODO maps 1:1 to task_id in DB
- Calendar object paths are `{prefix}/tasks/{task_id}.ics`
- PutCalendarObject is upsert: creates if missing, updates if exists
- Delete is soft-delete (delegates to store)
- DB fields always win over blob fields on read (blob is supplementary)

## Gotchas
- QueryCalendarObjects does basic VTODO filter only; no date-range filtering
- Notify errors are swallowed (logged, not returned) to avoid failing DB writes
- Corrupt iCal blobs silently fall back to field-only rendering (logged at warn level)
