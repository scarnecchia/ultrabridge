package booxpipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sysop/ultrabridge/internal/booxnote"
	"github.com/sysop/ultrabridge/internal/booxrender"
	"github.com/sysop/ultrabridge/internal/processor"
	ubwebdav "github.com/sysop/ultrabridge/internal/webdav"
)

// OCRer abstracts the OCR capability. processor.OCRClient satisfies this interface.
type OCRer interface {
	Recognize(ctx context.Context, jpegData []byte, prompt string) (string, error)
}

// ContentDeleter removes indexed content for a note path. search.Store satisfies this.
type ContentDeleter interface {
	Delete(ctx context.Context, path string) error
}

// TodoItem represents a to-do extracted from red ink on a page.
type TodoItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// WorkerConfig configures the Boox processing worker.
type WorkerConfig struct {
	Indexer        processor.Indexer
	ContentDeleter ContentDeleter // for clearing old content on re-process
	OCR            OCRer          // nil = OCR disabled
	OCRPrompt      func() string  // returns current OCR prompt; nil = use default
	TodoEnabled    func() bool    // returns whether red ink to-do extraction is on
	TodoPrompt     func() string  // returns current to-do extraction prompt
	OnTodosFound   func(ctx context.Context, notePath string, todos []TodoItem) // callback for extracted todos
	CachePath      string         // base dir for rendered page cache
}

func (p *Processor) executeJob(ctx context.Context, job *BooxJob) error {
	notePath := job.NotePath

	// 1. Open and parse the .note file.
	f, err := os.Open(notePath)
	if err != nil {
		return fmt.Errorf("open note: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat note: %w", err)
	}

	note, err := booxnote.Open(f, info.Size())
	if err != nil {
		return fmt.Errorf("parse note: %w", err)
	}

	// 2. Compute file hash for dedup.
	f.Seek(0, io.SeekStart)
	h := sha256.New()
	io.Copy(h, f)
	fileHash := hex.EncodeToString(h.Sum(nil))

	// 3. Extract path metadata.
	relPath, _ := filepath.Rel(p.notesPath, notePath)
	pm := ubwebdav.ExtractPathMetadata(relPath)

	// 4. Update boox_notes row (note.NoteID is the top-level directory name from the ZIP).
	if err := p.store.UpsertNote(ctx, notePath, note.NoteID, note.Title, pm.DeviceModel, pm.NoteType, pm.Folder, len(note.Pages), fileHash); err != nil {
		return fmt.Errorf("upsert note: %w", err)
	}

	// 5. Clear old cached renders and indexed content for re-processing.
	cacheDir := filepath.Join(p.cfg.CachePath, note.NoteID)
	os.RemoveAll(cacheDir)
	os.MkdirAll(cacheDir, 0755)
	// Use ContentDeleter to clear old indexed content (ensures FTS5 triggers fire correctly).
	if p.cfg.ContentDeleter != nil {
		p.cfg.ContentDeleter.Delete(ctx, notePath)
	}

	// 6. Render, OCR, and index each page.
	for i, page := range note.Pages {
		// Render to image.
		img, err := booxrender.RenderPage(page)
		if err != nil {
			return fmt.Errorf("render page %d: %w", i, err)
		}

		// Encode to JPEG.
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return fmt.Errorf("encode page %d: %w", i, err)
		}

		// Cache rendered JPEG.
		cachePath := filepath.Join(cacheDir, fmt.Sprintf("page_%d.jpg", i))
		if err := os.WriteFile(cachePath, buf.Bytes(), 0644); err != nil {
			return fmt.Errorf("cache page %d: %w", i, err)
		}

		// OCR if client available.
		var ocrText string
		if p.cfg.OCR != nil {
			prompt := ""
			if p.cfg.OCRPrompt != nil {
				prompt = p.cfg.OCRPrompt()
			}
			text, err := p.cfg.OCR.Recognize(ctx, buf.Bytes(), prompt)
			if err != nil {
				return fmt.Errorf("ocr page %d: %w", i, err)
			}
			ocrText = text
		}

		// Index OCR'd text via shared Indexer.
		titleText := ""
		keywords := ""
		if i == 0 {
			titleText = note.Title
		}
		if err := p.cfg.Indexer.IndexPage(ctx, notePath, i, "api", ocrText, titleText, keywords); err != nil {
			return fmt.Errorf("index page %d: %w", i, err)
		}
	}

	// 7. Second OCR pass: red ink to-do extraction.
	if p.cfg.OCR != nil && p.cfg.TodoEnabled != nil && p.cfg.TodoEnabled() {
		var allTodos []TodoItem
		todoPrompt := ""
		if p.cfg.TodoPrompt != nil {
			todoPrompt = p.cfg.TodoPrompt()
		}

		for i := range note.Pages {
			cachePath := filepath.Join(cacheDir, fmt.Sprintf("page_%d.jpg", i))
			jpegData, err := os.ReadFile(cachePath)
			if err != nil {
				p.logger.Warn("todo pass: read cached page", "page", i, "error", err)
				continue
			}

			resp, err := p.cfg.OCR.Recognize(ctx, jpegData, todoPrompt)
			if err != nil {
				p.logger.Warn("todo pass: OCR failed", "page", i, "error", err)
				continue
			}

			todos := parseTodoResponse(resp)
			if len(todos) > 0 {
				p.logger.Info("todos extracted", "page", i, "count", len(todos), "path", notePath)
				allTodos = append(allTodos, todos...)
			}
		}

		if len(allTodos) > 0 && p.cfg.OnTodosFound != nil {
			p.cfg.OnTodosFound(ctx, notePath, allTodos)
		}
	}

	return nil
}

// parseTodoResponse extracts TodoItem objects from the OCR response.
// Each line is tried as a JSON object; non-JSON lines are skipped.
func parseTodoResponse(resp string) []TodoItem {
	var todos []TodoItem
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var item TodoItem
		if err := json.Unmarshal([]byte(line), &item); err == nil && item.Text != "" {
			todos = append(todos, item)
		}
	}
	return todos
}
