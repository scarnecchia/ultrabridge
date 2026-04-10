# Application Configuration Package

Last verified: 2026-04-10

## Purpose

Provides SQLite-backed application configuration with environment variable overrides and restart detection. Acts as the single source of truth for all application settings.

## Contracts

### Public Functions

- **Load(ctx, db)** → (*Config, error) — Reads all config keys from SQLite settings table, applies env var overrides, applies defaults, and returns typed Config struct
- **Save(ctx, db, cfg)** → (*SaveResult, error) — Persists changed keys to DB, detects which keys changed, and reports if any restart-required keys were modified

### Config Struct

Typed fields grouped by concern: Auth, OCR, Embedding/RAG, Chat, Supernote sync, Logging, CalDAV, Server, MariaDB connection, and transitional per-source fields.

All fields are exported for use by other packages. See config.go for complete field list.

### SaveResult

```go
type SaveResult struct {
    ChangedKeys     []string  // List of keys that were modified
    RestartRequired bool      // True if any changed key requires restart
}
```

## Dependencies

- **Uses**: `database/sql`, `internal/notedb` (GetSetting, SetSetting)
- **Used by**: cmd/ultrabridge (future phases will use this to load config)

## Key Decisions

### Three-Layer Load

Load applies configuration in three layers, with later layers overriding earlier ones:
1. Read from SQLite settings table
2. Apply environment variable overrides (keys with corresponding `UB_` env vars)
3. Apply defaults for any remaining empty values

This ensures env vars always override DB, DB overrides defaults.

### Save Compares Raw DB Values

Save loads the current config from DB **without env var overlay** to detect true changes. This prevents env vars from being written to the DB, keeping the DB a persistent store independent of environment.

### Type Conversion

- Bool: Stored as `"true"` / `"false"` in DB, parsed with case-insensitive comparison or `"1"`
- Int: Stored as string, parsed with `strconv.Atoi`, falling back to default on error
- String: Stored as-is

### Restart Detection

Certain keys require application restart when changed (e.g., authentication, database connection, core service endpoints). The `restartRequired` set in keys.go identifies these keys. Save() checks this set and reports if any changed key requires restart, allowing the UI to show a restart banner.

## Schema

Configuration is stored in the existing SQLite `settings` table:

```sql
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
)
```

## Invariants

- Each key appears in settings table at most once (PRIMARY KEY enforces this)
- Keys are never deleted from the table (Phase 2 may soft-delete via empty string)
- The DB is the source of truth; env vars are read-only overlays
- Defaults in code match environment documentation; mismatches are bugs

## Testing

Tests use real in-memory SQLite (`:memory:`) and no mocks. Test patterns from `internal/taskdb/store_test.go`.

Coverage includes:
- Load reads from DB and applies types correctly
- Load applies env var overrides
- Load applies defaults for missing keys
- Save writes changed keys
- Save detects restart-required keys
- Save reports no changes when config unchanged
- Bool/int parsing with fallback to defaults
- Full roundtrip: Save followed by Load preserves values
- Multiple env var override scenarios

## Future Phases

- Phase 2: Web UI for editing settings
- Phase 3: Settings API endpoints
- Phase 4+: Per-source configuration (sources.config_json)
