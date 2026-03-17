package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Note: dialSyslog is in syslog.go with //go:build !windows tag

type Config struct {
	Level         string // "debug", "info", "warn", "error"
	Format        string // "json" or "text"
	File          string // path, empty = no file logging
	FileMaxMB     int
	FileMaxAge    int
	FileMaxBackup int
	SyslogAddr    string // e.g., "udp://graylog:1514", empty = no syslog
}

func Setup(cfg Config) *slog.Logger {
	var writers []io.Writer
	writers = append(writers, os.Stdout)

	if cfg.File != "" {
		writers = append(writers, &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.FileMaxMB,
			MaxAge:     cfg.FileMaxAge,
			MaxBackups: cfg.FileMaxBackup,
		})
	}

	if cfg.SyslogAddr != "" {
		if w := dialSyslog(cfg.SyslogAddr); w != nil {
			writers = append(writers, w)
		}
	}

	w := io.MultiWriter(writers...)

	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
