package service

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"image/jpeg"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gosnote "github.com/jdkruzr/go-sn/note"
	"github.com/sysop/ultrabridge/internal/appconfig"
	"github.com/sysop/ultrabridge/internal/booxpipeline"
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/search"
)

// BooxStore is the interface required by the NoteService for Boox notes.
type BooxStore interface {
	ListNotes(ctx context.Context) ([]booxpipeline.BooxNoteEntry, error)
	GetVersions(ctx context.Context, path string) ([]booxpipeline.BooxVersion, error)
	GetNoteID(ctx context.Context, path string) (string, error)
	EnqueueJob(ctx context.Context, notePath string) error
	GetLatestJob(ctx context.Context, notePath string) (*booxpipeline.BooxJob, error)
	RetryAllFailed(ctx context.Context) (int64, error)
	DeleteNote(ctx context.Context, path string) error
	SkipNote(ctx context.Context, path, reason string) error
	UnskipNote(ctx context.Context, path string) error
	GetQueueStatus(ctx context.Context) (booxpipeline.QueueStatus, error)
}

// BooxImporter is the interface required by the NoteService for Boox imports.
type BooxImporter interface {
	ScanAndEnqueue(ctx context.Context, cfg booxpipeline.ImportConfig, logger *slog.Logger) booxpipeline.ImportResult
	MigrateImportedFiles(ctx context.Context, importPath, notesPath string, logger *slog.Logger) booxpipeline.MigrateResult
	Enqueue(ctx context.Context, notePath string) error
}

// BooxProcessor is the narrow handle the NoteService needs to start and stop
// the Boox pipeline worker on demand. Implemented by *booxpipeline.Processor.
// Kept separate from BooxImporter so tests can mock the controls without
// having to build importer plumbing too.
type BooxProcessor interface {
	Start(ctx context.Context) error
	Stop()
}

// FileScanner triggers a filesystem scan.
type FileScanner interface {
	ScanNow(ctx context.Context)
}

type noteService struct {
	noteStore     notestore.NoteStore
	proc          processor.Processor
	booxStore     BooxStore
	booxImporter  BooxImporter
	booxProc      BooxProcessor
	searchIndex   search.SearchIndex
	scanner       FileScanner
	noteDB        *sql.DB // for settings
	booxCachePath string
	booxNotesPath string
	logger        *slog.Logger
}

func NewNoteService(
	ns notestore.NoteStore,
	p processor.Processor,
	bs BooxStore,
	bi BooxImporter,
	bp BooxProcessor,
	si search.SearchIndex,
	scanner FileScanner,
	noteDB *sql.DB,
	booxCachePath string,
	booxNotesPath string,
	logger *slog.Logger,
) NoteService {
	return &noteService{
		noteStore:     ns,
		proc:          p,
		booxStore:     bs,
		booxImporter:  bi,
		booxProc:      bp,
		searchIndex:   si,
		scanner:       scanner,
		noteDB:        noteDB,
		booxCachePath: booxCachePath,
		booxNotesPath: booxNotesPath,
		logger:        logger,
	}
}

func (s *noteService) ListFiles(ctx context.Context, path string, sortField, order string, page, perPage int) ([]NoteFile, int, error) {
	var files []NoteFile

	// 1. Get Supernote files
	if s.noteStore != nil {
		snFiles, err := s.noteStore.List(ctx, path)
		if err != nil {
			s.logger.Error("list supernote files", "path", path, "error", err)
		} else {
			for _, f := range snFiles {
				files = append(files, mapSupernoteFile(f))
			}
		}
	}

	// 2. Merge Boox notes (only at root level per current implementation)
	if s.booxStore != nil && path == "" {
		booxNotes, err := s.booxStore.ListNotes(ctx)
		if err != nil {
			s.logger.Error("list boox notes", "error", err)
		} else {
			for _, bn := range booxNotes {
				files = append(files, mapBooxFile(bn))
			}
		}
	}

	// 3. Sorting
	if sortField == "" {
		sortField = "name"
	}
	if order == "" {
		order = "asc"
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}

		var cmp int
		switch sortField {
		case "created":
			cmp = compareTime(files[i].CreatedAt, files[j].CreatedAt)
		case "modified":
			cmp = compareTime(files[i].ModifiedAt, files[j].ModifiedAt)
		case "size":
			if files[i].SizeBytes == files[j].SizeBytes {
				cmp = strings.Compare(files[i].Name, files[j].Name)
			} else if files[i].SizeBytes < files[j].SizeBytes {
				cmp = -1
			} else {
				cmp = 1
			}
		default: // "name"
			cmp = strings.Compare(files[i].Name, files[j].Name)
		}

		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})

	// 4. Pagination
	totalFiles := len(files)
	if perPage <= 0 {
		perPage = 25
	}
	if page <= 0 {
		page = 1
	}

	totalPages := (totalFiles + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * perPage
	if start > totalFiles {
		start = totalFiles
	}
	end := start + perPage
	if end > totalFiles {
		end = totalFiles
	}

	return files[start:end], totalFiles, nil
}

// ListSupernoteFiles returns only Supernote-sourced files (directory tree
// browser model). Sort/pagination matches ListFiles; no Boox notes are mixed
// in. Returns an empty page with total=0 when no Supernote store is wired.
func (s *noteService) ListSupernoteFiles(ctx context.Context, path, sortField, order string, page, perPage int) ([]NoteFile, int, error) {
	if s.noteStore == nil {
		return nil, 0, nil
	}
	snFiles, err := s.noteStore.List(ctx, path)
	if err != nil {
		s.logger.Error("list supernote files", "path", path, "error", err)
		return nil, 0, err
	}
	files := make([]NoteFile, 0, len(snFiles))
	for _, f := range snFiles {
		files = append(files, mapSupernoteFile(f))
	}
	sortNoteFiles(files, sortField, order)
	return paginateNoteFiles(files, page, perPage)
}

// ListBooxNotes returns Boox-catalog rows with the richer per-note fields
// (Title, Folder, DeviceModel, NoteType, PageCount) that NoteFile flattens.
// Sort supports "title" (default), "folder", "pages", "size", "created",
// "modified". Returns empty/zero when no Boox store is wired.
func (s *noteService) ListBooxNotes(ctx context.Context, sortField, order string, page, perPage int) ([]BooxNoteSummary, int, error) {
	if s.booxStore == nil {
		return nil, 0, nil
	}
	rows, err := s.booxStore.ListNotes(ctx)
	if err != nil {
		s.logger.Error("list boox notes", "error", err)
		return nil, 0, err
	}
	out := make([]BooxNoteSummary, 0, len(rows))
	for _, bn := range rows {
		out = append(out, mapBooxSummary(bn))
	}
	sortBooxNotes(out, sortField, order)

	totalFiles := len(out)
	if perPage <= 0 {
		perPage = 25
	}
	if page <= 0 {
		page = 1
	}
	totalPages := (totalFiles + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	if start > totalFiles {
		start = totalFiles
	}
	end := start + perPage
	if end > totalFiles {
		end = totalFiles
	}
	return out[start:end], totalFiles, nil
}

// GetBooxNote returns the Boox-tab summary for a single path. Returns
// sql.ErrNoRows if the path is not in the Boox catalog.
func (s *noteService) GetBooxNote(ctx context.Context, path string) (BooxNoteSummary, error) {
	if s.booxStore == nil {
		return BooxNoteSummary{}, fmt.Errorf("boox store not available")
	}
	rows, err := s.booxStore.ListNotes(ctx)
	if err != nil {
		return BooxNoteSummary{}, err
	}
	for _, bn := range rows {
		if bn.Path == path {
			return mapBooxSummary(bn), nil
		}
	}
	return BooxNoteSummary{}, sql.ErrNoRows
}

// GetFile returns a single NoteFile by path, dispatching to the Boox or
// Supernote branch based on isBooxPath. Returns sql.ErrNoRows when the path
// is not found in the relevant store.
func (s *noteService) GetFile(ctx context.Context, path string) (NoteFile, error) {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return NoteFile{}, fmt.Errorf("boox store not available")
		}
		notes, err := s.booxStore.ListNotes(ctx)
		if err != nil {
			return NoteFile{}, err
		}
		for _, bn := range notes {
			if bn.Path == path {
				return mapBooxFile(bn), nil
			}
		}
		return NoteFile{}, sql.ErrNoRows
	}
	if s.noteStore == nil {
		return NoteFile{}, fmt.Errorf("note store not available")
	}
	f, err := s.noteStore.Get(ctx, path)
	if err != nil {
		return NoteFile{}, err
	}
	if f == nil {
		return NoteFile{}, sql.ErrNoRows
	}
	return mapSupernoteFile(*f), nil
}

func (s *noteService) GetNoteDetails(ctx context.Context, path string) (interface{}, error) {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return nil, fmt.Errorf("boox store not available")
		}
		return s.booxStore.GetLatestJob(ctx, path)
	}
	if s.proc == nil {
		return nil, fmt.Errorf("supernote processor not available")
	}
	return s.proc.GetJob(ctx, path)
}

func (s *noteService) GetContent(ctx context.Context, path string) (interface{}, error) {
	if s.searchIndex == nil {
		return nil, fmt.Errorf("search index not available")
	}
	return s.searchIndex.GetContent(ctx, path)
}

func (s *noteService) RenderPage(ctx context.Context, path string, pageIdx int) (io.ReadCloser, string, error) {
	if s.isBooxPath(path) {
		return s.renderBooxPage(ctx, path, pageIdx)
	}
	return s.renderSupernotePage(ctx, path, pageIdx)
}

func (s *noteService) renderBooxPage(ctx context.Context, path string, pageIdx int) (io.ReadCloser, string, error) {
	if s.booxStore == nil {
		return nil, "", fmt.Errorf("boox store not available")
	}
	noteID, err := s.booxStore.GetNoteID(ctx, path)
	if err != nil || noteID == "" {
		return nil, "", fmt.Errorf("note id not found for path: %s", path)
	}
	cachePath := filepath.Join(s.booxCachePath, noteID, fmt.Sprintf("page_%d.jpg", pageIdx))
	f, err := os.Open(cachePath)
	if err != nil {
		return nil, "", fmt.Errorf("page not rendered: %w", err)
	}
	return f, "image/jpeg", nil
}

func (s *noteService) renderSupernotePage(ctx context.Context, path string, pageIdx int) (io.ReadCloser, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	n, err := gosnote.Load(f)
	if err != nil {
		return nil, "", err
	}

	if pageIdx < 0 || pageIdx >= len(n.Pages) {
		return nil, "", fmt.Errorf("page index %d out of range", pageIdx)
	}

	p := n.Pages[pageIdx]
	tp, err := n.TotalPathData(p)
	if err != nil || tp == nil {
		return nil, "", fmt.Errorf("no stroke data for page")
	}
	pageW, pageH := n.PageDimensions(p)
	objs, err := gosnote.DecodeObjects(tp, pageW, pageH)
	if err != nil {
		return nil, "", err
	}
	img := gosnote.RenderObjects(objs, pageW, pageH, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", err
	}

	return io.NopCloser(&buf), "image/jpeg", nil
}

func (s *noteService) HasSupernoteSource() bool {
	return s.noteStore != nil
}

func (s *noteService) HasBooxSource() bool {
	return s.booxStore != nil
}

func (s *noteService) ListVersions(ctx context.Context, path string) (interface{}, error) {
	if !s.isBooxPath(path) || s.booxStore == nil {
		return nil, nil
	}
	return s.booxStore.GetVersions(ctx, path)
}

func (s *noteService) ScanFiles(ctx context.Context) error {
	if s.scanner != nil {
		s.scanner.ScanNow(ctx)
	}
	return nil
}

func (s *noteService) Enqueue(ctx context.Context, path string, force bool) error {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return fmt.Errorf("boox store not available")
		}
		return s.booxStore.EnqueueJob(ctx, path)
	}
	if s.proc == nil {
		return fmt.Errorf("supernote processor not available")
	}
	return s.proc.Enqueue(ctx, path)
}

func (s *noteService) Skip(ctx context.Context, path, reason string) error {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return fmt.Errorf("boox store not available")
		}
		return s.booxStore.SkipNote(ctx, path, reason)
	}
	if s.proc == nil {
		return fmt.Errorf("supernote processor not available")
	}
	return s.proc.Skip(ctx, path, reason)
}

func (s *noteService) Unskip(ctx context.Context, path string) error {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return fmt.Errorf("boox store not available")
		}
		return s.booxStore.UnskipNote(ctx, path)
	}
	if s.proc == nil {
		return fmt.Errorf("supernote processor not available")
	}
	return s.proc.Unskip(ctx, path)
}

func (s *noteService) RetryFailed(ctx context.Context) error {
	if s.booxStore != nil {
		_, _ = s.booxStore.RetryAllFailed(ctx)
	}
	return nil
}

func (s *noteService) DeleteNote(ctx context.Context, path string) error {
	if s.isBooxPath(path) {
		if s.booxStore == nil {
			return fmt.Errorf("boox store not available")
		}
		noteID, _ := s.booxStore.GetNoteID(ctx, path)
		if err := s.booxStore.DeleteNote(ctx, path); err != nil {
			return err
		}
		if noteID != "" && s.booxCachePath != "" {
			os.RemoveAll(filepath.Join(s.booxCachePath, noteID))
		}
		return nil
	}
	return nil
}

func (s *noteService) BulkDelete(ctx context.Context, paths []string) error {
	for _, p := range paths {
		_ = s.DeleteNote(ctx, p)
	}
	return nil
}

func (s *noteService) StartProcessor(ctx context.Context) error {
	if s.proc != nil {
		return s.proc.Start(ctx)
	}
	return nil
}

func (s *noteService) StopProcessor(ctx context.Context) error {
	if s.proc != nil {
		return s.proc.Stop()
	}
	return nil
}

// StartBooxProcessor starts the Boox pipeline worker. Nil-safe: returns nil
// when no Boox source is wired.
func (s *noteService) StartBooxProcessor(ctx context.Context) error {
	if s.booxProc != nil {
		return s.booxProc.Start(ctx)
	}
	return nil
}

// StopBooxProcessor signals shutdown on the Boox worker. Nil-safe.
func (s *noteService) StopBooxProcessor(ctx context.Context) error {
	if s.booxProc != nil {
		s.booxProc.Stop()
	}
	return nil
}

func (s *noteService) GetProcessorStatus(ctx context.Context) (EmbeddingJobStatus, error) {
	status := EmbeddingJobStatus{}
	
	if s.proc != nil {
		procStatus := s.proc.Status()
		status.Running = procStatus.Running
		status.PendingCount = procStatus.Pending
		status.InFlightCount = procStatus.InFlight
		status.ProcessedCount = procStatus.Done
		status.FailedCount = procStatus.Failed
	}
	
	if s.booxStore != nil {
		booxStatus, err := s.booxStore.GetQueueStatus(ctx)
		if err == nil {
			status.Boox = &booxStatus
		} else {
			s.logger.Error("failed to get boox queue status", "error", err)
		}
	}
	
	return status, nil
}

func (s *noteService) ImportFiles(ctx context.Context) error {
	if s.booxImporter == nil || s.noteDB == nil {
		return fmt.Errorf("boox importer or database not available")
	}
	importPath, _ := notedb.GetSetting(ctx, s.noteDB, appconfig.KeyBooxImportPath)
	if importPath == "" {
		return fmt.Errorf("no import path configured")
	}
	importNotes, _ := notedb.GetSetting(ctx, s.noteDB, appconfig.KeyBooxImportNotes)
	importPDFs, _ := notedb.GetSetting(ctx, s.noteDB, appconfig.KeyBooxImportPDFs)
	onyxPaths, _ := notedb.GetSetting(ctx, s.noteDB, appconfig.KeyBooxImportOnyxPaths)

	cfg := booxpipeline.ImportConfig{
		ImportPath:  importPath,
		ImportNotes: importNotes == "true",
		ImportPDFs:  importPDFs == "true",
		OnyxPaths:   onyxPaths == "true",
	}

	s.booxImporter.ScanAndEnqueue(ctx, cfg, s.logger)
	return nil
}

func (s *noteService) MigrateImports(ctx context.Context) error {
	if s.booxImporter == nil || s.noteDB == nil {
		return fmt.Errorf("boox importer or database not available")
	}
	importPath, _ := notedb.GetSetting(ctx, s.noteDB, appconfig.KeyBooxImportPath)
	if importPath == "" || s.booxNotesPath == "" {
		return fmt.Errorf("import or notes path not configured")
	}

	go func() {
		s.booxImporter.MigrateImportedFiles(context.Background(), importPath, s.booxNotesPath, s.logger)
	}()
	return nil
}

// Helpers

func (s *noteService) isBooxPath(path string) bool {
	if s.booxStore == nil {
		return false
	}
	if s.booxNotesPath != "" && strings.HasPrefix(path, s.booxNotesPath) {
		return true
	}
	// Check for settings-based import path too if needed, but for now
	// let's rely on booxNotesPath which covers the main store.
	// Heuristic: if it's explicitly boox-y or we have no supernote store
	return (strings.Contains(path, "boox") || strings.HasSuffix(path, ".note")) && s.noteStore == nil
}

func mapSupernoteFile(f notestore.NoteFile) NoteFile {
	return NoteFile{
		Name:       f.Name,
		Path:       f.Path,
		RelPath:    f.RelPath,
		IsDir:      f.IsDir,
		FileType:   string(f.FileType),
		SizeBytes:  f.SizeBytes,
		CreatedAt:  f.CTime,
		ModifiedAt: f.MTime,
		Source:     "supernote",
		DeviceInfo: &f.DeviceInfo,
		JobStatus:  f.JobStatus,
	}
}

func mapBooxFile(bn booxpipeline.BooxNoteEntry) NoteFile {
	var mtime time.Time
	if bn.UpdatedAt > 0 {
		mtime = time.UnixMilli(bn.UpdatedAt)
	}
	var ctime time.Time
	if bn.CreatedAt > 0 {
		ctime = time.UnixMilli(bn.CreatedAt)
	}
	var sizeBytes int64
	if info, err := os.Stat(bn.Path); err == nil {
		sizeBytes = info.Size()
	}
	deviceInfo := bn.DeviceModel
	if bn.Folder != "" {
		deviceInfo += " / " + bn.Folder
	}

	return NoteFile{
		Name:       bn.Title,
		Path:       bn.Path,
		RelPath:    bn.Title,
		IsDir:      false,
		FileType:   "note",
		SizeBytes:  sizeBytes,
		CreatedAt:  ctime,
		ModifiedAt: mtime,
		Source:     "boox",
		DeviceInfo: &deviceInfo,
		JobStatus:  bn.JobStatus,
	}
}

func mapBooxSummary(bn booxpipeline.BooxNoteEntry) BooxNoteSummary {
	var mtime, ctime time.Time
	if bn.UpdatedAt > 0 {
		mtime = time.UnixMilli(bn.UpdatedAt)
	}
	if bn.CreatedAt > 0 {
		ctime = time.UnixMilli(bn.CreatedAt)
	}
	var size int64
	if info, err := os.Stat(bn.Path); err == nil {
		size = info.Size()
	}
	return BooxNoteSummary{
		Path:        bn.Path,
		NoteID:      bn.NoteID,
		Title:       bn.Title,
		Filename:    filepath.Base(bn.Path),
		DeviceModel: bn.DeviceModel,
		NoteType:    bn.NoteType,
		Folder:      bn.Folder,
		PageCount:   bn.PageCount,
		SizeBytes:   size,
		CreatedAt:   ctime,
		ModifiedAt:  mtime,
		JobStatus:   bn.JobStatus,
	}
}

// sortNoteFiles sorts files in place: directories first, then by the named
// field. Matches the ordering ListFiles applies to its merged result.
func sortNoteFiles(files []NoteFile, sortField, order string) {
	if sortField == "" {
		sortField = "name"
	}
	if order == "" {
		order = "asc"
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		var cmp int
		switch sortField {
		case "created":
			cmp = compareTime(files[i].CreatedAt, files[j].CreatedAt)
		case "modified":
			cmp = compareTime(files[i].ModifiedAt, files[j].ModifiedAt)
		case "size":
			if files[i].SizeBytes == files[j].SizeBytes {
				cmp = strings.Compare(files[i].Name, files[j].Name)
			} else if files[i].SizeBytes < files[j].SizeBytes {
				cmp = -1
			} else {
				cmp = 1
			}
		default:
			cmp = strings.Compare(files[i].Name, files[j].Name)
		}
		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
}

// paginateNoteFiles returns the page slice and the total count before paging.
func paginateNoteFiles(files []NoteFile, page, perPage int) ([]NoteFile, int, error) {
	total := len(files)
	if perPage <= 0 {
		perPage = 25
	}
	if page <= 0 {
		page = 1
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return files[start:end], total, nil
}

// sortBooxNotes sorts in place by the requested field. Supports "title",
// "folder", "pages", "size", "created", "modified". Default "title" asc.
func sortBooxNotes(rows []BooxNoteSummary, sortField, order string) {
	if sortField == "" {
		sortField = "title"
	}
	if order == "" {
		order = "asc"
	}
	sort.Slice(rows, func(i, j int) bool {
		var cmp int
		switch sortField {
		case "folder":
			cmp = strings.Compare(rows[i].Folder, rows[j].Folder)
			if cmp == 0 {
				cmp = strings.Compare(rows[i].Title, rows[j].Title)
			}
		case "pages":
			cmp = rows[i].PageCount - rows[j].PageCount
			if cmp == 0 {
				cmp = strings.Compare(rows[i].Title, rows[j].Title)
			}
		case "size":
			if rows[i].SizeBytes < rows[j].SizeBytes {
				cmp = -1
			} else if rows[i].SizeBytes > rows[j].SizeBytes {
				cmp = 1
			} else {
				cmp = strings.Compare(rows[i].Title, rows[j].Title)
			}
		case "created":
			cmp = compareTime(rows[i].CreatedAt, rows[j].CreatedAt)
		case "modified":
			cmp = compareTime(rows[i].ModifiedAt, rows[j].ModifiedAt)
		default:
			cmp = strings.Compare(rows[i].Title, rows[j].Title)
		}
		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
}

func compareTime(a, b time.Time) int {
	if a.Equal(b) {
		return 0
	}
	if a.Before(b) {
		return -1
	}
	return 1
}
