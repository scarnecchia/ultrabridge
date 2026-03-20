package notedb

import (
	"context"
	"database/sql"
	"fmt"
)

// migrate creates all tables and indexes if they do not exist. Idempotent.
func migrate(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS notes (
			path         TEXT NOT NULL PRIMARY KEY,
			rel_path     TEXT NOT NULL,
			file_type    TEXT NOT NULL,
			size_bytes   INTEGER,
			mtime        INTEGER,
			sha256       TEXT,
			backup_path  TEXT,
			backed_up_at INTEGER,
			created_at   INTEGER NOT NULL,
			updated_at   INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id           INTEGER PRIMARY KEY,
			note_path    TEXT NOT NULL UNIQUE REFERENCES notes(path),
			status       TEXT NOT NULL,
			skip_reason  TEXT,
			ocr_source   TEXT,
			api_model    TEXT,
			attempts     INTEGER NOT NULL DEFAULT 0,
			last_error   TEXT,
			queued_at    INTEGER,
			started_at   INTEGER,
			finished_at  INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_note_path ON jobs(note_path)`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
		`CREATE TABLE IF NOT EXISTS note_content (
			id          INTEGER PRIMARY KEY,
			note_path   TEXT NOT NULL,
			page        INTEGER NOT NULL,
			title_text  TEXT,
			body_text   TEXT,
			keywords    TEXT,
			source      TEXT,
			model       TEXT,
			indexed_at  INTEGER NOT NULL,
			UNIQUE(note_path, page)
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS note_fts USING fts5(
			note_path, page UNINDEXED, title_text, body_text, keywords,
			content="note_content",
			content_rowid="id"
		)`,
		`CREATE TRIGGER IF NOT EXISTS note_content_ai
			AFTER INSERT ON note_content BEGIN
				INSERT INTO note_fts(rowid, note_path, page, title_text, body_text, keywords)
				VALUES (new.id, new.note_path, new.page, new.title_text, new.body_text, new.keywords);
			END`,
		`CREATE TRIGGER IF NOT EXISTS note_content_ad
			AFTER DELETE ON note_content BEGIN
				INSERT INTO note_fts(note_fts, rowid, note_path, page, title_text, body_text, keywords)
				VALUES ('delete', old.id, old.note_path, old.page, old.title_text, old.body_text, old.keywords);
			END`,
		`CREATE TRIGGER IF NOT EXISTS note_content_au
			AFTER UPDATE ON note_content BEGIN
				INSERT INTO note_fts(note_fts, rowid, note_path, page, title_text, body_text, keywords)
				VALUES ('delete', old.id, old.note_path, old.page, old.title_text, old.body_text, old.keywords);
				INSERT INTO note_fts(rowid, note_path, page, title_text, body_text, keywords)
				VALUES (new.id, new.note_path, new.page, new.title_text, new.body_text, new.keywords);
			END`,
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migration statement %d: %w", i, err)
		}
	}

	// Add requeue_after column to jobs table (idempotent — check first, then ALTER)
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name='requeue_after'`).Scan(&count)
	if count == 0 {
		if _, err := db.ExecContext(ctx, `ALTER TABLE jobs ADD COLUMN requeue_after INTEGER`); err != nil {
			return fmt.Errorf("add requeue_after column: %w", err)
		}
	}

	return nil
}
