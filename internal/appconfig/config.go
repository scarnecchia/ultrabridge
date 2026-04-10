package appconfig

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"

	"github.com/sysop/ultrabridge/internal/notedb"
)

// Config represents application configuration. Fields are grouped by concern.
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
}

// SaveResult reports the outcome of a Save operation.
type SaveResult struct {
	ChangedKeys     []string
	RestartRequired bool
}

// Load reads all config keys from the database, applies env var overrides,
// and returns a typed Config struct.
func Load(ctx context.Context, db *sql.DB) (*Config, error) {
	// Layer 1: Read all known keys from DB.
	dbVals := make(map[string]string)
	for key := range envVarForKey {
		val, err := notedb.GetSetting(ctx, db, key)
		if err != nil {
			return nil, err
		}
		dbVals[key] = val
	}

	// Also load the runtime-configurable keys (not in envVarForKey).
	runtimeKeys := []string{
		KeySNInjectEnabled,
		KeySNOCRPrompt,
		KeyBooxOCRPrompt,
		KeyBooxTodoEnabled,
		KeyBooxTodoPrompt,
		KeyBooxImportPath,
		KeyBooxImportNotes,
		KeyBooxImportPDFs,
		KeyBooxImportOnyxPaths,
	}
	for _, key := range runtimeKeys {
		val, err := notedb.GetSetting(ctx, db, key)
		if err != nil {
			return nil, err
		}
		dbVals[key] = val
	}

	// Layer 2: Apply env var overrides.
	applyEnvOverrides(dbVals)

	// Layer 3: Apply defaults for missing values.
	for key, defaultVal := range defaultValues {
		if dbVals[key] == "" {
			dbVals[key] = defaultVal
		}
	}

	// Parse the map into a typed Config struct.
	cfg := &Config{
		Username:             dbVals[KeyUsername],
		PasswordHash:         dbVals[KeyPasswordHash],
		OCREnabled:           parseBool(dbVals[KeyOCREnabled]),
		OCRAPIURL:            dbVals[KeyOCRAPIURL],
		OCRAPIKey:            dbVals[KeyOCRAPIKey],
		OCRModel:             dbVals[KeyOCRModel],
		OCRConcurrency:       parseIntWithDefault(dbVals[KeyOCRConcurrency], 1),
		OCRMaxFileMB:         parseIntWithDefault(dbVals[KeyOCRMaxFileMB], 0),
		OCRFormat:            dbVals[KeyOCRFormat],
		EmbedEnabled:         parseBool(dbVals[KeyEmbedEnabled]),
		OllamaURL:            dbVals[KeyOllamaURL],
		OllamaEmbedModel:     dbVals[KeyOllamaEmbedModel],
		ChatEnabled:          parseBool(dbVals[KeyChatEnabled]),
		ChatAPIURL:           dbVals[KeyChatAPIURL],
		ChatModel:            dbVals[KeyChatModel],
		SNSyncEnabled:        parseBool(dbVals[KeySNSyncEnabled]),
		SNSyncInterval:       parseIntWithDefault(dbVals[KeySNSyncInterval], 300),
		SNAPIURL:             dbVals[KeySNAPIURL],
		SNAccount:            dbVals[KeySNAccount],
		SNPassword:           dbVals[KeySNPassword],
		LogLevel:             dbVals[KeyLogLevel],
		LogFormat:            dbVals[KeyLogFormat],
		LogFile:              dbVals[KeyLogFile],
		LogFileMaxMB:         parseIntWithDefault(dbVals[KeyLogFileMaxMB], 50),
		LogFileMaxAge:        parseIntWithDefault(dbVals[KeyLogFileMaxAge], 30),
		LogFileMaxBackup:     parseIntWithDefault(dbVals[KeyLogFileMaxBackup], 5),
		LogSyslogAddr:        dbVals[KeyLogSyslogAddr],
		CalDAVCollectionName: dbVals[KeyCalDAVCollectionName],
		DueTimeMode:          dbVals[KeyDueTimeMode],
		WebEnabled:           parseBool(dbVals[KeyWebEnabled]),
		SocketIOURL:          dbVals[KeySocketIOURL],
		DBHost:               dbVals[KeyDBHost],
		DBPort:               dbVals[KeyDBPort],
		DBEnvPath:            dbVals[KeyDBEnvPath],
		UserID:               parseInt64(dbVals[KeyUserID]),
	}

	return cfg, nil
}

// Save writes changed keys to the database and reports which keys changed
// and whether any restart-required keys were modified.
func Save(ctx context.Context, db *sql.DB, cfg *Config) (*SaveResult, error) {
	// Load the current config from DB (without env overlay).
	current, err := loadDBOnly(ctx, db)
	if err != nil {
		return nil, err
	}

	// Convert both to maps for comparison.
	oldMap := configToMap(current)
	newMap := configToMap(cfg)

	// Find changed keys.
	changedKeys := []string{}
	restartRequiredChanged := false

	for key, newVal := range newMap {
		oldVal, exists := oldMap[key]
		if !exists || oldVal != newVal {
			changedKeys = append(changedKeys, key)
			if restartRequired[key] {
				restartRequiredChanged = true
			}
		}
	}

	// Write changed keys to DB.
	for _, key := range changedKeys {
		if err := notedb.SetSetting(ctx, db, key, newMap[key]); err != nil {
			return nil, err
		}
	}

	return &SaveResult{
		ChangedKeys:     changedKeys,
		RestartRequired: restartRequiredChanged,
	}, nil
}

// IsSetupRequired returns true when no auth credentials exist in either
// the settings DB or environment variables. This indicates first-boot setup
// is needed before the application can enforce authentication.
func IsSetupRequired(ctx context.Context, db *sql.DB) bool {
	// Check DB first
	username, _ := notedb.GetSetting(ctx, db, KeyUsername)
	hash, _ := notedb.GetSetting(ctx, db, KeyPasswordHash)
	if username != "" && hash != "" {
		return false
	}

	// Check env vars (backward compatibility for existing installs)
	if os.Getenv("UB_USERNAME") != "" && os.Getenv("UB_PASSWORD_HASH") != "" {
		return false
	}

	// Also check password hash file
	if os.Getenv("UB_USERNAME") != "" {
		hashPath := os.Getenv("UB_PASSWORD_HASH_PATH")
		if hashPath == "" {
			hashPath = "/run/secrets/ub_password_hash"
		}
		if data, err := os.ReadFile(hashPath); err == nil && strings.TrimSpace(string(data)) != "" {
			return false
		}
	}

	return true
}

// loadDBOnly loads config from DB without env var overlay.
// Used by Save to detect changes.
func loadDBOnly(ctx context.Context, db *sql.DB) (*Config, error) {
	dbVals := make(map[string]string)

	// Read all known keys from DB.
	for key := range envVarForKey {
		val, err := notedb.GetSetting(ctx, db, key)
		if err != nil {
			return nil, err
		}
		dbVals[key] = val
	}

	// Read runtime-configurable keys.
	runtimeKeys := []string{
		KeySNInjectEnabled,
		KeySNOCRPrompt,
		KeyBooxOCRPrompt,
		KeyBooxTodoEnabled,
		KeyBooxTodoPrompt,
		KeyBooxImportPath,
		KeyBooxImportNotes,
		KeyBooxImportPDFs,
		KeyBooxImportOnyxPaths,
	}
	for _, key := range runtimeKeys {
		val, err := notedb.GetSetting(ctx, db, key)
		if err != nil {
			return nil, err
		}
		dbVals[key] = val
	}

	// Read per-source keys.
	sourceKeys := []string{
		"notes_path",
		"backup_path",
		"boox_enabled",
		"boox_notes_path",
	}
	for _, key := range sourceKeys {
		val, err := notedb.GetSetting(ctx, db, key)
		if err != nil {
			return nil, err
		}
		dbVals[key] = val
	}

	// Apply defaults only (no env var overlay).
	for key, defaultVal := range defaultValues {
		if dbVals[key] == "" {
			dbVals[key] = defaultVal
		}
	}

	// Parse into Config.
	cfg := &Config{
		Username:             dbVals[KeyUsername],
		PasswordHash:         dbVals[KeyPasswordHash],
		OCREnabled:           parseBool(dbVals[KeyOCREnabled]),
		OCRAPIURL:            dbVals[KeyOCRAPIURL],
		OCRAPIKey:            dbVals[KeyOCRAPIKey],
		OCRModel:             dbVals[KeyOCRModel],
		OCRConcurrency:       parseIntWithDefault(dbVals[KeyOCRConcurrency], 1),
		OCRMaxFileMB:         parseIntWithDefault(dbVals[KeyOCRMaxFileMB], 0),
		OCRFormat:            dbVals[KeyOCRFormat],
		EmbedEnabled:         parseBool(dbVals[KeyEmbedEnabled]),
		OllamaURL:            dbVals[KeyOllamaURL],
		OllamaEmbedModel:     dbVals[KeyOllamaEmbedModel],
		ChatEnabled:          parseBool(dbVals[KeyChatEnabled]),
		ChatAPIURL:           dbVals[KeyChatAPIURL],
		ChatModel:            dbVals[KeyChatModel],
		SNSyncEnabled:        parseBool(dbVals[KeySNSyncEnabled]),
		SNSyncInterval:       parseIntWithDefault(dbVals[KeySNSyncInterval], 300),
		SNAPIURL:             dbVals[KeySNAPIURL],
		SNAccount:            dbVals[KeySNAccount],
		SNPassword:           dbVals[KeySNPassword],
		LogLevel:             dbVals[KeyLogLevel],
		LogFormat:            dbVals[KeyLogFormat],
		LogFile:              dbVals[KeyLogFile],
		LogFileMaxMB:         parseIntWithDefault(dbVals[KeyLogFileMaxMB], 50),
		LogFileMaxAge:        parseIntWithDefault(dbVals[KeyLogFileMaxAge], 30),
		LogFileMaxBackup:     parseIntWithDefault(dbVals[KeyLogFileMaxBackup], 5),
		LogSyslogAddr:        dbVals[KeyLogSyslogAddr],
		CalDAVCollectionName: dbVals[KeyCalDAVCollectionName],
		DueTimeMode:          dbVals[KeyDueTimeMode],
		WebEnabled:           parseBool(dbVals[KeyWebEnabled]),
		SocketIOURL:          dbVals[KeySocketIOURL],
		DBHost:               dbVals[KeyDBHost],
		DBPort:               dbVals[KeyDBPort],
		DBEnvPath:            dbVals[KeyDBEnvPath],
		UserID:               parseInt64(dbVals[KeyUserID]),
	}

	return cfg, nil
}

// configToMap converts a Config struct to a map for comparison.
func configToMap(cfg *Config) map[string]string {
	m := map[string]string{
		KeyUsername:             cfg.Username,
		KeyPasswordHash:         cfg.PasswordHash,
		KeyOCREnabled:           boolToString(cfg.OCREnabled),
		KeyOCRAPIURL:            cfg.OCRAPIURL,
		KeyOCRAPIKey:            cfg.OCRAPIKey,
		KeyOCRModel:             cfg.OCRModel,
		KeyOCRConcurrency:       strconv.Itoa(cfg.OCRConcurrency),
		KeyOCRMaxFileMB:         strconv.Itoa(cfg.OCRMaxFileMB),
		KeyOCRFormat:            cfg.OCRFormat,
		KeyEmbedEnabled:         boolToString(cfg.EmbedEnabled),
		KeyOllamaURL:            cfg.OllamaURL,
		KeyOllamaEmbedModel:     cfg.OllamaEmbedModel,
		KeyChatEnabled:          boolToString(cfg.ChatEnabled),
		KeyChatAPIURL:           cfg.ChatAPIURL,
		KeyChatModel:            cfg.ChatModel,
		KeySNSyncEnabled:        boolToString(cfg.SNSyncEnabled),
		KeySNSyncInterval:       strconv.Itoa(cfg.SNSyncInterval),
		KeySNAPIURL:             cfg.SNAPIURL,
		KeySNAccount:            cfg.SNAccount,
		KeySNPassword:           cfg.SNPassword,
		KeyLogLevel:             cfg.LogLevel,
		KeyLogFormat:            cfg.LogFormat,
		KeyLogFile:              cfg.LogFile,
		KeyLogFileMaxMB:         strconv.Itoa(cfg.LogFileMaxMB),
		KeyLogFileMaxAge:        strconv.Itoa(cfg.LogFileMaxAge),
		KeyLogFileMaxBackup:     strconv.Itoa(cfg.LogFileMaxBackup),
		KeyLogSyslogAddr:        cfg.LogSyslogAddr,
		KeyCalDAVCollectionName: cfg.CalDAVCollectionName,
		KeyDueTimeMode:          cfg.DueTimeMode,
		KeyWebEnabled:           boolToString(cfg.WebEnabled),
		KeySocketIOURL:          cfg.SocketIOURL,
		KeyDBHost:    cfg.DBHost,
		KeyDBPort:    cfg.DBPort,
		KeyDBEnvPath: cfg.DBEnvPath,
		KeyUserID:    strconv.FormatInt(cfg.UserID, 10),
	}
	return m
}

// Parsing helpers.

func parseBool(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseIntWithDefault(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func parseInt64(v string) int64 {
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
