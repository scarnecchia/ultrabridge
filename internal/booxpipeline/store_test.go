package booxpipeline

import (
	"context"
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
	err := s.UpsertNote(context.Background(), path, "note-1", "Test Title", "device1", "Notebooks", "folder1", 5, "hash1")
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
	err = s.UpsertNote(context.Background(), path, "note-1", "Updated Title", "device2", "Reading Notes", "folder2", 10, "hash2")
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
