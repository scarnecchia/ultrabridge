package web

import (
	"context"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
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
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/service"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Handler struct {
	tasks    service.TaskService
	notes    service.NoteService
	search   service.SearchService
	config   service.ConfigService
	
	noteDB          *sql.DB
	notesPathPrefix string
	booxNotesPath   string
	booxImportPath  string
	tmpl            *template.Template
	mux             *http.ServeMux
	logger          *slog.Logger
	broadcaster     *logging.LogBroadcaster
}

func formatDueTime(val interface{}) string {
	var t time.Time
	switch v := val.(type) {
	case int64:
		if v == 0 { return "No due date" }
		t = time.UnixMilli(v).UTC()
	case *time.Time:
		if v == nil { return "No due date" }
		t = *v
	case time.Time:
		if v.IsZero() { return "No due date" }
		t = v
	default:
		return "No due date"
	}
	return t.Format("2006-01-02")
}

func formatCreated(val interface{}) string {
	var t time.Time
	switch v := val.(type) {
	case int64:
		if v == 0 { return "-" }
		t = time.UnixMilli(v).UTC()
	case *time.Time:
		if v == nil { return "-" }
		t = *v
	case time.Time:
		if v.IsZero() { return "-" }
		t = v
	case sql.NullInt64:
		if !v.Valid || v.Int64 == 0 { return "-" }
		t = time.UnixMilli(v.Int64).UTC()
	default:
		return "-"
	}
	return t.Format("2006-01-02")
}

func NewHandler(
	tasks service.TaskService,
	notes service.NoteService,
	search service.SearchService,
	config service.ConfigService,
	noteDB *sql.DB,
	notesPathPrefix string,
	booxNotesPath string,
	logger *slog.Logger,
	broadcaster *logging.LogBroadcaster,
) *Handler {
	h := &Handler{
		tasks:           tasks,
		notes:           notes,
		search:          search,
		config:          config,
		noteDB:          noteDB,
		notesPathPrefix: notesPathPrefix,
		booxNotesPath:   booxNotesPath,
		logger:          logger,
		broadcaster:     broadcaster,
		mux:             http.NewServeMux(),
	}

	if noteDB != nil {
		h.booxImportPath, _ = notedb.GetSetting(context.Background(), noteDB, appconfig.KeyBooxImportPath)
	}

	funcMap := template.FuncMap{
		"formatDueTime": formatDueTime,
		"formatCreated": formatCreated,
		"formatTimestamp": func(ms int64) string {
			if ms == 0 { return "Never" }
			return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
		},
		"fileTypeStr": func(ft string) string { return ft },
		"noteSource": func(path string) string {
			if h.booxNotesPath != "" && strings.HasPrefix(path, h.booxNotesPath) { return "Boox" }
			if h.booxImportPath != "" && strings.HasPrefix(path, h.booxImportPath) { return "Boox" }
			return "Supernote"
		},
		"hasPrefix":  strings.HasPrefix,
		"add":        func(a, b int) int { return a + b },
		"sub":        func(a, b int) int { return a - b },
		"trimPrefix": strings.TrimPrefix,
		"taskLink": func(val interface{}) map[string]interface{} {
			if val == nil { return nil }
			var link struct {
				AppName  string `json:"appName"`
				FilePath string `json:"filePath"`
				Page     int    `json:"page"`
			}
			switch v := val.(type) {
			case string:
				if v == "" { return nil }
				data, _ := base64.StdEncoding.DecodeString(v)
				json.Unmarshal(data, &link)
			case *service.TaskLink:
				if v == nil { return nil }
				link.AppName, link.FilePath, link.Page = v.AppName, v.FilePath, v.Page
			case service.TaskLink:
				link.AppName, link.FilePath, link.Page = v.AppName, v.FilePath, v.Page
			default:
				return nil
			}
			if link.FilePath == "" { return nil }
			const devicePrefix = "/storage/emulated/0/Note/"
			localPath := link.FilePath
			if h.notesPathPrefix != "" && strings.HasPrefix(link.FilePath, devicePrefix) {
				localPath = filepath.Join(h.notesPathPrefix, link.FilePath[len(devicePrefix):])
			}
			return map[string]interface{}{"Path": localPath, "Page": link.Page}
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil { panic(fmt.Sprintf("failed to parse templates: %v", err)) }
	h.tmpl = tmpl

	h.mux.HandleFunc("GET /setup", h.handleSetup)
	h.mux.HandleFunc("POST /setup/save", h.handleSetupSave)
	h.mux.HandleFunc("GET /{$}", h.handleIndex)
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
	
	h.mux.HandleFunc("GET /sync/status", func(w http.ResponseWriter, r *http.Request) {
		if h.config == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(service.SyncStatus{})
			return
		}
		h.handleSyncStatus(w, r)
	})
	h.mux.HandleFunc("POST /sync/trigger", func(w http.ResponseWriter, r *http.Request) {
		if h.config == nil || !h.config.HasSyncProvider() {
			http.NotFound(w, r)
			return
		}
		h.handleSyncTrigger(w, r)
	})
	h.registerLogStreamHandler(broadcaster)

	h.mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		if h.search == nil || !h.search.HasEmbeddingPipeline() {
			http.NotFound(w, r)
			return
		}
		h.handleAPISearch(w, r)
	})
	h.mux.HandleFunc("GET /api/notes/pages", h.handleAPIGetPages)
	h.mux.HandleFunc("GET /api/notes/pages/image", h.handleAPIGetImage)

	if h.noteDB != nil {
		h.mux.HandleFunc("GET /api/config", h.handleGetConfig)
		h.mux.HandleFunc("PUT /api/config", h.handlePutConfig)
		h.mux.HandleFunc("GET /api/sources", h.handleListSources)
		h.mux.HandleFunc("POST /api/sources", h.handleAddSource)
		h.mux.HandleFunc("PUT /api/sources/{id}", h.handleUpdateSource)
		h.mux.HandleFunc("DELETE /api/sources/{id}", h.handleDeleteSource)
	}

	h.mux.HandleFunc("GET /chat", h.handleChat)
	h.mux.HandleFunc("POST /chat/ask", h.handleAsk)
	h.mux.HandleFunc("GET /chat/sessions", h.handleChatSessions)
	h.mux.HandleFunc("GET /chat/messages", h.handleChatMessages)

	h.RegisterAPIv1()

	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	h.mux.Handle("GET /manifest.json", fileServer)
	h.mux.Handle("GET /sw.js", fileServer)
	h.mux.Handle("GET /htmx.min.js", fileServer)
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", fileServer))
	h.mux.Handle("GET /erb.png", fileServer)

	return h
}

func (h *Handler) renderTemplate(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = h.baseTemplateData(r.Context())
	} else {
		base := h.baseTemplateData(r.Context())
		for k, v := range base {
			if _, ok := data[k]; !ok {
				data[k] = v
			}
		}
	}
	if _, ok := data["activeTab"]; !ok {
		data["activeTab"] = name
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Clone to avoid race condition when defining "content" template
	t, err := h.tmpl.Clone()
	if err != nil {
		h.logger.Error("failed to clone template", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Define "content" as the specific fragment being rendered
	fragmentPath := "templates/" + name + ".html"
	content, err := templateFS.ReadFile(fragmentPath)
	if err != nil {
		h.logger.Error("failed to read fragment", "path", fragmentPath, "error", err)
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	_, err = t.New("content").Parse(string(content))
	if err != nil {
		h.logger.Error("failed to parse fragment", "name", name, "error", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		t.ExecuteTemplate(w, "content", data)
		return
	}
	t.ExecuteTemplate(w, "layout.html", data)
}

// renderFragment executes a named, pre-parsed template block (e.g. "_task_row")
// without the layout shell. It Clones h.tmpl before executing so that h.tmpl
// remains Clone-able: html/template permanently locks a template tree against
// future Clones once ExecuteTemplate has run on it, and renderTemplate relies
// on Clone per request.
func (h *Handler) renderFragment(w http.ResponseWriter, r *http.Request, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, err := h.tmpl.Clone()
	if err != nil {
		h.logger.Error("failed to clone template for fragment", "name", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		h.logger.Error("failed to execute fragment", "name", name, "error", err)
	}
}

func (h *Handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	h.renderTemplate(w, r, "tasks", nil)
}

func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	data := map[string]interface{}{"activeTab": "files"}
	if detail := r.URL.Query().Get("detail"); detail != "" {
		data["detailPath"] = detail
	}
	if !h.notes.HasSupernoteSource() {
		data["filesError"] = "No Supernote source configured. Add a source in Settings."
		h.renderTemplate(w, r, "files", data)
		return
	}
	rawPath := r.URL.Query().Get("path")
	relPath, ok := safeRelPath(rawPath)
	if !ok { http.Error(w, "invalid path", http.StatusBadRequest); return }
	sortField, sortOrder := r.URL.Query().Get("sort"), r.URL.Query().Get("order")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage <= 0 { perPage = 25 }
	if page <= 0 { page = 1 }

	files, total, err := h.notes.ListFiles(ctx, relPath, sortField, sortOrder, page, perPage)
	if err != nil { http.Error(w, "internal error", http.StatusInternalServerError); return }
	data["files"], data["relPath"], data["breadcrumbs"], data["filesTotalFiles"] = files, relPath, buildBreadcrumbs(relPath), total
	data["filesPage"], data["filesPerPage"] = page, perPage
	data["filesTotalPages"] = (total + perPage - 1) / perPage
	if data["filesTotalPages"] == 0 { data["filesTotalPages"] = 1 }
	h.renderTemplate(w, r, "files", data)
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{"activeTab": "search"}
	query, folder := strings.TrimSpace(r.URL.Query().Get("q")), strings.TrimSpace(r.URL.Query().Get("folder"))
	data["searchQuery"], data["searchFolder"] = query, folder
	if query != "" {
		results, _ := h.search.Search(r.Context(), query, folder)
		data["searchResults"] = results
	}
	h.renderTemplate(w, r, "search", data)
}

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	sessions, _ := h.search.ListSessions(r.Context())
	h.renderTemplate(w, r, "chat", map[string]interface{}{"chatSessions": sessions})
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) { h.renderTemplate(w, r, "logs", nil) }

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, _ := h.config.GetConfig(ctx)
	srcs, _ := h.config.ListSources(ctx)
	data := map[string]interface{}{"Config": cfg, "Sources": srcs, "activeTab": "settings"}
	if h.noteDB != nil {
		tokens, _ := mcpauth.ListTokens(ctx, h.noteDB)
		data["MCPTokens"], data["MCPTokensEnabled"] = tokens, true
	}
	if nt := r.URL.Query().Get("new_token"); nt != "" { data["NewMCPToken"] = nt }
	h.renderTemplate(w, r, "settings", data)
}

func (h *Handler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	dueDateStr := strings.TrimSpace(r.FormValue("due_date"))
	var dueAt *time.Time
	if dueDateStr != "" {
		if t, err := time.Parse("2006-01-02", dueDateStr); err == nil {
			utc := t.UTC()
			dueAt = &utc
		} else {
			http.Error(w, "invalid due date", http.StatusBadRequest)
			return
		}
	}
	created, err := h.tasks.Create(r.Context(), title, dueAt)
	if err != nil {
		http.Error(w, "failed to create task", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.renderFragment(w, r, "_task_row", created)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	if h.tasks == nil {
		http.NotFound(w, r)
		return
	}
	taskID := r.PathValue("id")
	if err := h.tasks.Complete(r.Context(), taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "task not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to complete task", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		t, err := h.tasks.Get(r.Context(), taskID)
		if err != nil {
			h.logger.Error("failed to fetch completed task for fragment render", "id", taskID, "error", err)
			http.Error(w, "failed to render row", http.StatusInternalServerError)
			return
		}
		h.renderFragment(w, r, "_task_row", t)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handleBulkAction(w http.ResponseWriter, r *http.Request) {
	action, ids := r.FormValue("action"), r.Form["task_ids"]
	if action != "complete" && action != "delete" {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if len(ids) > 0 {
		if action == "complete" {
			h.tasks.BulkComplete(r.Context(), ids)
		} else if action == "delete" {
			h.tasks.BulkDelete(r.Context(), ids)
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		if action == "complete" {
			for _, id := range ids {
				t, err := h.tasks.Get(r.Context(), id)
				if err != nil {
					h.logger.Error("bulk complete: failed to fetch task for fragment render", "id", id, "error", err)
					continue
				}
				h.renderFragment(w, r, "_task_row", t)
			}
		}
		// action=delete: empty response body; client removes checked rows.
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handlePurgeCompleted(w http.ResponseWriter, r *http.Request) {
	h.tasks.PurgeCompleted(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleIndex(w, r); return }
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	cObj, _ := h.config.GetConfig(r.Context())
	cfg := cObj.(*appconfig.Config)
	switch r.FormValue("section") {
	case "supernote": cfg.SNSyncEnabled = r.FormValue("inject_enabled") != "false"
	case "general":
		cfg.EmbedEnabled = r.FormValue("embed_enabled") == "true"
		cfg.OllamaURL, cfg.OllamaEmbedModel = r.FormValue("ollama_url"), r.FormValue("ollama_embed_model")
		cfg.ChatEnabled = r.FormValue("chat_enabled") == "true"
		cfg.ChatAPIURL, cfg.ChatModel = r.FormValue("chat_api_url"), r.FormValue("chat_model")
		cfg.LogVerboseAPI = r.FormValue("log_verbose_api") == "true"
	}
	h.config.UpdateConfig(r.Context(), cfg)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleBackfillEmbeddings(w http.ResponseWriter, r *http.Request) {
	if h.search == nil || !h.search.HasEmbeddingPipeline() {
		http.NotFound(w, r)
		return
	}
	h.search.TriggerBackfill(r.Context())
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) handleProcessorStart(w http.ResponseWriter, r *http.Request) {
	h.notes.StartProcessor(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleProcessorStop(w http.ResponseWriter, r *http.Request) {
	h.notes.StopProcessor(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesScan(w http.ResponseWriter, r *http.Request) {
	h.notes.ScanFiles(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesImport(w http.ResponseWriter, r *http.Request) {
	h.notes.ImportFiles(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesRetryFailed(w http.ResponseWriter, r *http.Request) {
	h.notes.RetryFailed(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesDeleteNote(w http.ResponseWriter, r *http.Request) {
	h.notes.DeleteNote(r.Context(), r.FormValue("path"))
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesDeleteBulk(w http.ResponseWriter, r *http.Request) {
	paths := r.Form["paths"]
	if len(paths) > 0 { h.notes.BulkDelete(r.Context(), paths) }
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleFilesMigrateImports(w http.ResponseWriter, r *http.Request) {
	h.notes.MigrateImports(r.Context())
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files", http.StatusSeeOther)
}

func (h *Handler) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	status, _ := h.config.GetSyncStatus(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	h.config.TriggerSync(r.Context())
	status, _ := h.config.GetSyncStatus(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleFilesQueue(w http.ResponseWriter, r *http.Request) {
	h.notes.Enqueue(r.Context(), r.FormValue("path"), false)
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files?path="+url.QueryEscape(r.FormValue("back")), http.StatusSeeOther)
}

func (h *Handler) handleFilesSkip(w http.ResponseWriter, r *http.Request) {
	h.notes.Skip(r.Context(), r.FormValue("path"), "manual")
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files?path="+url.QueryEscape(r.FormValue("back")), http.StatusSeeOther)
}

func (h *Handler) handleFilesUnskip(w http.ResponseWriter, r *http.Request) {
	h.notes.Unskip(r.Context(), r.FormValue("path"))
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files?path="+url.QueryEscape(r.FormValue("back")), http.StatusSeeOther)
}

func (h *Handler) handleFilesForce(w http.ResponseWriter, r *http.Request) {
	h.notes.Enqueue(r.Context(), r.FormValue("path"), true)
	if r.Header.Get("HX-Request") == "true" { h.handleFiles(w, r); return }
	http.Redirect(w, r, "/files?path="+url.QueryEscape(r.FormValue("back")), http.StatusSeeOther)
}

func (h *Handler) handleBooxRender(w http.ResponseWriter, r *http.Request) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	stream, ct, err := h.notes.RenderPage(r.Context(), r.URL.Query().Get("path"), p)
	if err != nil { http.Error(w, "not found", http.StatusNotFound); return }
	defer stream.Close()
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "public, max-age=300")
	io.Copy(w, stream)
}

func (h *Handler) handleBooxVersions(w http.ResponseWriter, r *http.Request) {
	v, _ := h.notes.ListVersions(r.Context(), r.URL.Query().Get("path"))
	if v == nil { v = []interface{}{} }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		http.Error(w, "token label is required", http.StatusBadRequest)
		return
	}
	t, _, err := mcpauth.CreateToken(r.Context(), h.noteDB, label)
	if err != nil {
		h.logger.Error("failed to create token", "error", err)
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		h.renderTemplate(w, r, "settings", map[string]interface{}{"NewMCPToken": t})
		return
	}
	http.Redirect(w, r, "/settings?new_token="+url.QueryEscape(t)+"#mcp-tokens", http.StatusSeeOther)
}

func (h *Handler) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
	mcpauth.RevokeToken(r.Context(), h.noteDB, r.FormValue("token_hash"))
	if r.Header.Get("HX-Request") == "true" { h.handleSettings(w, r); return }
	http.Redirect(w, r, "/settings#mcp-tokens", http.StatusSeeOther)
}

func (h *Handler) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req struct { SessionID int `json:"session_id"`; Question string `json:"question"` }
	json.NewDecoder(r.Body).Decode(&req)
	responses, err := h.search.Ask(r.Context(), req.Question, req.SessionID)
	if err != nil { http.Error(w, "chat failed", http.StatusInternalServerError); return }
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for resp := range responses {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(resp))
		if flusher != nil { flusher.Flush() }
	}
}

func (h *Handler) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	s, _ := h.search.ListSessions(r.Context())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s)
}

func (h *Handler) handleFilesStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.notes.GetProcessorStatus(r.Context())
	if err != nil {
		h.logger.Error("failed to get processor status", "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func (h *Handler) handleFilesHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" {
		w.Write([]byte("null"))
		return
	}
	details, err := h.notes.GetNoteDetails(r.Context(), path)
	if err != nil {
		h.logger.Error("failed to get note details", "path", path, "error", err)
		w.Write([]byte("null"))
		return
	}
	json.NewEncoder(w).Encode(details)
}

func (h *Handler) handleFilesContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Query().Get("path")
	if path == "" {
		w.Write([]byte("[]"))
		return
	}
	docs, err := h.notes.GetContent(r.Context(), path)
	if err != nil {
		h.logger.Error("failed to get content", "path", path, "error", err)
		w.Write([]byte("[]"))
		return
	}
	json.NewEncoder(w).Encode(docs)
}

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

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (h *Handler) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.URL.Query().Get("session_id"))
	m, _ := h.search.GetMessages(r.Context(), id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

// HandleOAuthAuthorize handles the first leg of Claude's OAuth flow.
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
func (h *Handler) HandleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

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
		"expires_in":   315360000,
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *Handler) baseTemplateData(ctx context.Context) map[string]interface{} {
	data := map[string]interface{}{}
	if h.tasks != nil {
		if t, err := h.tasks.List(ctx); err == nil {
			data["tasks"] = t
		}
	}
	data["BooxNotesPath"] = h.booxNotesPath
	data["BooxImportPath"] = h.booxImportPath
	if h.config != nil {
		data["RestartRequired"] = h.config.IsRestartRequired()
	}
	data["chatEnabled"] = h.search != nil
	return data
}

type breadcrumb struct { Label, RelPath string }
func buildBreadcrumbs(p string) []breadcrumb {
	res := []breadcrumb{{Label: "Home", RelPath: ""}}
	if p == "" { return res }
	parts := strings.Split(p, "/")
	for i := range parts { res = append(res, breadcrumb{Label: parts[i], RelPath: strings.Join(parts[:i+1], "/")}) }
	return res
}

func safeRelPath(p string) (string, bool) {
	if p == "" { return "", true }
	c := filepath.Clean(p)
	if filepath.IsAbs(c) || strings.HasPrefix(c, "..") { return "", false }
	return c, true
}
