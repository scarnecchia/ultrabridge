package web

import (
	"encoding/json"
	"net/http"
	"time"
)

// RegisterAPIv1 registers all v1 standard API endpoints.
func (h *Handler) RegisterAPIv1() {
	// Tasks
	h.mux.HandleFunc("GET /api/v1/tasks", h.handleV1ListTasks)
	h.mux.HandleFunc("POST /api/v1/tasks", h.handleV1CreateTask)
	h.mux.HandleFunc("POST /api/v1/tasks/{id}/complete", h.handleV1CompleteTask)
	h.mux.HandleFunc("DELETE /api/v1/tasks/{id}", h.handleV1DeleteTask)
	h.mux.HandleFunc("POST /api/v1/tasks/bulk", h.handleV1BulkTasks)

	// Files
	h.mux.HandleFunc("GET /api/v1/files", h.handleV1ListFiles)
	h.mux.HandleFunc("POST /api/v1/files/scan", h.handleV1ScanFiles)
	h.mux.HandleFunc("POST /api/v1/files/queue", h.handleV1EnqueueFile)
	h.mux.HandleFunc("GET /api/v1/files/content", h.handleV1GetFileContent)
	h.mux.HandleFunc("GET /api/v1/files/render", h.handleV1RenderFile)

	// Search & Chat
	h.mux.HandleFunc("GET /api/v1/search", h.handleV1Search)
	h.mux.HandleFunc("POST /api/v1/chat/ask", h.handleV1ChatAsk)

	// System
	h.mux.HandleFunc("GET /api/v1/status", h.handleV1Status)
	h.mux.HandleFunc("GET /api/v1/config", h.handleV1GetConfig)
	h.mux.HandleFunc("PUT /api/v1/config", h.handleV1UpdateConfig)
	h.mux.HandleFunc("POST /api/v1/client-error", h.handleV1ClientError)
}

// --- Tasks ---

func (h *Handler) handleV1ListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.tasks.List(r.Context())
	if err != nil {
		apiError(w, http.StatusInternalServerError, "failed to list tasks")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (h *Handler) handleV1CreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string     `json:"title"`
		DueAt *time.Time `json:"due_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		apiError(w, http.StatusBadRequest, "title is required")
		return
	}

	task, err := h.tasks.Create(r.Context(), req.Title, req.DueAt)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "failed to create task")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(task)
}

func (h *Handler) handleV1CompleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.tasks.Complete(r.Context(), id); err != nil {
		apiError(w, http.StatusInternalServerError, "failed to complete task")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleV1DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.tasks.Delete(r.Context(), id); err != nil {
		apiError(w, http.StatusInternalServerError, "failed to delete task")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleV1BulkTasks(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var err error
	switch req.Action {
	case "complete":
		err = h.tasks.BulkComplete(r.Context(), req.IDs)
	case "delete":
		err = h.tasks.BulkDelete(r.Context(), req.IDs)
	default:
		apiError(w, http.StatusBadRequest, "invalid action")
		return
	}

	if err != nil {
		apiError(w, http.StatusInternalServerError, "bulk operation failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Files ---

func (h *Handler) handleV1ListFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	sort := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	
	files, _, err := h.notes.ListFiles(r.Context(), path, sort, order, 0, 0)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "failed to list files")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

func (h *Handler) handleV1ScanFiles(w http.ResponseWriter, r *http.Request) {
	if err := h.notes.ScanFiles(r.Context()); err != nil {
		apiError(w, http.StatusInternalServerError, "failed to trigger scan")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) handleV1EnqueueFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string `json:"path"`
		Force bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.notes.Enqueue(r.Context(), req.Path, req.Force); err != nil {
		apiError(w, http.StatusInternalServerError, "failed to enqueue file")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) handleV1GetFileContent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	content, err := h.notes.GetContent(r.Context(), path)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "failed to get content")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(content)
}

func (h *Handler) handleV1RenderFile(w http.ResponseWriter, r *http.Request) {
	// For v1, we might just proxy to the existing logic but keep it here for standard
	h.handleFilesRender(w, r)
}

// --- Search & Chat ---

func (h *Handler) handleV1Search(w http.ResponseWriter, r *http.Request) {
	h.handleAPISearch(w, r)
}

func (h *Handler) handleV1ChatAsk(w http.ResponseWriter, r *http.Request) {
	h.handleAsk(w, r)
}

// --- System ---

func (h *Handler) handleV1Status(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	syncStatus, _ := h.config.GetSyncStatus(ctx)
	jobStatus, _ := h.notes.GetProcessorStatus(ctx)

	resp := map[string]interface{}{
		"sync": syncStatus,
		"jobs": jobStatus,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleV1GetConfig(w http.ResponseWriter, r *http.Request) {
	h.handleGetConfig(w, r)
}

func (h *Handler) handleV1UpdateConfig(w http.ResponseWriter, r *http.Request) {
	h.handlePutConfig(w, r)
}

func (h *Handler) handleV1ClientError(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		URL     string `json:"url"`
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	h.logger.Warn("frontend client error",
		"url", payload.URL,
		"status", payload.Status,
		"message", payload.Message,
	)

	w.WriteHeader(http.StatusNoContent)
}
