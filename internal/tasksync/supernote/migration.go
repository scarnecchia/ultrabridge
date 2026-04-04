package supernote

// pattern: Imperative Shell

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/sysop/ultrabridge/internal/taskstore"
	"github.com/sysop/ultrabridge/internal/tasksync"
)

// MigrateFromSPC imports all non-deleted tasks from SPC into the local task store
// and creates sync map entries. Returns the number of tasks imported.
func MigrateFromSPC(ctx context.Context, client *Client, store TaskCreator, syncMap *tasksync.SyncMap, logger *slog.Logger) (int, error) {
	spcTasks, err := client.FetchTasks(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch SPC tasks for migration: %w", err)
	}

	imported := 0
	now := time.Now().UnixMilli()
	for _, spc := range spcTasks {
		if spc.IsDeleted == "Y" {
			continue
		}

		t := &taskstore.Task{
			TaskID:        spc.ID, // Preserve original SPC task ID
			Title:         taskstore.SqlStr(spc.Title),
			Detail:        taskstore.SqlStr(spc.Detail),
			Status:        taskstore.SqlStr(spc.Status),
			Importance:    taskstore.SqlStr(spc.Importance),
			DueTime:       spc.DueTime,
			CompletedTime: sql.NullInt64{Int64: spc.CompletedTime, Valid: spc.CompletedTime != 0},
			LastModified:  sql.NullInt64{Int64: spc.LastModified, Valid: spc.LastModified != 0},
			Recurrence:    taskstore.SqlStr(spc.Recurrence),
			IsReminderOn:  spc.IsReminderOn,
			Links:         taskstore.SqlStr(spc.Links),
			IsDeleted:     "N",
		}

		if err := store.Create(ctx, t); err != nil {
			logger.Warn("migration: failed to import task", "task_id", spc.ID, "title", spc.Title, "error", err)
			continue
		}

		// Create sync map entry so engine knows this task exists on SPC
		entry := &tasksync.SyncMapEntry{
			TaskID:     t.TaskID,
			AdapterID:  "supernote",
			RemoteID:   spc.ID,
			RemoteETag: computeSPCETag(spc),
			LastPulled: now,
			LastPushed: now, // Mark as pushed too so engine doesn't re-push
		}
		if err := syncMap.Upsert(ctx, entry); err != nil {
			logger.Warn("migration: failed to create sync map entry", "task_id", spc.ID, "error", err)
		}

		imported++
	}

	logger.Info("migration complete", "imported", imported, "total_spc", len(spcTasks))
	return imported, nil
}

// TaskCreator is the subset of the task store needed for migration.
type TaskCreator interface {
	Create(ctx context.Context, t *taskstore.Task) error
}
