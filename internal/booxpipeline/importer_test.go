package booxpipeline

import (
	"path/filepath"
	"testing"
)

func TestExtractImportMetadata_OnyxPaths(t *testing.T) {
	model, nType, folder := ExtractImportMetadata("Palma2_Pro_C/Notebooks/Moffitt/notes.pdf", true)
	if model != "Palma2_Pro_C" {
		t.Errorf("model = %q, want 'Palma2_Pro_C'", model)
	}
	if nType != "Notebooks" {
		t.Errorf("noteType = %q, want 'Notebooks'", nType)
	}
	if folder != "Moffitt" {
		t.Errorf("folder = %q, want 'Moffitt'", folder)
	}
}

func TestExtractImportMetadata_OnyxPaths_Short(t *testing.T) {
	// Only 2 path segments — not enough for full Onyx structure.
	model, nType, folder := ExtractImportMetadata("Palma2/notes.pdf", true)
	// Falls through to generic since < 4 parts.
	if model != "" {
		t.Errorf("model = %q, want '' (too short for onyx)", model)
	}
	_ = nType
	if folder != "Palma2" {
		t.Errorf("folder = %q, want 'Palma2'", folder)
	}
}

func TestExtractImportMetadata_Generic(t *testing.T) {
	model, nType, folder := ExtractImportMetadata("some/path/Work/notes.pdf", false)
	if model != "" {
		t.Errorf("model = %q, want ''", model)
	}
	if nType != "" {
		t.Errorf("noteType = %q, want ''", nType)
	}
	if folder != "Work" {
		t.Errorf("folder = %q, want 'Work'", folder)
	}
}

func TestExtractImportMetadata_Generic_RootFile(t *testing.T) {
	model, _, folder := ExtractImportMetadata("notes.pdf", false)
	if model != "" {
		t.Errorf("model = %q, want ''", model)
	}
	if folder != "" {
		t.Errorf("folder = %q, want '' (file at root)", folder)
	}
}

func TestExtractImportMetadata_OnyxPaths_DeepNesting(t *testing.T) {
	// Deeper nesting: model/type/folder/subfolder/file — folder is still parts[2].
	model, nType, folder := ExtractImportMetadata(
		filepath.Join("Go103", "Notebooks", "Moffitt", "20250818 Steering Committee", "file.pdf"),
		true,
	)
	if model != "Go103" {
		t.Errorf("model = %q, want 'Go103'", model)
	}
	if nType != "Notebooks" {
		t.Errorf("noteType = %q, want 'Notebooks'", nType)
	}
	if folder != "Moffitt" {
		t.Errorf("folder = %q, want 'Moffitt'", folder)
	}
}
