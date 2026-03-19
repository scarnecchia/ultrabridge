package processor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
)

func openTestProcessor(t *testing.T) *Store {
	t.Helper()
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

// seedNotesRow inserts a minimal notes row so the jobs FK constraint is satisfied.
func seedNotesRow(t *testing.T, s *Store, path string) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO notes (path, rel_path, file_type, size_bytes, mtime, created_at, updated_at)
		 VALUES (?, ?, 'note', 0, 0, 0, 0)`, path, filepath.Base(path))
	if err != nil {
		t.Fatalf("seedNotesRow %s: %v", path, err)
	}
}

// AC2.1: Not running by default
func TestProcessor_NotRunningByDefault(t *testing.T) {
	s := openTestProcessor(t)
	if s.Status().Running {
		t.Error("processor should not be running by default")
	}
}

// AC2.2 + AC2.3: Start/Stop lifecycle, stop waits for goroutine
func TestProcessor_StartStop(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.Status().Running {
		t.Error("expected running after Start")
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.Status().Running {
		t.Error("expected stopped after Stop")
	}
}

// AC2.3: Stop is graceful
func TestProcessor_StopGraceful(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	s.Start(ctx)
	seedNotesRow(t, s, "/fake/path.note")
	s.Enqueue(ctx, "/fake/path.note")
	time.Sleep(50 * time.Millisecond)
	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// AC2.4: Pending jobs visible after create (SQLite persistence)
func TestProcessor_PendingJobsPersist(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	seedNotesRow(t, s, "/persistent.note")
	if err := s.Enqueue(ctx, "/persistent.note"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Jobs are in SQLite, visible via Status without restarting
	st := s.Status()
	if st.Pending == 0 {
		t.Error("expected pending job to be visible")
	}
}

// AC2.5: Status reports running and queue depth
func TestProcessor_StatusReportsDepth(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	seedNotesRow(t, s, "/a.note")
	seedNotesRow(t, s, "/b.note")
	s.Enqueue(ctx, "/a.note")
	s.Enqueue(ctx, "/b.note")
	st := s.Status()
	if st.Pending != 2 {
		t.Errorf("pending = %d, want 2", st.Pending)
	}
}

func TestSkipUnskip(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	seedNotesRow(t, s, "/test.note")
	s.Enqueue(ctx, "/test.note")
	s.Skip(ctx, "/test.note", SkipReasonManual)

	var status, reason string
	s.db.QueryRowContext(ctx,
		"SELECT status, skip_reason FROM jobs WHERE note_path=?", "/test.note").
		Scan(&status, &reason)
	if status != StatusSkipped {
		t.Errorf("status = %q, want skipped", status)
	}
	if reason != SkipReasonManual {
		t.Errorf("skip_reason = %q, want manual", reason)
	}

	s.Unskip(ctx, "/test.note")
	s.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE note_path=?", "/test.note").Scan(&status)
	if status != StatusPending {
		t.Errorf("after unskip status = %q, want pending", status)
	}
}

// AC4.6: Watchdog reclaims stuck in_progress jobs
func TestWatchdog_ReclaimsStuckJobs(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()

	// Seed the notes table (FK constraint)
	s.db.ExecContext(ctx, `
		INSERT INTO notes (path, rel_path, file_type, size_bytes, mtime, created_at, updated_at)
		VALUES ('/stuck.note', 'stuck.note', 'note', 0, 0, 0, 0)`)

	// Insert a job that is in_progress with a stale started_at
	oldTime := time.Now().Add(-20 * time.Minute).Unix()
	s.db.ExecContext(ctx,
		"INSERT INTO jobs (note_path, status, started_at, queued_at) VALUES (?, ?, ?, ?)",
		"/stuck.note", StatusInProgress, oldTime, oldTime,
	)

	s.reclaimStuck(ctx)

	var status string
	s.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE note_path=?", "/stuck.note").Scan(&status)
	if status != StatusPending {
		t.Errorf("after watchdog status = %q, want pending", status)
	}
}
