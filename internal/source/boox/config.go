package boox

// Config holds Boox source-specific settings parsed from sources.config_json.
type Config struct {
	NotesPath string `json:"notes_path"` // filesystem root for uploads + page cache
}
