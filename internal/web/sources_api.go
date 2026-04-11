package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sysop/ultrabridge/internal/source"
)

// handleListSources handles GET /api/sources — list all sources.
func (h *Handler) handleListSources(w http.ResponseWriter, r *http.Request) {
	sources, err := h.config.ListSources(r.Context())
	if err != nil {
		h.logger.Error("list sources", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to list sources")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sources)
}

// handleAddSource handles POST /api/sources — add a new source.
func (h *Handler) handleAddSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var row source.SourceRow
	if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate required fields.
	if strings.TrimSpace(row.Type) == "" {
		apiError(w, http.StatusBadRequest, "type must be non-empty")
		return
	}
	if strings.TrimSpace(row.Name) == "" {
		apiError(w, http.StatusBadRequest, "name must be non-empty")
		return
	}

	if err := h.config.AddSource(ctx, &row); err != nil {
		h.logger.Error("add source", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to add source")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleUpdateSource handles PUT /api/sources/{id} — update a source.
func (h *Handler) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := r.PathValue("id")
	if id == "" {
		apiError(w, http.StatusBadRequest, "missing source ID")
		return
	}

	var row source.SourceRow
	if err := json.NewDecoder(r.Body).Decode(&row); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := h.config.UpdateSource(ctx, id, &row); err != nil {
		h.logger.Error("update source", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to update source")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleDeleteSource handles DELETE /api/sources/{id} — delete a source.
func (h *Handler) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		apiError(w, http.StatusBadRequest, "missing source ID")
		return
	}

	if err := h.config.DeleteSource(r.Context(), id); err != nil {
		h.logger.Error("delete source", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to delete source")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
