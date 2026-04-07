package pdfrender

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requirePdftoppm(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not available, skipping")
	}
}

// createTestPDF generates a minimal single-page PDF for testing.
func createTestPDF(t *testing.T, dir string) string {
	t.Helper()
	// Minimal valid PDF with one blank page.
	pdf := `%PDF-1.0
1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj
2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj
3 0 obj<</Type/Page/MediaBox[0 0 612 792]/Parent 2 0 R/Resources<<>>>>endobj
xref
0 4
0000000000 65535 f
0000000009 00000 n
0000000058 00000 n
0000000115 00000 n
trailer<</Size 4/Root 1 0 R>>
startxref
206
%%EOF`

	path := filepath.Join(dir, "test.pdf")
	if err := os.WriteFile(path, []byte(pdf), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAvailable(t *testing.T) {
	// Just verify Available() runs without panic.
	_ = Available()
}

func TestPageCount(t *testing.T) {
	requirePdftoppm(t)
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo not available, skipping")
	}

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	count, err := PageCount(pdf)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("PageCount = %d, want 1", count)
	}
}

func TestRenderPage(t *testing.T) {
	requirePdftoppm(t)

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	data, err := RenderPage(pdf, 0, 72)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("RenderPage returned empty data")
	}

	// Verify it's valid JPEG.
	img, err := DecodeJPEG(data)
	if err != nil {
		t.Fatalf("DecodeJPEG: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		t.Error("rendered image has zero dimensions")
	}
}

func TestRenderAllPages(t *testing.T) {
	requirePdftoppm(t)
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo not available, skipping")
	}

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	pages, err := RenderAllPages(pdf, 72)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Errorf("RenderAllPages returned %d pages, want 1", len(pages))
	}
	if len(pages[0]) == 0 {
		t.Error("page 0 is empty")
	}
}

func TestRenderPage_InvalidPage(t *testing.T) {
	requirePdftoppm(t)

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	// Page index 5 doesn't exist in a 1-page PDF.
	_, err := RenderPage(pdf, 5, 72)
	if err == nil {
		t.Error("expected error for out-of-range page, got nil")
	}
}
