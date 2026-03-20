package web

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
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

	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/search"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

//go:embed templates
var templateFS embed.FS

type Handler struct {
	store       ubcaldav.TaskStore
	notifier    ubcaldav.SyncNotifier
	noteStore   notestore.NoteStore
	searchIndex search.SearchIndex
	proc        processor.Processor
	tmpl        *template.Template
	mux         *http.ServeMux
	logger      *slog.Logger
	broadcaster *logging.LogBroadcaster
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
func NewHandler(store ubcaldav.TaskStore, notifier ubcaldav.SyncNotifier, noteStore notestore.NoteStore, searchIndex search.SearchIndex, proc processor.Processor, logger *slog.Logger, broadcaster *logging.LogBroadcaster) *Handler {
	h := &Handler{
		store:       store,
		notifier:    notifier,
		noteStore:   noteStore,
		searchIndex: searchIndex,
		proc:        proc,
		logger:      logger,
		mux:         http.NewServeMux(),
		broadcaster: broadcaster,
	}

	// Parse the embedded templates with custom function map
	funcMap := template.FuncMap{
		"formatDueTime": formatDueTime,
		"formatCreated": formatCreated,
		"fileTypeStr":   func(ft notestore.FileType) string { return string(ft) },
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
	h.mux.HandleFunc("POST /processor/start", h.handleProcessorStart)
	h.mux.HandleFunc("POST /processor/stop", h.handleProcessorStop)
	h.registerLogStreamHandler(broadcaster)

	return h
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleIndex renders the task list page
func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	tasks, err := h.store.List(ctx)
	if err != nil {
		h.logger.Error("failed to list tasks", "error", err)
		http.Error(w, "failed to load tasks", http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"tasks":     tasks,
		"activeTab": "tasks",
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
		http.Error(w, "failed to render page", http.StatusInternalServerError)
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

func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	if h.noteStore == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
			"filesError": "UB_NOTES_PATH is not configured",
			"activeTab":  "files",
		}); err != nil {
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

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	files, err := h.noteStore.List(ctx, relPath)
	if err != nil {
		h.logger.Error("handleFiles list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
		"files":       files,
		"relPath":     relPath,
		"breadcrumbs": buildBreadcrumbs(relPath),
		"activeTab":   "files",
	}); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	var results []search.SearchResult
	if h.searchIndex != nil && query != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var err error
		results, err = h.searchIndex.Search(ctx, search.SearchQuery{Text: query})
		if err != nil {
			h.logger.Error("handleSearch", "err", err)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", map[string]interface{}{
		"searchQuery":   query,
		"searchResults": results,
		"activeTab":     "search",
	}); err != nil {
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
	if path != "" && h.proc != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.proc.Enqueue(ctx, path); err != nil {
			h.logger.Error("failed to enqueue file", "path", path, "error", err)
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
	if path != "" && h.proc != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.proc.Skip(ctx, path, processor.SkipReasonManual); err != nil {
			h.logger.Error("failed to skip file", "path", path, "error", err)
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
	if path != "" && h.proc != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := h.proc.Unskip(ctx, path); err != nil {
			h.logger.Error("failed to unskip file", "path", path, "error", err)
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
	var st processor.ProcessorStatus
	if h.proc != nil {
		st = h.proc.Status()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

// handleFilesHistory returns JSON job history for a single file (AC7.6).
// GET /files/history?path=<absolute_path>
func (h *Handler) handleFilesHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" || h.proc == nil {
		w.Write([]byte("null"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
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
