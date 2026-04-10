package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sysop/ultrabridge/internal/source"
)

// handleListSources handles GET /api/sources — list all sources.
func (h *Handler) handleListSources(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sources, err := source.ListSources(ctx, h.noteDB)
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

	// Validate config_json is valid JSON if provided.
	if row.ConfigJSON != "" && !json.Valid([]byte(row.ConfigJSON)) {
		apiError(w, http.StatusBadRequest, "config_json must be valid JSON")
		return
	}

	id, err := source.AddSource(ctx, h.noteDB, row)
	if err != nil {
		h.logger.Error("add source", "error", err)
		apiError(w, http.StatusInternalServerError, "failed to add source")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int64{"id": id})
}

// handleUpdateSource handles PUT /api/sources/{id} — update a source.
func (h *Handler) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse ID from URL path.
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid source ID")
		return
	}

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

	// Validate config_json is valid JSON if provided.
	if row.ConfigJSON != "" && !json.Valid([]byte(row.ConfigJSON)) {
		apiError(w, http.StatusBadRequest, "config_json must be valid JSON")
		return
	}

	row.ID = id
	if err := source.UpdateSource(ctx, h.noteDB, row); err != nil {
		if err.Error() == fmt.Sprintf("%s", source.ErrSourceNotFound) {
			apiError(w, http.StatusNotFound, "source not found")
		} else {
			h.logger.Error("update source", "error", err)
			apiError(w, http.StatusInternalServerError, "failed to update source")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleDeleteSource handles DELETE /api/sources/{id} — delete a source.
func (h *Handler) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse ID from URL path.
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid source ID")
		return
	}

	if err := source.RemoveSource(ctx, h.noteDB, id); err != nil {
		if err.Error() == fmt.Sprintf("%s", source.ErrSourceNotFound) {
			apiError(w, http.StatusNotFound, "source not found")
		} else {
			h.logger.Error("delete source", "error", err)
			apiError(w, http.StatusInternalServerError, "failed to delete source")
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
