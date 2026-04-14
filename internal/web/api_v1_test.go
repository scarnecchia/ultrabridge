package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/service"
)

// TestAPIv1GetTask verifies GET /api/v1/tasks/{id} returns the task JSON and
// 404s on unknown ids.
func TestAPIv1GetTask(t *testing.T) {
	h := newTestHandler()
	tasks := h.tasks.(*mockTaskService)
	tasks.tasks = []service.Task{
		{ID: "t1", Title: "Draft", Status: service.StatusNeedsAction},
	}

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/t1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var got service.Task
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ID != "t1" || got.Title != "Draft" {
			t.Errorf("unexpected task: %+v", got)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/missing", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status=%d, want 404; body=%s", w.Code, w.Body.String())
		}
	})
}

// TestAPIv1UpdateTask verifies PATCH /api/v1/tasks/{id} applies a partial
// update and returns the post-write task JSON.
func TestAPIv1UpdateTask(t *testing.T) {
	h := newTestHandler()
	tasks := h.tasks.(*mockTaskService)
	due := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tasks.tasks = []service.Task{
		{ID: "t1", Title: "Original", Status: service.StatusNeedsAction, DueAt: &due},
	}

	t.Run("title_only", func(t *testing.T) {
		body := `{"title":"Renamed"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/t1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var got service.Task
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Title != "Renamed" {
			t.Errorf("title not applied: %q", got.Title)
		}
		if got.DueAt == nil {
			t.Errorf("due date should be preserved on partial update")
		}
	})

	t.Run("clear_due_date", func(t *testing.T) {
		body := `{"clear_due_at":true}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/t1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var got service.Task
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.DueAt != nil {
			t.Errorf("due date should be cleared; got %v", got.DueAt)
		}
	})

	t.Run("invalid_json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/t1", strings.NewReader("{bad"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d, want 400", w.Code)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		body := `{"title":"ghost"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/missing", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("status=%d, want 404", w.Code)
		}
	})
}

// TestAPIv1PurgeCompleted verifies POST /api/v1/tasks/purge-completed
// invokes the service and returns 204.
func TestAPIv1PurgeCompleted(t *testing.T) {
	h := newTestHandler()
	tasks := h.tasks.(*mockTaskService)
	tasks.tasks = []service.Task{
		{ID: "t1", Status: service.StatusCompleted},
		{ID: "t2", Status: service.StatusNeedsAction},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/purge-completed", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204; body=%s", w.Code, w.Body.String())
	}
	// The mock's PurgeCompleted drops completed tasks from the slice.
	if len(tasks.tasks) != 1 || tasks.tasks[0].ID != "t2" {
		t.Errorf("expected only t2 to remain, got %+v", tasks.tasks)
	}
}

// TestAPIv1ListTasksFilters verifies the optional status + due_range filters
// and that unfiltered requests still return the full active list.
func TestAPIv1ListTasksFilters(t *testing.T) {
	h := newTestHandler()
	tasks := h.tasks.(*mockTaskService)
	dueSoon := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	dueLater := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	tasks.tasks = []service.Task{
		{ID: "t1", Title: "soon active", Status: service.StatusNeedsAction, DueAt: &dueSoon},
		{ID: "t2", Title: "later active", Status: service.StatusNeedsAction, DueAt: &dueLater},
		{ID: "t3", Title: "done", Status: service.StatusCompleted},
		{ID: "t4", Title: "no due", Status: service.StatusNeedsAction},
	}

	call := func(path string) []service.Task {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s -> %d body=%s", path, w.Code, w.Body.String())
		}
		var got []service.Task
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}

	t.Run("no_filter_returns_all", func(t *testing.T) {
		got := call("/api/v1/tasks")
		if len(got) != 4 {
			t.Errorf("want 4 tasks, got %d: %+v", len(got), got)
		}
	})

	t.Run("status_needs_action", func(t *testing.T) {
		got := call("/api/v1/tasks?status=needs_action")
		if len(got) != 3 {
			t.Errorf("want 3 needs_action tasks, got %d", len(got))
		}
		for _, g := range got {
			if g.Status != service.StatusNeedsAction {
				t.Errorf("unexpected status %q", g.Status)
			}
		}
	})

	t.Run("status_completed", func(t *testing.T) {
		got := call("/api/v1/tasks?status=completed")
		if len(got) != 1 || got[0].ID != "t3" {
			t.Errorf("want only t3, got %+v", got)
		}
	})

	t.Run("due_before_excludes_no_due", func(t *testing.T) {
		got := call("/api/v1/tasks?due_before=2026-06-01T00:00:00Z")
		// t1 is before; t2 is not; t3, t4 excluded (t4 has no due date).
		if len(got) != 1 || got[0].ID != "t1" {
			t.Errorf("want only t1, got %+v", got)
		}
	})

	t.Run("due_after_range", func(t *testing.T) {
		got := call("/api/v1/tasks?due_after=2026-06-01T00:00:00Z")
		if len(got) != 1 || got[0].ID != "t2" {
			t.Errorf("want only t2, got %+v", got)
		}
	})

	t.Run("invalid_status", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?status=bogus", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d, want 400", w.Code)
		}
	})

	t.Run("invalid_due_before", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks?due_before=nope", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d, want 400", w.Code)
		}
	})
}
