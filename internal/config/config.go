package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Database (read from .dbenv)
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string

	// Auth
	Username     string
	PasswordHash string

	// CalDAV
	CalDAVCollectionName string
	DueTimeMode          string // "preserve" or "date_only"

	// Logging
	LogLevel         string
	LogFormat        string
	LogFile          string
	LogFileMaxMB     int
	LogFileMaxAge    int
	LogFileMaxBackup int
	LogSyslogAddr    string

	// Server
	ListenAddr string
	WebEnabled bool

	// Socket.io
	SocketIOURL string

	// User
	UserID int64 // Explicit user ID override (0 = auto-discover)

	// Paths
	DBEnvPath  string
	TaskDBPath string

	// Supernote sync
	SNSyncEnabled  bool
	SNSyncInterval int    // seconds
	SNAPIURL       string
	SNAccount      string
	SNPassword     string

	// Notes pipeline
	NotesPath      string
	DBPath         string
	BackupPath     string
	OCREnabled     bool
	OCRAPIURL      string
	OCRAPIKey      string
	OCRModel       string
	OCRConcurrency int
	OCRMaxFileMB   int
	OCRFormat      string // "anthropic" (default) or "openai"
}

func Load() (*Config, error) {
	cfg := &Config{
		DBHost:               envOrDefault("UB_DB_HOST", "mariadb"),
		DBPort:               envOrDefault("UB_DB_PORT", "3306"),
		CalDAVCollectionName: envOrDefault("UB_CALDAV_COLLECTION_NAME", "Supernote Tasks"),
		DueTimeMode:          envOrDefault("UB_DUE_TIME_MODE", "preserve"),
		LogLevel:             envOrDefault("UB_LOG_LEVEL", "info"),
		LogFormat:            envOrDefault("UB_LOG_FORMAT", "json"),
		LogFile:              os.Getenv("UB_LOG_FILE"),
		LogFileMaxMB:         envIntOrDefault("UB_LOG_FILE_MAX_MB", 50),
		LogFileMaxAge:        envIntOrDefault("UB_LOG_FILE_MAX_AGE_DAYS", 30),
		LogFileMaxBackup:     envIntOrDefault("UB_LOG_FILE_MAX_BACKUPS", 5),
		LogSyslogAddr:        os.Getenv("UB_LOG_SYSLOG_ADDR"),
		ListenAddr:           envOrDefault("UB_LISTEN_ADDR", ":8443"),
		WebEnabled:           envOrDefault("UB_WEB_ENABLED", "true") == "true",
		SocketIOURL:          envOrDefault("UB_SOCKETIO_URL", "ws://supernote-service:8080/socket.io/"),
		Username:             os.Getenv("UB_USERNAME"),
		PasswordHash:         os.Getenv("UB_PASSWORD_HASH"),
		DBEnvPath:            envOrDefault("UB_SUPERNOTE_DBENV_PATH", "/run/secrets/dbenv"),
		UserID:               int64(envIntOrDefault("UB_USER_ID", 0)),
	}

	cfg.NotesPath      = os.Getenv("UB_NOTES_PATH")
	cfg.DBPath         = envOrDefault("UB_DB_PATH", "/data/ultrabridge.db")
	cfg.TaskDBPath     = envOrDefault("UB_TASK_DB_PATH", "/data/ultrabridge-tasks.db")
	cfg.BackupPath     = os.Getenv("UB_BACKUP_PATH")
	cfg.SNSyncEnabled  = envBoolOrDefault("UB_SN_SYNC_ENABLED", false)
	cfg.SNSyncInterval = envIntOrDefault("UB_SN_SYNC_INTERVAL", 300) // 5 minutes
	cfg.SNAPIURL       = envOrDefault("UB_SN_API_URL", "http://supernote-service:8080")
	cfg.SNAccount      = os.Getenv("UB_SN_ACCOUNT")
	cfg.SNPassword     = os.Getenv("UB_SN_PASSWORD")
	cfg.OCREnabled     = envBoolOrDefault("UB_OCR_ENABLED", false)
	cfg.OCRAPIURL      = os.Getenv("UB_OCR_API_URL")
	cfg.OCRAPIKey      = os.Getenv("UB_OCR_API_KEY")
	cfg.OCRModel       = os.Getenv("UB_OCR_MODEL")
	cfg.OCRConcurrency = envIntOrDefault("UB_OCR_CONCURRENCY", 1)
	cfg.OCRMaxFileMB   = envIntOrDefault("UB_OCR_MAX_FILE_MB", 0)
	cfg.OCRFormat      = envOrDefault("UB_OCR_FORMAT", "anthropic")

	if err := cfg.loadDBEnv(); err != nil {
		return nil, fmt.Errorf("loading .dbenv: %w", err)
	}

	return cfg, nil
}

func (c *Config) loadDBEnv() error {
	// Prefer environment variables (set via Docker env_file: .dbenv).
	// Fall back to parsing the file directly for backward compatibility.
	if v := os.Getenv("MYSQL_DATABASE"); v != "" {
		c.DBName = v
	}
	if v := os.Getenv("MYSQL_USER"); v != "" {
		c.DBUser = v
	}
	if v := os.Getenv("MYSQL_PASSWORD"); v != "" {
		c.DBPassword = v
	}

	// If we got all three from env, skip file parsing
	if c.DBName != "" && c.DBUser != "" && c.DBPassword != "" {
		return nil
	}

	// Try file as fallback
	f, err := os.Open(c.DBEnvPath)
	if err != nil {
		if c.DBName != "" || c.DBUser != "" {
			// Got partial config from env, file is optional
			return nil
		}
		return fmt.Errorf("open %s: %w", c.DBEnvPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "MYSQL_DATABASE":
			if c.DBName == "" {
				c.DBName = val
			}
		case "MYSQL_USER":
			if c.DBUser == "" {
				c.DBUser = val
			}
		case "MYSQL_PASSWORD":
			if c.DBPassword == "" {
				c.DBPassword = val
			}
		}
	}
	return scanner.Err()
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBoolOrDefault(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return strings.EqualFold(v, "true") || v == "1"
}
