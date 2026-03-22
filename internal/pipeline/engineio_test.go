package pipeline

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// buildFrame constructs a valid Socket.IO frame for testing.
// Format: 42["ServerMessage","<escaped JSON>"]
func buildFrame(t *testing.T, msgType string, entries []fileEntry) []byte {
	t.Helper()
	payload := serverPayload{MsgType: msgType, Data: entries}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr, _ := json.Marshal(string(payloadJSON))
	return []byte(fmt.Sprintf(`42["ServerMessage",%s]`, payloadStr))
}

func TestExtractNotePaths(t *testing.T) {
	notesPath := "/notes"

	tests := []struct {
		name     string
		frame    []byte
		notesPath string
		want     []string
	}{
		{
			name: "AC4.1 Valid FILE-SYN with single .note file",
			frame: buildFrame(t, "FILE-SYN", []fileEntry{
				{Name: "MyNote", Path: "Note/MyNote.note", MD5: "abc123", Size: 12345},
			}),
			notesPath: notesPath,
			want: []string{
				filepath.Join(notesPath, "Note/MyNote.note"),
			},
		},
		{
			name: "AC4.1 Valid DOWNLOADFILE with single .note file",
			frame: buildFrame(t, "DOWNLOADFILE", []fileEntry{
				{Name: "MyNote", Path: "Note/MyNote.note", MD5: "abc123", Size: 12345},
			}),
			notesPath: notesPath,
			want: []string{
				filepath.Join(notesPath, "Note/MyNote.note"),
			},
		},
		{
			name: "AC4.2 Multiple entries with mixed file types",
			frame: buildFrame(t, "FILE-SYN", []fileEntry{
				{Name: "Note1", Path: "Note/Note1.note", MD5: "abc", Size: 1000},
				{Name: "Document", Path: "Docs/Document.pdf", MD5: "def", Size: 2000},
				{Name: "Note2", Path: "Note/Note2.note", MD5: "ghi", Size: 1500},
				{Name: "Book", Path: "Books/Book.epub", MD5: "jkl", Size: 5000},
			}),
			notesPath: notesPath,
			want: []string{
				filepath.Join(notesPath, "Note/Note1.note"),
				filepath.Join(notesPath, "Note/Note2.note"),
			},
		},
		{
			name: "AC4.3 Non-FILE-SYN msgType returns nil",
			frame: buildFrame(t, "SOME-OTHER-TYPE", []fileEntry{
				{Name: "MyNote", Path: "Note/MyNote.note", MD5: "abc123", Size: 12345},
			}),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name: "AC4.3 STARTSYNC msgType returns nil",
			frame: buildFrame(t, "STARTSYNC", []fileEntry{
				{Name: "MyNote", Path: "Note/MyNote.note", MD5: "abc123", Size: 12345},
			}),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name: "AC4.4 Only non-.note files returns nil",
			frame: buildFrame(t, "FILE-SYN", []fileEntry{
				{Name: "Document", Path: "Docs/Document.pdf", MD5: "abc123", Size: 2000},
				{Name: "Book", Path: "Books/Book.epub", MD5: "def456", Size: 5000},
			}),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name:      "AC4.5 Empty input",
			frame:     []byte{},
			notesPath: notesPath,
			want:      nil,
		},
		{
			name:      "AC4.5 Input without 42 prefix",
			frame:     []byte(`3probe`),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name:      "AC4.5 Truncated JSON",
			frame:     []byte(`42["ServerMessage"`),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name:      "AC4.5 Valid frame structure but invalid payload JSON",
			frame:     []byte(`42["ServerMessage","not valid json"]`),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name: "AC4.5 Valid JSON but missing msgType field",
			frame: func() []byte {
				payload := map[string]interface{}{
					"data": []map[string]interface{}{},
				}
				payloadJSON, _ := json.Marshal(payload)
				payloadStr, _ := json.Marshal(string(payloadJSON))
				return []byte(fmt.Sprintf(`42["ServerMessage",%s]`, payloadStr))
			}(),
			notesPath: notesPath,
			want:      nil,
		},
		{
			name: "AC4.5 Valid JSON but missing data field",
			frame: func() []byte {
				payload := map[string]interface{}{
					"msgType": "FILE-SYN",
				}
				payloadJSON, _ := json.Marshal(payload)
				payloadStr, _ := json.Marshal(string(payloadJSON))
				return []byte(fmt.Sprintf(`42["ServerMessage",%s]`, payloadStr))
			}(),
			notesPath: notesPath,
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractNotePaths(tt.frame, tt.notesPath)
			if len(got) == 0 && len(tt.want) == 0 {
				return // Both nil/empty, test passes
			}
			if len(got) != len(tt.want) {
				t.Errorf("got %d paths, want %d", len(got), len(tt.want))
				return
			}
			for i, path := range got {
				if path != tt.want[i] {
					t.Errorf("path[%d]: got %q, want %q", i, path, tt.want[i])
				}
			}
		})
	}
}
