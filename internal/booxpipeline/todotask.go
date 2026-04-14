package booxpipeline

import (
	"context"
	"log/slog"
	"net/url"
	"path/filepath"

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
		// Detail format:
		//   "From Boox red ink in <basename>\nOpen: /files/boox?detail=<encoded>"
		//
		// Line 1 is human-readable (shows up in CalDAV clients as-is).
		// Line 2 is a relative URL that opens the Boox Files tab with the
		// note's details modal auto-opened. The _task_row template parses
		// this format to render a proper <a href> that navigates via HTMX
		// to /files/boox?detail=<path>. CalDAV clients which auto-linkify
		// URLs will also expose the second line as a clickable link.
		basename := filepath.Base(notePath)
		detailURL := "/files/boox?detail=" + url.QueryEscape(notePath)
		task := &taskstore.Task{
			Title: taskstore.SqlStr(todo.Text),
			Detail: taskstore.SqlStr(
				"From Boox red ink in " + basename + "\nOpen: " + detailURL,
			),
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
