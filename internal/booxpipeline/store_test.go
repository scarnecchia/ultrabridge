package booxpipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db)
}

// boox-notes-pipeline.AC4.1: TestEnqueueJob verifies that a job is enqueued with pending status
func TestEnqueueJob(t *testing.T) {
	s := openTestStore(t)
	notePath := "/tmp/test.note"

	err := s.EnqueueJob(context.Background(), notePath)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Verify boox_notes row was created
	note, err := s.GetNote(context.Background(), notePath)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if note == nil {
		t.Fatal("expected note row to exist after EnqueueJob")
	}
	if note.Path != notePath {
		t.Errorf("note.Path = %q, want %q", note.Path, notePath)
	}

	// Query boox_jobs to verify status
	var status string
	err = s.db.QueryRowContext(context.Background(), "SELECT status FROM boox_jobs WHERE note_path = ?", notePath).Scan(&status)
	if err != nil {
		t.Fatalf("query job status: %v", err)
	}
	if status != "pending" {
		t.Errorf("job status = %q, want pending", status)
	}
}

// TestClaimNextJob_Atomic verifies atomic claiming of multiple jobs in order
func TestClaimNextJob_Atomic(t *testing.T) {
	s := openTestStore(t)

	// Enqueue two jobs
	path1 := "/tmp/test1.note"
	path2 := "/tmp/test2.note"
	if err := s.EnqueueJob(context.Background(), path1); err != nil {
		t.Fatalf("EnqueueJob path1: %v", err)
	}
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps

	if err := s.EnqueueJob(context.Background(), path2); err != nil {
		t.Fatalf("EnqueueJob path2: %v", err)
	}

	// Claim first job
	job1, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob job1: %v", err)
	}
	if job1 == nil {
		t.Fatal("expected job1 to be claimed")
	}
	if job1.NotePath != path1 {
		t.Errorf("job1.NotePath = %q, want %q", job1.NotePath, path1)
	}
	if job1.Status != "in_progress" {
		t.Errorf("job1.Status = %q, want in_progress", job1.Status)
	}

	// Claim second job
	job2, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob job2: %v", err)
	}
	if job2 == nil {
		t.Fatal("expected job2 to be claimed")
	}
	if job2.NotePath != path2 {
		t.Errorf("job2.NotePath = %q, want %q", job2.NotePath, path2)
	}

	// No more jobs available
	job3, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob job3: %v", err)
	}
	if job3 != nil {
		t.Errorf("expected no more jobs, but got job %d", job3.ID)
	}
}

// TestClaimNextJob_Empty verifies that no job is returned when queue is empty
func TestClaimNextJob_Empty(t *testing.T) {
	s := openTestStore(t)

	job, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if job != nil {
		t.Errorf("expected no jobs, but got job %d", job.ID)
	}
}

// boox-notes-pipeline.AC4.4: TestUpsertNote_VersionIncrement verifies version increments on re-upsert
func TestUpsertNote_VersionIncrement(t *testing.T) {
	s := openTestStore(t)
	path := "/tmp/test.note"

	// First upsert
	err := s.UpsertNote(context.Background(), path, "note-1", "Test Title", "device1", "Notebooks", "folder1", 5, "hash1", 0)
	if err != nil {
		t.Fatalf("first UpsertNote: %v", err)
	}

	note1, err := s.GetNote(context.Background(), path)
	if err != nil {
		t.Fatalf("GetNote after first upsert: %v", err)
	}
	if note1.Version != 1 {
		t.Errorf("first upsert version = %d, want 1", note1.Version)
	}

	// Second upsert (same path)
	err = s.UpsertNote(context.Background(), path, "note-1", "Updated Title", "device2", "Reading Notes", "folder2", 10, "hash2", 0)
	if err != nil {
		t.Fatalf("second UpsertNote: %v", err)
	}

	note2, err := s.GetNote(context.Background(), path)
	if err != nil {
		t.Fatalf("GetNote after second upsert: %v", err)
	}
	if note2.Version != 2 {
		t.Errorf("second upsert version = %d, want 2", note2.Version)
	}

	// Verify title and other fields were updated
	if note2.Title != "Updated Title" {
		t.Errorf("title = %q, want Updated Title", note2.Title)
	}
	if note2.PageCount != 10 {
		t.Errorf("page_count = %d, want 10", note2.PageCount)
	}
}

// TestCompleteJob verifies job completion updates status and timestamps
func TestCompleteJob(t *testing.T) {
	s := openTestStore(t)

	err := s.EnqueueJob(context.Background(), "/tmp/test.note")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	job, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}

	err = s.CompleteJob(context.Background(), job.ID, "api", "claude-3.5-sonnet")
	if err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	var status, ocrSource, apiModel string
	var finishedAt int64
	err = s.db.QueryRowContext(context.Background(),
		"SELECT status, ocr_source, api_model, finished_at FROM boox_jobs WHERE id = ?",
		job.ID,
	).Scan(&status, &ocrSource, &apiModel, &finishedAt)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}

	if status != "done" {
		t.Errorf("status = %q, want done", status)
	}
	if ocrSource != "api" {
		t.Errorf("ocr_source = %q, want api", ocrSource)
	}
	if apiModel != "claude-3.5-sonnet" {
		t.Errorf("api_model = %q, want claude-3.5-sonnet", apiModel)
	}
	if finishedAt <= 0 {
		t.Errorf("finished_at = %d, want > 0", finishedAt)
	}
}

// TestClaimNextJob_BumpsAttempts verifies that each claim increments the
// attempts counter so the Details modal reflects "how many times this job
// has started" instead of always-0 except for watchdog-recovered jobs.
func TestClaimNextJob_BumpsAttempts(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(context.Background(), "/tmp/attempt.note"); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	job, err := s.ClaimNextJob(context.Background())
	if err != nil || job == nil {
		t.Fatalf("ClaimNextJob first: err=%v job=%v", err, job)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts after first claim = %d, want 1", job.Attempts)
	}

	// Simulate a requeue by flipping the job back to pending.
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE boox_jobs SET status='pending' WHERE id = ?`, job.ID); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	job2, err := s.ClaimNextJob(context.Background())
	if err != nil || job2 == nil {
		t.Fatalf("ClaimNextJob second: err=%v job=%v", err, job2)
	}
	if job2.Attempts != 2 {
		t.Errorf("attempts after second claim = %d, want 2", job2.Attempts)
	}
}

// TestFailJob verifies job failure updates status and error message
func TestFailJob(t *testing.T) {
	s := openTestStore(t)

	err := s.EnqueueJob(context.Background(), "/tmp/test.note")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	job, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}

	errMsg := "test error: parse failed"
	err = s.FailJob(context.Background(), job.ID, errMsg)
	if err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	var status, lastError string
	err = s.db.QueryRowContext(context.Background(),
		"SELECT status, last_error FROM boox_jobs WHERE id = ?",
		job.ID,
	).Scan(&status, &lastError)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}

	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if lastError != errMsg {
		t.Errorf("last_error = %q, want %q", lastError, errMsg)
	}
}

// TestRetryAllFailed verifies that only failed jobs are reset to pending.
func TestRetryAllFailed(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Enqueue three jobs: one done, one failed, one pending.
	paths := []string{"/tmp/done.note", "/tmp/failed.note", "/tmp/pending.note"}
	for _, p := range paths {
		if err := s.EnqueueJob(ctx, p); err != nil {
			t.Fatalf("EnqueueJob %s: %v", p, err)
		}
	}

	// Claim and complete the "done" job.
	jobDone, err := s.ClaimNextJob(ctx)
	if err != nil || jobDone == nil {
		t.Fatalf("ClaimNextJob for done: %v", err)
	}
	if err := s.CompleteJob(ctx, jobDone.ID, "api", "test-model"); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	// Claim and fail the "failed" job.
	jobFailed, err := s.ClaimNextJob(ctx)
	if err != nil || jobFailed == nil {
		t.Fatalf("ClaimNextJob for failed: %v", err)
	}
	if err := s.FailJob(ctx, jobFailed.ID, "some parse error"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	// "pending" job remains unclaimed (third in queue).

	n, err := s.RetryAllFailed(ctx)
	if err != nil {
		t.Fatalf("RetryAllFailed: %v", err)
	}
	if n != 1 {
		t.Errorf("RetryAllFailed returned %d rows affected, want 1", n)
	}

	// The failed job should now be pending.
	var statusFailed string
	if err := s.db.QueryRowContext(ctx, "SELECT status FROM boox_jobs WHERE id = ?", jobFailed.ID).Scan(&statusFailed); err != nil {
		t.Fatalf("query failed job status: %v", err)
	}
	if statusFailed != "pending" {
		t.Errorf("failed job status after RetryAllFailed = %q, want pending", statusFailed)
	}

	// The done job should remain done.
	var statusDone string
	if err := s.db.QueryRowContext(ctx, "SELECT status FROM boox_jobs WHERE id = ?", jobDone.ID).Scan(&statusDone); err != nil {
		t.Fatalf("query done job status: %v", err)
	}
	if statusDone != "done" {
		t.Errorf("done job status after RetryAllFailed = %q, want done", statusDone)
	}
}

// TestDeleteNote verifies that DeleteNote removes the note, its jobs, and any note_content rows.
func TestDeleteNote(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/todelete.note"

	// Create a note and a job.
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Insert a note_content row for this note (simulating indexed content).
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO note_content (note_path, page, title_text, body_text, keywords, source, model, indexed_at)
		 VALUES (?, 1, 'Title', 'Body', 'kw', 'api', 'test-model', ?)`,
		notePath, now,
	)
	if err != nil {
		t.Fatalf("insert note_content: %v", err)
	}

	// Verify rows exist before deletion.
	var noteCount, jobCount, contentCount int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM boox_notes WHERE path = ?", notePath).Scan(&noteCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM boox_jobs WHERE note_path = ?", notePath).Scan(&jobCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM note_content WHERE note_path = ?", notePath).Scan(&contentCount)
	if noteCount != 1 || jobCount != 1 || contentCount != 1 {
		t.Fatalf("pre-delete counts: notes=%d jobs=%d content=%d, want all 1", noteCount, jobCount, contentCount)
	}

	if err := s.DeleteNote(ctx, notePath); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}

	// All three tables should now have zero rows for this path.
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM boox_notes WHERE path = ?", notePath).Scan(&noteCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM boox_jobs WHERE note_path = ?", notePath).Scan(&jobCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM note_content WHERE note_path = ?", notePath).Scan(&contentCount)
	if noteCount != 0 {
		t.Errorf("boox_notes count after delete = %d, want 0", noteCount)
	}
	if jobCount != 0 {
		t.Errorf("boox_jobs count after delete = %d, want 0", jobCount)
	}
	if contentCount != 0 {
		t.Errorf("note_content count after delete = %d, want 0", contentCount)
	}
}

// TestSkipNote verifies that SkipNote marks the latest job as skipped with a reason.
func TestSkipNote(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/toskip.note"
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	skipReason := "file too large"
	if err := s.SkipNote(ctx, notePath, skipReason); err != nil {
		t.Fatalf("SkipNote: %v", err)
	}

	job, err := s.GetLatestJob(ctx, notePath)
	if err != nil {
		t.Fatalf("GetLatestJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected a job, got nil")
	}
	if job.Status != "skipped" {
		t.Errorf("job.Status = %q, want skipped", job.Status)
	}
	if job.SkipReason != skipReason {
		t.Errorf("job.SkipReason = %q, want %q", job.SkipReason, skipReason)
	}
}

// TestSkipNote_LatestJobOnly verifies SkipNote affects only the most recent job, not earlier ones.
func TestSkipNote_LatestJobOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/toskip2.note"

	// Enqueue and complete a first job.
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	job1, err := s.ClaimNextJob(ctx)
	if err != nil || job1 == nil {
		t.Fatalf("ClaimNextJob first: %v", err)
	}
	if err := s.CompleteJob(ctx, job1.ID, "", ""); err != nil {
		t.Fatalf("CompleteJob first: %v", err)
	}

	// Enqueue a second (pending) job.
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}

	if err := s.SkipNote(ctx, notePath, "intentional skip"); err != nil {
		t.Fatalf("SkipNote: %v", err)
	}

	// The first job should still be done.
	var statusFirst string
	if err := s.db.QueryRowContext(ctx, "SELECT status FROM boox_jobs WHERE id = ?", job1.ID).Scan(&statusFirst); err != nil {
		t.Fatalf("query first job: %v", err)
	}
	if statusFirst != "done" {
		t.Errorf("first job status = %q, want done", statusFirst)
	}

	// The latest job should be skipped.
	latest, err := s.GetLatestJob(ctx, notePath)
	if err != nil || latest == nil {
		t.Fatalf("GetLatestJob: %v", err)
	}
	if latest.Status != "skipped" {
		t.Errorf("latest job status = %q, want skipped", latest.Status)
	}
}

// TestUnskipNote verifies that UnskipNote resets a skipped job back to pending.
func TestUnskipNote(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/tounskip.note"
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Skip it first.
	if err := s.SkipNote(ctx, notePath, "skipped for testing"); err != nil {
		t.Fatalf("SkipNote: %v", err)
	}

	// Confirm it is skipped.
	job, err := s.GetLatestJob(ctx, notePath)
	if err != nil || job == nil {
		t.Fatalf("GetLatestJob after skip: %v", err)
	}
	if job.Status != "skipped" {
		t.Fatalf("job status before unskip = %q, want skipped", job.Status)
	}

	// Unskip.
	if err := s.UnskipNote(ctx, notePath); err != nil {
		t.Fatalf("UnskipNote: %v", err)
	}

	job, err = s.GetLatestJob(ctx, notePath)
	if err != nil || job == nil {
		t.Fatalf("GetLatestJob after unskip: %v", err)
	}
	if job.Status != "pending" {
		t.Errorf("job.Status after UnskipNote = %q, want pending", job.Status)
	}
	if job.SkipReason != "" {
		t.Errorf("job.SkipReason after UnskipNote = %q, want empty", job.SkipReason)
	}
}

// TestUnskipNote_NonSkipped verifies that UnskipNote does not affect non-skipped jobs.
func TestUnskipNote_NonSkipped(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/notskipped.note"
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// UnskipNote on a pending job should be a no-op (no error, status unchanged).
	if err := s.UnskipNote(ctx, notePath); err != nil {
		t.Fatalf("UnskipNote on pending job: %v", err)
	}

	job, err := s.GetLatestJob(ctx, notePath)
	if err != nil || job == nil {
		t.Fatalf("GetLatestJob: %v", err)
	}
	if job.Status != "pending" {
		t.Errorf("job.Status = %q, want pending (should be unchanged)", job.Status)
	}
}

// TestGetQueueStatus verifies aggregate counts match the actual jobs in the queue.
func TestGetQueueStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Start with an empty queue.
	qs, err := s.GetQueueStatus(ctx)
	if err != nil {
		t.Fatalf("GetQueueStatus on empty queue: %v", err)
	}
	if qs.Pending != 0 || qs.InProgress != 0 || qs.Done != 0 || qs.Failed != 0 {
		t.Errorf("empty queue counts = {%d %d %d %d}, want all 0", qs.Pending, qs.InProgress, qs.Done, qs.Failed)
	}

	// Create: 2 pending, 1 in_progress, 1 done, 1 failed.
	for i := 0; i < 5; i++ {
		p := fmt.Sprintf("/tmp/qs%d.note", i)
		if err := s.EnqueueJob(ctx, p); err != nil {
			t.Fatalf("EnqueueJob %d: %v", i, err)
		}
	}
	// 5 pending; claim three of them.
	job1, _ := s.ClaimNextJob(ctx) // in_progress → will stay in_progress
	job2, _ := s.ClaimNextJob(ctx) // in_progress → will be completed (done)
	job3, _ := s.ClaimNextJob(ctx) // in_progress → will be failed
	if job1 == nil || job2 == nil || job3 == nil {
		t.Fatal("expected three jobs to be claimed")
	}
	if err := s.CompleteJob(ctx, job2.ID, "api", "test-model"); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if err := s.FailJob(ctx, job3.ID, "test error"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	// job1 remains in_progress; 2 jobs remain pending.

	qs, err = s.GetQueueStatus(ctx)
	if err != nil {
		t.Fatalf("GetQueueStatus: %v", err)
	}
	if qs.Pending != 2 {
		t.Errorf("Pending = %d, want 2", qs.Pending)
	}
	if qs.InProgress != 1 {
		t.Errorf("InProgress = %d, want 1", qs.InProgress)
	}
	if qs.Done != 1 {
		t.Errorf("Done = %d, want 1", qs.Done)
	}
	if qs.Failed != 1 {
		t.Errorf("Failed = %d, want 1", qs.Failed)
	}
}

// TestGetQueueStatus_ActiveDetails verifies ActiveTitle and ActivePages are populated when a job is in_progress.
func TestGetQueueStatus_ActiveDetails(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	notePath := "/tmp/active.note"
	// Upsert a note with known metadata so the JOIN can find title/page_count.
	if err := s.UpsertNote(ctx, notePath, "note-active", "My Active Note", "Tab X", "Notebooks", "folder", 7, "hash", 0); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	job, err := s.ClaimNextJob(ctx)
	if err != nil || job == nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}

	qs, err := s.GetQueueStatus(ctx)
	if err != nil {
		t.Fatalf("GetQueueStatus: %v", err)
	}
	if qs.InProgress != 1 {
		t.Fatalf("InProgress = %d, want 1", qs.InProgress)
	}
	if qs.ActiveTitle != "My Active Note" {
		t.Errorf("ActiveTitle = %q, want My Active Note", qs.ActiveTitle)
	}
	if qs.ActivePages != 7 {
		t.Errorf("ActivePages = %d, want 7", qs.ActivePages)
	}
}

// TestReclaimStuckJobs verifies stuck jobs are returned to pending status
func TestReclaimStuckJobs(t *testing.T) {
	s := openTestStore(t)

	// Enqueue a job
	err := s.EnqueueJob(context.Background(), "/tmp/test.note")
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Claim it (sets in_progress)
	job, err := s.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}

	// Manually set started_at to 11 minutes ago
	oldTime := time.Now().Add(-11 * time.Minute).Unix()
	_, err = s.db.ExecContext(context.Background(), "UPDATE boox_jobs SET started_at = ? WHERE id = ?", oldTime, job.ID)
	if err != nil {
		t.Fatalf("update started_at: %v", err)
	}

	// Reclaim stuck jobs
	err = s.ReclaimStuckJobs(context.Background(), 10*time.Minute)
	if err != nil {
		t.Fatalf("ReclaimStuckJobs: %v", err)
	}

	// Verify status is back to pending
	var status string
	err = s.db.QueryRowContext(context.Background(), "SELECT status FROM boox_jobs WHERE id = ?", job.ID).Scan(&status)
	if err != nil {
		t.Fatalf("query job status: %v", err)
	}

	if status != "pending" {
		t.Errorf("status = %q, want pending", status)
	}
}

// TestHasDoneJobWithHash verifies the dedup check used by the worker to
// short-circuit re-uploads of unchanged Boox notes.
func TestHasDoneJobWithHash(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	notePath := "/tmp/dedup.note"
	hash := "abc123"

	// Empty hash always returns false.
	if got, _ := s.HasDoneJobWithHash(ctx, notePath, ""); got {
		t.Error("empty hash should return false")
	}

	// No row yet — should be false.
	if got, _ := s.HasDoneJobWithHash(ctx, notePath, hash); got {
		t.Error("missing row should return false")
	}

	// Row exists with matching hash but only a pending job — should be false.
	if err := s.UpsertNote(ctx, notePath, "nid", "Title", "dev", "Notebooks", "f", 1, hash, 0); err != nil {
		t.Fatalf("UpsertNote: %v", err)
	}
	if err := s.EnqueueJob(ctx, notePath); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if got, _ := s.HasDoneJobWithHash(ctx, notePath, hash); got {
		t.Error("pending job should not count as done")
	}

	// Mark job done — should now return true.
	job, _ := s.ClaimNextJob(ctx)
	if err := s.CompleteJob(ctx, job.ID, "api", "test-model"); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if got, _ := s.HasDoneJobWithHash(ctx, notePath, hash); !got {
		t.Error("done job with matching hash should return true")
	}

	// Different hash should not match.
	if got, _ := s.HasDoneJobWithHash(ctx, notePath, "different"); got {
		t.Error("non-matching hash should return false")
	}
}
