package supernote

// pattern: Functional Core

import (
	"crypto/md5"
	"fmt"
	"time"

	"github.com/sysop/ultrabridge/internal/tasksync"
)

// SPCTaskToRemote converts an SPC wire-format task to the adapter-neutral RemoteTask.
func SPCTaskToRemote(spc SPCTask) tasksync.RemoteTask {
	return tasksync.RemoteTask{
		RemoteID:      spc.ID,
		Title:         spc.Title,
		Detail:        spc.Detail,
		Status:        spc.Status,
		Importance:    spc.Importance,
		DueTime:       spc.DueTime,
		CompletedTime: spc.CompletedTime,
		Recurrence:    spc.Recurrence,
		IsReminderOn:  spc.IsReminderOn,
		Links:         spc.Links,
		ETag:          computeSPCETag(spc),
	}
}

// RemoteToSPCTask converts an adapter-neutral RemoteTask to SPC wire format for pushing.
// If remoteID is empty (new task), generates an MD5 ID matching Supernote device convention.
func RemoteToSPCTask(rt tasksync.RemoteTask, remoteID string) SPCTask {
	now := time.Now().UnixMilli()
	if remoteID == "" {
		remoteID = fmt.Sprintf("%x", md5.Sum([]byte(rt.Title+fmt.Sprint(now))))
	}
	ct := rt.CompletedTime
	if ct == 0 {
		ct = now // Supernote quirk: completedTime holds creation time
	}
	return SPCTask{
		ID:             remoteID,
		Title:          rt.Title,
		Detail:         rt.Detail,
		Status:         rt.Status,
		Importance:     rt.Importance,
		DueTime:        rt.DueTime,
		CompletedTime:  ct,
		LastModified:   now,
		Recurrence:     rt.Recurrence,
		IsReminderOn:   rt.IsReminderOn,
		Links:          rt.Links,
		IsDeleted:      "N",
		Sort:           0,
		SortCompleted:  0,
		SortTime:       now,
		PlanerSort:     0,
		PlanerSortTime: now,
	}
}

// computeSPCETag generates an opaque hash for change detection from SPC task fields.
func computeSPCETag(spc SPCTask) string {
	data := fmt.Sprintf("%s|%s|%s|%d|%d",
		spc.Title, spc.Status, spc.Detail, spc.DueTime, spc.LastModified)
	return fmt.Sprintf("%x", md5.Sum([]byte(data)))
}
