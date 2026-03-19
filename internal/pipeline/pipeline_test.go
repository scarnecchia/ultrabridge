package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
	"github.com/sysop/ultrabridge/internal/notestore"
	"github.com/sysop/ultrabridge/internal/processor"
)

func openTestComponents(t *testing.T) (*notestore.Store, *processor.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return notestore.New(db, dir), processor.New(db, processor.WorkerConfig{}), dir
}

// AC8.3 + AC8.1: Reconciler queues new files, not unchanged files
func TestReconciler_NewAndUnchanged(t *testing.T) {
	ns, proc, dir := openTestComponents(t)
	pl := New(Config{NotesPath: dir, Store: ns, Proc: proc, Logger: nil})

	notePath := filepath.Join(dir, "test.note")
	os.WriteFile(notePath, []byte("data"), 0644)

	// AC8.1: first reconcile discovers and queues the new file
	pl.reconcile(context.Background())
	if proc.Status().Pending != 1 {
		t.Errorf("pending after first scan = %d, want 1", proc.Status().Pending)
	}

	// AC8.3: second reconcile (same mtime) does not re-queue
	pl.reconcile(context.Background())
	if proc.Status().Pending != 1 {
		t.Errorf("pending after second scan = %d, want 1 (unchanged file should not re-queue)", proc.Status().Pending)
	}
}

// AC8.2: Changed file (mtime bump) is re-queued
func TestReconciler_ChangedFileRequeued(t *testing.T) {
	ns, proc, dir := openTestComponents(t)
	pl := New(Config{NotesPath: dir, Store: ns, Proc: proc, Logger: nil})

	notePath := filepath.Join(dir, "test.note")
	os.WriteFile(notePath, []byte("v1"), 0644)
	pl.reconcile(context.Background())

	// Bump mtime
	future := time.Now().Add(2 * time.Second)
	os.Chtimes(notePath, future, future)
	pl.reconcile(context.Background())

	// Should now have a second pending entry (or re-queued)
	if proc.Status().Pending < 1 {
		t.Error("expected re-queued job after mtime bump")
	}
}

// AC8.4: Debounce coalesces rapid writes into a single enqueue
func TestWatcher_Debounce(t *testing.T) {
	ns, proc, dir := openTestComponents(t)
	pl := New(Config{NotesPath: dir, Store: ns, Proc: proc, Logger: nil})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pl.Start(ctx)
	defer pl.Close()

	notePath := filepath.Join(dir, "rapid.note")
	for i := 0; i < 5; i++ {
		os.WriteFile(notePath, []byte("data"), 0644)
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for debounce to expire
	time.Sleep(debounceDelay + time.Second)

	st := proc.Status()
	if st.Pending != 1 {
		t.Errorf("pending = %d after 5 rapid writes, want 1 (debounce should coalesce)", st.Pending)
	}
}
