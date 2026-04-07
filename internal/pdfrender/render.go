// Package pdfrender renders PDF pages to JPEG images via pdftoppm (poppler-utils).
package pdfrender

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PageCount returns the number of pages in a PDF file.
// Uses pdfinfo from poppler-utils.
func PageCount(pdfPath string) (int, error) {
	out, err := exec.Command("pdfinfo", pdfPath).Output()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Pages:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pages:")))
			if err != nil {
				return 0, fmt.Errorf("parse page count: %w", err)
			}
			return n, nil
		}
	}
	return 0, fmt.Errorf("page count not found in pdfinfo output")
}

// RenderPage renders a single page (0-indexed) of a PDF to a JPEG byte slice.
// dpi controls resolution (200 is a good default for OCR).
func RenderPage(pdfPath string, pageIndex int, dpi int) ([]byte, error) {
	if dpi <= 0 {
		dpi = 200
	}

	// pdftoppm uses 1-indexed pages.
	pageNum := pageIndex + 1

	tmpDir, err := os.MkdirTemp("", "pdfrender-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outPrefix := filepath.Join(tmpDir, "page")

	// Render single page to JPEG.
	cmd := exec.Command("pdftoppm",
		"-jpeg",
		"-r", strconv.Itoa(dpi),
		"-f", strconv.Itoa(pageNum),
		"-l", strconv.Itoa(pageNum),
		pdfPath,
		outPrefix,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %w: %s", err, string(out))
	}

	// pdftoppm outputs page-{N}.jpg (zero-padded).
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jpg") {
			data, err := os.ReadFile(filepath.Join(tmpDir, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("read rendered page: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("pdftoppm produced no output for page %d", pageIndex)
}

// RenderAllPages renders all pages of a PDF to JPEG byte slices.
// Returns slices in page order (0-indexed).
func RenderAllPages(pdfPath string, dpi int) ([][]byte, error) {
	if dpi <= 0 {
		dpi = 200
	}

	count, err := PageCount(pdfPath)
	if err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "pdfrender-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	outPrefix := filepath.Join(tmpDir, "page")

	// Render all pages at once (more efficient than one-by-one).
	cmd := exec.Command("pdftoppm",
		"-jpeg",
		"-r", strconv.Itoa(dpi),
		pdfPath,
		outPrefix,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm: %w: %s", err, string(out))
	}

	// Collect output files in sorted order.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("read temp dir: %w", err)
	}

	var jpgFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jpg") {
			jpgFiles = append(jpgFiles, e.Name())
		}
	}
	sort.Strings(jpgFiles) // pdftoppm names are zero-padded, so lexicographic = page order

	if len(jpgFiles) != count {
		return nil, fmt.Errorf("expected %d pages, got %d output files", count, len(jpgFiles))
	}

	pages := make([][]byte, len(jpgFiles))
	for i, name := range jpgFiles {
		data, err := os.ReadFile(filepath.Join(tmpDir, name))
		if err != nil {
			return nil, fmt.Errorf("read page %d: %w", i, err)
		}
		pages[i] = data
	}
	return pages, nil
}

// Available reports whether pdftoppm is installed and runnable.
func Available() bool {
	_, err := exec.LookPath("pdftoppm")
	return err == nil
}

// DecodeJPEG decodes a JPEG byte slice into an image.Image.
// Convenience wrapper for callers that need the image dimensions.
func DecodeJPEG(data []byte) (image.Image, error) {
	return jpeg.Decode(bytes.NewReader(data))
}
