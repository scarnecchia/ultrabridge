# Platform-Neutral Configuration Implementation Plan

**Goal:** Create `internal/appconfig` package that loads and saves application config from/to the SQLite settings KV table with env-var override support.

**Architecture:** New `internal/appconfig` package builds on the existing `notedb.GetSetting`/`SetSetting` infrastructure. Config struct groups fields by concern. `Load()` reads all keys from DB, overlays env vars. `Save()` writes changed keys and reports which changed. A restart-required set flags keys whose changes need a restart.

**Tech Stack:** Go stdlib, `database/sql`, existing `internal/notedb` settings functions

**Scope:** 8 phases from original design (this is phase 1 of 8)

**Codebase verified:** 2026-04-10

---

## Acceptance Criteria Coverage

This phase implements and tests:

### platform-neutral-config.AC1: SQLite-backed configuration system
- **platform-neutral-config.AC1.1 Success:** `appconfig.Load()` reads all config keys from settings table and returns typed Config struct
- **platform-neutral-config.AC1.2 Success:** `appconfig.Save()` writes changed keys to settings table and returns list of changed keys
- **platform-neutral-config.AC1.3 Success:** Env var set for a config key overrides the DB value in loaded Config
- **platform-neutral-config.AC1.8 Edge:** First boot with no DB values falls back to env vars for all config keys

---

## Codebase Verification Findings

- ✓ `internal/notedb/settings.go` exists with `GetSetting(ctx, db, key)` and `SetSetting(ctx, db, key, value)` — upsert semantics confirmed
- ✓ Settings table DDL: `CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`
- ✓ `internal/config/config.go` exists with `Load() (*Config, error)` reading 45 UB_ env vars
- ✓ 9 current setting keys in use: `sn_inject_enabled`, `sn_ocr_prompt`, `boox_ocr_prompt`, `boox_todo_enabled`, `boox_todo_prompt`, `boox_import_path`, `boox_import_notes`, `boox_import_pdfs`, `boox_import_onyx_paths`
- ✓ Setting key constants defined in `internal/web/handler.go:344-352`
- ✓ Runtime callback pattern: `func() string` / `func() bool` closures read from settings at job time — these bypass the Config struct and continue to work unchanged
- ✓ Tests use real SQLite in-memory (`:memory:`), stdlib `testing` package, no third-party frameworks
- ✓ Env var handling in tests: `os.Setenv`/`os.Unsetenv` with defer cleanup

**Testing approach:** Domain CLAUDE.md files document contracts and invariants. Tests use real in-memory SQLite via `notedb.Open(ctx, ":memory:")`. No mocks for database. See `/home/sysop/src/ultrabridge/internal/notedb/CLAUDE.md` and `/home/sysop/src/ultrabridge/internal/taskdb/CLAUDE.md` for patterns.

---

<!-- START_SUBCOMPONENT_A (tasks 1-2) -->
<!-- START_TASK_1 -->
### Task 1: Create `internal/appconfig/keys.go` — setting key constants, env-var mapping, defaults, restart-required set

**Verifies:** None (infrastructure — constants only)

**Files:**
- Create: `internal/appconfig/keys.go`

**Implementation:**

This file defines three things:
1. String constants for every setting key stored in the settings table
2. A mapping from setting key to its corresponding `UB_` env var name (for env overlay)
3. A set of keys that require restart when changed
4. A map of default values for each key

The setting keys cover all config currently loaded from env vars in `internal/config/config.go:86-136`, EXCEPT:
- Bootstrap env vars that stay as env vars: `UB_DB_PATH`, `UB_LISTEN_ADDR`, `UB_TASK_DB_PATH`
- Per-source config that moves to `sources.config_json` in later phases: `UB_NOTES_PATH`, `UB_BACKUP_PATH`, `UB_BOOX_ENABLED`, `UB_BOOX_NOTES_PATH`, `UB_BOOX_IMPORT_PATH`

Also includes the 9 existing runtime-configurable setting keys currently defined in `internal/web/handler.go:344-352`.

Use snake_case for setting keys (matching the existing pattern: `sn_inject_enabled`, `boox_ocr_prompt`, etc.).

```go
package appconfig

// Setting key constants. Each maps to a row in the settings KV table.
const (
	// Auth
	KeyUsername     = "auth_username"
	KeyPasswordHash = "auth_password_hash"

	// OCR
	KeyOCREnabled     = "ocr_enabled"
	KeyOCRAPIURL      = "ocr_api_url"
	KeyOCRAPIKey      = "ocr_api_key"
	KeyOCRModel       = "ocr_model"
	KeyOCRConcurrency = "ocr_concurrency"
	KeyOCRMaxFileMB   = "ocr_max_file_mb"
	KeyOCRFormat      = "ocr_format"

	// Embedding / RAG
	KeyEmbedEnabled    = "embed_enabled"
	KeyOllamaURL       = "ollama_url"
	KeyOllamaEmbedModel = "ollama_embed_model"

	// Chat
	KeyChatEnabled = "chat_enabled"
	KeyChatAPIURL  = "chat_api_url"
	KeyChatModel   = "chat_model"

	// Supernote device sync
	KeySNSyncEnabled  = "sn_sync_enabled"
	KeySNSyncInterval = "sn_sync_interval"
	KeySNAPIURL       = "sn_api_url"
	KeySNAccount      = "sn_account"
	KeySNPassword     = "sn_password"

	// Logging
	KeyLogLevel         = "log_level"
	KeyLogFormat        = "log_format"
	KeyLogFile          = "log_file"
	KeyLogFileMaxMB     = "log_file_max_mb"
	KeyLogFileMaxAge    = "log_file_max_age_days"
	KeyLogFileMaxBackup = "log_file_max_backups"
	KeyLogSyslogAddr    = "log_syslog_addr"

	// CalDAV
	KeyCalDAVCollectionName = "caldav_collection_name"
	KeyDueTimeMode          = "due_time_mode"

	// Server
	KeyWebEnabled  = "web_enabled"
	KeySocketIOURL = "socketio_url"

	// MariaDB / SPC connection
	KeyDBHost    = "db_host"
	KeyDBPort    = "db_port"
	KeyDBEnvPath = "dbenv_path"
	KeyUserID    = "user_id"

	// Runtime-configurable (existing keys, read at job time via closures — NOT loaded into Config struct)
	// These are included here for completeness but are accessed via notedb.GetSetting directly.
	KeySNInjectEnabled    = "sn_inject_enabled"
	KeySNOCRPrompt        = "sn_ocr_prompt"
	KeyBooxOCRPrompt      = "boox_ocr_prompt"
	KeyBooxTodoEnabled    = "boox_todo_enabled"
	KeyBooxTodoPrompt     = "boox_todo_prompt"
	KeyBooxImportPath     = "boox_import_path"
	KeyBooxImportNotes    = "boox_import_notes"
	KeyBooxImportPDFs     = "boox_import_pdfs"
	KeyBooxImportOnyxPaths = "boox_import_onyx_paths"
)

// envVarForKey maps each setting key to its UB_ env var name.
// Only keys that have a corresponding env var are listed.
var envVarForKey = map[string]string{
	KeyUsername:          "UB_USERNAME",
	KeyPasswordHash:     "UB_PASSWORD_HASH",
	KeyOCREnabled:       "UB_OCR_ENABLED",
	KeyOCRAPIURL:        "UB_OCR_API_URL",
	KeyOCRAPIKey:        "UB_OCR_API_KEY",
	KeyOCRModel:         "UB_OCR_MODEL",
	KeyOCRConcurrency:   "UB_OCR_CONCURRENCY",
	KeyOCRMaxFileMB:     "UB_OCR_MAX_FILE_MB",
	KeyOCRFormat:        "UB_OCR_FORMAT",
	KeyEmbedEnabled:     "UB_EMBED_ENABLED",
	KeyOllamaURL:        "UB_OLLAMA_URL",
	KeyOllamaEmbedModel: "UB_OLLAMA_EMBED_MODEL",
	KeyChatEnabled:      "UB_CHAT_ENABLED",
	KeyChatAPIURL:       "UB_CHAT_API_URL",
	KeyChatModel:        "UB_CHAT_MODEL",
	KeySNSyncEnabled:    "UB_SN_SYNC_ENABLED",
	KeySNSyncInterval:   "UB_SN_SYNC_INTERVAL",
	KeySNAPIURL:         "UB_SN_API_URL",
	KeySNAccount:        "UB_SN_ACCOUNT",
	KeySNPassword:       "UB_SN_PASSWORD",
	KeyLogLevel:         "UB_LOG_LEVEL",
	KeyLogFormat:        "UB_LOG_FORMAT",
	KeyLogFile:          "UB_LOG_FILE",
	KeyLogFileMaxMB:     "UB_LOG_FILE_MAX_MB",
	KeyLogFileMaxAge:    "UB_LOG_FILE_MAX_AGE_DAYS",
	KeyLogFileMaxBackup: "UB_LOG_FILE_MAX_BACKUPS",
	KeyLogSyslogAddr:    "UB_LOG_SYSLOG_ADDR",
	KeyCalDAVCollectionName: "UB_CALDAV_COLLECTION_NAME",
	KeyDueTimeMode:      "UB_DUE_TIME_MODE",
	KeyWebEnabled:       "UB_WEB_ENABLED",
	KeySocketIOURL:      "UB_SOCKETIO_URL",
	KeyDBHost:           "UB_DB_HOST",
	KeyDBPort:           "UB_DB_PORT",
	KeyDBEnvPath:        "UB_SUPERNOTE_DBENV_PATH",
	KeyUserID:           "UB_USER_ID",
}

// defaultValues provides the default for each setting key when neither DB nor env var is set.
// Keys not in this map default to empty string.
var defaultValues = map[string]string{
	KeyOCRFormat:          "anthropic",
	KeyOCRConcurrency:     "1",
	KeyOCRMaxFileMB:       "0",
	KeyOllamaURL:          "http://localhost:11434",
	KeyOllamaEmbedModel:   "nomic-embed-text:v1.5",
	KeyChatAPIURL:         "http://localhost:8000",
	KeyChatModel:          "Qwen/Qwen3-8B",
	KeySNSyncInterval:     "300",
	KeySNAPIURL:           "http://supernote-service:8080",
	KeyLogLevel:           "info",
	KeyLogFormat:          "json",
	KeyLogFileMaxMB:       "50",
	KeyLogFileMaxAge:      "30",
	KeyLogFileMaxBackup:   "5",
	KeyCalDAVCollectionName: "Supernote Tasks",
	KeyDueTimeMode:        "preserve",
	KeyWebEnabled:         "true",
	KeySocketIOURL:        "ws://supernote-service:8080/socket.io/",
	KeyDBHost:             "mariadb",
	KeyDBPort:             "3306",
	KeyDBEnvPath:          "/run/secrets/dbenv",
}

// restartRequired is the set of keys whose changes require a restart to take effect.
// Changes to these keys are detected by Save() and reported so the UI can show a banner.
var restartRequired = map[string]bool{
	KeyUsername:          true,
	KeyPasswordHash:     true,
	KeyOCREnabled:       true,
	KeyOCRAPIURL:        true,
	KeyOCRAPIKey:        true,
	KeyOCRModel:         true,
	KeyOCRConcurrency:   true,
	KeyOCRMaxFileMB:     true,
	KeyOCRFormat:        true,
	KeyEmbedEnabled:     true,
	KeyOllamaURL:        true,
	KeyOllamaEmbedModel: true,
	KeyChatEnabled:      true,
	KeyChatAPIURL:       true,
	KeyChatModel:        true,
	KeySNSyncEnabled:    true,
	KeySNSyncInterval:   true,
	KeySNAPIURL:         true,
	KeySNAccount:        true,
	KeySNPassword:       true,
	KeyLogLevel:         true,
	KeyLogFormat:        true,
	KeyLogFile:          true,
	KeyLogSyslogAddr:    true,
	KeyWebEnabled:       true,
	KeySocketIOURL:      true,
	KeyDBHost:           true,
	KeyDBPort:           true,
	KeyDBEnvPath:        true,
	KeyUserID:           true,
}
```

**Verification:**
Run: `go build -C /home/sysop/src/ultrabridge ./internal/appconfig/`
Expected: Compiles without errors

**Commit:** `feat(appconfig): add setting key constants, env mapping, defaults, and restart-required set`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Create `internal/appconfig/config.go` and `internal/appconfig/env.go` — Config struct, Load(), Save()

**Verifies:** platform-neutral-config.AC1.1, platform-neutral-config.AC1.2, platform-neutral-config.AC1.3, platform-neutral-config.AC1.8

**Files:**
- Create: `internal/appconfig/config.go`
- Create: `internal/appconfig/env.go`
- Test: `internal/appconfig/appconfig_test.go`

**Implementation:**

`config.go` defines the Config struct with typed fields grouped by concern, plus `Load()` and `Save()` functions.

**Config struct** with all fields and Go types:

```go
type Config struct {
	// Auth
	Username     string
	PasswordHash string

	// OCR
	OCREnabled     bool
	OCRAPIURL      string
	OCRAPIKey      string
	OCRModel       string
	OCRConcurrency int
	OCRMaxFileMB   int
	OCRFormat      string // "anthropic" or "openai"

	// Embedding / RAG
	EmbedEnabled     bool
	OllamaURL        string
	OllamaEmbedModel string

	// Chat
	ChatEnabled bool
	ChatAPIURL  string
	ChatModel   string

	// Supernote device sync
	SNSyncEnabled  bool
	SNSyncInterval int    // seconds
	SNAPIURL       string
	SNAccount      string
	SNPassword     string

	// Logging
	LogLevel         string
	LogFormat        string
	LogFile          string
	LogFileMaxMB     int
	LogFileMaxAge    int
	LogFileMaxBackup int
	LogSyslogAddr    string

	// CalDAV
	CalDAVCollectionName string
	DueTimeMode          string // "preserve" or "date_only"

	// Server
	WebEnabled  bool
	SocketIOURL string

	// MariaDB / SPC connection
	DBHost    string
	DBPort    string
	DBEnvPath string
	UserID    int64

	// Transitional per-source fields (used for backward-compat seeding in Phase 5,
	// removed in Phase 8 when sources own their paths via config_json)
	NotesPath     string
	BackupPath    string
	BooxEnabled   bool
	BooxNotesPath string
}
```

**Excluded from Config struct (remain env-only):**
- Bootstrap: UB_DB_PATH, UB_LISTEN_ADDR, UB_TASK_DB_PATH — needed before DB opens
- Password hash file path: UB_PASSWORD_HASH_PATH — needed before DB opens

**Load(ctx, db)** works in three layers:
1. Read all setting keys from DB via `notedb.GetSetting` — store raw string values in a `map[string]string`
2. Call `applyEnvOverrides` from env.go — for each key that has a corresponding env var set, replace the DB value
3. For any key still empty, apply default from `defaultValues`
4. Parse the final string map into the typed Config struct

**Save(ctx, db, new)** works by:
1. Load current config from DB (without env overlay — raw DB values only)
2. Convert new Config to string map
3. Compare old vs new — collect changed keys
4. Write each changed key via `notedb.SetSetting`
5. Check if any changed key is in `restartRequired` set
6. Return `SaveResult{ChangedKeys []string, RestartRequired bool}`

`env.go` contains `applyEnvOverrides(vals map[string]string)` which iterates `envVarForKey` and overrides values when the corresponding env var is set.

Key behaviors:
- Bool fields: stored as `"true"` / `"false"` in DB, parsed with `strings.EqualFold(v, "true") || v == "1"` (matching existing pattern in `internal/config/config.go:240`)
- Int fields: stored as string in DB, parsed with `strconv.Atoi` (falling back to default on parse error)
- Empty string in DB means "not set" — triggers default

**SaveResult type:**

```go
type SaveResult struct {
	ChangedKeys     []string
	RestartRequired bool
}
```

**Testing:**

Tests must verify each AC listed above using real in-memory SQLite via `notedb.Open(ctx, ":memory:")`:

- platform-neutral-config.AC1.1: Load with pre-populated settings returns correct typed Config values (set several keys via `notedb.SetSetting`, call `Load`, verify struct fields match)
- platform-neutral-config.AC1.2: Save with changed values writes to DB and returns correct changed keys list (Load, modify fields, Save, verify ChangedKeys contains exactly the modified keys, verify DB has new values via `notedb.GetSetting`)
- platform-neutral-config.AC1.3: Env var overrides DB value (set a key in DB, set corresponding UB_ env var to different value, Load, verify struct field has env var value not DB value)
- platform-neutral-config.AC1.8: First boot with empty DB falls back to env vars (set UB_ env vars, Load with empty DB, verify struct fields have env var values)
- Additional: Save detects restart-required keys and sets `RestartRequired: true` in result
- Additional: Save with no changes returns empty ChangedKeys and `RestartRequired: false`
- Additional: Default values applied when neither DB nor env var is set

Follow test patterns from `internal/taskdb/store_test.go` (helper opens `:memory:` SQLite) and `internal/config/config_pipeline_test.go` (env var setup with `os.Setenv`/`os.Unsetenv` + defer).

**Verification:**
Run: `go test -C /home/sysop/src/ultrabridge ./internal/appconfig/`
Expected: All tests pass

**Commit:** `feat(appconfig): implement Config struct with Load/Save, env overlay, and restart detection`
<!-- END_TASK_2 -->
<!-- END_SUBCOMPONENT_A -->
