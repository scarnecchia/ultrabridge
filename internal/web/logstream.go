package web

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/sysop/ultrabridge/internal/logging"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In a real application, you might want to restrict this further
		return true
	},
}

// RegisterLogStreamHandler registers the WebSocket log streaming endpoint
// on the mux.
func (h *Handler) registerLogStreamHandler(broadcaster *logging.LogBroadcaster) {
	h.mux.HandleFunc("GET /ws/logs", func(w http.ResponseWriter, r *http.Request) {
		h.handleWebSocketLogs(w, r, broadcaster)
	})
}

// handleWebSocketLogs upgrades the HTTP connection to WebSocket and streams
// log entries with optional level filtering.
func (h *Handler) handleWebSocketLogs(w http.ResponseWriter, r *http.Request, broadcaster *logging.LogBroadcaster) {
	// Get log level filter from query parameter
	levelStr := strings.ToLower(r.URL.Query().Get("level"))
	if levelStr == "" {
		levelStr = "info"
	}
	minLevel := parseLogLevel(levelStr)

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade websocket", "error", err)
		return
	}
	defer conn.Close()

	// Subscribe to log entries and unsubscribe on disconnect
	subscriberID, logChan := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(subscriberID)

	// Send log entries to the WebSocket client
	for entry := range logChan {
		// Filter by level
		if !shouldIncludeLogEntry(entry, minLevel) {
			continue
		}

		// Send to WebSocket client
		if err := conn.WriteMessage(websocket.TextMessage, []byte(entry)); err != nil {
			h.logger.Warn("failed to write log to websocket", "error", err)
			return
		}
	}
}

// parseLogLevel converts a string to slog.Level.
func parseLogLevel(s string) slog.Level {
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

// shouldIncludeLogEntry checks if a log entry should be included based on
// the minimum level filter.
func shouldIncludeLogEntry(entry string, minLevel slog.Level) bool {
	// Parse the log level from the entry format "[LEVEL] MESSAGE"
	// Expected format: "[DEBUG] ...", "[INFO] ...", "[WARN] ...", "[ERROR] ..."
	if len(entry) < 2 || entry[0] != '[' {
		return true
	}

	// Find the closing bracket
	closingIdx := strings.Index(entry[1:], "]")
	if closingIdx == -1 {
		return true
	}

	levelStr := strings.ToUpper(strings.TrimSpace(entry[1 : closingIdx+1]))
	if levelStr == "" {
		return true
	}

	entryLevel := parseLogLevel(levelStr)

	// Include if entry level >= minLevel
	return entryLevel >= minLevel
}
