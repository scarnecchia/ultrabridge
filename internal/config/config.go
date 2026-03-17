package config

import (
	"bufio"
	"fmt"
	"os"
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

	// Paths
	DBEnvPath string
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
	}

	if err := cfg.loadDBEnv(); err != nil {
		return nil, fmt.Errorf("loading .dbenv: %w", err)
	}

	return cfg, nil
}

func (c *Config) loadDBEnv() error {
	f, err := os.Open(c.DBEnvPath)
	if err != nil {
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
			c.DBName = val
		case "MYSQL_USER":
			c.DBUser = val
		case "MYSQL_PASSWORD":
			c.DBPassword = val
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
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}
