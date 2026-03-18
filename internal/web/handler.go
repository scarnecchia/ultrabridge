package web

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	ubcaldav "github.com/sysop/ultrabridge/internal/caldav"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

//go:embed templates
var templateFS embed.FS

type Handler struct {
	store       ubcaldav.TaskStore
	notifier    ubcaldav.SyncNotifier
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
func NewHandler(store ubcaldav.TaskStore, notifier ubcaldav.SyncNotifier, logger *slog.Logger, broadcaster *logging.LogBroadcaster) *Handler {
	h := &Handler{
		store:       store,
		notifier:    notifier,
		logger:      logger,
		mux:         http.NewServeMux(),
		broadcaster: broadcaster,
	}

	// Parse the embedded templates with custom function map
	funcMap := template.FuncMap{
		"formatDueTime":  formatDueTime,
		"formatCreated":  formatCreated,
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
		"tasks": tasks,
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
