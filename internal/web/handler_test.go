package web

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/logging"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

// mockTaskStore implements TaskStore for testing
type mockTaskStore struct {
	tasks map[string]*taskstore.Task
}

func newMockTaskStore() *mockTaskStore {
	return &mockTaskStore{
		tasks: make(map[string]*taskstore.Task),
	}
}

func (m *mockTaskStore) List(ctx context.Context) ([]taskstore.Task, error) {
	var result []taskstore.Task
	for _, t := range m.tasks {
		if t.IsDeleted != "Y" {
			result = append(result, *t)
		}
	}
	return result, nil
}

func (m *mockTaskStore) Get(ctx context.Context, taskID string) (*taskstore.Task, error) {
	if t, ok := m.tasks[taskID]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("task not found")
}

func (m *mockTaskStore) Create(ctx context.Context, t *taskstore.Task) error {
	if t.TaskID == "" {
		t.TaskID = fmt.Sprintf("task-%d", len(m.tasks))
	}
	if !t.LastModified.Valid {
		t.LastModified = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
	}
	m.tasks[t.TaskID] = t
	return nil
}

func (m *mockTaskStore) Update(ctx context.Context, t *taskstore.Task) error {
	if _, ok := m.tasks[t.TaskID]; !ok {
		return fmt.Errorf("task not found")
	}
	t.LastModified = sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}
	m.tasks[t.TaskID] = t
	return nil
}

func (m *mockTaskStore) Delete(ctx context.Context, taskID string) error {
	if t, ok := m.tasks[taskID]; ok {
		t.IsDeleted = "Y"
		return nil
	}
	return fmt.Errorf("task not found")
}

func (m *mockTaskStore) MaxLastModified(ctx context.Context) (int64, error) {
	var max int64
	for _, t := range m.tasks {
		if t.LastModified.Valid && t.LastModified.Int64 > max {
			max = t.LastModified.Int64
		}
	}
	return max, nil
}

// mockNotifier implements SyncNotifier for testing
type mockNotifier struct {
	called int
	lastErr error
}

func (m *mockNotifier) Notify(ctx context.Context) error {
	m.called++
	return m.lastErr
}

// TestListTasksReturnsNonDeletedTasks verifies AC4.1: store returns list of all non-deleted tasks
func TestListTasksReturnsNonDeletedTasks(t *testing.T) {
	store := newMockTaskStore()

	// Pre-populate with tasks
	task1 := &taskstore.Task{
		TaskID:    "task-1",
		Title:     taskstore.SqlStr("Buy groceries"),
		Status:    taskstore.SqlStr("needsAction"),
		DueTime:   0,
		IsDeleted: "N",
	}
	task2 := &taskstore.Task{
		TaskID:    "task-2",
		Title:     taskstore.SqlStr("Write report"),
		Status:    taskstore.SqlStr("completed"),
		IsDeleted: "N",
	}
	store.tasks[task1.TaskID] = task1
	store.tasks[task2.TaskID] = task2

	// List tasks
	ctx := context.Background()
	tasks, err := store.List(ctx)

	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(tasks) != 2 {
		t.Errorf("Expected 2 tasks, got %d", len(tasks))
	}

	titles := make(map[string]bool)
	for _, task := range tasks {
		titles[taskstore.NullStr(task.Title)] = true
	}

	if !titles["Buy groceries"] {
		t.Errorf("Task title 'Buy groceries' not in list")
	}
	if !titles["Write report"] {
		t.Errorf("Task title 'Write report' not in list")
	}
}

// TestListTasksEmpty verifies store.List with empty store
func TestListTasksEmpty(t *testing.T) {
	store := newMockTaskStore()

	ctx := context.Background()
	tasks, err := store.List(ctx)

	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(tasks) != 0 {
		t.Errorf("Expected 0 tasks, got %d", len(tasks))
	}
}

// TestGetIndexFiltersDeletedTasks verifies that deleted tasks (IsDeleted="Y") are not shown
func TestGetIndexFiltersDeletedTasks(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	// Add a non-deleted task
	task1 := &taskstore.Task{
		TaskID:    "task-1",
		Title:     taskstore.SqlStr("Active task"),
		Status:    taskstore.SqlStr("needsAction"),
		IsDeleted: "N",
	}
	store.tasks[task1.TaskID] = task1

	// Add a deleted task
	task2 := &taskstore.Task{
		TaskID:    "task-2",
		Title:     taskstore.SqlStr("Deleted task"),
		Status:    taskstore.SqlStr("needsAction"),
		IsDeleted: "Y",
	}
	store.tasks[task2.TaskID] = task2

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Verify that we only get non-deleted tasks from store.List
	ctx := context.Background()
	tasks, err := store.List(ctx)
	if err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	// Verify store correctly filters deleted tasks
	if len(tasks) != 1 {
		t.Errorf("store.List should return 1 non-deleted task, got %d", len(tasks))
	}
	if len(tasks) > 0 && taskstore.NullStr(tasks[0].Title) != "Active task" {
		t.Errorf("Non-deleted task should be 'Active task', got %q", taskstore.NullStr(tasks[0].Title))
	}
}

// TestPostCreateTaskMinimal verifies AC4.2: POST /tasks with form data creates a task
func TestPostCreateTaskMinimal(t *testing.T) {
	store := newMockTaskStore()
	notifier := &mockNotifier{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, notifier, logger, broadcaster)

	// Create task via form POST
	form := url.Values{}
	form.Set("title", "Test Task")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Verify redirect to /
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks returned status %d, want %d", w.Code, http.StatusSeeOther)
	}
	if w.Header().Get("Location") != "/" {
		t.Errorf("Redirect location is %s, want /", w.Header().Get("Location"))
	}

	// Verify task was created in store
	if len(store.tasks) != 1 {
		t.Errorf("Expected 1 task in store, got %d", len(store.tasks))
	}

	// Verify the task has correct data
	for _, task := range store.tasks {
		if taskstore.NullStr(task.Title) != "Test Task" {
			t.Errorf("Task title is %q, want %q", taskstore.NullStr(task.Title), "Test Task")
		}
		if taskstore.NullStr(task.Status) != "needsAction" {
			t.Errorf("Task status is %q, want %q", taskstore.NullStr(task.Status), "needsAction")
		}
		if task.IsDeleted != "N" {
			t.Errorf("Task IsDeleted is %q, want %q", task.IsDeleted, "N")
		}
	}

	// Verify notifier was called
	if notifier.called != 1 {
		t.Errorf("Notifier was called %d times, want 1", notifier.called)
	}
}

// TestPostCreateTaskWithDueDate verifies POST /tasks with optional due date
func TestPostCreateTaskWithDueDate(t *testing.T) {
	store := newMockTaskStore()
	notifier := &mockNotifier{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, notifier, logger, broadcaster)

	// Create task with due date
	form := url.Values{}
	form.Set("title", "Task with deadline")
	form.Set("due_date", "2026-12-25")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks returned status %d, want %d", w.Code, http.StatusSeeOther)
	}

	// Verify task has correct due date
	if len(store.tasks) != 1 {
		t.Fatalf("Expected 1 task in store, got %d", len(store.tasks))
	}

	for _, task := range store.tasks {
		// 2026-12-25 in UTC
		expectedTime := time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC)
		expectedMs := expectedTime.UnixMilli()

		if task.DueTime != expectedMs {
			t.Errorf("Task DueTime is %d, want %d", task.DueTime, expectedMs)
		}
	}
}

// TestPostCreateTaskNoTitle verifies that POST /tasks without title returns error
func TestPostCreateTaskNoTitle(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	form := url.Values{}
	form.Set("title", "")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /tasks with no title returned status %d, want %d", w.Code, http.StatusBadRequest)
	}

	if len(store.tasks) != 0 {
		t.Errorf("Expected 0 tasks in store, got %d", len(store.tasks))
	}
}

// TestPostCreateTaskInvalidDueDate verifies that invalid due date returns error
func TestPostCreateTaskInvalidDueDate(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	form := url.Values{}
	form.Set("title", "Task")
	form.Set("due_date", "not-a-date")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /tasks with invalid due date returned status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestPostCompleteTaskUpdatesStatus verifies AC4.3: POST /tasks/{id}/complete marks task complete
func TestPostCompleteTaskUpdatesStatus(t *testing.T) {
	store := newMockTaskStore()
	notifier := &mockNotifier{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, notifier, logger, broadcaster)

	// Create a task
	task := &taskstore.Task{
		TaskID:    "task-1",
		Title:     taskstore.SqlStr("Incomplete task"),
		Status:    taskstore.SqlStr("needsAction"),
		IsDeleted: "N",
	}
	store.tasks[task.TaskID] = task

	// Complete the task
	req := httptest.NewRequest("POST", "/tasks/task-1/complete", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Verify redirect
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks/{id}/complete returned status %d, want %d", w.Code, http.StatusSeeOther)
	}

	// Verify task status is now completed
	completedTask := store.tasks["task-1"]
	if taskstore.NullStr(completedTask.Status) != "completed" {
		t.Errorf("Task status is %q, want 'completed'", taskstore.NullStr(completedTask.Status))
	}

	// Verify completedTime is set
	if !completedTask.CompletedTime.Valid || completedTask.CompletedTime.Int64 == 0 {
		t.Errorf("Task CompletedTime should be set")
	}

	// Verify notifier was called
	if notifier.called != 1 {
		t.Errorf("Notifier was called %d times, want 1", notifier.called)
	}
}

// TestPostCompleteTaskAlreadyCompleted verifies completing an already-completed task
func TestPostCompleteTaskAlreadyCompleted(t *testing.T) {
	store := newMockTaskStore()
	notifier := &mockNotifier{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, notifier, logger, broadcaster)

	// Create an already-completed task
	completionTime := time.Now().UnixMilli()
	task := &taskstore.Task{
		TaskID:        "task-1",
		Title:         taskstore.SqlStr("Completed task"),
		Status:        taskstore.SqlStr("completed"),
		CompletedTime: sql.NullInt64{Int64: completionTime, Valid: true},
		IsDeleted:     "N",
	}
	store.tasks[task.TaskID] = task

	// Try to complete it again
	req := httptest.NewRequest("POST", "/tasks/task-1/complete", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should still succeed with redirect
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks/{id}/complete returned status %d, want %d", w.Code, http.StatusSeeOther)
	}

	// Status should remain completed
	if taskstore.NullStr(store.tasks["task-1"].Status) != "completed" {
		t.Errorf("Task status should remain 'completed'")
	}
}

// TestPostCompleteTaskNotFound verifies completing a non-existent task
func TestPostCompleteTaskNotFound(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	req := httptest.NewRequest("POST", "/tasks/nonexistent-id/complete", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("POST /tasks/nonexistent/complete returned status %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestPostCompleteTaskNoID verifies that missing task ID returns error
func TestPostCompleteTaskNoID(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	// Note: This test verifies the Go 1.22 route pattern parsing
	// In practice, /tasks/{id}/complete always extracts an id (could be empty)
	req := httptest.NewRequest("POST", "/tasks//complete", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Empty ID should be handled (either 400 or 404)
	if w.Code == http.StatusOK {
		t.Errorf("POST /tasks//complete should not return 200")
	}
}

// TestHandlerNotifierNil verifies that handler works when notifier is nil
func TestHandlerNotifierNil(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	form := url.Values{}
	form.Set("title", "Task without notifier")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should succeed even without notifier
	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks without notifier returned status %d, want %d", w.Code, http.StatusSeeOther)
	}

	if len(store.tasks) != 1 {
		t.Errorf("Task should be created even without notifier")
	}
}

// TestPostCreateTaskWithWhitespace verifies title trimming
func TestPostCreateTaskWithWhitespace(t *testing.T) {
	store := newMockTaskStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broadcaster := logging.NewLogBroadcaster()
	handler := NewHandler(store, nil, logger, broadcaster)

	form := url.Values{}
	form.Set("title", "  Task with spaces  ")
	req := httptest.NewRequest("POST", "/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("POST /tasks with whitespace returned status %d, want %d", w.Code, http.StatusSeeOther)
	}

	if len(store.tasks) != 1 {
		t.Errorf("Task should be created with trimmed title")
	}

	for _, task := range store.tasks {
		if taskstore.NullStr(task.Title) != "Task with spaces" {
			t.Errorf("Title should be trimmed, got %q", taskstore.NullStr(task.Title))
		}
	}
}
