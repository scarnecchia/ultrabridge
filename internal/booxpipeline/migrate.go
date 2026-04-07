package booxpipeline

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MigrateResult summarizes a completed migration.
type MigrateResult struct {
	Migrated int // files successfully moved and DB updated
	Skipped  int // files already in the target location
	Errors   int // files that failed to migrate
}

// MigrateImportedFiles copies files from the import path to the Boox notes
// directory, preserving directory structure, and updates all database
// references (boox_notes, boox_jobs, note_content) to the new paths.
//
// Only migrates files whose boox_notes.path starts with importPath.
// Files already under notesPath are skipped.
func (p *Processor) MigrateImportedFiles(ctx context.Context, importPath, notesPath string, logger *slog.Logger) MigrateResult {
	var result MigrateResult

	if importPath == "" || notesPath == "" {
		logger.Warn("migrate: import path or notes path not configured")
		return result
	}

	// Find all boox_notes with paths under the import root.
	notes, err := p.store.ListNotesWithPrefix(ctx, importPath)
	if err != nil {
		logger.Error("migrate: list notes", "error", err)
		return result
	}

	logger.Info("migrate: found files to migrate", "count", len(notes), "from", importPath, "to", notesPath)

	for _, oldPath := range notes {
		select {
		case <-ctx.Done():
			logger.Warn("migrate: cancelled", "migrated", result.Migrated)
			return result
		default:
		}

		// Skip if already under the notes path.
		if strings.HasPrefix(oldPath, notesPath) {
			result.Skipped++
			continue
		}

		// Compute new path: replace import root with notes root.
		relPath, err := filepath.Rel(importPath, oldPath)
		if err != nil {
			logger.Error("migrate: rel path", "path", oldPath, "error", err)
			result.Errors++
			continue
		}
		newPath := filepath.Join(notesPath, relPath)

		// Copy file.
		if err := copyFile(oldPath, newPath); err != nil {
			logger.Error("migrate: copy file", "from", oldPath, "to", newPath, "error", err)
			result.Errors++
			continue
		}

		// Update all DB references in a transaction.
		if err := p.store.UpdateNotePath(ctx, oldPath, newPath); err != nil {
			logger.Error("migrate: update db", "from", oldPath, "to", newPath, "error", err)
			// Clean up the copy since DB update failed.
			os.Remove(newPath)
			result.Errors++
			continue
		}

		result.Migrated++
		logger.Info("migrate: moved", "from", oldPath, "to", newPath)
	}

	logger.Info("migrate: complete",
		"migrated", result.Migrated,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)
	return result
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create dst: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return out.Close()
}
