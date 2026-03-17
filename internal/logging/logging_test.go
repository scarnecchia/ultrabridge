package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestSetupJSONFormat(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger with JSON format
	cfg := Config{
		Level:  "info",
		Format: "json",
	}

	// We need to test Setup in isolation, but it calls slog.SetDefault
	// Instead, we'll test the handler creation directly
	level := parseLevel(cfg.Level)
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewJSONHandler(&buf, opts)
	logger := slog.New(handler)

	// Log a message
	logger.Info("test message", "key", "value")

	// Verify output is valid JSON
	output := buf.String()
	var logEntry map[string]interface{}
	err := json.Unmarshal([]byte(output), &logEntry)
	if err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Verify expected fields
	if logEntry["msg"] != "test message" {
		t.Errorf("expected msg='test message', got %v", logEntry["msg"])
	}
	if logEntry["key"] != "value" {
		t.Errorf("expected key='value', got %v", logEntry["key"])
	}
}

func TestSetupTextFormat(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger with text format
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewTextHandler(&buf, opts)
	logger := slog.New(handler)

	// Log a message
	logger.Info("test message", "key", "value")

	// Verify output contains key=value pairs
	output := buf.String()
	if !strings.Contains(output, "key=value") {
		t.Errorf("expected output to contain 'key=value', got: %s", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("expected output to contain 'test message', got: %s", output)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger at info level
	level := parseLevel("info")
	opts := &slog.HandlerOptions{Level: level}
	handler := slog.NewTextHandler(&buf, opts)
	logger := slog.New(handler)

	// Log at different levels
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()

	// At info level, debug should be suppressed
	if strings.Contains(output, "debug message") {
		t.Errorf("debug message should be suppressed at info level")
	}
	// But info, warn, error should pass through
	if !strings.Contains(output, "info message") {
		t.Errorf("info message should be present")
	}
	if !strings.Contains(output, "warn message") {
		t.Errorf("warn message should be present")
	}
	if !strings.Contains(output, "error message") {
		t.Errorf("error message should be present")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo}, // defaults to info
		{"", slog.LevelInfo},        // defaults to info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseLevel(tt.input)
			if result != tt.expected {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
