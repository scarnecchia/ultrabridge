# Supernote Sync Adapter

Last verified: 2026-04-04

## Purpose
Outbound sync adapter for Supernote devices via the Supernote Private Cloud (SPC) REST API. Implements the `tasksync.DeviceAdapter` interface.

## Contracts
- **Exposes**: `Adapter` (implements DeviceAdapter), `Client` (SPC REST API), `MigrateFromSPC` (first-run import)
- **Guarantees**: JWT auth with auto-retry on 401. Field mapping isolates Supernote quirks. STARTSYNC push after changes. Migration preserves original SPC task IDs.
- **Expects**: SPC API URL, password, and optional SyncNotifier.

## Dependencies
- **Uses**: `tasksync` (DeviceAdapter interface, types), `taskstore` (Task model, helpers)
- **Used by**: `cmd/ultrabridge` (adapter registration and migration)
- **Boundary**: Only package that knows SPC wire format. No CalDAV or web imports.

## Key Decisions
- Challenge-response JWT: SHA-256(password + randomCode)
- Re-auth on 401 with retry guard (max 1 retry per request)
- MD5 task IDs generated for creates (matches device convention)
- CompletedTime quirk: SPC completedTime = creation time, lastModified = completion time

## Invariants
- All SPC timestamps are millisecond UTC unix
- Deleted tasks filtered (isDeleted='Y') during Pull
