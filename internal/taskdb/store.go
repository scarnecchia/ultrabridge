package taskdb

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
)

// Store implements caldav.TaskStore against a local SQLite database.
type Store struct {
	db *sql.DB
}

// NewStore creates a new SQLite-backed task store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const taskColumns = `task_id, title, detail, status, importance, due_time,
	completed_time, last_modified, recurrence, is_reminder_on, links, is_deleted`

func (s *Store) List(ctx context.Context) ([]taskstore.Task, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE is_deleted = 'N'")
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []taskstore.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) Get(ctx context.Context, taskID string) (*taskstore.Task, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM tasks WHERE task_id = ? AND is_deleted = 'N'",
		taskID)
	t, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, taskstore.ErrNotFound
		}
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return &t, nil
}

func (s *Store) Create(ctx context.Context, t *taskstore.Task) error {
	now := time.Now().UnixMilli()
	if t.TaskID == "" {
		t.TaskID = taskstore.GenerateTaskID(taskstore.NullStr(t.Title), now)
	}
	if !t.CompletedTime.Valid {
		t.CompletedTime = sql.NullInt64{Int64: now, Valid: true}
	}
	if !t.LastModified.Valid {
		t.LastModified = sql.NullInt64{Int64: now, Valid: true}
	}
	if t.IsDeleted == "" {
		t.IsDeleted = "N"
	}
	if t.IsReminderOn == "" {
		t.IsReminderOn = "N"
	}
	if !t.Status.Valid {
		t.Status = sql.NullString{String: "needsAction", Valid: true}
	}

	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks
		(task_id, title, detail, status, importance, due_time,
		 completed_time, last_modified, recurrence, is_reminder_on,
		 links, is_deleted, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.CompletedTime, t.LastModified, t.Recurrence, t.IsReminderOn,
		t.Links, t.IsDeleted, now, now)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Store) Update(ctx context.Context, t *taskstore.Task) error {
	now := time.Now().UnixMilli()
	t.LastModified = sql.NullInt64{Int64: now, Valid: true}

	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET
		title = ?, detail = ?, status = ?, importance = ?, due_time = ?,
		completed_time = ?, last_modified = ?, recurrence = ?,
		is_reminder_on = ?, links = ?, updated_at = ?
		WHERE task_id = ?`,
		t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.CompletedTime, t.LastModified, t.Recurrence,
		t.IsReminderOn, t.Links, now,
		t.TaskID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.TaskID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update task %s rows affected: %w", t.TaskID, err)
	}
	if affected == 0 {
		return taskstore.ErrNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, taskID string) error {
	now := time.Now().UnixMilli()
	result, err := s.db.ExecContext(ctx, `UPDATE tasks SET
		is_deleted = 'Y', last_modified = ?, updated_at = ?
		WHERE task_id = ?`,
		now, now, taskID)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task %s rows affected: %w", taskID, err)
	}
	if affected == 0 {
		return taskstore.ErrNotFound
	}
	return nil
}

func (s *Store) MaxLastModified(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT MAX(last_modified) FROM tasks WHERE is_deleted = 'N'").Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("max last_modified: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (taskstore.Task, error) {
	var t taskstore.Task
	err := s.Scan(
		&t.TaskID, &t.Title, &t.Detail, &t.Status, &t.Importance,
		&t.DueTime, &t.CompletedTime, &t.LastModified, &t.Recurrence,
		&t.IsReminderOn, &t.Links, &t.IsDeleted,
	)
	return t, err
}

func scanTaskRow(row *sql.Row) (taskstore.Task, error) {
	return scanTask(row)
}
