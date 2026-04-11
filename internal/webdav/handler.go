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
func ExtractPathMetadata(relPath string) PathMetadata {
	// Expected: onyx/{model}/{type}/{folder}/{name}.note
	parts := strings.Split(strings.TrimPrefix(relPath, "/"), "/")
	var pm PathMetadata
	// parts[0] = "onyx", parts[1] = model, parts[2] = type, parts[3] = folder, parts[4] = name.note
	if len(parts) >= 2 {
		pm.DeviceModel = parts[1]
	}
	if len(parts) >= 3 {
		pm.NoteType = parts[2]
	}
	if len(parts) >= 4 {
		pm.Folder = parts[3]
	}
	if len(parts) >= 5 {
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
