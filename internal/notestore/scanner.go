package notestore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Scan walks s.notesPath, upserts file records, and returns the absolute paths
// of files that are new or have a changed mtime.
// Returns nil, nil if notesPath is empty (not configured).
func (s *Store) Scan(ctx context.Context) ([]string, error) {
	if s.notesPath == "" {
		return nil, nil
	}

	now := time.Now().Unix()
	var changed []string

	err := filepath.WalkDir(s.notesPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries, don't abort
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		relPath, err := filepath.Rel(s.notesPath, path)
		if err != nil {
			return nil
		}

		ft := ClassifyFileType(filepath.Ext(path))
		mtimeUnix := info.ModTime().Unix()
		sizeBytes := info.Size()

		var existingMtime int64
		err = s.db.QueryRowContext(ctx, "SELECT mtime FROM notes WHERE path = ?", path).Scan(&existingMtime)
		if err != nil {
			// New file — insert
			_, insertErr := s.db.ExecContext(ctx, `
				INSERT INTO notes (path, rel_path, file_type, size_bytes, mtime, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				path, relPath, string(ft), sizeBytes, mtimeUnix, now, now)
			if insertErr != nil {
				return fmt.Errorf("insert note %s: %w", path, insertErr)
			}
			changed = append(changed, path)
		} else if existingMtime != mtimeUnix {
			// mtime changed — update
			_, updateErr := s.db.ExecContext(ctx, `
				UPDATE notes SET size_bytes=?, mtime=?, updated_at=? WHERE path=?`,
				sizeBytes, mtimeUnix, now, path)
			if updateErr != nil {
				return fmt.Errorf("update note %s: %w", path, updateErr)
			}
			changed = append(changed, path)
		}
		// mtime unchanged — skip

		return nil
	})

	return changed, err
}

// UpsertFile ensures a single file is present in the notes table.
// Used by the pipeline watcher before enqueuing to satisfy the jobs FK constraint.
func (s *Store) UpsertFile(ctx context.Context, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("UpsertFile stat: %w", err)
	}
	if info.IsDir() {
		return nil
	}
	relPath, err := filepath.Rel(s.notesPath, path)
	if err != nil {
		return fmt.Errorf("UpsertFile rel: %w", err)
	}
	ft := ClassifyFileType(filepath.Ext(path))
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO notes (path, rel_path, file_type, size_bytes, mtime, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size_bytes=excluded.size_bytes, mtime=excluded.mtime, updated_at=excluded.updated_at`,
		path, relPath, string(ft), info.Size(), info.ModTime().Unix(), now, now,
	)
	return err
}

// ComputeSHA256 reads the file at path and returns its SHA-256 hex digest.
// Called lazily by the worker before first modification, not during scan.
func ComputeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
