package web

import (
	"encoding/json"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	"github.com/sysop/ultrabridge/internal/appconfig"
)

// RedactedConfig is like appconfig.Config but with secrets redacted for API responses.
type RedactedConfig struct {
	Username              string `json:"username"`
	PasswordHash          string `json:"password_hash"` // "[set]" or "[not set]"
	OCREnabled            bool   `json:"ocr_enabled"`
	OCRAPIURL             string `json:"ocr_api_url"`
	OCRAPIKey             string `json:"ocr_api_key"` // "[set]" or "[not set]"
	OCRModel              string `json:"ocr_model"`
	OCRConcurrency        int    `json:"ocr_concurrency"`
	OCRMaxFileMB          int    `json:"ocr_max_file_mb"`
	OCRFormat             string `json:"ocr_format"`
	EmbedEnabled          bool   `json:"embed_enabled"`
	OllamaURL             string `json:"ollama_url"`
	OllamaEmbedModel      string `json:"ollama_embed_model"`
	ChatEnabled           bool   `json:"chat_enabled"`
	ChatAPIURL            string `json:"chat_api_url"`
	ChatModel             string `json:"chat_model"`
	SNSyncEnabled         bool   `json:"sn_sync_enabled"`
	SNSyncInterval        int    `json:"sn_sync_interval"`
	SNAPIURL              string `json:"sn_api_url"`
	SNAccount             string `json:"sn_account"`
	SNPassword            string `json:"sn_password"` // "[set]" or "[not set]"
	LogLevel              string `json:"log_level"`
	LogFormat             string `json:"log_format"`
	LogFile               string `json:"log_file"`
	LogFileMaxMB          int    `json:"log_file_max_mb"`
	LogFileMaxAge         int    `json:"log_file_max_age"`
	LogFileMaxBackup      int    `json:"log_file_max_backup"`
	LogSyslogAddr         string `json:"log_syslog_addr"`
	CalDAVCollectionName  string `json:"caldav_collection_name"`
	DueTimeMode           string `json:"due_time_mode"`
	WebEnabled            bool   `json:"web_enabled"`
	SocketIOURL           string `json:"socketio_url"`
	DBHost                string `json:"db_host"`
	DBPort                string `json:"db_port"`
	DBEnvPath             string `json:"db_env_path"`
	UserID                int64  `json:"user_id"`
}

// redactConfig returns a copy of cfg with secrets replaced with "[set]" or "[not set]".
func redactConfig(cfg *appconfig.Config) *RedactedConfig {
	if cfg == nil {
		return nil
	}
	return &RedactedConfig{
		Username:             cfg.Username,
		PasswordHash:         redactSecret(cfg.PasswordHash),
		OCREnabled:           cfg.OCREnabled,
		OCRAPIURL:            cfg.OCRAPIURL,
		OCRAPIKey:            redactSecret(cfg.OCRAPIKey),
		OCRModel:             cfg.OCRModel,
		OCRConcurrency:       cfg.OCRConcurrency,
		OCRMaxFileMB:         cfg.OCRMaxFileMB,
		OCRFormat:            cfg.OCRFormat,
		EmbedEnabled:         cfg.EmbedEnabled,
		OllamaURL:            cfg.OllamaURL,
		OllamaEmbedModel:     cfg.OllamaEmbedModel,
		ChatEnabled:          cfg.ChatEnabled,
		ChatAPIURL:           cfg.ChatAPIURL,
		ChatModel:            cfg.ChatModel,
		SNSyncEnabled:        cfg.SNSyncEnabled,
		SNSyncInterval:       cfg.SNSyncInterval,
		SNAPIURL:             cfg.SNAPIURL,
		SNAccount:            cfg.SNAccount,
		SNPassword:           redactSecret(cfg.SNPassword),
		LogLevel:             cfg.LogLevel,
		LogFormat:            cfg.LogFormat,
		LogFile:              cfg.LogFile,
		LogFileMaxMB:         cfg.LogFileMaxMB,
		LogFileMaxAge:        cfg.LogFileMaxAge,
		LogFileMaxBackup:     cfg.LogFileMaxBackup,
		LogSyslogAddr:        cfg.LogSyslogAddr,
		CalDAVCollectionName: cfg.CalDAVCollectionName,
		DueTimeMode:          cfg.DueTimeMode,
		WebEnabled:  cfg.WebEnabled,
		SocketIOURL: cfg.SocketIOURL,
		DBHost:      cfg.DBHost,
		DBPort:      cfg.DBPort,
		DBEnvPath:   cfg.DBEnvPath,
		UserID:      cfg.UserID,
	}
}

// redactSecret returns "[set]" if secret is non-empty, "[not set]" otherwise.
func redactSecret(s string) string {
	if s != "" {
		return "[set]"
	}
	return "[not set]"
}

// handleGetConfig handles GET /api/config — returns current config with secrets redacted.
func (h *Handler) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, err := appconfig.Load(ctx, h.noteDB)
	if err != nil {
		h.logger.Error("load config", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to load config")
		return
	}

	redacted := redactConfig(cfg)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(redacted)
}

// IncomingConfig is the request body format for PUT /api/config.
// It mirrors appconfig.Config but adds a "password" field for plaintext input.
type IncomingConfig struct {
	Username              string `json:"username"`
	Password              string `json:"password"` // plaintext; if non-empty, hashed server-side
	OCREnabled            bool   `json:"ocr_enabled"`
	OCRAPIURL             string `json:"ocr_api_url"`
	OCRAPIKey             string `json:"ocr_api_key"`
	OCRModel              string `json:"ocr_model"`
	OCRConcurrency        int    `json:"ocr_concurrency"`
	OCRMaxFileMB          int    `json:"ocr_max_file_mb"`
	OCRFormat             string `json:"ocr_format"`
	EmbedEnabled          bool   `json:"embed_enabled"`
	OllamaURL             string `json:"ollama_url"`
	OllamaEmbedModel      string `json:"ollama_embed_model"`
	ChatEnabled           bool   `json:"chat_enabled"`
	ChatAPIURL            string `json:"chat_api_url"`
	ChatModel             string `json:"chat_model"`
	SNSyncEnabled         bool   `json:"sn_sync_enabled"`
	SNSyncInterval        int    `json:"sn_sync_interval"`
	SNAPIURL              string `json:"sn_api_url"`
	SNAccount             string `json:"sn_account"`
	SNPassword            string `json:"sn_password"`
	LogLevel              string `json:"log_level"`
	LogFormat             string `json:"log_format"`
	LogFile               string `json:"log_file"`
	LogFileMaxMB          int    `json:"log_file_max_mb"`
	LogFileMaxAge         int    `json:"log_file_max_age"`
	LogFileMaxBackup      int    `json:"log_file_max_backup"`
	LogSyslogAddr         string `json:"log_syslog_addr"`
	CalDAVCollectionName  string `json:"caldav_collection_name"`
	DueTimeMode           string `json:"due_time_mode"`
	WebEnabled            bool   `json:"web_enabled"`
	SocketIOURL           string `json:"socketio_url"`
	DBHost                string `json:"db_host"`
	DBPort                string `json:"db_port"`
	DBEnvPath             string `json:"db_env_path"`
	UserID                int64  `json:"user_id"`
}

// handlePutConfig handles PUT /api/config — update config with password hashing.
// Uses merge semantics: loads current config from DB first, then overlays
// only the fields present in the incoming JSON. This prevents partial PUTs
// from wiping fields the client didn't send (e.g., web_enabled defaulting
// to false when omitted from the request body).
func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Load current config as the base — incoming values overlay this.
	cfg, err := appconfig.Load(ctx, h.noteDB)
	if err != nil {
		h.logger.Error("load config for merge", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to load current config")
		return
	}

	// Decode incoming into a map so we can detect which fields were actually sent.
	var rawMap map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawMap); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Re-encode the map and decode into IncomingConfig for typed access.
	rawBytes, _ := json.Marshal(rawMap)
	var incoming IncomingConfig
	json.Unmarshal(rawBytes, &incoming)

	// Only overlay fields that were explicitly present in the request.
	if _, ok := rawMap["username"]; ok {
		cfg.Username = incoming.Username
	}
	if _, ok := rawMap["ocr_enabled"]; ok {
		cfg.OCREnabled = incoming.OCREnabled
	}
	if _, ok := rawMap["ocr_api_url"]; ok {
		cfg.OCRAPIURL = incoming.OCRAPIURL
	}
	if _, ok := rawMap["ocr_api_key"]; ok {
		cfg.OCRAPIKey = incoming.OCRAPIKey
	}
	if _, ok := rawMap["ocr_model"]; ok {
		cfg.OCRModel = incoming.OCRModel
	}
	if _, ok := rawMap["ocr_concurrency"]; ok {
		cfg.OCRConcurrency = incoming.OCRConcurrency
	}
	if _, ok := rawMap["ocr_max_file_mb"]; ok {
		cfg.OCRMaxFileMB = incoming.OCRMaxFileMB
	}
	if _, ok := rawMap["ocr_format"]; ok {
		cfg.OCRFormat = incoming.OCRFormat
	}
	if _, ok := rawMap["embed_enabled"]; ok {
		cfg.EmbedEnabled = incoming.EmbedEnabled
	}
	if _, ok := rawMap["ollama_url"]; ok {
		cfg.OllamaURL = incoming.OllamaURL
	}
	if _, ok := rawMap["ollama_embed_model"]; ok {
		cfg.OllamaEmbedModel = incoming.OllamaEmbedModel
	}
	if _, ok := rawMap["chat_enabled"]; ok {
		cfg.ChatEnabled = incoming.ChatEnabled
	}
	if _, ok := rawMap["chat_api_url"]; ok {
		cfg.ChatAPIURL = incoming.ChatAPIURL
	}
	if _, ok := rawMap["chat_model"]; ok {
		cfg.ChatModel = incoming.ChatModel
	}
	if _, ok := rawMap["sn_sync_enabled"]; ok {
		cfg.SNSyncEnabled = incoming.SNSyncEnabled
	}
	if _, ok := rawMap["sn_sync_interval"]; ok {
		cfg.SNSyncInterval = incoming.SNSyncInterval
	}
	if _, ok := rawMap["sn_api_url"]; ok {
		cfg.SNAPIURL = incoming.SNAPIURL
	}
	if _, ok := rawMap["sn_account"]; ok {
		cfg.SNAccount = incoming.SNAccount
	}
	if _, ok := rawMap["sn_password"]; ok {
		cfg.SNPassword = incoming.SNPassword
	}
	if _, ok := rawMap["log_level"]; ok {
		cfg.LogLevel = incoming.LogLevel
	}
	if _, ok := rawMap["log_format"]; ok {
		cfg.LogFormat = incoming.LogFormat
	}
	if _, ok := rawMap["log_file"]; ok {
		cfg.LogFile = incoming.LogFile
	}
	if _, ok := rawMap["log_file_max_mb"]; ok {
		cfg.LogFileMaxMB = incoming.LogFileMaxMB
	}
	if _, ok := rawMap["log_file_max_age"]; ok {
		cfg.LogFileMaxAge = incoming.LogFileMaxAge
	}
	if _, ok := rawMap["log_file_max_backup"]; ok {
		cfg.LogFileMaxBackup = incoming.LogFileMaxBackup
	}
	if _, ok := rawMap["log_syslog_addr"]; ok {
		cfg.LogSyslogAddr = incoming.LogSyslogAddr
	}
	if _, ok := rawMap["caldav_collection_name"]; ok {
		cfg.CalDAVCollectionName = incoming.CalDAVCollectionName
	}
	if _, ok := rawMap["due_time_mode"]; ok {
		cfg.DueTimeMode = incoming.DueTimeMode
	}
	if _, ok := rawMap["web_enabled"]; ok {
		cfg.WebEnabled = incoming.WebEnabled
	}
	if _, ok := rawMap["socketio_url"]; ok {
		cfg.SocketIOURL = incoming.SocketIOURL
	}
	if _, ok := rawMap["db_host"]; ok {
		cfg.DBHost = incoming.DBHost
	}
	if _, ok := rawMap["db_port"]; ok {
		cfg.DBPort = incoming.DBPort
	}
	if _, ok := rawMap["db_env_path"]; ok {
		cfg.DBEnvPath = incoming.DBEnvPath
	}
	if _, ok := rawMap["user_id"]; ok {
		cfg.UserID = incoming.UserID
	}

	// If plaintext password provided, hash it and set PasswordHash.
	if pw, ok := rawMap["password"]; ok {
		var password string
		json.Unmarshal(pw, &password)
		if password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				h.logger.Error("bcrypt hash failed", "error", err)
				apiError(w, http.StatusInternalServerError, "failed to hash password")
				return
			}
			cfg.PasswordHash = string(hash)
		}
	}

	// Save to DB.
	result, err := appconfig.Save(ctx, h.noteDB, cfg)
	if err != nil {
		h.logger.Error("save config", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to save config")
		return
	}

	// Set dirty flag if restart required.
	if result.RestartRequired {
		h.configDirty.Store(true)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
