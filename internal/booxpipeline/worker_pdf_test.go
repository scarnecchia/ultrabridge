package booxpipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requirePdftoppm(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not available, skipping")
	}
}

// createTestPDF generates a minimal single-page PDF.
func createTestPDF(t *testing.T, dir string) string {
	t.Helper()
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

func TestRenderPDFPageScaled_FitsWithinLimit(t *testing.T) {
	requirePdftoppm(t)

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	// Large limit — should render at original DPI without scaling.
	data, err := renderPDFPageScaled(pdf, 0, 72, 10_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JPEG data")
	}
}

func TestRenderPDFPageScaled_ScalesDown(t *testing.T) {
	requirePdftoppm(t)

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	// Very small limit — forces scaling down.
	data, err := renderPDFPageScaled(pdf, 0, 150, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JPEG data")
	}
}

func TestRenderPDFPageScaled_DoesNotInfiniteLoop(t *testing.T) {
	requirePdftoppm(t)

	dir := t.TempDir()
	pdf := createTestPDF(t, dir)

	// maxPixels=1 is impossibly small — should still terminate (at minDPI).
	done := make(chan error, 1)
	go func() {
		data, err := renderPDFPageScaled(pdf, 0, 150, 1)
		if err == nil && len(data) == 0 {
			t.Error("expected non-empty JPEG data")
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("renderPDFPageScaled did not terminate — infinite loop detected")
	}
}
