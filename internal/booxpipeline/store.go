package booxpipeline

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store manages Boox notes and jobs in SQLite.
type Store struct {
	db *sql.DB
}

// NewStore creates a new Boox pipeline store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// BooxNote represents a Boox note in the database.
type BooxNote struct {
	Path        string
	NoteID      string
	Title       string
	DeviceModel string
	NoteType    string
	Folder      string
	PageCount   int
	FileHash    string
	Version     int
	CreatedAt   int64
	UpdatedAt   int64
}

// BooxJob represents a processing job in the queue.
type BooxJob struct {
	ID           int64
	NotePath     string
	Status       string
	SkipReason   string
	OCRSource    string
	APIModel     string
	Attempts     int
	LastError    string
	QueuedAt     int64
	StartedAt    int64
	FinishedAt   int64
	RequeueAfter *int64
}

// UpsertNote inserts or updates a boox_notes row, incrementing version on conflict.
func (s *Store) UpsertNote(ctx context.Context, path, noteID, title, deviceModel, noteType, folder string, pageCount int, fileHash string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO boox_notes (path, note_id, title, device_model, note_type, folder, page_count, file_hash, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			note_id = excluded.note_id,
			title = excluded.title,
			device_model = excluded.device_model,
			note_type = excluded.note_type,
			folder = excluded.folder,
			page_count = excluded.page_count,
			file_hash = excluded.file_hash,
			version = version + 1,
			updated_at = excluded.updated_at`,
		path, noteID, title, deviceModel, noteType, folder, pageCount, fileHash, now, now,
	)
	return err
}

// EnqueueJob adds a job to the queue, first ensuring a boox_notes row exists with minimal defaults.
func (s *Store) EnqueueJob(ctx context.Context, notePath string) error {
	// First ensure a boox_notes row exists (INSERT OR IGNORE with minimal defaults).
	// This satisfies the FK constraint for the WebDAV callback before the worker
	// has parsed the note metadata.
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO boox_notes (path, note_id, title, device_model, note_type, folder, page_count, file_hash, version, created_at, updated_at)
		VALUES (?, '', '', '', '', '', 0, '', 1, ?, ?)`,
		notePath, time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("ensure note row: %w", err)
	}

	// Now insert the job.
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO boox_jobs (note_path, status, queued_at)
		VALUES (?, 'pending', ?)`,
		notePath, time.Now().UnixMilli(),
	)
	if err != nil {
		return fmt.Errorf("enqueue job: %w", err)
	}
	return nil
}

// ClaimNextJob atomically claims the oldest pending job using SQLite RETURNING.
// Returns nil, nil if no jobs are available.
func (s *Store) ClaimNextJob(ctx context.Context) (*BooxJob, error) {
	now := time.Now().Unix()
	var job BooxJob
	err := s.db.QueryRowContext(ctx, `
		UPDATE boox_jobs SET status = 'in_progress', started_at = ?
		WHERE id = (SELECT id FROM boox_jobs WHERE status = 'pending'
			AND (requeue_after IS NULL OR requeue_after <= ?)
			ORDER BY queued_at ASC LIMIT 1)
		RETURNING id, note_path, status, attempts, last_error, queued_at, started_at`,
		now, now,
	).Scan(&job.ID, &job.NotePath, &job.Status, &job.Attempts, &job.LastError, &job.QueuedAt, &job.StartedAt)
	if err == sql.ErrNoRows {
		return nil, nil // no jobs available
	}
	if err != nil {
		return nil, fmt.Errorf("claim boox job: %w", err)
	}
	return &job, nil
}

// CompleteJob marks a job as done with optional OCR source and API model.
func (s *Store) CompleteJob(ctx context.Context, jobID int64, ocrSource, apiModel string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE boox_jobs SET status = 'done', ocr_source = ?, api_model = ?, finished_at = ?
		WHERE id = ?`,
		ocrSource, apiModel, now, jobID,
	)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return nil
}

// FailJob marks a job as failed with an error message.
func (s *Store) FailJob(ctx context.Context, jobID int64, errMsg string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		UPDATE boox_jobs SET status = 'failed', last_error = ?, finished_at = ?
		WHERE id = ?`,
		errMsg, now, jobID,
	)
	if err != nil {
		return fmt.Errorf("fail job: %w", err)
	}
	return nil
}

// GetNote retrieves a boox_notes row by path.
func (s *Store) GetNote(ctx context.Context, path string) (*BooxNote, error) {
	var note BooxNote
	err := s.db.QueryRowContext(ctx, `
		SELECT path, note_id, title, device_model, note_type, folder, page_count, file_hash, version, created_at, updated_at
		FROM boox_notes WHERE path = ?`,
		path,
	).Scan(&note.Path, &note.NoteID, &note.Title, &note.DeviceModel, &note.NoteType, &note.Folder, &note.PageCount, &note.FileHash, &note.Version, &note.CreatedAt, &note.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note: %w", err)
	}
	return &note, nil
}

// ReclaimStuckJobs reclaims jobs that have been in_progress for longer than the timeout.
// Sets status back to pending and increments the attempts counter.
func (s *Store) ReclaimStuckJobs(ctx context.Context, timeout time.Duration) error {
	cutoff := time.Now().Add(-timeout).Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE boox_jobs SET status = 'pending', attempts = attempts + 1
		WHERE status = 'in_progress' AND started_at < ?`,
		cutoff,
	)
	if err != nil {
		return fmt.Errorf("reclaim stuck jobs: %w", err)
	}
	return nil
}
