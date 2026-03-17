package taskstore

import "database/sql"

// Task represents a row in t_schedule_task.
// Note: The DB table has 8 additional sort columns (sort, sort_completed,
// planer_sort, all_sort, all_sort_completed, sort_time, planer_sort_time,
// all_sort_time) that are NOT included here. Tasks created via CalDAV will
// have NULL for these columns. This is acceptable because:
// 1. The Supernote device populates sort columns when it syncs
// 2. All sort columns are NULLable with no NOT NULL constraints
// 3. Observed behavior: the device reassigns sort order on sync
// If device behavior differs, the Create method can set default sort values.
type Task struct {
	TaskID        string
	TaskListID    sql.NullString
	UserID        int64
	Title         sql.NullString
	Detail        sql.NullString
	LastModified  sql.NullInt64
	Recurrence    sql.NullString
	IsReminderOn  string
	Status        sql.NullString
	Importance    sql.NullString
	DueTime       int64
	CompletedTime sql.NullInt64
	Links         sql.NullString
	IsDeleted     string
}
