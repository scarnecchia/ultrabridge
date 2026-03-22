package processor

import (
	"context"
	"os"
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
	return New(db, WorkerConfig{}) // WorkerConfig{} = no OCR, no backup
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

	// Copy test data file so executeJob can process it
	src := filepath.Join("../../testdata", "20260318_154108 std one line.note")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("test file not found: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "test.note")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	seedNotesRow(t, s, tmpFile)
	s.Enqueue(ctx, tmpFile)

	// Wait for the job to be claimed and start processing (with 7-second timeout).
	// The poll interval in run() is 5 seconds, so we need to wait long enough for
	// at least one iteration to claim the job.
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := s.GetJob(ctx, tmpFile)
		if j != nil && j.Status != StatusPending {
			// Job has been claimed, allow a brief moment for processJob to complete
			time.Sleep(50 * time.Millisecond)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := s.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}

	// Verify the job completed before shutdown.
	// After Stop() returns, the run() goroutine has exited, so all pending work
	// should be complete. The job should be marked done.
	j, err := s.GetJob(ctx, tmpFile)
	if err != nil {
		t.Errorf("GetJob: %v", err)
	}
	if j == nil {
		t.Error("expected job to exist after Stop")
	} else if j.Status != StatusDone {
		t.Errorf("job status = %q, want done (graceful stop should complete in-flight jobs)", j.Status)
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

// AC6.2: claimNext skips jobs with future requeue_after
func TestClaimNext_SkipsFutureRequeueAfter(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()

	// Seed the notes table (FK constraint)
	seedNotesRow(t, s, "/future.note")

	// Insert a pending job with requeue_after set 1 hour in the future
	futureTime := time.Now().Add(1 * time.Hour).Unix()
	s.db.ExecContext(ctx,
		"INSERT INTO jobs (note_path, status, queued_at, requeue_after) VALUES (?, ?, ?, ?)",
		"/future.note", StatusPending, time.Now().Unix(), futureTime,
	)

	// Attempt to claim the job
	job, err := s.claimNext(ctx)

	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if job != nil {
		t.Errorf("expected no job to be claimed (future requeue_after), but got job %d", job.ID)
	}

	// Verify the job is still pending
	var status string
	s.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE note_path=?", "/future.note").Scan(&status)
	if status != StatusPending {
		t.Errorf("job status = %q, want pending", status)
	}
}

// AC6.3: claimNext picks up jobs with past requeue_after
func TestClaimNext_ClaimsPastRequeueAfter(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()

	// Seed the notes table (FK constraint)
	seedNotesRow(t, s, "/past.note")

	// Insert a pending job with requeue_after set to a past time (10 minutes ago)
	pastTime := time.Now().Add(-10 * time.Minute).Unix()
	s.db.ExecContext(ctx,
		"INSERT INTO jobs (note_path, status, queued_at, requeue_after) VALUES (?, ?, ?, ?)",
		"/past.note", StatusPending, time.Now().Unix(), pastTime,
	)

	// Claim the job
	job, err := s.claimNext(ctx)

	if err != nil {
		t.Fatalf("claimNext: %v", err)
	}
	if job == nil {
		t.Error("expected a job to be claimed (past requeue_after), but got nil")
	}

	// Verify the job is now in_progress
	var status string
	s.db.QueryRowContext(ctx, "SELECT status FROM jobs WHERE note_path=?", "/past.note").Scan(&status)
	if status != StatusInProgress {
		t.Errorf("job status = %q, want in_progress", status)
	}
}

// AC2.1: Enqueue with no options sets requeue_after to NULL
func TestEnqueue_NoOptions_RequeueAfterNull(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	seedNotesRow(t, s, "/test.note")

	if err := s.Enqueue(ctx, "/test.note"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var requeueAfter interface{}
	err := s.db.QueryRowContext(ctx, "SELECT requeue_after FROM jobs WHERE note_path=?", "/test.note").Scan(&requeueAfter)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if requeueAfter != nil {
		t.Errorf("requeue_after = %v, want NULL", requeueAfter)
	}
}

// AC2.2: Enqueue with WithRequeueAfter sets requeue_after to now+delay
func TestEnqueue_WithRequeueAfter_SetsFutureTime(t *testing.T) {
	s := openTestProcessor(t)
	ctx := context.Background()
	seedNotesRow(t, s, "/test.note")

	delay := 30 * time.Second
	beforeEnqueue := time.Now().Unix()
	if err := s.Enqueue(ctx, "/test.note", WithRequeueAfter(delay)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	afterEnqueue := time.Now().Unix()

	var requeueAfterUnix int64
	err := s.db.QueryRowContext(ctx, "SELECT requeue_after FROM jobs WHERE note_path=?", "/test.note").Scan(&requeueAfterUnix)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	expectedMin := beforeEnqueue + int64(delay.Seconds())
	expectedMax := afterEnqueue + int64(delay.Seconds()) + 2 // 2-second tolerance

	if requeueAfterUnix < expectedMin || requeueAfterUnix > expectedMax {
		t.Errorf("requeue_after = %d, expected between %d and %d", requeueAfterUnix, expectedMin, expectedMax)
	}
}
