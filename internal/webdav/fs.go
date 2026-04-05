package webdav

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

// OnNoteUpload is called after a .note file is successfully uploaded.
// absPath is the absolute filesystem path to the written file.
type OnNoteUpload func(absPath string)

// FS implements webdav.FileSystem with version-on-overwrite and upload hooks.
type FS struct {
	root         string
	onNoteUpload OnNoteUpload
}

// NewFS creates a new Boox WebDAV filesystem rooted at the given directory.
func NewFS(root string, onUpload OnNoteUpload) *FS {
	return &FS{root: root, onNoteUpload: onUpload}
}

func (fs *FS) resolve(name string) string {
	// Sanitize: clean path, remove leading slash, prevent traversal.
	name = filepath.Clean("/" + name)
	return filepath.Join(fs.root, filepath.FromSlash(name))
}

func (fs *FS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return os.MkdirAll(fs.resolve(name), perm)
}

func (fs *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return os.Stat(fs.resolve(name))
}

func (fs *FS) RemoveAll(ctx context.Context, name string) error {
	return os.RemoveAll(fs.resolve(name))
}

func (fs *FS) Rename(ctx context.Context, oldName, newName string) error {
	return os.Rename(fs.resolve(oldName), fs.resolve(newName))
}

func (fs *FS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	absPath := fs.resolve(name)

	// Version-on-overwrite: if creating/truncating and file already exists, archive it.
	if flag&(os.O_CREATE|os.O_TRUNC) != 0 {
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			if err := fs.archiveVersion(name, absPath); err != nil {
				return nil, fmt.Errorf("archive version: %w", err)
			}
		}
	}

	// Ensure parent directory exists.
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			return nil, err
		}
	}

	f, err := os.OpenFile(absPath, flag, perm)
	if err != nil {
		return nil, err
	}

	// Wrap with hook for .note file upload detection.
	isWrite := flag&(os.O_WRONLY|os.O_RDWR) != 0
	isNote := strings.HasSuffix(strings.ToLower(name), ".note")

	return &hookFile{
		File:       f,
		absPath:    absPath,
		triggerHook: isWrite && isNote,
		onClose:    fs.onNoteUpload,
	}, nil
}

// archiveVersion moves the existing file to .versions/{relpath}/{timestamp}.note
func (fs *FS) archiveVersion(name, absPath string) error {
	// Construct version directory: {root}/.versions/{relpath}/
	relDir := filepath.Dir(name)
	baseName := filepath.Base(name)
	ext := filepath.Ext(baseName)
	nameNoExt := strings.TrimSuffix(baseName, ext)

	versionDir := filepath.Join(fs.root, ".versions", relDir, nameNoExt)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return err
	}

	timestamp := time.Now().UTC().Format("20060102T150405")
	versionPath := filepath.Join(versionDir, timestamp+ext)

	return os.Rename(absPath, versionPath)
}
