package booxpipeline

import (
	"context"
	"log/slog"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

// TaskCreator abstracts the subset of caldav.TaskStore needed for todo creation.
type TaskCreator interface {
	List(ctx context.Context) ([]taskstore.Task, error)
	Create(ctx context.Context, t *taskstore.Task) error
}

// CreateTasksFromTodos creates CalDAV tasks from extracted red ink todos,
// deduplicating against existing tasks by title (both incomplete and completed).
func CreateTasksFromTodos(ctx context.Context, tc TaskCreator, notePath string, todos []TodoItem, logger *slog.Logger) int {
	existing, err := tc.List(ctx)
	if err != nil {
		logger.Error("todo: list tasks for dedup", "error", err)
		return 0
	}
	titleSet := make(map[string]bool, len(existing))
	for _, t := range existing {
		if t.Title.Valid {
			titleSet[t.Title.String] = true
		}
	}

	created := 0
	for _, todo := range todos {
		if titleSet[todo.Text] {
			logger.Info("todo: skipping duplicate", "text", todo.Text)
			continue
		}
		task := &taskstore.Task{
			Title:  taskstore.SqlStr(todo.Text),
			Detail: taskstore.SqlStr("From Boox red ink: " + notePath),
		}
		if err := tc.Create(ctx, task); err != nil {
			logger.Error("todo: create task", "text", todo.Text, "error", err)
		} else {
			logger.Info("todo: created task from red ink", "text", todo.Text, "task_id", task.TaskID)
			titleSet[todo.Text] = true
			created++
		}
	}
	return created
}
