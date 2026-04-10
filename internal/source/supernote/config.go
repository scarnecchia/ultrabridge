package supernote

// Config holds Supernote source-specific settings parsed from sources.config_json.
type Config struct {
	NotesPath  string `json:"notes_path"`
	BackupPath string `json:"backup_path"`
}
