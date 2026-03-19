package config

import (
	"os"
	"testing"
)

// AC9.2: New config vars with defaults do not break existing deployments that omit them.
func TestLoad_PipelineDefaults(t *testing.T) {
	// Clear all new pipeline vars
	for _, k := range []string{
		"UB_NOTES_PATH", "UB_DB_PATH", "UB_BACKUP_PATH",
		"UB_OCR_ENABLED", "UB_OCR_API_URL", "UB_OCR_API_KEY",
		"UB_OCR_MODEL", "UB_OCR_CONCURRENCY", "UB_OCR_MAX_FILE_MB",
	} {
		os.Unsetenv(k)
	}
	// Set required fields so Load() doesn't fail
	os.Setenv("UB_USERNAME", "test")
	os.Setenv("UB_PASSWORD_HASH", "$2a$10$test")
	os.Setenv("MYSQL_DATABASE", "testdb")
	os.Setenv("MYSQL_USER", "testuser")
	os.Setenv("MYSQL_PASSWORD", "testpass")
	defer func() {
		os.Unsetenv("UB_USERNAME")
		os.Unsetenv("UB_PASSWORD_HASH")
		os.Unsetenv("MYSQL_DATABASE")
		os.Unsetenv("MYSQL_USER")
		os.Unsetenv("MYSQL_PASSWORD")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NotesPath != "" {
		t.Errorf("NotesPath = %q, want empty", cfg.NotesPath)
	}
	if cfg.DBPath != "/data/ultrabridge.db" {
		t.Errorf("DBPath = %q, want /data/ultrabridge.db", cfg.DBPath)
	}
	if cfg.BackupPath != "" {
		t.Errorf("BackupPath = %q, want empty", cfg.BackupPath)
	}
	if cfg.OCREnabled {
		t.Error("OCREnabled = true, want false")
	}
	if cfg.OCRConcurrency != 1 {
		t.Errorf("OCRConcurrency = %d, want 1", cfg.OCRConcurrency)
	}
	if cfg.OCRMaxFileMB != 0 {
		t.Errorf("OCRMaxFileMB = %d, want 0", cfg.OCRMaxFileMB)
	}
}
