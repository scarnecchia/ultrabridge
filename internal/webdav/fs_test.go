package webdav

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestFS_OpenFile_WritesCorrectPath verifies AC3.2: file written to correct path preserving device path structure.
func TestFS_OpenFile_WritesCorrectPath(t *testing.T) {
	root := t.TempDir()
	uploadedPaths := []string{}
	var mu sync.Mutex

	fs := NewFS(root, func(absPath string) {
		mu.Lock()
		uploadedPaths = append(uploadedPaths, absPath)
		mu.Unlock()
	})

	// Create a file at onyx/TabUltra/Notebooks/Work/meeting.note
	relPath := "onyx/TabUltra/Notebooks/Work/meeting.note"
	f, err := fs.OpenFile(context.Background(), relPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}

	content := []byte("test content")
	if _, err := f.Write(content); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify file exists at correct path
	expectedPath := filepath.Join(root, relPath)
	if stat, err := os.Stat(expectedPath); err != nil {
		t.Errorf("file not found at %s: %v", expectedPath, err)
	} else if stat.IsDir() {
		t.Errorf("path is directory, not file: %s", expectedPath)
	}

	// Verify file content
	readContent, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if string(readContent) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", readContent, content)
	}

	// Verify upload callback was triggered
	if len(uploadedPaths) != 1 {
		t.Errorf("expected 1 upload callback, got %d", len(uploadedPaths))
	} else if uploadedPaths[0] != expectedPath {
		t.Errorf("callback path mismatch: got %q, want %q", uploadedPaths[0], expectedPath)
	}
}

// TestFS_OpenFile_VersionsOnOverwrite verifies AC3.3: old file moved to .versions/ before writing new.
func TestFS_OpenFile_VersionsOnOverwrite(t *testing.T) {
	root := t.TempDir()
	uploadedPaths := []string{}
	var mu sync.Mutex

	fs := NewFS(root, func(absPath string) {
		mu.Lock()
		uploadedPaths = append(uploadedPaths, absPath)
		mu.Unlock()
	})

	relPath := "test.note"

	// Write initial file
	f1, err := fs.OpenFile(context.Background(), relPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}

	oldContent := []byte("old content")
	if _, err := f1.Write(oldContent); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := f1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Overwrite the same file
	f2, err := fs.OpenFile(context.Background(), relPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatalf("OpenFile failed: %v", err)
	}

	newContent := []byte("new content")
	if _, err := f2.Write(newContent); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := f2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify new file at original path
	originalPath := filepath.Join(root, relPath)
	readContent, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if string(readContent) != string(newContent) {
		t.Errorf("new content mismatch: got %q, want %q", readContent, newContent)
	}

	// Verify old file moved to .versions/ directory
	// For "test.note" at root, archiveVersion creates: {root}/.versions//test/{timestamp}.note
	versionsDir := filepath.Join(root, ".versions")

	// Walk .versions to find the timestamped file
	versionFound := false
	err = filepath.Walk(versionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		// Check if this is our versioned file
		content, readErr := os.ReadFile(path)
		if readErr == nil && string(content) == string(oldContent) {
			versionFound = true
		}
		return nil
	})

	if err != nil && !versionFound {
		t.Fatalf("walk .versions failed: %v", err)
	}

	if !versionFound {
		t.Errorf("old content not found in any version file under %s", versionsDir)
	}

	// Verify callbacks: first write + second write = 2 callbacks
	if len(uploadedPaths) != 2 {
		t.Errorf("expected 2 upload callbacks, got %d", len(uploadedPaths))
	}
}

// TestFS_Mkdir_and_Stat verifies AC3.4: PROPFIND/MKCOL work by testing Mkdir and Stat.
func TestFS_Mkdir_and_Stat(t *testing.T) {
	root := t.TempDir()
	fs := NewFS(root, nil)

	dirPath := "onyx/TabUltra/Notebooks"
	if err := fs.Mkdir(context.Background(), dirPath, 0755); err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}

	// Verify directory exists using Stat
	stat, err := fs.Stat(context.Background(), dirPath)
	if err != nil {
		t.Errorf("Stat failed: %v", err)
	} else if !stat.IsDir() {
		t.Errorf("stat.IsDir() = false, want true")
	}

	// Verify we can create nested directories
	nested := filepath.Join(dirPath, "Work", "Subfolder")
	if err := fs.Mkdir(context.Background(), nested, 0755); err != nil {
		t.Fatalf("Mkdir nested failed: %v", err)
	}

	nestedStat, err := fs.Stat(context.Background(), nested)
	if err != nil {
		t.Errorf("Stat nested failed: %v", err)
	} else if !nestedStat.IsDir() {
		t.Errorf("nested stat.IsDir() = false, want true")
	}
}

// TestFS_OpenFile_NonNoteNoCallback verifies AC3.7: non-.note files don't trigger callback.
func TestFS_OpenFile_NonNoteNoCallback(t *testing.T) {
	root := t.TempDir()
	callbackCount := 0
	var mu sync.Mutex

	fs := NewFS(root, func(absPath string) {
		mu.Lock()
		callbackCount++
		mu.Unlock()
	})

	// Write a .txt file
	f1, err := fs.OpenFile(context.Background(), "test.txt", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile .txt failed: %v", err)
	}

	if _, err := f1.Write([]byte("text content")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := f1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify no callback for .txt file
	if callbackCount != 0 {
		t.Errorf("callback triggered for .txt file: count = %d, want 0", callbackCount)
	}

	// Write a .note file
	f2, err := fs.OpenFile(context.Background(), "test.note", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile .note failed: %v", err)
	}

	if _, err := f2.Write([]byte("note content")); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := f2.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify callback for .note file
	if callbackCount != 1 {
		t.Errorf("callback not triggered for .note file: count = %d, want 1", callbackCount)
	}
}

// TestFS_ConcurrentUploads verifies AC3.8: concurrent uploads of different files don't corrupt each other.
func TestFS_ConcurrentUploads(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	uploadedPaths := []string{}

	fs := NewFS(root, func(absPath string) {
		mu.Lock()
		uploadedPaths = append(uploadedPaths, absPath)
		mu.Unlock()
	})

	var wg sync.WaitGroup
	numFiles := 10

	for i := 0; i < numFiles; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			relPath := filepath.Join("onyx", "Device", "Notebooks", "Folder", "note_"+string(rune(48+index))+".note")
			content := []byte("content_" + string(rune(48+index)))

			f, err := fs.OpenFile(context.Background(), relPath, os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				t.Errorf("OpenFile failed: %v", err)
				return
			}

			if _, err := f.Write(content); err != nil {
				t.Errorf("Write failed: %v", err)
				f.Close()
				return
			}

			if err := f.Close(); err != nil {
				t.Errorf("Close failed: %v", err)
				return
			}

			// Verify content is correct
			expectedPath := filepath.Join(root, relPath)
			readContent, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Errorf("ReadFile failed: %v", err)
				return
			}

			if string(readContent) != string(content) {
				t.Errorf("content mismatch for note_%d: got %q, want %q", index, readContent, content)
			}
		}(i)
	}

	wg.Wait()

	// Verify all files created and callbacks triggered
	if len(uploadedPaths) != numFiles {
		t.Errorf("expected %d upload callbacks, got %d", numFiles, len(uploadedPaths))
	}

	// Verify all files exist with correct names
	for i := 0; i < numFiles; i++ {
		noteName := "note_" + string(rune(48+i)) + ".note"
		expectedPath := filepath.Join(root, "onyx", "Device", "Notebooks", "Folder", noteName)
		if _, err := os.Stat(expectedPath); err != nil {
			t.Errorf("file not found: %s", expectedPath)
		}
	}
}
