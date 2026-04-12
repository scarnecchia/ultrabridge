package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

type mockTaskStore struct {
	tasks map[string]taskstore.Task
}

func (m *mockTaskStore) List(ctx context.Context) ([]taskstore.Task, error) {
	var list []taskstore.Task
	for _, t := range m.tasks {
		if t.IsDeleted == "N" {
			list = append(list, t)
		}
	}
	return list, nil
}

func (m *mockTaskStore) Get(ctx context.Context, taskID string) (*taskstore.Task, error) {
	t, ok := m.tasks[taskID]
	if !ok || t.IsDeleted == "Y" {
		return nil, sql.ErrNoRows
	}
	return &t, nil
}

func (m *mockTaskStore) Create(ctx context.Context, t *taskstore.Task) error {
	m.tasks[t.TaskID] = *t
	return nil
}

func (m *mockTaskStore) Update(ctx context.Context, t *taskstore.Task) error {
	m.tasks[t.TaskID] = *t
	return nil
}

func (m *mockTaskStore) Delete(ctx context.Context, taskID string) error {
	t, ok := m.tasks[taskID]
	if ok {
		t.IsDeleted = "Y"
		m.tasks[taskID] = t
	}
	return nil
}

func (m *mockTaskStore) DeleteCompleted(ctx context.Context) (int64, error) {
	var count int64
	for id, t := range m.tasks {
		if t.Status.String == "completed" && t.IsDeleted == "N" {
			t.IsDeleted = "Y"
			m.tasks[id] = t
			count++
		}
	}
	return count, nil
}

type mockNotifier struct {
	notified int
}

func (m *mockNotifier) Notify(ctx context.Context) error {
	m.notified++
	return nil
}

func TestTaskService_Create(t *testing.T) {
	store := &mockTaskStore{tasks: make(map[string]taskstore.Task)}
	notifier := &mockNotifier{}
	svc := NewTaskService(store, notifier)

	title := "Test Task"
	due := time.Now().Add(24 * time.Hour)
	task, err := svc.Create(context.Background(), title, &due)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if task.Title != title {
		t.Errorf("expected title %s, got %s", title, task.Title)
	}
	if task.Status != StatusNeedsAction {
		t.Errorf("expected status %s, got %s", StatusNeedsAction, task.Status)
	}
	if notifier.notified != 1 {
		t.Errorf("expected 1 notification, got %d", notifier.notified)
	}
}

func TestTaskService_Complete(t *testing.T) {
	store := &mockTaskStore{tasks: make(map[string]taskstore.Task)}
	notifier := &mockNotifier{}
	svc := NewTaskService(store, notifier)

	task, _ := svc.Create(context.Background(), "Task 1", nil)
	
	err := svc.Complete(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	updated, _ := svc.List(context.Background())
	if len(updated) != 1 || updated[0].Status != StatusCompleted {
		t.Errorf("expected status completed, got %v", updated[0].Status)
	}
	if updated[0].CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
	if notifier.notified != 2 { // 1 for create, 1 for complete
		t.Errorf("expected 2 notifications, got %d", notifier.notified)
	}
}

func TestTaskService_BulkActions(t *testing.T) {
	store := &mockTaskStore{tasks: make(map[string]taskstore.Task)}
	notifier := &mockNotifier{}
	svc := NewTaskService(store, notifier)

	t1, _ := svc.Create(context.Background(), "Task 1", nil)
	t2, _ := svc.Create(context.Background(), "Task 2", nil)
	t3, _ := svc.Create(context.Background(), "Task 3", nil)

	err := svc.BulkComplete(context.Background(), []string{t1.ID, t2.ID})
	if err != nil {
		t.Fatalf("BulkComplete failed: %v", err)
	}

	list, _ := svc.List(context.Background())
	completedCount := 0
	for _, tk := range list {
		if tk.Status == StatusCompleted {
			completedCount++
		}
	}
	if completedCount != 2 {
		t.Errorf("expected 2 completed tasks, got %d", completedCount)
	}

	err = svc.BulkDelete(context.Background(), []string{t1.ID, t3.ID})
	if err != nil {
		t.Fatalf("BulkDelete failed: %v", err)
	}

	list, _ = svc.List(context.Background())
	if len(list) != 1 {
		t.Errorf("expected 1 task remaining, got %d", len(list))
	}
	if list[0].ID != t2.ID {
		t.Errorf("expected remaining task to be t2, got %s", list[0].ID)
	}
}
