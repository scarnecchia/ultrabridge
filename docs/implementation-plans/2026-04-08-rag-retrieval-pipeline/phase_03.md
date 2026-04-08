# RAG Retrieval Pipeline — Phase 3: JSON API Endpoints

**Goal:** Expose search, content, and image data via JSON API endpoints for the MCP server and future integrations.

**Architecture:** Three new routes on the existing web handler: `/api/search`, `/api/notes/{path}/pages`, `/api/notes/{path}/pages/{page}/image`. All behind existing Basic Auth middleware. JSON responses with proper error handling.

**Tech Stack:** Go stdlib `net/http`, Go 1.25 path parameters (`{name}` syntax), existing search and handler patterns

**Scope:** 6 phases from original design (phase 3 of 6)

**Codebase verified:** 2026-04-08

---

## Acceptance Criteria Coverage

This phase implements and tests:

### rag-retrieval-pipeline.AC3: JSON API Endpoints
- **rag-retrieval-pipeline.AC3.1 Success:** `GET /api/search?q=...&folder=...&device=...&from=...&to=...&limit=...` returns JSON array of search results with metadata. Verified by: curl returns valid JSON with expected fields.
- **rag-retrieval-pipeline.AC3.2 Success:** `GET /api/notes/{path}/pages` returns JSON array of all page content for a note. Verified by: curl returns page text ordered by page number.
- **rag-retrieval-pipeline.AC3.3 Success:** `GET /api/notes/{path}/pages/{page}/image` returns JPEG image bytes. Verified by: curl returns image/jpeg content-type with valid JPEG data.
- **rag-retrieval-pipeline.AC3.4 Success:** All API endpoints require Basic Auth (same as existing web UI). Verified by: unauthenticated request returns 401.
- **rag-retrieval-pipeline.AC3.5 Success:** API endpoints return appropriate error codes: 400 for bad parameters, 404 for unknown note paths, 500 for internal errors. Verified by: error scenarios return correct status codes with JSON error body.

---

<!-- START_SUBCOMPONENT_A (tasks 1-4) -->
## Subcomponent A: API Endpoints

<!-- START_TASK_1 -->
### Task 1: Add JSON API handler methods and routes

**Verifies:** rag-retrieval-pipeline.AC3.1, rag-retrieval-pipeline.AC3.2, rag-retrieval-pipeline.AC3.3, rag-retrieval-pipeline.AC3.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/web/api.go` (new file for API handlers, keeps handler.go clean)
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go:81-99` (add retriever field to Handler struct)
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go:120` (add retriever param to NewHandler)
- Modify: `/home/jtd/ultrabridge/internal/web/handler.go:223-235` (register API routes)

**CLAUDE.md references for executor:** Read `/home/jtd/ultrabridge/internal/web/CLAUDE.md` for handler conventions and nil-safe patterns.

**Implementation:**

Add a `retriever` field to the Handler struct (nil-safe):

```go
// In Handler struct (handler.go)
retriever   rag.SearchRetriever // nil = API endpoints disabled
```

Add `retriever rag.SearchRetriever` as a parameter to `NewHandler()` and assign it in the constructor. Using the interface (not concrete `*rag.Retriever`) enables mock retriever injection in tests.

Register API routes in `NewHandler()` (after existing routes, before `return h`):

```go
// JSON API endpoints (requires retriever)
if h.retriever != nil {
    h.mux.HandleFunc("GET /api/search", h.handleAPISearch)
    h.mux.HandleFunc("GET /api/notes/{path...}/pages", h.handleAPIGetPages)
    h.mux.HandleFunc("GET /api/notes/{path...}/pages/{page}/image", h.handleAPIGetImage)
}
```

Note: `{path...}` is a Go 1.22+ wildcard that captures the remaining path including slashes. This is needed because note paths are absolute filesystem paths containing `/`.

Create `internal/web/api.go` with the three handler methods:

```go
package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

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
		NotePath  string `json:"note_path"`
		Page      int    `json:"page"`
		BodyText  string `json:"body_text"`
		TitleText string `json:"title_text"`
		Score     float64 `json:"score"`
		Folder    string `json:"folder"`
		Device    string `json:"device"`
		NoteDate  string `json:"note_date,omitempty"`
		URL       string `json:"url"`
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
```

Key design decisions:
- The image handler uses `h.booxCachePath` and `h.snNotesPath` which already exist on the Handler struct (see `handler.go:91-93`: `snNotesPath string`, `booxNotesPath string`, `booxCachePath string`). No new fields needed for this.
- `{path...}` wildcard captures absolute filesystem paths (e.g., `/home/jtd/notes/test.note` → path value is `home/jtd/notes/test.note`, restore leading `/`)
- API routes are only registered if retriever is non-nil
- Auth is handled by the existing `authMW.Wrap(webHandler)` in main.go — API routes are on the same handler, so they're automatically behind Basic Auth (AC3.4)
- `apiError` helper outputs JSON `{"error": "..."}` with appropriate HTTP status codes (AC3.5)
- Image endpoint tries Boox cache first (pre-rendered JPEG), then falls back to on-the-fly Supernote rendering
- Note: The `api.go` file needs imports including the `go-sn` package with alias: `gosnote "github.com/jdkruzr/go-sn/note"` (same alias used in `handler.go`). Also needs: `"encoding/json"`, `"fmt"`, `"image/jpeg"`, `"net/http"`, `"net/url"`, `"os"`, `"path/filepath"`, `"strconv"`, `"strings"`, `"time"`, and `"github.com/sysop/ultrabridge/internal/rag"`

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go vet -C /home/jtd/ultrabridge ./...
```

Expected: Both succeed.

**Commit:** `feat(web): add JSON API endpoints for search, pages, and images`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Update main.go wiring for retriever and API

**Verifies:** rag-retrieval-pipeline.AC3.4

**Files:**
- Modify: `/home/jtd/ultrabridge/cmd/ultrabridge/main.go` (create retriever, pass to NewHandler)

**Implementation:**

After embedding initialization (from Phase 1) and before `NewHandler` call, create the retriever:

```go
// Create retriever if embedding is available (also works FTS-only when embedStore is nil)
var retriever *rag.Retriever
if embedStore != nil {
    retriever = rag.NewRetriever(noteDB, si, embedStore, embedder, logger)
} else {
    // FTS-only mode: retriever works without embeddings
    retriever = rag.NewRetriever(noteDB, si, nil, nil, logger)
}
```

Pass `retriever` to `NewHandler` call. Update the call site at line ~310 to include the new parameter.

Note: The retriever is always created (even without embeddings) so that the API endpoints are available in FTS-only mode. When `embedStore` is nil, the retriever falls back to FTS-only search (AC2.5).

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
```

Expected: Build succeeds.

**Commit:** `feat: wire retriever into main for JSON API`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Tests for API endpoints

**Verifies:** rag-retrieval-pipeline.AC3.1, rag-retrieval-pipeline.AC3.2, rag-retrieval-pipeline.AC3.3, rag-retrieval-pipeline.AC3.4, rag-retrieval-pipeline.AC3.5

**Files:**
- Create: `/home/jtd/ultrabridge/internal/web/api_test.go`

**Testing:**

Follow existing patterns in `/home/jtd/ultrabridge/internal/web/handler_test.go` — use mock stores and `httptest.NewRecorder`.

Tests must verify:

**rag-retrieval-pipeline.AC3.1 — Search endpoint:**
- GET `/api/search?q=test` returns 200 with JSON array
- Response includes `note_path`, `page`, `body_text`, `score`, `url` fields
- GET `/api/search` without `q` parameter returns 400 with JSON error body
- Verify query parameter parsing: `folder`, `device`, `from`, `to`, `limit`

**rag-retrieval-pipeline.AC3.2 — Pages endpoint:**
- GET `/api/notes/{path}/pages` with valid indexed path returns 200 with JSON array of pages ordered by page number
- GET `/api/notes/{path}/pages` with unknown path returns 404

**rag-retrieval-pipeline.AC3.3 — Image endpoint:**
- GET `/api/notes/{path}/pages/0/image` with valid Boox note returns 200 with `Content-Type: image/jpeg`
- GET `/api/notes/{path}/pages/99/image` with invalid page returns 404

**rag-retrieval-pipeline.AC3.4 — Authentication:**
- This is implicitly tested because routes are on the same handler behind authMW. Document that auth is handled at the mux level in main.go, not in individual handlers.

**rag-retrieval-pipeline.AC3.5 — Error codes:**
- 400: missing `q` param, invalid `from` date, invalid `limit`
- 404: unknown note path, page out of range
- All error responses have JSON body `{"error": "..."}`

For the mock retriever, create a simple wrapper that returns pre-configured results:

```go
// Use a real rag.Retriever with in-memory DB and mock embedder,
// or wrap the retriever interface if needed.
```

**Verification:**

```bash
go test -C /home/jtd/ultrabridge ./internal/web/ -run TestAPI -v
```

Expected: All tests pass.

**Commit:** `test(web): add JSON API endpoint tests`
<!-- END_TASK_3 -->

<!-- START_TASK_4 -->
### Task 4: Final build and full test suite verification

**Verifies:** None (verification checkpoint)

**Files:** None

**Verification:**

```bash
go build -C /home/jtd/ultrabridge ./cmd/ultrabridge/
go test -C /home/jtd/ultrabridge ./...
go vet -C /home/jtd/ultrabridge ./...
```

Expected: All commands succeed. No regressions.

**Commit:** No commit — verification only.
<!-- END_TASK_4 -->
<!-- END_SUBCOMPONENT_A -->
