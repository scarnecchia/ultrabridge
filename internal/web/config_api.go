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
func (h *Handler) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var incoming IncomingConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Convert incoming to appconfig.Config, handling password hashing.
	cfg := &appconfig.Config{
		Username:             incoming.Username,
		PasswordHash:         "", // will be set below if password provided
		OCREnabled:           incoming.OCREnabled,
		OCRAPIURL:            incoming.OCRAPIURL,
		OCRAPIKey:            incoming.OCRAPIKey,
		OCRModel:             incoming.OCRModel,
		OCRConcurrency:       incoming.OCRConcurrency,
		OCRMaxFileMB:         incoming.OCRMaxFileMB,
		OCRFormat:            incoming.OCRFormat,
		EmbedEnabled:         incoming.EmbedEnabled,
		OllamaURL:            incoming.OllamaURL,
		OllamaEmbedModel:     incoming.OllamaEmbedModel,
		ChatEnabled:          incoming.ChatEnabled,
		ChatAPIURL:           incoming.ChatAPIURL,
		ChatModel:            incoming.ChatModel,
		SNSyncEnabled:        incoming.SNSyncEnabled,
		SNSyncInterval:       incoming.SNSyncInterval,
		SNAPIURL:             incoming.SNAPIURL,
		SNAccount:            incoming.SNAccount,
		SNPassword:           incoming.SNPassword,
		LogLevel:             incoming.LogLevel,
		LogFormat:            incoming.LogFormat,
		LogFile:              incoming.LogFile,
		LogFileMaxMB:         incoming.LogFileMaxMB,
		LogFileMaxAge:        incoming.LogFileMaxAge,
		LogFileMaxBackup:     incoming.LogFileMaxBackup,
		LogSyslogAddr:        incoming.LogSyslogAddr,
		CalDAVCollectionName: incoming.CalDAVCollectionName,
		DueTimeMode:          incoming.DueTimeMode,
		WebEnabled:           incoming.WebEnabled,
		SocketIOURL:          incoming.SocketIOURL,
		DBHost:               incoming.DBHost,
		DBPort:    incoming.DBPort,
		DBEnvPath: incoming.DBEnvPath,
		UserID:    incoming.UserID,
	}

	// If plaintext password provided, hash it and set PasswordHash.
	if incoming.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(incoming.Password), bcrypt.DefaultCost)
		if err != nil {
			h.logger.Error("bcrypt hash failed", "error", err)
			apiError(w, http.StatusInternalServerError, "failed to hash password")
			return
		}
		cfg.PasswordHash = string(hash)
	} else {
		// No password provided; keep existing hash by loading from DB.
		current, err := appconfig.Load(ctx, h.noteDB)
		if err == nil {
			cfg.PasswordHash = current.PasswordHash
		} else {
			h.logger.Warn("failed to load existing config for password preservation", "error", err)
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
