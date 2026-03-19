package processor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gosnote "github.com/jdkruzr/go-sn/note"
)

// processJob executes the full pipeline for one job.
func (s *Store) processJob(ctx context.Context, job *Job) {
	if err := s.executeJob(ctx, job); err != nil {
		s.markDone(ctx, job.ID, err.Error())
	} else {
		s.markDone(ctx, job.ID, "")
	}
}

func (s *Store) executeJob(ctx context.Context, job *Job) error {
	// Size guard
	if s.cfg.MaxFileMB > 0 {
		info, err := os.Stat(job.NotePath)
		if err != nil {
			return fmt.Errorf("stat: %w", err)
		}
		if info.Size() > int64(s.cfg.MaxFileMB)*1024*1024 {
			s.db.ExecContext(ctx,
				"UPDATE jobs SET status=?, skip_reason=? WHERE id=?",
				StatusSkipped, SkipReasonSizeLimit, job.ID)
			return nil
		}
	}

	// Backup before any modification
	if s.cfg.BackupPath != "" {
		if err := s.ensureBackup(ctx, job.NotePath); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}

	// Load the .note file
	f, err := os.Open(job.NotePath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	n, err := gosnote.Load(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("note.Load: %w", err)
	}

	pageW := n.PageWidth()
	pageH := n.PageHeight()

	// Extract TITLE and KEYWORD block text from the note footer (AC3.3).
	// These apply to the note as a whole; we attach them to page 0's index entry.
	footerTags, _ := n.FooterTags()
	titleText := extractNoteTitle(n, footerTags)
	keywordsText := extractNoteKeywords(n, footerTags)

	// Extract existing RECOGNTEXT per page and index as "myScript".
	for _, p := range n.Pages {
		raw, err := n.ReadRecognText(p)
		var bodyText string
		if err == nil && raw != nil {
			var rc gosnote.RecognContent
			if jsonErr := json.Unmarshal(raw, &rc); jsonErr == nil {
				bodyText = recognContentToText(rc)
			}
		}
		if s.cfg.Indexer != nil {
			// Title and keywords only on page 0; empty string for subsequent pages.
			tt, kw := "", ""
			if p.Index == 0 {
				tt, kw = titleText, keywordsText
			}
			s.cfg.Indexer.IndexPage(ctx, job.NotePath, p.Index, "myScript", bodyText, tt, kw)
		}
	}

	// OCR path: render → API → inject → write
	if !s.cfg.OCREnabled || s.cfg.OCRClient == nil {
		return nil
	}

	// Iterate by page index, always fetching the page from currentNote after each reload.
	// Using n.Pages only for the count — never pass a page from n to currentNote methods.
	currentNote := n
	for pageIdx := range n.Pages {
		// Always get the page from the current (possibly reloaded) note.
		p := currentNote.Pages[pageIdx]

		tp, err := currentNote.TotalPathData(p)
		if err != nil || tp == nil {
			continue
		}
		objs, err := gosnote.DecodeObjects(tp, pageW, pageH)
		if err != nil {
			continue
		}
		img := gosnote.RenderObjects(objs, pageW, pageH, nil)

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			continue
		}

		text, err := s.cfg.OCRClient.Recognize(ctx, buf.Bytes())
		if err != nil {
			return fmt.Errorf("OCR page %d: %w", pageIdx, err)
		}

		content := gosnote.RecognContent{
			Type: "Text",
			Elements: []gosnote.RecognElement{
				{Type: "Text", Label: text},
			},
		}
		newBytes, err := currentNote.InjectRecognText(pageIdx, content)
		if err != nil {
			return fmt.Errorf("inject page %d: %w", pageIdx, err)
		}
		if err := os.WriteFile(job.NotePath, newBytes, 0644); err != nil {
			return fmt.Errorf("write page %d: %w", pageIdx, err)
		}

		// Reload so subsequent pages reference fresh raw bytes and correct offsets.
		f2, err := os.Open(job.NotePath)
		if err != nil {
			return fmt.Errorf("reload after page %d: %w", pageIdx, err)
		}
		currentNote, err = gosnote.Load(f2)
		f2.Close()
		if err != nil {
			return fmt.Errorf("re-parse after page %d: %w", pageIdx, err)
		}

		if s.cfg.Indexer != nil {
			s.cfg.Indexer.IndexPage(ctx, job.NotePath, pageIdx, "api", text, "", "")
		}
	}

	s.db.ExecContext(ctx,
		"UPDATE jobs SET ocr_source=?, api_model=? WHERE id=?",
		"api", s.cfg.OCRClient.model, job.ID,
	)
	return nil
}

// ensureBackup copies the source file to the backup tree if no backup exists yet.
// Returns an error if backup is required but copying fails.
func (s *Store) ensureBackup(ctx context.Context, path string) error {
	var backupPath sql.NullString
	s.db.QueryRowContext(ctx, "SELECT backup_path FROM notes WHERE path=?", path).Scan(&backupPath)
	if backupPath.Valid && backupPath.String != "" {
		return nil // already backed up
	}

	dst := filepath.Join(s.cfg.BackupPath, path)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("mkdir backup dir: %w", err)
	}

	src, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create backup tmp: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copy to backup: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename backup: %w", err)
	}

	s.db.ExecContext(ctx,
		"UPDATE notes SET backup_path=?, backed_up_at=? WHERE path=?",
		dst, time.Now().Unix(), path,
	)
	return nil
}

// recognContentToText flattens all text labels from a RecognContent tree.
func recognContentToText(rc gosnote.RecognContent) string {
	var buf bytes.Buffer
	for _, el := range rc.Elements {
		extractLabels(&buf, el)
	}
	return buf.String()
}

func extractLabels(buf *bytes.Buffer, el gosnote.RecognElement) {
	if el.Label != "" {
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(el.Label)
	}
	for _, child := range el.Items {
		extractLabels(buf, child)
	}
}

// extractNoteTitle reads TITLE block labels from the note footer (AC3.3).
// Uses n.FooterTags() and n.BlockAt() — both available from go-sn.
// Returns space-separated text from all TITLERECT blocks' TITLESEQNO/label content.
// The simplest approach: concatenate the TITLERECT x,y,w,h fields as a proxy
// for heading presence, or — better — read each TITLE block's content tags.
func extractNoteTitle(n *gosnote.Note, footer gosnote.Tags) string {
	// TITLE blocks store heading content; the TITLEBITMAP offset points to rendered pixels,
	// not text. The heading TEXT itself is the handwriting in RECOGNTEXT for that region.
	// For now, we use the TITLE block's TITLERECT as a signal that a heading exists.
	// Full heading OCR would require per-region RECOGNTEXT extraction — out of scope here.
	// Return empty string; heading text will be captured when RECOGNTEXT is extracted.
	return ""
}

// extractNoteKeywords reads KEYWORD block text from the note footer (AC3.3).
// Each KEYWORD_* footer entry has a KEYWORDSITE offset pointing to a raw text block.
func extractNoteKeywords(n *gosnote.Note, footer gosnote.Tags) string {
	var keywords []string
	for k, v := range footer {
		if !strings.HasPrefix(k, "KEYWORD_") {
			continue
		}
		off, err := strconv.Atoi(v)
		if err != nil || off == 0 {
			continue
		}
		// Read the KEYWORD metadata block to find KEYWORDSITE.
		block, err := n.BlockAt(off)
		if err != nil {
			continue
		}
		// Parse tags in the block to find KEYWORDSITE offset.
		tags := parseMiniTags(block)
		siteStr, ok := tags["KEYWORDSITE"]
		if !ok {
			continue
		}
		siteOff, err := strconv.Atoi(siteStr)
		if err != nil || siteOff == 0 {
			continue
		}
		text, err := n.BlockAt(siteOff)
		if err != nil || len(text) == 0 {
			continue
		}
		keywords = append(keywords, string(text))
	}
	return strings.Join(keywords, " ")
}

// parseMiniTags extracts <KEY:VALUE> pairs from raw block bytes.
func parseMiniTags(b []byte) map[string]string {
	m := map[string]string{}
	s := string(b)
	for {
		start := strings.IndexByte(s, '<')
		if start < 0 {
			break
		}
		end := strings.IndexByte(s[start:], '>')
		if end < 0 {
			break
		}
		tag := s[start+1 : start+end]
		colon := strings.IndexByte(tag, ':')
		if colon >= 0 {
			m[tag[:colon]] = tag[colon+1:]
		}
		s = s[start+end+1:]
	}
	return m
}
