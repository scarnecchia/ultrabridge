package taskstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Store struct {
	db     *sql.DB
	userID int64
}

func New(db *sql.DB, userID int64) *Store {
	return &Store{db: db, userID: userID}
}

func (s *Store) UserID() int64 { return s.userID }

const taskColumns = `task_id, task_list_id, user_id, title, detail, last_modified,
	recurrence, is_reminder_on, status, importance, due_time, completed_time,
	links, is_deleted`

func (s *Store) List(ctx context.Context) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+taskColumns+" FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'",
		s.userID)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) Get(ctx context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+taskColumns+" FROM t_schedule_task WHERE task_id = ? AND user_id = ? AND is_deleted = 'N'",
		taskID, s.userID)
	var t Task
	if err := scanTaskRow(row, &t); err != nil {
		return nil, fmt.Errorf("get task %s: %w", taskID, err)
	}
	return &t, nil
}

func (s *Store) Create(ctx context.Context, t *Task) error {
	now := time.Now().UnixMilli()
	t.UserID = s.userID
	if t.TaskID == "" {
		t.TaskID = GenerateTaskID(NullStr(t.Title), now)
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

	_, err := s.db.ExecContext(ctx, `INSERT INTO t_schedule_task
		(task_id, task_list_id, user_id, title, detail, last_modified,
		 recurrence, is_reminder_on, status, importance, due_time, completed_time,
		 links, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, t.TaskListID, t.UserID, t.Title, t.Detail, t.LastModified,
		t.Recurrence, t.IsReminderOn, t.Status, t.Importance, t.DueTime, t.CompletedTime,
		t.Links, t.IsDeleted)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

func (s *Store) Update(ctx context.Context, t *Task) error {
	now := time.Now().UnixMilli()
	t.LastModified = sql.NullInt64{Int64: now, Valid: true}

	_, err := s.db.ExecContext(ctx, `UPDATE t_schedule_task SET
		title = ?, detail = ?, status = ?, importance = ?, due_time = ?,
		last_modified = ?, recurrence = ?
		WHERE task_id = ? AND user_id = ?`,
		t.Title, t.Detail, t.Status, t.Importance, t.DueTime,
		t.LastModified, t.Recurrence,
		t.TaskID, s.userID)
	if err != nil {
		return fmt.Errorf("update task %s: %w", t.TaskID, err)
	}
	return nil
}

// MaxLastModified returns the maximum last_modified value across all non-deleted tasks.
// Used for CTag computation without loading all tasks into memory.
func (s *Store) MaxLastModified(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		"SELECT MAX(last_modified) FROM t_schedule_task WHERE user_id = ? AND is_deleted = 'N'",
		s.userID).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("max last_modified: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

func (s *Store) Delete(ctx context.Context, taskID string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE t_schedule_task SET
		is_deleted = 'Y', last_modified = ?
		WHERE task_id = ? AND user_id = ?`,
		now, taskID, s.userID)
	if err != nil {
		return fmt.Errorf("delete task %s: %w", taskID, err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner, t *Task) error {
	return s.Scan(
		&t.TaskID, &t.TaskListID, &t.UserID, &t.Title, &t.Detail,
		&t.LastModified, &t.Recurrence, &t.IsReminderOn, &t.Status,
		&t.Importance, &t.DueTime, &t.CompletedTime, &t.Links, &t.IsDeleted,
	)
}

func scanTaskRow(row *sql.Row, t *Task) error {
	return scanTask(row, t)
}
