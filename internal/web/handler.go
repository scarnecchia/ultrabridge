package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sysop/ultrabridge/internal/appconfig"
	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/mcpauth"
	"github.com/sysop/ultrabridge/internal/service"
)

//go:embed templates
var templateFS embed.FS

type Handler struct {
	tasks    service.TaskService
	notes    service.NoteService
	search   service.SearchService
	config   service.ConfigService
	
	noteDB      *sql.DB // for settings directly (mcp tokens)
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
func NewHandler(
	tasks service.TaskService,
	notes service.NoteService,
	search service.SearchService,
	config service.ConfigService,
	noteDB *sql.DB,
	logger *slog.Logger,
	broadcaster *logging.LogBroadcaster,
) *Handler {
	h := &Handler{
		tasks:       tasks,
		notes:       notes,
		search:      search,
		config:      config,
		noteDB:      noteDB,
		logger:      logger,
		broadcaster: broadcaster,
		mux:         http.NewServeMux(),
	}

	// Parse the embedded templates with custom function map
	funcMap := template.FuncMap{
		"formatDueTime": formatDueTime,
		"formatCreated": formatCreated,
		"formatTimestamp": func(ms int64) string {
			if ms == 0 {
				return "Never"
			}
			return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
		},
		"hasPrefix":  strings.HasPrefix,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"trimPrefix": strings.TrimPrefix,
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		panic(fmt.Sprintf("failed to parse templates: %v", err))
	}
	h.tmpl = tmpl

	// Register routes
	h.mux.HandleFunc("GET /setup", h.handleSetup)
	h.mux.HandleFunc("POST /setup/save", h.handleSetupSave)
	h.mux.HandleFunc("GET /", h.handleIndex)
	h.mux.HandleFunc("POST /tasks", h.handleCreateTask)
	h.mux.HandleFunc("POST /tasks/{id}/complete", h.handleCompleteTask)
	h.mux.HandleFunc("POST /tasks/bulk", h.handleBulkAction)
	h.mux.HandleFunc("POST /tasks/purge-completed", h.handlePurgeCompleted)
	h.mux.HandleFunc("GET /logs", h.handleLogs)
	h.mux.HandleFunc("GET /settings", h.handleSettings)
	h.mux.HandleFunc("POST /settings/save", h.handleSettingsSave)
	h.mux.HandleFunc("POST /settings/backfill-embeddings", h.handleBackfillEmbeddings)
	
	if h.noteDB != nil {
		h.mux.HandleFunc("POST /settings/mcp-tokens/create", h.handleMCPTokenCreate)
		h.mux.HandleFunc("POST /settings/mcp-tokens/revoke", h.handleMCPTokenRevoke)
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

	// JSON API endpoints
	h.mux.HandleFunc("GET /api/search", h.handleAPISearch)
	h.mux.HandleFunc("GET /api/notes/pages", h.handleAPIGetPages)
	h.mux.HandleFunc("GET /api/notes/pages/image", h.handleAPIGetImage)

	// Config and sources API endpoints
	if h.noteDB != nil {
		h.mux.HandleFunc("GET /api/config", h.handleGetConfig)
		h.mux.HandleFunc("PUT /api/config", h.handlePutConfig)
		h.mux.HandleFunc("GET /api/sources", h.handleListSources)
		h.mux.HandleFunc("POST /api/sources", h.handleAddSource)
		h.mux.HandleFunc("PUT /api/sources/{id}", h.handleUpdateSource)
		h.mux.HandleFunc("DELETE /api/sources/{id}", h.handleDeleteSource)
	}

	// Chat routes
	h.mux.HandleFunc("GET /chat", h.handleChat)
	h.mux.HandleFunc("POST /chat/ask", h.handleAsk)
	h.mux.HandleFunc("GET /chat/sessions", h.handleChatSessions)
	h.mux.HandleFunc("GET /chat/messages", h.handleChatMessages)

	return h
}

// handleAsk handles POST /chat/ask. Orchestrates retrieval → prompt → vLLM → SSE proxy.
func (h *Handler) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID int    `json:"session_id"`
		Question  string `json:"question"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if req.Question == "" {
		http.Error(w, `{"error":"question is required"}`, http.StatusBadRequest)
		return
	}

	responses, err := h.search.Ask(r.Context(), req.Question, req.SessionID)
	if err != nil {
		h.logger.Error("ask failed", "error", err)
		http.Error(w, "chat failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)
	for resp := range responses {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(resp))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ServeHTTP implements http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// baseTemplateData returns shared data needed by all routes that render index.html.
// This ensures the task list is always available regardless of which tab is active.
func (h *Handler) baseTemplateData(ctx context.Context) map[string]interface{} {
	data := map[string]interface{}{}
	
	if h.tasks != nil {
		tasks, err := h.tasks.List(ctx)
		if err != nil {
			h.logger.Error("failed to list tasks for template", "error", err)
		} else {
			data["tasks"] = tasks
		}
	}

	cfg, _ := h.config.GetConfig(ctx)
	if c, ok := cfg.(*appconfig.Config); ok {
		data["BooxNotesPath"] = c.DBEnvPath // or specific path from config
	}

	data["RestartRequired"] = h.config.IsRestartRequired()
	
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

// DefaultBooxTodoPrompt is the default prompt for red ink to-do extraction.
const DefaultBooxTodoPrompt = `Find any passages on this page written in RED ink. For each red passage, return a JSON object on its own line like: {"type":"todo","text":"the red text content"}. If there are no red passages, return nothing.`

// handleSettings renders the settings page
func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "settings"

	cfg, err := h.config.GetConfig(ctx)
	if err != nil {
		h.logger.Error("failed to get config", "error", err)
	} else {
		data["Config"] = cfg
	}

	sources, err := h.config.ListSources(ctx)
	if err != nil {
		h.logger.Error("failed to list sources", "error", err)
	} else {
		data["Sources"] = sources
	}

	// MCP Tokens (mcp_tokens table managed directly for now)
	tokens, err := mcpauth.ListTokens(ctx, h.noteDB)
	if err != nil {
		h.logger.Error("list mcp tokens", "error", err)
	}
	data["MCPTokens"] = tokens
	data["MCPTokensEnabled"] = true

	// One-time flash: display raw token after creation
	if newToken := r.URL.Query().Get("new_token"); newToken != "" {
		data["NewMCPToken"] = newToken
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

	// Get current config to update it
	cObj, err := h.config.GetConfig(ctx)
	if err != nil {
		h.logger.Error("failed to get config for save", "error", err)
		http.Error(w, "failed to load configuration", http.StatusInternalServerError)
		return
	}
	cfg := cObj.(*appconfig.Config)

	section := r.FormValue("section")
	switch section {
	case "supernote":
		cfg.SNSyncEnabled = r.FormValue("inject_enabled") != "false"
	case "general":
		cfg.EmbedEnabled = r.FormValue("embed_enabled") == "true"
		cfg.OllamaURL = r.FormValue("ollama_url")
		cfg.OllamaEmbedModel = r.FormValue("ollama_embed_model")
		cfg.ChatEnabled = r.FormValue("chat_enabled") == "true"
		cfg.ChatAPIURL = r.FormValue("chat_api_url")
		cfg.ChatModel = r.FormValue("chat_model")
		cfg.LogVerboseAPI = r.FormValue("log_verbose_api") == "true"
	}

	if err := h.config.UpdateConfig(ctx, cfg); err != nil {
		h.logger.Error("failed to update config", "error", err)
		http.Error(w, "failed to save configuration", http.StatusInternalServerError)
		return
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
	var dueAt *time.Time
	if dueDateStr != "" {
		t, err := time.Parse("2006-01-02", dueDateStr)
		if err != nil {
			h.logger.Warn("invalid due date", "error", err, "value", dueDateStr)
			http.Error(w, "invalid due date format", http.StatusBadRequest)
			return
		}
		utc := t.UTC()
		dueAt = &utc
	}

	if _, err := h.tasks.Create(r.Context(), title, dueAt); err != nil {
		h.logger.Error("failed to create task", "error", err)
		http.Error(w, "failed to create task", http.StatusInternalServerError)
		return
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

	if err := h.tasks.Complete(r.Context(), taskID); err != nil {
		h.logger.Error("failed to complete task", "error", err, "task_id", taskID)
		http.Error(w, "failed to complete task", http.StatusInternalServerError)
		return
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

	var err error
	switch action {
	case "complete":
		err = h.tasks.BulkComplete(r.Context(), taskIDs)
	case "delete":
		err = h.tasks.BulkDelete(r.Context(), taskIDs)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	if err != nil {
		h.logger.Error("bulk action failure", "action", action, "error", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handlePurgeCompleted(w http.ResponseWriter, r *http.Request) {
	if err := h.tasks.PurgeCompleted(r.Context()); err != nil {
		h.logger.Error("purge completed tasks", "error", err)
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
	if err := h.search.TriggerBackfill(r.Context()); err != nil {
		h.logger.Error("backfill failed", "err", err)
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "files"

	rawPath := r.URL.Query().Get("path")
	relPath, _ := safeRelPath(rawPath)
	
	sortField := r.URL.Query().Get("sort")
	sortOrder := r.URL.Query().Get("order")
	
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))

	files, total, err := h.notes.ListFiles(ctx, relPath, sortField, sortOrder, page, perPage)
	if err != nil {
		h.logger.Error("handleFiles list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data["files"] = files
	data["relPath"] = relPath
	data["breadcrumbs"] = buildBreadcrumbs(relPath)
	data["filesTotalFiles"] = total

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

	if query != "" {
		results, err := h.search.Search(ctx, query, folder)
		if err != nil {
			h.logger.Error("handleSearch", "err", err)
		} else {
			data["searchResults"] = results
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

// handleChat renders the chat page
func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	data := h.baseTemplateData(ctx)
	data["activeTab"] = "chat"
	
	sessions, _ := h.search.ListSessions(ctx)
	data["chatSessions"] = sessions

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		h.logger.Error("failed to render template", "error", err)
	}
}

// handleChatSessions returns JSON list of chat sessions
func (h *Handler) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	sessions, err := h.search.ListSessions(ctx)
	if err != nil {
		h.logger.Error("list sessions", "err", err)
		http.Error(w, "failed to list sessions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

// handleChatMessages returns JSON list of messages for a session
func (h *Handler) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	sessionIDStr := r.URL.Query().Get("session_id")
	sessionID, _ := strconv.Atoi(sessionIDStr)

	if sessionID == 0 {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	messages, err := h.search.GetMessages(ctx, sessionID)
	if err != nil {
		h.logger.Error("get messages", "err", err)
		http.Error(w, "failed to get messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
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
		if err := h.notes.Enqueue(r.Context(), path, false); err != nil {
			h.logger.Error("failed to enqueue", "path", path, "error", err)
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
		if err := h.notes.Skip(r.Context(), path, "manual"); err != nil {
			h.logger.Error("failed to skip", "path", path, "error", err)
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
		if err := h.notes.Unskip(r.Context(), path); err != nil {
			h.logger.Error("failed to unskip", "path", path, "error", err)
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesForce(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.logger.Error("failed to parse form", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	path := r.FormValue("path")
	back := r.FormValue("back")
	if path != "" {
		if err := h.notes.Enqueue(r.Context(), path, true); err != nil {
			h.logger.Error("failed to force enqueue", "path", path, "error", err)
		}
	}
	http.Redirect(w, r, "/files?path="+url.QueryEscape(back), http.StatusSeeOther)
}

func (h *Handler) handleFilesStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.notes.GetProcessorStatus(r.Context())
	if err != nil {
		h.logger.Error("failed to get processor status", "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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

	details, err := h.notes.GetNoteDetails(ctx, path)
	if err != nil {
		h.logger.Error("failed to get note details", "path", path, "error", err)
		w.Write([]byte("null"))
		return
	}
	json.NewEncoder(w).Encode(details)
}

// handleFilesContent returns indexed note_content for a single file as JSON.
// GET /files/content?path=<absolute_path>
func (h *Handler) handleFilesContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" {
		w.Write([]byte("[]"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	docs, err := h.notes.GetContent(ctx, path)
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

	stream, contentType, err := h.notes.RenderPage(r.Context(), path, pageIdx)
	if err != nil {
		h.logger.Error("render failed", "path", path, "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	io.Copy(w, stream)
}

func (h *Handler) handleProcessorStart(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.StartProcessor(r.Context()); err != nil {
		h.logger.Error("failed to start processor", "error", err)
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleProcessorStop(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.StopProcessor(r.Context()); err != nil {
		h.logger.Error("failed to stop processor", "error", err)
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesScan(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.ScanFiles(r.Context()); err != nil {
		h.logger.Error("scan failed", "error", err)
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesImport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	if err := h.notes.ImportFiles(ctx); err != nil {
		h.logger.Error("import failed", "error", err)
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesRetryFailed(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.RetryFailed(r.Context()); err != nil {
		h.logger.Error("retry failed jobs", "error", err)
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

	if err := h.notes.DeleteNote(r.Context(), path); err != nil {
		h.logger.Error("delete failed", "path", path, "error", err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
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

	if err := h.notes.BulkDelete(r.Context(), paths); err != nil {
		h.logger.Error("bulk delete failure", "error", err)
	}

	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesMigrateImports(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.MigrateImports(r.Context()); err != nil {
		h.logger.Error("migrate failed", "error", err)
	}
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status, _ := h.config.GetSyncStatus(r.Context())
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	h.config.TriggerSync(r.Context())
	status, _ := h.config.GetSyncStatus(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleBooxRender serves cached JPEG page images for Boox notes.
// GET /files/boox/render?path=<absolute_path>&page=<int>
func (h *Handler) handleBooxRender(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	pageStr := r.URL.Query().Get("page")
	page, _ := strconv.Atoi(pageStr)

	stream, contentType, err := h.notes.RenderPage(r.Context(), path, page)
	if err != nil {
		h.logger.Debug("boox render failed", "path", path, "error", err)
		http.Error(w, "Note not found", http.StatusNotFound)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	io.Copy(w, stream)
}

// handleBooxVersions returns a list of archived versions for a Boox note.
// GET /files/boox/versions?path=<absolute_path>
func (h *Handler) handleBooxVersions(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	
	// Note: Currently service doesn't have ListVersions, we might need to add it or use GetNoteDetails
	details, err := h.notes.GetNoteDetails(r.Context(), path)
	if err != nil {
		h.logger.Error("boox versions failed", "path", path, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(details)
}

// handleMCPTokenCreate creates a new MCP bearer token and redirects with one-time display.
func (h *Handler) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "token label is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rawToken, _, err := mcpauth.CreateToken(ctx, h.noteDB, label)
	if err != nil {
		h.logger.Error("create mcp token", "error", err)
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings?new_token="+url.QueryEscape(rawToken)+"#mcp-tokens", http.StatusSeeOther)
}

// handleMCPTokenRevoke revokes an MCP token by its hash.
func (h *Handler) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	tokenHash := r.FormValue("token_hash")
	if tokenHash == "" {
		http.Error(w, "token hash is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := mcpauth.RevokeToken(ctx, h.noteDB, tokenHash); err != nil {
		h.logger.Error("revoke mcp token", "error", err)
		http.Error(w, "failed to revoke token", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings#mcp-tokens", http.StatusSeeOther)
}

// HandleOAuthAuthorize handles the first leg of Claude's OAuth flow.
// It skips user consent and immediately redirects back with a fixed code.
func (h *Handler) HandleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")

	if redirectURI == "" {
		http.Error(w, "missing redirect_uri", http.StatusBadRequest)
		return
	}

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}

	q := target.Query()
	q.Set("code", "mcp-fixed-flow-code")
	if state != "" {
		q.Set("state", state)
	}
	target.RawQuery = q.Encode()

	h.logger.Info("OAuth authorize: redirecting to client", "target", target.String())
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// HandleOAuthToken handles the token exchange leg of Claude's OAuth flow.
// It ignores the code and returns a new persistent MCP bearer token.
func (h *Handler) HandleOAuthToken(w http.ResponseWriter, r *http.Request) {
	// Claude sends form-encoded data
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Generate a new persistent bearer token for this connection.
	// We label it "Claude-OAuth" so the user can identify it in Settings.
	rawToken, _, err := mcpauth.CreateToken(ctx, h.noteDB, "Claude-OAuth")
	if err != nil {
		h.logger.Error("OAuth token: failed to create bearer token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.logger.Info("OAuth token: issued new bearer token for Claude")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": rawToken,
		"token_type":   "Bearer",
		"expires_in":   315360000, // 10 years (effectively permanent)
	})
}
