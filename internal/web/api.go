package web

// Imperative Shell: HTTP API handlers with JSON serialization and filesystem I/O.

import (
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gosnote "github.com/jdkruzr/go-sn/note"

	"github.com/sysop/ultrabridge/internal/rag"
)

// apiError writes a JSON error response.
func apiError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleAPISearch handles GET /api/search
func (h *Handler) handleAPISearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		apiError(w, http.StatusBadRequest, "missing required parameter: q")
		return
	}

	req := rag.SearchRequest{
		Query:  q,
		Folder: r.URL.Query().Get("folder"),
		Device: r.URL.Query().Get("device"),
	}

	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid 'from' date format, expected YYYY-MM-DD")
			return
		}
		req.DateFrom = t
	}
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			apiError(w, http.StatusBadRequest, "invalid 'to' date format, expected YYYY-MM-DD")
			return
		}
		req.DateTo = t
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			apiError(w, http.StatusBadRequest, "invalid 'limit' parameter")
			return
		}
		req.Limit = n
	}

	results, err := h.retriever.Search(r.Context(), req)
	if err != nil {
		h.logger.Error("api search failed", "err", err)
		apiError(w, http.StatusInternalServerError, "search failed")
		return
	}

	// Build response with URL field for linking back to web UI
	type apiResult struct {
		NotePath  string  `json:"note_path"`
		Page      int     `json:"page"`
		BodyText  string  `json:"body_text"`
		TitleText string  `json:"title_text"`
		Score     float64 `json:"score"`
		Folder    string  `json:"folder"`
		Device    string  `json:"device"`
		NoteDate  string  `json:"note_date,omitempty"`
		URL       string  `json:"url"`
	}

	out := make([]apiResult, len(results))
	for i, r := range results {
		out[i] = apiResult{
			NotePath:  r.NotePath,
			Page:      r.Page,
			BodyText:  r.BodyText,
			TitleText: r.TitleText,
			Score:     r.Score,
			Folder:    r.Folder,
			Device:    r.Device,
			URL:       "/files/history?path=" + url.QueryEscape(r.NotePath),
		}
		if !r.NoteDate.IsZero() {
			out[i].NoteDate = r.NoteDate.Format("2006-01-02")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleAPIGetPages handles GET /api/notes/{path...}/pages
func (h *Handler) handleAPIGetPages(w http.ResponseWriter, r *http.Request) {
	notePath := "/" + r.PathValue("path") // restore leading slash for absolute path

	docs, err := h.searchIndex.GetContent(r.Context(), notePath)
	if err != nil {
		h.logger.Error("api get pages failed", "path", notePath, "err", err)
		apiError(w, http.StatusInternalServerError, "failed to get pages")
		return
	}
	if len(docs) == 0 {
		apiError(w, http.StatusNotFound, "note not found or no content indexed")
		return
	}

	type pageResult struct {
		Page      int    `json:"page"`
		BodyText  string `json:"body_text"`
		TitleText string `json:"title_text"`
		Keywords  string `json:"keywords,omitempty"`
	}
	out := make([]pageResult, len(docs))
	for i, d := range docs {
		out[i] = pageResult{
			Page:      d.Page,
			BodyText:  d.BodyText,
			TitleText: d.TitleText,
			Keywords:  d.Keywords,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleAPIGetImage handles GET /api/notes/{path...}/pages/{page}/image
func (h *Handler) handleAPIGetImage(w http.ResponseWriter, r *http.Request) {
	notePath := "/" + r.PathValue("path") // restore leading slash
	pageStr := r.PathValue("page")
	page, err := strconv.Atoi(pageStr)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid page number")
		return
	}

	// Try Boox cache first
	if h.booxStore != nil {
		noteID, err := h.booxStore.GetNoteID(r.Context(), notePath)
		if err == nil && noteID != "" {
			cachePath := filepath.Join(h.booxCachePath, noteID, fmt.Sprintf("page_%d.jpg", page))
			data, err := os.ReadFile(cachePath)
			if err == nil {
				w.Header().Set("Content-Type", "image/jpeg")
				w.Write(data)
				return
			}
		}
	}

	// Try Supernote note rendering
	if h.snNotesPath != "" && strings.HasPrefix(notePath, h.snNotesPath) {
		f, err := os.Open(notePath)
		if err != nil {
			apiError(w, http.StatusNotFound, "note file not found")
			return
		}
		n, err := gosnote.Load(f)
		f.Close()
		if err != nil {
			apiError(w, http.StatusInternalServerError, "failed to parse note")
			return
		}
		if page < 0 || page >= len(n.Pages) {
			apiError(w, http.StatusNotFound, "page out of range")
			return
		}
		p := n.Pages[page]
		tp, err := n.TotalPathData(p)
		if err != nil || tp == nil {
			apiError(w, http.StatusNotFound, "no stroke data for page")
			return
		}
		pageW, pageH := n.PageDimensions(p)
		objs, err := gosnote.DecodeObjects(tp, pageW, pageH)
		if err != nil {
			apiError(w, http.StatusInternalServerError, "failed to decode page")
			return
		}
		img := gosnote.RenderObjects(objs, pageW, pageH, nil)
		w.Header().Set("Content-Type", "image/jpeg")
		jpeg.Encode(w, img, &jpeg.Options{Quality: 90})
		return
	}

	apiError(w, http.StatusNotFound, "page image not available")
}
