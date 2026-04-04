package taskdb

import (
	"context"
	"database/sql"
	"fmt"
)

func migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			task_id        TEXT PRIMARY KEY,
			title          TEXT,
			detail         TEXT,
			status         TEXT NOT NULL DEFAULT 'needsAction',
			importance     TEXT,
			due_time       INTEGER NOT NULL DEFAULT 0,
			completed_time INTEGER NOT NULL DEFAULT 0,
			last_modified  INTEGER NOT NULL DEFAULT 0,
			recurrence     TEXT,
			is_reminder_on TEXT NOT NULL DEFAULT 'N',
			links          TEXT,
			is_deleted     TEXT NOT NULL DEFAULT 'N',
			ical_blob      TEXT,
			created_at     INTEGER NOT NULL,
			updated_at     INTEGER NOT NULL
		)`,
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration statement %d: %w", i, err)
		}
	}
	return nil
}
