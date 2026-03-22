package pipeline

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/sysop/ultrabridge/internal/notestore"
)

// runEngineIOListener watches for inbound Engine.IO events and enqueues
// affected note paths. Parses FILE-SYN and DOWNLOADFILE frames from the
// Supernote Private Cloud service.
//
// If supernote-service emits no useful sync-complete events, extractNotePaths
// returns nil and file detection falls back to the watcher and reconciler.
func (p *Pipeline) runEngineIOListener(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-p.events:
			if !ok {
				return
			}
			for _, path := range extractNotePaths(msg, p.notesPath) {
				p.enqueue(ctx, path)
			}
		}
	}
}

// serverPayload represents the parsed payload from an Engine.IO message.
type serverPayload struct {
	MsgType string      `json:"msgType"`
	Data    []fileEntry `json:"data"`
}

// fileEntry represents a single file entry in the data array.
type fileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	MD5  string `json:"md5"`
	Size int64  `json:"size"`
}

// extractNotePaths parses an inbound Engine.IO frame and returns any note
// file paths that should be queued for processing.
//
// Frame format: 42["ServerMessage","<escaped JSON>"]
// The JSON contains msgType and data fields.
// Only FILE-SYN and DOWNLOADFILE messages are processed.
// Only .note files are returned.
// Returns absolute paths resolved against notesPath.
func extractNotePaths(msg []byte, notesPath string) []string {
	if len(msg) == 0 {
		return nil
	}

	// Verify "42" prefix
	if len(msg) < 2 || !strings.HasPrefix(string(msg), "42") {
		return nil
	}

	// Strip the "42" prefix and parse as JSON array
	var socketMsg []json.RawMessage
	err := json.Unmarshal(msg[2:], &socketMsg)
	if err != nil || len(socketMsg) < 2 {
		return nil
	}

	// First element should be "ServerMessage"
	var eventName string
	err = json.Unmarshal(socketMsg[0], &eventName)
	if err != nil || eventName != "ServerMessage" {
		return nil
	}

	// Second element is a JSON-encoded string containing the payload
	var payloadStr string
	err = json.Unmarshal(socketMsg[1], &payloadStr)
	if err != nil {
		return nil
	}

	// Parse the payload JSON
	var payload serverPayload
	err = json.Unmarshal([]byte(payloadStr), &payload)
	if err != nil {
		return nil
	}

	// Check msgType is FILE-SYN or DOWNLOADFILE
	if payload.MsgType != "FILE-SYN" && payload.MsgType != "DOWNLOADFILE" {
		return nil
	}

	// Extract .note file paths
	var result []string
	for _, entry := range payload.Data {
		// Filter for .note files
		if notestore.ClassifyFileType(filepath.Ext(entry.Path)) != notestore.FileTypeNote {
			continue
		}

		// Resolve to absolute path
		absPath := filepath.Join(notesPath, entry.Path)
		result = append(result, absPath)
	}

	return result
}
