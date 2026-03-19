package notestore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db, t.TempDir())
}

func TestClassifyFileType(t *testing.T) {
	cases := []struct {
		ext  string
		want FileType
	}{
		{".note", FileTypeNote},
		{".NOTE", FileTypeNote},
		{".pdf", FileTypePDF},
		{".PDF", FileTypePDF},
		{".epub", FileTypeEPUB},
		{".mark", FileTypeOther},
		{".mobi", FileTypeOther},
		{"", FileTypeOther},
	}
	for _, c := range cases {
		if got := ClassifyFileType(c.ext); got != c.want {
			t.Errorf("ClassifyFileType(%q) = %q, want %q", c.ext, got, c.want)
		}
	}
}

func TestScan_NewFileDiscovered(t *testing.T) {
	s := openTestStore(t)
	noteFile := filepath.Join(s.notesPath, "test.note")
	if err := os.WriteFile(noteFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(changed) != 1 || changed[0] != noteFile {
		t.Errorf("Scan changed = %v, want [%s]", changed, noteFile)
	}

	nf, err := s.Get(context.Background(), noteFile)
	if err != nil {
		t.Fatalf("Get after scan: %v", err)
	}
	if nf.FileType != FileTypeNote {
		t.Errorf("FileType = %q, want note", nf.FileType)
	}
}

func TestScan_ChangedFileDetectedByMtime(t *testing.T) {
	s := openTestStore(t)
	noteFile := filepath.Join(s.notesPath, "test.note")
	if err := os.WriteFile(noteFile, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(noteFile, future, future); err != nil {
		t.Fatal(err)
	}

	changed, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if len(changed) != 1 {
		t.Errorf("expected 1 changed file, got %d: %v", len(changed), changed)
	}
}

func TestScan_UnchangedFileNotReported(t *testing.T) {
	s := openTestStore(t)
	noteFile := filepath.Join(s.notesPath, "test.note")
	if err := os.WriteFile(noteFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	changed, err := s.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected 0 changed, got %d: %v", len(changed), changed)
	}
}

func TestList_ReturnsDirectChildren(t *testing.T) {
	s := openTestStore(t)
	root := filepath.Join(s.notesPath, "root.note")
	subdir := filepath.Join(s.notesPath, "sub")
	subfile := filepath.Join(subdir, "child.note")
	os.MkdirAll(subdir, 0755)
	os.WriteFile(root, []byte("r"), 0644)
	os.WriteFile(subfile, []byte("c"), 0644)

	if _, err := s.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	files, err := s.List(context.Background(), "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// List("") returns: subdirectories first (from os.ReadDir), then files from DB.
	// Expects: [sub/ (dir), root.note (file)]
	if len(files) != 2 {
		t.Fatalf("List(\"\") returned %d items, want 2 (sub dir + root.note)", len(files))
	}
	if files[0].Name != "sub" || !files[0].IsDir {
		t.Errorf("files[0] = %+v, want sub dir", files[0])
	}
	if files[1].Name != "root.note" {
		t.Errorf("files[1] = %+v, want root.note", files[1])
	}

	subFiles, err := s.List(context.Background(), "sub")
	if err != nil {
		t.Fatalf("List sub: %v", err)
	}
	if len(subFiles) != 1 || subFiles[0].Name != "child.note" {
		t.Errorf("List(\"sub\") = %v, want [child.note]", subFiles)
	}
}

func TestGet_NotFound(t *testing.T) {
	s := openTestStore(t)
	_, err := s.Get(context.Background(), "/nonexistent.note")
	if !IsNotFound(err) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
