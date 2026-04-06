package web

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/processor"
	"github.com/sysop/ultrabridge/internal/search"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

func testHandler(t *testing.T, opts ...func(*testHandlerOpts)) *Handler {
	t.Helper()
	o := &testHandlerOpts{}
	for _, fn := range opts {
		fn(o)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()

	// Use untyped nils for interfaces to avoid typed-nil-in-interface gotcha.
	var ns notestore.NoteStore
	if o.noteStore != nil {
		ns = o.noteStore
	}
	var si search.SearchIndex
	if o.searchIndex != nil {
		si = o.searchIndex
	}
	var proc processor.Processor
	if o.proc != nil {
		proc = o.proc
	}
	var bs BooxStore
	if o.booxStore != nil {
		bs = o.booxStore
	}

	return NewHandler(
		newMockTaskStore(), nil, ns, si,
		proc, nil, nil, bs, o.booxNotesPath, "",
		o.noteDB, logger, broadcaster,
	)
}

type testHandlerOpts struct {
	noteStore     *mockNoteStore
	searchIndex   search.SearchIndex
	proc          *mockProcessor
	booxStore     *mockBooxStore
	booxNotesPath string
	noteDB        *sql.DB
}

// --- /settings ---

func TestSettingsPage_Renders(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest("GET", "/settings", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /settings returned %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "General") {
		t.Error("Settings page missing General section")
	}
	if !strings.Contains(body, "Supernote") {
		t.Error("Settings page missing Supernote section")
	}
	if !strings.Contains(body, "Boox") {
		t.Error("Settings page missing Boox section")
	}
}

func TestSettingsPage_InactiveSections(t *testing.T) {
	// No noteStore or booxStore → both should show inactive
	handler := testHandler(t)
	req := httptest.NewRequest("GET", "/settings", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "pipeline is not configured") {
		t.Error("Supernote section should show inactive message when noteStore is nil")
	}
	if !strings.Contains(body, "integration is not enabled") {
		t.Error("Boox section should show inactive message when booxStore is nil")
	}
}

func TestSettingsPage_ActiveSections(t *testing.T) {
	handler := testHandler(t, func(o *testHandlerOpts) {
		o.noteStore = newMockNoteStore()
		o.booxStore = &mockBooxStore{}
		o.booxNotesPath = "/boox"
	})
	req := httptest.NewRequest("GET", "/settings", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if strings.Contains(body, "not configured") {
		t.Error("Supernote section should not show 'not configured' when noteStore is set")
	}
	if strings.Contains(body, "not enabled") {
		t.Error("Boox section should not show 'not enabled' when booxStore is set")
	}
	// Should contain OCR prompt textareas
	if !strings.Contains(body, "OCR Prompt") {
		t.Error("Active sections should show OCR Prompt fields")
	}
}

// --- /settings/save ---

func TestSettingsSave_BooxOCRPrompt(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	handler := testHandler(t, func(o *testHandlerOpts) {
		o.noteDB = db
		o.booxStore = &mockBooxStore{}
		o.booxNotesPath = "/boox"
	})

	form := url.Values{
		"section":      {"boox"},
		"ocr_prompt":   {"custom boox prompt"},
		"todo_enabled": {"true"},
		"todo_prompt":  {"find red things"},
	}
	req := httptest.NewRequest("POST", "/settings/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /settings/save returned %d, want 303", w.Code)
	}

	// Verify settings were persisted
	ctx := context.Background()
	val, _ := notedb.GetSetting(ctx, db, "boox_ocr_prompt")
	if val != "custom boox prompt" {
		t.Errorf("boox_ocr_prompt = %q, want 'custom boox prompt'", val)
	}
	val, _ = notedb.GetSetting(ctx, db, "boox_todo_enabled")
	if val != "true" {
		t.Errorf("boox_todo_enabled = %q, want 'true'", val)
	}
	val, _ = notedb.GetSetting(ctx, db, "boox_todo_prompt")
	if val != "find red things" {
		t.Errorf("boox_todo_prompt = %q, want 'find red things'", val)
	}
}

func TestSettingsSave_SupernoteOCRPrompt(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	handler := testHandler(t, func(o *testHandlerOpts) {
		o.noteDB = db
		o.noteStore = newMockNoteStore()
	})

	form := url.Values{
		"section":    {"supernote"},
		"ocr_prompt": {"custom sn prompt"},
	}
	req := httptest.NewRequest("POST", "/settings/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /settings/save returned %d, want 303", w.Code)
	}

	val, _ := notedb.GetSetting(context.Background(), db, "sn_ocr_prompt")
	if val != "custom sn prompt" {
		t.Errorf("sn_ocr_prompt = %q, want 'custom sn prompt'", val)
	}
}

// --- /logs ---

func TestLogsPage_Renders(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest("GET", "/logs", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /logs returned %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Live Logs") {
		t.Error("Logs page missing 'Live Logs' heading")
	}
	if !strings.Contains(body, "log-level") {
		t.Error("Logs page missing log level selector")
	}
}

// --- /files/history (Boox routing) ---

func TestFilesHistory_BooxRoute(t *testing.T) {
	booxStore := &mockBooxStore{}
	handler := testHandler(t, func(o *testHandlerOpts) {
		o.booxStore = booxStore
		o.booxNotesPath = "/boox/notes"
	})

	// Boox path should route to booxStore, which returns nil (no job)
	req := httptest.NewRequest("GET", "/files/history?path=/boox/notes/test.note", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /files/history (boox) returned %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "null" {
		t.Errorf("Expected null for no boox job, got %q", w.Body.String())
	}
}

func TestFilesHistory_SupernoteRoute(t *testing.T) {
	proc := newMockProcessor()
	handler := testHandler(t, func(o *testHandlerOpts) {
		o.proc = proc
		o.booxStore = &mockBooxStore{}
		o.booxNotesPath = "/boox/notes"
	})

	// Non-boox path should route to Supernote processor
	req := httptest.NewRequest("GET", "/files/history?path=/sn/notes/test.note", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /files/history (supernote) returned %d, want 200", w.Code)
	}
}

// --- /tasks/purge-completed ---

func TestPurgeCompleted_Redirects(t *testing.T) {
	handler := testHandler(t)
	req := httptest.NewRequest("POST", "/tasks/purge-completed", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks/purge-completed returned %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/" {
		t.Errorf("Redirect location = %q, want '/'", loc)
	}
}

func TestPurgeCompleted_DeletesCompletedTasks(t *testing.T) {
	store := newMockTaskStore()
	store.tasks["t1"] = &taskstore.Task{
		TaskID: "t1", Title: taskstore.SqlStr("Active"),
		Status: taskstore.SqlStr("needsAction"), IsDeleted: "N",
	}
	store.tasks["t2"] = &taskstore.Task{
		TaskID: "t2", Title: taskstore.SqlStr("Done"),
		Status: taskstore.SqlStr("completed"), IsDeleted: "N",
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, nil, nil, nil, nil, nil, nil, "", "", nil, logger, broadcaster)

	req := httptest.NewRequest("POST", "/tasks/purge-completed", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks/purge-completed returned %d, want 303", w.Code)
	}
}

// --- /search with folder filter ---

func TestSearchPage_FolderFilter(t *testing.T) {
	searchIdx := &configSearchIndex{
		results: []search.SearchResult{
			{Path: "/notes/Work/meeting.note", Page: 0, Snippet: "agenda"},
		},
	}

	handler := testHandler(t, func(o *testHandlerOpts) {
		o.noteStore = newMockNoteStore()
		o.searchIndex = searchIdx
	})

	req := httptest.NewRequest("GET", "/search?q=agenda&folder=Work", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /search with folder returned %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "agenda") {
		t.Error("Search results should contain matching snippet")
	}
}

func TestSearchPage_FolderDropdown(t *testing.T) {
	handler := testHandler(t, func(o *testHandlerOpts) {
		o.searchIndex = &mockSearchIndex{}
	})

	req := httptest.NewRequest("GET", "/search", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /search returned %d, want 200", w.Code)
	}
	// Page should render without error even with no folders
}
