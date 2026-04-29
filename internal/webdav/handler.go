package webdav

import (
	"net/http"
	"strings"

	"golang.org/x/net/webdav"
)

// PathMetadata extracted from the Boox upload path structure:
// /onyx/{device_model}/{Notebooks|Reading Notes}/{folder_name}/{notebook_name}.note
type PathMetadata struct {
	DeviceModel string // e.g., "Tab_Ultra_C_Pro"
	NoteType    string // "Notebooks" or "Reading Notes"
	Folder      string // folder name
	NoteName    string // notebook name (without .note extension)
}

// ExtractPathMetadata parses Boox device path convention from a relative path.
// Two layouts are supported:
//   - Modern WebDAV: onyx/{model}/{type}/{folder}/{name}.note
//   - Legacy/imported: {model}/{type}/{folder}/{name}.note (no onyx/ prefix)
//
// The legacy layout is what bulk-imported trees produced before the WebDAV
// convention added the onyx/ root. Treating both the same prevents filename-as-
// folder pollution when legacy files get re-ingested via the maintenance
// scan-untracked path.
func ExtractPathMetadata(relPath string) PathMetadata {
	parts := strings.Split(strings.TrimPrefix(relPath, "/"), "/")
	if len(parts) > 0 && parts[0] == "onyx" {
		parts = parts[1:]
	}
	var pm PathMetadata
	// Need at least model + filename (2 parts) to extract any metadata.
	// A bare filename like "short.note" yields nothing.
	if len(parts) < 2 {
		return pm
	}
	pm.DeviceModel = parts[0]
	if len(parts) >= 3 {
		pm.NoteType = parts[1]
	}
	if len(parts) >= 4 {
		pm.Folder = parts[2]
		name := parts[len(parts)-1]
		if idx := strings.LastIndex(name, "."); idx > 0 {
			pm.NoteName = name[:idx]
		} else {
			pm.NoteName = name
		}
	}
	return pm
}

// NewHandler creates a WebDAV handler for Boox note uploads.
// rootDir is the absolute path to the Boox source notes directory.
// onUpload is called after each .note file is written.
func NewHandler(rootDir string, onUpload OnNoteUpload) http.Handler {
	fs := NewFS(rootDir, onUpload)

	return &webdav.Handler{
		Prefix:     "/webdav",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}
}
