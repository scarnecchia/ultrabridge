package web

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gosnote "github.com/jdkruzr/go-sn/note"

	"github.com/sysop/ultrabridge/internal/booxpipeline"
	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/rag"
	"github.com/sysop/ultrabridge/internal/search"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

//go:embed templates
var templateFS embed.FS

// FileScanner triggers a filesystem scan. Implemented by pipeline.Pipeline.
type FileScanner interface {
	ScanNow(ctx context.Context)
}

// SyncStatus represents sync engine state for the web UI.
type SyncStatus struct {
	LastSyncAt    int64  `json:"lastSyncAt"`
	NextSyncAt    int64  `json:"nextSyncAt"`
	InProgress    bool   `json:"inProgress"`
	LastError     string `json:"lastError"`
	AdapterID     string `json:"adapterId"`
	AdapterActive bool   `json:"adapterActive"`
}

// SyncStatusProvider provides sync status and manual trigger.
// Implemented by a wrapper around tasksync.SyncEngine. Nil-safe in Handler.
type SyncStatusProvider interface {
	Status() SyncStatus
	TriggerSync()
}

// BooxStore provides Boox note data to the web handler.
// Types are defined in booxpipeline package to avoid circular imports.
type BooxStore interface {
	ListNotes(ctx context.Context) ([]booxpipeline.BooxNoteEntry, error)
	GetVersions(ctx context.Context, path string) ([]booxpipeline.BooxVersion, error)
	GetNoteID(ctx context.Context, path string) (string, error) // returns note_id for cache path resolution
	EnqueueJob(ctx context.Context, notePath string) error
	GetLatestJob(ctx context.Context, notePath string) (*booxpipeline.BooxJob, error)
	RetryAllFailed(ctx context.Context) (int64, error)
	DeleteNote(ctx context.Context, path string) error
	SkipNote(ctx context.Context, path, reason string) error
	UnskipNote(ctx context.Context, path string) error
	GetQueueStatus(ctx context.Context) (booxpipeline.QueueStatus, error)
	CountNotesWithPrefix(ctx context.Context, prefix string) (int, error)
}

// BooxImporter can scan an import path and enqueue files for processing.
type BooxImporter interface {
	ScanAndEnqueue(ctx context.Context, cfg booxpipeline.ImportConfig, logger *slog.Logger) booxpipeline.ImportResult
	MigrateImportedFiles(ctx context.Context, importPath, notesPath string, logger *slog.Logger) booxpipeline.MigrateResult
}

type Handler struct {
	store           ubcaldav.TaskStore
	notifier        ubcaldav.SyncNotifier
	noteStore       notestore.NoteStore
	searchIndex     search.SearchIndex
	proc            processor.Processor
	scanner         FileScanner
	syncProvider    SyncStatusProvider
	booxStore       BooxStore
	booxImporter    BooxImporter
	snNotesPath     string // UB_NOTES_PATH for Supernote device path mapping
	booxNotesPath   string
	booxCachePath   string
	noteDB          *sql.DB // shared SQLite DB for settings
	tmpl            *template.Template
	mux             *http.ServeMux
	logger          *slog.Logger
	broadcaster     *logging.LogBroadcaster
	embedder        rag.Embedder
	embedStore      *rag.Store
	embedModel      string
}

// formatDueTime converts a millisecond Unix timestamp to a formatted date string.
func formatDueTime(ms int64) string {
	if ms == 0 {
		return "No due date"
	}
	t := time.UnixMilli(ms).UTC()
	return t.Format("2006-01-02")
}

// formatCreated converts the CompletedTime (which per Supernote quirk holds creation time)
// to a human-readable date.
func formatCreated(ct sql.NullInt64) string {
	if !ct.Valid || ct.Int64 == 0 {
		return "-"
	}
	return time.UnixMilli(ct.Int64).UTC().Format("2006-01-02")
}

// NewHandler creates a new web handler with embedded templates.
func NewHandler(store ubcaldav.TaskStore, notifier ubcaldav.SyncNotifier, noteStore notestore.NoteStore, searchIndex search.SearchIndex, proc processor.Processor, scanner FileScanner, syncProvider SyncStatusProvider, booxStore BooxStore, booxImporter BooxImporter, booxNotesPath, snNotesPath string, noteDB *sql.DB, logger *slog.Logger, broadcaster *logging.LogBroadcaster, embedder rag.Embedder, embedStore *rag.Store, embedModel string) *Handler {
	h := &Handler{
		store:         store,
		notifier:      notifier,
		noteStore:     noteStore,
		searchIndex:   searchIndex,
		proc:          proc,
		scanner:       scanner,
		syncProvider:  syncProvider,
		snNotesPath:   snNotesPath,
		booxStore:     booxStore,
		booxImporter:  booxImporter,
		booxNotesPath: booxNotesPath,
		booxCachePath: filepath.Join(booxNotesPath, ".cache"),
		noteDB:        noteDB,
		logger:        logger,
		mux:           http.NewServeMux(),
		broadcaster:   broadcaster,
		embedder:      embedder,
		embedStore:    embedStore,
		embedModel:    embedModel,
	}

	// Cache the import path for the noteSource template function.
	var booxImportPath string
	if noteDB != nil {
		booxImportPath, _ = notedb.GetSetting(context.Background(), noteDB, SettingKeyBooxImportPath)
	}

	// Parse the embedded templates with custom function map
	funcMap := template.FuncMap{
		"formatDueTime": formatDueTime,
		"formatCreated": formatCreated,
		"fileTypeStr":   func(ft notestore.FileType) string { return string(ft) },
		"noteSource": func(path string) string {
			if h.booxStore != nil {
				if h.booxNotesPath != "" && strings.HasPrefix(path, h.booxNotesPath) {
					return "Boox"
				}
				if booxImportPath != "" && strings.HasPrefix(path, booxImportPath) {
					return "Boox"
				}
			}
			return "Supernote"
		},
		"hasPrefix":  strings.HasPrefix,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"trimPrefix": strings.TrimPrefix,
		"taskLink": func(links string) map[string]interface{} {
			if links == "" {
				return nil
			}
			data, err := base64.StdEncoding.DecodeString(links)
			if err != nil {
				return nil
			}
			var link struct {
				AppName  string `json:"appName"`
				FilePath string `json:"filePath"`
				Page     int    `json:"page"`
			}
			if err := json.Unmarshal(data, &link); err != nil {
				return nil
			}
			if link.FilePath == "" {
				return nil
			}
			// Map device path to local path.
			// Device: /storage/emulated/0/Note/... → local: {snNotesPath}/...
			const devicePrefix = "/storage/emulated/0/Note/"
			localPath := link.FilePath
			if h.snNotesPath != "" && strings.HasPrefix(link.FilePath, devicePrefix) {
				localPath = filepath.Join(h.snNotesPath, link.FilePath[len(devicePrefix):])
			}
			return map[string]interface{}{
				"Path": localPath,
				"Page": link.Page,
			}
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse templates: %v", err))
	}
	h.tmpl = tmpl

	// Register routes
	h.mux.HandleFunc("GET /", h.handleIndex)
	h.mux.HandleFunc("POST /tasks", h.handleCreateTask)
	h.mux.HandleFunc("POST /tasks/{id}/complete", h.handleCompleteTask)
	h.mux.HandleFunc("POST /tasks/bulk", h.handleBulkAction)
	h.mux.HandleFunc("POST /tasks/purge-completed", h.handlePurgeCompleted)
	h.mux.HandleFunc("GET /logs", h.handleLogs)
	h.mux.HandleFunc("GET /settings", h.handleSettings)
	h.mux.HandleFunc("POST /settings/save", h.handleSettingsSave)
	if h.embedder != nil && h.embedStore != nil {
		h.mux.HandleFunc("POST /settings/backfill-embeddings", h.handleBackfillEmbeddings)
	}
	h.mux.HandleFunc("GET /files", h.handleFiles)
	h.mux.HandleFunc("GET /search", h.handleSearch)
	h.mux.HandleFunc("POST /files/queue", h.handleFilesQueue)
	h.mux.HandleFunc("POST /files/skip", h.handleFilesSkip)
	h.mux.HandleFunc("POST /files/unskip", h.handleFilesUnskip)
	h.mux.HandleFunc("POST /files/force", h.handleFilesForce)
	h.mux.HandleFunc("GET /files/status", h.handleFilesStatus)
	h.mux.HandleFunc("GET /files/history", h.handleFilesHistory)
	h.mux.HandleFunc("GET /files/content", h.handleFilesContent)
	h.mux.HandleFunc("GET /files/render", h.handleFilesRender)
	h.mux.HandleFunc("GET /files/boox/render", h.handleBooxRender)
	h.mux.HandleFunc("GET /files/boox/versions", h.handleBooxVersions)
	h.mux.HandleFunc("POST /processor/start", h.handleProcessorStart)
	h.mux.HandleFunc("POST /processor/stop", h.handleProcessorStop)
	h.mux.HandleFunc("POST /files/scan", h.handleFilesScan)
	h.mux.HandleFunc("POST /files/import", h.handleFilesImport)
	h.mux.HandleFunc("POST /files/retry-failed", h.handleFilesRetryFailed)
	h.mux.HandleFunc("POST /files/delete-note", h.handleFilesDeleteNote)
	h.mux.HandleFunc("POST /files/delete-bulk", h.handleFilesDeleteBulk)
	h.mux.HandleFunc("POST /files/migrate-imports", h.handleFilesMigrateImports)
	h.mux.HandleFunc("GET /sync/status", h.handleSyncStatus)
	h.mux.HandleFunc("POST /sync/trigger", h.handleSyncTrigger)
	h.registerLogStreamHandler(broadcaster)

	return h
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// baseTemplateData returns shared data needed by all routes that render index.html.
// This ensures the task list is always available regardless of which tab is active.
func (h *Handler) baseTemplateData(ctx context.Context) map[string]interface{} {
	data := map[string]interface{}{}
	tasks, err := h.store.List(ctx)
	if err != nil {
		h.logger.Error("failed to list tasks for template", "error", err)
	} else {
		data["tasks"] = tasks
	}
	data["BooxNotesPath"] = h.booxNotesPath
	if h.noteDB != nil {
		importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
		data["BooxImportPath"] = importPath
		todoEnabled, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxTodoEnabled)
		data["BooxTodoEnabled"] = todoEnabled == "true"
	}
	return data
}

// handleIndex renders the task list page
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "tasks"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
	}
}

// Settings keys for per-pipeline OCR prompts.
const (
	SettingKeySNOCRPrompt     = "sn_ocr_prompt"
	SettingKeyBooxOCRPrompt   = "boox_ocr_prompt"
	SettingKeyBooxTodoEnabled = "boox_todo_enabled"
	SettingKeyBooxTodoPrompt  = "boox_todo_prompt"
	SettingKeyBooxImportPath  = "boox_import_path"
	SettingKeyBooxImportNotes = "boox_import_notes"
	SettingKeyBooxImportPDFs      = "boox_import_pdfs"
	SettingKeyBooxImportOnyxPaths = "boox_import_onyx_paths"
)

// DefaultBooxTodoPrompt is the default prompt for red ink to-do extraction.
const DefaultBooxTodoPrompt = `Find any passages on this page written in RED ink. For each red passage, return a JSON object on its own line like: {"type":"todo","text":"the red text content"}. If there are no red passages, return nothing.`

// handleSettings renders the settings page
func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "settings"
	data["SNPipelineActive"] = h.noteStore != nil
	data["BooxActive"] = h.booxStore != nil

	// Load OCR prompts from DB, falling back to default.
	if h.noteDB != nil {
		snPrompt, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeySNOCRPrompt)
		if snPrompt == "" {
			snPrompt = processor.DefaultOCRPrompt
		}
		data["SNOCRPrompt"] = snPrompt

		booxPrompt, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxOCRPrompt)
		if booxPrompt == "" {
			booxPrompt = processor.DefaultOCRPrompt
		}
		data["BooxOCRPrompt"] = booxPrompt

		todoEnabled, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxTodoEnabled)
		data["BooxTodoEnabled"] = todoEnabled == "true"

		todoPrompt, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxTodoPrompt)
		if todoPrompt == "" {
			todoPrompt = DefaultBooxTodoPrompt
		}
		data["BooxTodoPrompt"] = todoPrompt

		importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
		data["BooxImportPath"] = importPath
		importNotes, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportNotes)
		data["BooxImportNotes"] = importNotes == "true"
		importPDFs, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPDFs)
		data["BooxImportPDFs"] = importPDFs == "true"
		importOnyxPaths, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportOnyxPaths)
		data["BooxImportOnyxPaths"] = importOnyxPaths == "true"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

// handleSettingsSave saves a settings form submission.
func (h *Handler) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	section := r.FormValue("section")
	ocrPrompt := r.FormValue("ocr_prompt")

	if h.noteDB != nil {
		switch section {
		case "supernote":
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeySNOCRPrompt, ocrPrompt); err != nil {
				h.logger.Error("save setting", "key", SettingKeySNOCRPrompt, "error", err)
			}
		case "boox":
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxOCRPrompt, ocrPrompt); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxOCRPrompt, "error", err)
			}
			// Save to-do extraction settings.
			todoEnabled := "false"
			if r.FormValue("todo_enabled") == "true" {
				todoEnabled = "true"
			}
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxTodoEnabled, todoEnabled); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxTodoEnabled, "error", err)
			}
			todoPrompt := r.FormValue("todo_prompt")
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxTodoPrompt, todoPrompt); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxTodoPrompt, "error", err)
			}
			// Save bulk import settings (path is read-only, set via env var).
			importNotes := "false"
			if r.FormValue("import_notes") == "true" {
				importNotes = "true"
			}
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxImportNotes, importNotes); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxImportNotes, "error", err)
			}
			importPDFs := "false"
			if r.FormValue("import_pdfs") == "true" {
				importPDFs = "true"
			}
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxImportPDFs, importPDFs); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxImportPDFs, "error", err)
			}
			importOnyxPaths := "false"
			if r.FormValue("import_onyx_paths") == "true" {
				importOnyxPaths = "true"
			}
			if err := notedb.SetSetting(ctx, h.noteDB, SettingKeyBooxImportOnyxPaths, importOnyxPaths); err != nil {
				h.logger.Error("save setting", "key", SettingKeyBooxImportOnyxPaths, "error", err)
			}
		}
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// handleLogs renders the logs page
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "logs"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

// handleCreateTask creates a new task from form data
func (h *Handler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.Warn("failed to parse form", "error", err)
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		h.logger.Warn("create task: title is required")
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}

	dueDateStr := strings.TrimSpace(r.FormValue("due_date"))
	var dueTime int64 = 0
	if dueDateStr != "" {
		// Parse HTML date format: 2006-01-02
		t, err := time.Parse("2006-01-02", dueDateStr)
		if err != nil {
			h.logger.Warn("invalid due date", "error", err, "value", dueDateStr)
			http.Error(w, "invalid due date format", http.StatusBadRequest)
			return
		}
		// Convert to milliseconds UTC
		dueTime = t.UTC().UnixMilli()
	}

	now := time.Now().UnixMilli()
	task := &taskstore.Task{
		TaskID: taskstore.GenerateTaskID(title, now),
		Title:  taskstore.SqlStr(title),
		Status: taskstore.SqlStr("needsAction"),
		DueTime: dueTime,
		IsDeleted: "N",
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := h.store.Create(ctx, task); err != nil {
		h.logger.Error("failed to create task", "error", err, "task_id", task.TaskID)
		http.Error(w, "failed to create task", http.StatusInternalServerError)
		return
	}

	// Notify device of sync
	if h.notifier != nil {
		if err := h.notifier.Notify(ctx); err != nil {
			h.logger.Warn("failed to notify", "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleCompleteTask marks a task as completed
func (h *Handler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if taskID == "" {
		h.logger.Warn("complete task: task ID is required")
		http.Error(w, "task ID is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	task, err := h.store.Get(ctx, taskID)
	if err != nil {
		h.logger.Error("failed to get task", "error", err, "task_id", taskID)
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	// Mark as completed
	task.Status = taskstore.SqlStr("completed")
	if !task.CompletedTime.Valid {
		task.CompletedTime = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
	}

	if err := h.store.Update(ctx, task); err != nil {
		h.logger.Error("failed to update task", "error", err, "task_id", taskID)
		http.Error(w, "failed to complete task", http.StatusInternalServerError)
		return
	}

	// Notify device of sync
	if h.notifier != nil {
		if err := h.notifier.Notify(ctx); err != nil {
			h.logger.Warn("failed to notify", "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleBulkAction completes or deletes multiple tasks at once
func (h *Handler) handleBulkAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	action := r.FormValue("action")
	taskIDs := r.Form["task_ids"]

	if len(taskIDs) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var failed int
	for _, taskID := range taskIDs {
		switch action {
		case "complete":
			task, err := h.store.Get(ctx, taskID)
			if err != nil {
				h.logger.Error("bulk complete: get failed", "task_id", taskID, "error", err)
				failed++
				continue
			}
			task.Status = taskstore.SqlStr("completed")
			if !task.CompletedTime.Valid {
				task.CompletedTime = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
			}
			if err := h.store.Update(ctx, task); err != nil {
				h.logger.Error("bulk complete: update failed", "task_id", taskID, "error", err)
				failed++
			}
		case "delete":
			if err := h.store.Delete(ctx, taskID); err != nil {
				h.logger.Error("bulk delete: failed", "task_id", taskID, "error", err)
				failed++
			}
		default:
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}
	}

	if failed > 0 {
		h.logger.Warn("bulk action partial failure", "action", action, "total", len(taskIDs), "failed", failed)
	}

	if h.notifier != nil {
		if err := h.notifier.Notify(ctx); err != nil {
			h.logger.Warn("failed to notify", "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handlePurgeCompleted(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	count, err := h.store.DeleteCompleted(ctx)
	if err != nil {
		h.logger.Error("purge completed tasks", "error", err)
	} else {
		h.logger.Info("purged completed tasks", "count", count)
	}

	if h.notifier != nil {
		if err := h.notifier.Notify(ctx); err != nil {
			h.logger.Warn("failed to notify after purge", "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// breadcrumb is a navigation segment for the Files tab.
type breadcrumb struct {
	Label   string
	RelPath string
}

// buildBreadcrumbs returns the crumb chain for a relative path.
// e.g. "Note/Folder" → [{Home,""},{Note,"Note"},{Folder,"Note/Folder"}]
func buildBreadcrumbs(relPath string) []breadcrumb {
	crumbs := []breadcrumb{{Label: "Home", RelPath: ""}}
	if relPath == "" {
		return crumbs
	}
	parts := strings.Split(relPath, "/")
	for i, p := range parts {
		crumbs = append(crumbs, breadcrumb{
			Label:   p,
			RelPath: strings.Join(parts[:i+1], "/"),
		})
	}
	return crumbs
}

// safeRelPath validates and cleans a user-supplied relative path.
// Returns the cleaned path and true on success, or "", false on traversal attempt.
func safeRelPath(relPath string) (string, bool) {
	if relPath == "" {
		return "", true
	}
	cleaned := filepath.Clean(relPath)
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", false
	}
	return cleaned, true
}

// handleBackfillEmbeddings triggers embedding backfill in the background.
func (h *Handler) handleBackfillEmbeddings(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx := context.Background() // independent of request lifecycle
		n, err := rag.Backfill(ctx, h.embedStore, h.embedder, h.embedModel, h.logger)
		if err != nil {
			h.logger.Error("backfill failed", "err", err)
			return
		}
		h.logger.Info("backfill triggered via settings", "embedded", n)
	}()

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "files"

	if h.noteStore == nil {
		data["filesError"] = "UB_NOTES_PATH is not configured"
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
			h.logger.Error("failed to render template", "error", err)
		}
		return
	}

	rawPath := r.URL.Query().Get("path")
	relPath, ok := safeRelPath(rawPath)
	if !ok {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	files, err := h.noteStore.List(ctx, relPath)
	if err != nil {
		h.logger.Error("handleFiles list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Set DeviceInfo for Supernote files.
	for i := range files {
		if !files[i].IsDir && files[i].DeviceInfo == "" {
			files[i].DeviceInfo = "Supernote"
		}
	}

	// Merge Boox notes into file list (only at root level).
	if h.booxStore != nil && relPath == "" {
		booxNotes, err := h.booxStore.ListNotes(ctx)
		if err != nil {
			h.logger.Error("list boox notes", "error", err)
		}
		for _, bn := range booxNotes {
			var mtime time.Time
			if bn.UpdatedAt > 0 {
				mtime = time.UnixMilli(bn.UpdatedAt)
			}
			var sizeBytes int64
			if info, err := os.Stat(bn.Path); err == nil {
				sizeBytes = info.Size()
			}
			deviceInfo := bn.DeviceModel
			if bn.Folder != "" {
				deviceInfo += " / " + bn.Folder
			}
			files = append(files, notestore.NoteFile{
				Path:       bn.Path,
				RelPath:    bn.Title, // display title instead of path
				Name:       bn.Title,
				IsDir:      false,
				FileType:   notestore.FileTypeNote,
				SizeBytes:  sizeBytes,
				MTime:      mtime,
				JobStatus:  bn.JobStatus,
				DeviceInfo: deviceInfo,
			})
		}
	}

	// Populate folder filter dropdown (only at root level).
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	data["filesFolder"] = folder
	if relPath == "" && h.searchIndex != nil {
		folders, err := h.searchIndex.ListFolders(ctx)
		if err != nil {
			h.logger.Error("handleFiles list folders", "err", err)
		} else {
			data["filesFolders"] = folders
		}
	}

	// Apply folder filter if set.
	if folder != "" {
		needle := "/" + folder + "/"
		filtered := files[:0]
		for _, f := range files {
			if strings.Contains(f.Path, needle) {
				filtered = append(filtered, f)
			}
		}
		files = filtered
	}

	// Pagination. Query param overrides cookie; cookie persists preference.
	perPage := 25
	if c, err := r.Cookie("files_per_page"); err == nil {
		if pp, err := strconv.Atoi(c.Value); err == nil {
			switch pp {
			case 10, 25, 50:
				perPage = pp
			}
		}
	}
	if pp, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil {
		switch pp {
		case 10, 25, 50:
			perPage = pp
		}
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	totalFiles := len(files)
	totalPages := (totalFiles + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	end := start + perPage
	if end > totalFiles {
		end = totalFiles
	}
	files = files[start:end]

	data["files"] = files
	data["relPath"] = relPath
	data["breadcrumbs"] = buildBreadcrumbs(relPath)
	data["filesPage"] = page
	data["filesPerPage"] = perPage
	data["filesTotalPages"] = totalPages
	data["filesTotalFiles"] = totalFiles

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "search"

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	data["searchQuery"] = query
	data["searchFolder"] = folder

	if h.searchIndex != nil {
		// Populate folder dropdown.
		folders, err := h.searchIndex.ListFolders(ctx)
		if err != nil {
			h.logger.Error("handleSearch list folders", "err", err)
		} else {
			data["searchFolders"] = folders
		}

		if query != "" {
			results, err := h.searchIndex.Search(ctx, search.SearchQuery{Text: query, Folder: folder})
			if err != nil {
				h.logger.Error("handleSearch", "err", err)
			} else {
				data["searchResults"] = results
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

func (h *Handler) handleFilesQueue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.Error("failed to parse form", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	back := r.FormValue("back")
	if path != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if h.isBooxPath(ctx, path) {
			if err := h.booxStore.EnqueueJob(ctx, path); err != nil {
				h.logger.Error("failed to enqueue boox file", "path", path, "error", err)
			}
		} else if h.proc != nil {
			if err := h.proc.Enqueue(ctx, path); err != nil {
				h.logger.Error("failed to enqueue file", "path", path, "error", err)
			}
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesSkip(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.Error("failed to parse form", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	back := r.FormValue("back")
	if path != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if h.isBooxPath(ctx, path) && h.booxStore != nil {
			if err := h.booxStore.SkipNote(ctx, path, "manual"); err != nil {
				h.logger.Error("failed to skip boox file", "path", path, "error", err)
			}
		} else if h.proc != nil {
			if err := h.proc.Skip(ctx, path, processor.SkipReasonManual); err != nil {
				h.logger.Error("failed to skip file", "path", path, "error", err)
			}
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesUnskip(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.Error("failed to parse form", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	back := r.FormValue("back")
	if path != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if h.isBooxPath(ctx, path) && h.booxStore != nil {
			if err := h.booxStore.UnskipNote(ctx, path); err != nil {
				h.logger.Error("failed to unskip boox file", "path", path, "error", err)
			}
		} else if h.proc != nil {
			if err := h.proc.Unskip(ctx, path); err != nil {
				h.logger.Error("failed to unskip file", "path", path, "error", err)
			}
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesForce(w http.ResponseWriter, r *http.Request) {
	// Force-include: unskip then re-enqueue regardless of previous skip reason.
	if err := r.ParseForm(); err != nil {
		h.logger.Error("failed to parse form", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	back := r.FormValue("back")
	if path != "" && h.proc != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.proc.Unskip(ctx, path); err != nil {
			h.logger.Error("failed to unskip file during force", "path", path, "error", err)
		}
		if err := h.proc.Enqueue(ctx, path); err != nil {
			h.logger.Error("failed to enqueue file during force", "path", path, "error", err)
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := struct {
		processor.ProcessorStatus
		Boox *booxpipeline.QueueStatus `json:"boox,omitempty"`
	}{}
	if h.proc != nil {
		resp.ProcessorStatus = h.proc.Status()
	}
	if h.booxStore != nil {
		qs, err := h.booxStore.GetQueueStatus(ctx)
		if err != nil {
			h.logger.Error("boox queue status", "error", err)
		} else {
			// Count unmigrated files if an import path is configured.
			if h.noteDB != nil {
				importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
				if importPath != "" {
					if count, err := h.booxStore.CountNotesWithPrefix(ctx, importPath); err == nil && count > 0 {
						qs.UnmigratedCount = count
					}
				}
			}
			resp.Boox = &qs
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// isBooxPath reports whether a file path belongs to the Boox pipeline
// (either WebDAV uploads or bulk imports).
func (h *Handler) isBooxPath(ctx context.Context, path string) bool {
	if h.booxStore == nil {
		return false
	}
	if h.booxNotesPath != "" && strings.HasPrefix(path, h.booxNotesPath) {
		return true
	}
	if h.noteDB != nil {
		importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
		if importPath != "" && strings.HasPrefix(path, importPath) {
			return true
		}
	}
	return false
}

// handleFilesHistory returns JSON job history for a single file (AC7.6).
// GET /files/history?path=<absolute_path>
func (h *Handler) handleFilesHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" {
		w.Write([]byte("null"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Route to Boox job store if path is a Boox note (WebDAV uploads or imports).
	if h.isBooxPath(ctx, path) {
		job, err := h.booxStore.GetLatestJob(ctx, path)
		if err != nil {
			h.logger.Error("failed to get boox job history", "path", path, "error", err)
			w.Write([]byte("null"))
			return
		}
		if job == nil {
			w.Write([]byte("null"))
			return
		}
		json.NewEncoder(w).Encode(job)
		return
	}

	// Supernote job lookup.
	if h.proc == nil {
		w.Write([]byte("null"))
		return
	}
	job, err := h.proc.GetJob(ctx, path)
	if err != nil {
		h.logger.Error("failed to get job history", "path", path, "error", err)
		w.Write([]byte("null"))
		return
	}
	if job == nil {
		w.Write([]byte("null"))
		return
	}
	json.NewEncoder(w).Encode(job)
}

// handleFilesContent returns indexed note_content for a single file as JSON.
// GET /files/content?path=<absolute_path>
func (h *Handler) handleFilesContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" || h.searchIndex == nil {
		w.Write([]byte("[]"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	docs, err := h.searchIndex.GetContent(ctx, path)
	if err != nil {
		h.logger.Error("failed to get content", "path", path, "error", err)
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(docs)
}

// handleFilesRender renders a single page of a .note file as JPEG.
// GET /files/render?path=<absolute_path>&page=<int>
func (h *Handler) handleFilesRender(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	pageStr := r.URL.Query().Get("page")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	pageIdx, err := strconv.Atoi(pageStr)
	if err != nil || pageIdx < 0 {
		pageIdx = 0
	}

	f, err := os.Open(path)
	if err != nil {
		h.logger.Error("render: open failed", "path", path, "err", err)
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	n, err := gosnote.Load(f)
	f.Close()
	if err != nil {
		h.logger.Error("render: load failed", "path", path, "err", err)
		http.Error(w, "invalid note file", http.StatusInternalServerError)
		return
	}

	if pageIdx >= len(n.Pages) {
		http.Error(w, fmt.Sprintf("page %d out of range (note has %d pages)", pageIdx, len(n.Pages)), http.StatusBadRequest)
		return
	}

	p := n.Pages[pageIdx]
	tp, err := n.TotalPathData(p)
	if err != nil || tp == nil {
		http.Error(w, "no stroke data for page", http.StatusNoContent)
		return
	}
	pageW, pageH := n.PageDimensions(p)
	objs, err := gosnote.DecodeObjects(tp, pageW, pageH)
	if err != nil {
		h.logger.Error("render: decode failed", "path", path, "page", pageIdx, "err", err)
		http.Error(w, "decode error", http.StatusInternalServerError)
		return
	}
	img := gosnote.RenderObjects(objs, pageW, pageH, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		h.logger.Error("render: jpeg encode failed", "err", err)
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(buf.Bytes())
}

func (h *Handler) handleProcessorStart(w http.ResponseWriter, r *http.Request) {
	if h.proc != nil {
		if err := h.proc.Start(r.Context()); err != nil {
			h.logger.Error("failed to start processor", "error", err)
		}
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleProcessorStop(w http.ResponseWriter, r *http.Request) {
	if h.proc != nil {
		if err := h.proc.Stop(); err != nil {
			h.logger.Error("failed to stop processor", "error", err)
		}
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesScan(w http.ResponseWriter, r *http.Request) {
	if h.scanner != nil {
		h.scanner.ScanNow(r.Context())
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesImport(w http.ResponseWriter, r *http.Request) {
	if h.booxImporter == nil {
		http.Error(w, "Boox pipeline not enabled", http.StatusNotFound)
		return
	}
	if h.noteDB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Read import settings from DB.
	importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
	if importPath == "" {
		h.logger.Warn("import: no import path configured")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	importNotes, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportNotes)
	importPDFs, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPDFs)
	onyxPaths, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportOnyxPaths)

	cfg := booxpipeline.ImportConfig{
		ImportPath:  importPath,
		ImportNotes: importNotes == "true",
		ImportPDFs:  importPDFs == "true",
		OnyxPaths:   onyxPaths == "true",
	}

	result := h.booxImporter.ScanAndEnqueue(ctx, cfg, h.logger)
	h.logger.Info("import complete",
		"scanned", result.Scanned,
		"enqueued", result.Enqueued,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesRetryFailed(w http.ResponseWriter, r *http.Request) {
	if h.booxStore == nil {
		http.Error(w, "Boox pipeline not enabled", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	count, err := h.booxStore.RetryAllFailed(ctx)
	if err != nil {
		h.logger.Error("retry failed jobs", "error", err)
	} else {
		h.logger.Info("retried failed jobs", "count", count)
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesDeleteNote(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if h.isBooxPath(ctx, path) && h.booxStore != nil {
		// Get noteID for cache cleanup before deleting DB records.
		noteID, _ := h.booxStore.GetNoteID(ctx, path)

		// Delete DB records (jobs, content, note).
		if err := h.booxStore.DeleteNote(ctx, path); err != nil {
			h.logger.Error("delete boox note", "path", path, "error", err)
			http.Error(w, "delete failed", http.StatusInternalServerError)
			return
		}
		// Delete cached renders.
		if noteID != "" && h.booxCachePath != "" {
			os.RemoveAll(filepath.Join(h.booxCachePath, noteID))
		}
		h.logger.Info("deleted boox note", "path", path)
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesDeleteBulk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	paths := r.Form["paths"]
	if len(paths) == 0 {
		http.Redirect(w, r, "/files", http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var deleted, failed int
	for _, path := range paths {
		if h.isBooxPath(ctx, path) && h.booxStore != nil {
			noteID, _ := h.booxStore.GetNoteID(ctx, path)
			if err := h.booxStore.DeleteNote(ctx, path); err != nil {
				h.logger.Error("bulk delete note", "path", path, "error", err)
				failed++
				continue
			}
			if noteID != "" && h.booxCachePath != "" {
				os.RemoveAll(filepath.Join(h.booxCachePath, noteID))
			}
			deleted++
		}
	}

	h.logger.Info("bulk delete complete", "deleted", deleted, "failed", failed)
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesMigrateImports(w http.ResponseWriter, r *http.Request) {
	if h.booxImporter == nil {
		http.Error(w, "Boox pipeline not enabled", http.StatusNotFound)
		return
	}
	if h.noteDB == nil {
		http.Error(w, "database not available", http.StatusInternalServerError)
		return
	}

	ctx := context.Background() // detached — survives browser redirect
	importPath, _ := notedb.GetSetting(ctx, h.noteDB, SettingKeyBooxImportPath)
	if importPath == "" || h.booxNotesPath == "" {
		h.logger.Warn("migrate: import path or notes path not configured")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	// Run in background so the browser redirect doesn't cancel it.
	go func() {
		result := h.booxImporter.MigrateImportedFiles(ctx, importPath, h.booxNotesPath, h.logger)
		h.logger.Info("migrate complete",
			"migrated", result.Migrated,
			"skipped", result.Skipped,
			"errors", result.Errors,
		)
	}()

	h.logger.Info("migrate started in background")
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.syncProvider == nil {
		json.NewEncoder(w).Encode(SyncStatus{})
		return
	}
	json.NewEncoder(w).Encode(h.syncProvider.Status())
}

func (h *Handler) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	if h.syncProvider == nil {
		http.Error(w, "sync not configured", http.StatusNotFound)
		return
	}
	h.syncProvider.TriggerSync()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.syncProvider.Status())
}

// handleBooxRender serves cached JPEG page images for Boox notes.
// GET /files/boox/render?path=<absolute_path>&page=<int>
func (h *Handler) handleBooxRender(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	pageStr := r.URL.Query().Get("page")
	page, _ := strconv.Atoi(pageStr)

	if h.booxStore == nil {
		http.Error(w, "Boox not configured", http.StatusNotFound)
		return
	}

	// Look up note_id from boox_notes table to construct cache path.
	// The cache is at {BooxCachePath}/{noteId}/page_{N}.jpg
	noteID, err := h.booxStore.GetNoteID(r.Context(), path)
	if err != nil || noteID == "" {
		h.logger.Debug("boox render: note not found", "path", path, "error", err)
		http.Error(w, "Note not found", http.StatusNotFound)
		return
	}
	cachePath := filepath.Join(h.booxCachePath, noteID, fmt.Sprintf("page_%d.jpg", page))

	data, err := os.ReadFile(cachePath)
	if err != nil {
		h.logger.Debug("boox render: page not rendered yet", "path", cachePath, "error", err)
		http.Error(w, "Page not rendered yet", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(data)
}

// handleBooxVersions returns a list of archived versions for a Boox note.
// GET /files/boox/versions?path=<absolute_path>
func (h *Handler) handleBooxVersions(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if h.booxStore == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]interface{}{})
		return
	}
	versions, err := h.booxStore.GetVersions(r.Context(), path)
	if err != nil {
		h.logger.Error("boox versions: get versions failed", "path", path, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if versions == nil {
		versions = []booxpipeline.BooxVersion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(versions)
}
