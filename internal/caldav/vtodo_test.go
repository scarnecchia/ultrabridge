package caldav

import (
	"database/sql"
	"testing"
	"time"

	ical "github.com/emersion/go-ical"
	"github.com/sysop/ultrabridge/internal/taskstore"
)

func TestTaskToVTODO(t *testing.T) {
	tests := []struct {
		name        string
		task        *taskstore.Task
		dueTimeMode string
		verify      func(t *testing.T, cal *ical.Calendar)
	}{
		{
			name: "minimal task with uid and title",
			task: &taskstore.Task{
				TaskID: "test-id-123",
				Title:  sql.NullString{String: "Test Task", Valid: true},
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				if todo.Props.Get("UID").Value != "test-id-123" {
					t.Errorf("UID mismatch: got %s", todo.Props.Get("UID").Value)
				}
				if todo.Props.Get("SUMMARY").Value != "Test Task" {
					t.Errorf("SUMMARY mismatch: got %s", todo.Props.Get("SUMMARY").Value)
				}
			},
		},
		{
			name: "task with zero due time",
			task: &taskstore.Task{
				TaskID:  "test-id-123",
				Title:   sql.NullString{String: "Task", Valid: true},
				DueTime: 0,
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				if todo.Props.Get("DUE") != nil {
					t.Error("DUE property should not be present for zero DueTime")
				}
			},
		},
		{
			name: "completed task status",
			task: &taskstore.Task{
				TaskID:       "test-id",
				Title:        sql.NullString{String: "Done Task", Valid: true},
				Status:       sql.NullString{String: "completed", Valid: true},
				LastModified: sql.NullInt64{Int64: taskstore.TimeToMs(time.Date(2025, 3, 17, 14, 30, 0, 0, time.UTC)), Valid: true},
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				if todo.Props.Get("STATUS").Value != "COMPLETED" {
					t.Errorf("STATUS mismatch for completed: got %s", todo.Props.Get("STATUS").Value)
				}
				if todo.Props.Get("COMPLETED") == nil {
					t.Error("COMPLETED property should be set for completed task")
				}
			},
		},
		{
			name: "needs-action task status",
			task: &taskstore.Task{
				TaskID:       "test-id",
				Title:        sql.NullString{String: "Open Task", Valid: true},
				Status:       sql.NullString{String: "needsAction", Valid: true},
				LastModified: sql.NullInt64{Int64: taskstore.TimeToMs(time.Now().UTC()), Valid: true},
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				if todo.Props.Get("STATUS").Value != "NEEDS-ACTION" {
					t.Errorf("STATUS mismatch for needsAction: got %s", todo.Props.Get("STATUS").Value)
				}
			},
		},
		{
			name: "due time with date_only mode",
			task: &taskstore.Task{
				TaskID:  "test-id",
				Title:   sql.NullString{String: "Task", Valid: true},
				DueTime: taskstore.TimeToMs(time.Date(2025, 4, 15, 14, 30, 0, 0, time.UTC)),
			},
			dueTimeMode: "date_only",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				dueProp := todo.Props.Get("DUE")
				if dueProp == nil {
					t.Fatal("DUE property should be set")
				}
				// Check that it's a DATE (not DATE-TIME)
				// If it's DATE-only, ValueType should be "DATE"
				vt := dueProp.ValueType()
				if vt != ical.ValueDate {
					t.Errorf("DUE value type should be DATE for date_only mode, got %s", vt)
				}
			},
		},
		{
			name: "due time with preserve mode",
			task: &taskstore.Task{
				TaskID:  "test-id",
				Title:   sql.NullString{String: "Task", Valid: true},
				DueTime: taskstore.TimeToMs(time.Date(2025, 4, 15, 14, 30, 0, 0, time.UTC)),
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				dueProp := todo.Props.Get("DUE")
				if dueProp == nil {
					t.Fatal("DUE property should be set")
				}
				vt := dueProp.ValueType()
				if vt != ical.ValueDateTime {
					t.Errorf("DUE value type should be DATE-TIME for preserve mode, got %s", vt)
				}
			},
		},
		{
			name: "tier 2 fields",
			task: &taskstore.Task{
				TaskID:     "test-id",
				Title:      sql.NullString{String: "Task", Valid: true},
				Detail:     sql.NullString{String: "Important notes", Valid: true},
				Importance: sql.NullString{String: "5", Valid: true},
				Links:      sql.NullString{String: "supernote://note/123/page/1", Valid: true},
			},
			dueTimeMode: "preserve",
			verify: func(t *testing.T, cal *ical.Calendar) {
				todo, err := FindVTODO(cal)
				if err != nil {
					t.Fatalf("FindVTODO failed: %v", err)
				}
				if todo.Props.Get("DESCRIPTION").Value != "Important notes" {
					t.Errorf("DESCRIPTION mismatch")
				}
				if todo.Props.Get("PRIORITY").Value != "5" {
					t.Errorf("PRIORITY mismatch")
				}
				if todo.Props.Get("URL").Value != "supernote://note/123/page/1" {
					t.Errorf("URL mismatch")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := TaskToVTODO(tt.task, tt.dueTimeMode)
			if cal == nil {
				t.Fatal("TaskToVTODO returned nil")
			}
			tt.verify(t, cal)
		})
	}
}

func TestVTODOToTask(t *testing.T) {
	tests := []struct {
		name        string
		cal         *ical.Calendar
		dueTimeMode string
		verify      func(t *testing.T, task *taskstore.Task)
		expectErr   bool
	}{
		{
			name: "minimal VTODO",
			cal: createTestCalendar(map[string]string{
				"UID":     "test-id",
				"SUMMARY": "Test Task",
				"STATUS":  "NEEDS-ACTION",
			}),
			dueTimeMode: "preserve",
			verify: func(t *testing.T, task *taskstore.Task) {
				if task.TaskID != "test-id" {
					t.Errorf("TaskID mismatch: got %s", task.TaskID)
				}
				if taskstore.NullStr(task.Title) != "Test Task" {
					t.Errorf("Title mismatch")
				}
				if taskstore.NullStr(task.Status) != "needsAction" {
					t.Errorf("Status mismatch")
				}
			},
		},
		{
			name: "no VTODO component",
			cal: func() *ical.Calendar {
				cal := ical.NewCalendar()
				// Add VEVENT instead
				vevent := ical.NewComponent("VEVENT")
				cal.Children = append(cal.Children, vevent)
				return cal
			}(),
			dueTimeMode: "preserve",
			expectErr:   true,
		},
		{
			name: "COMPLETED status",
			cal: createTestCalendar(map[string]string{
				"UID":     "test-id",
				"SUMMARY": "Done Task",
				"STATUS":  "COMPLETED",
			}),
			dueTimeMode: "preserve",
			verify: func(t *testing.T, task *taskstore.Task) {
				if taskstore.NullStr(task.Status) != "completed" {
					t.Errorf("Status mismatch for COMPLETED")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task, err := VTODOToTask(tt.cal, tt.dueTimeMode)
			if (err != nil) != tt.expectErr {
				t.Errorf("error expectation mismatch: %v", err)
			}
			if err == nil && task != nil {
				tt.verify(t, task)
			}
		})
	}
}

func TestFindVTODO(t *testing.T) {
	t.Run("finds VTODO in calendar", func(t *testing.T) {
		cal := ical.NewCalendar()
		vtodo := ical.NewComponent("VTODO")
		cal.Children = append(cal.Children, vtodo)

		found, err := FindVTODO(cal)
		if err != nil {
			t.Errorf("FindVTODO failed: %v", err)
		}
		if found == nil {
			t.Error("FindVTODO returned nil")
		}
	})

	t.Run("returns error when no VTODO", func(t *testing.T) {
		cal := ical.NewCalendar()
		vevent := ical.NewComponent("VEVENT")
		cal.Children = append(cal.Children, vevent)

		_, err := FindVTODO(cal)
		if err == nil {
			t.Error("FindVTODO should return error when no VTODO present")
		}
	})
}

func TestHasVEvent(t *testing.T) {
	t.Run("returns true for calendar with VEVENT", func(t *testing.T) {
		cal := ical.NewCalendar()
		vevent := ical.NewComponent("VEVENT")
		cal.Children = append(cal.Children, vevent)

		if !HasVEvent(cal) {
			t.Error("HasVEvent should return true for calendar with VEVENT")
		}
	})

	t.Run("returns false for calendar without VEVENT", func(t *testing.T) {
		cal := ical.NewCalendar()
		vtodo := ical.NewComponent("VTODO")
		cal.Children = append(cal.Children, vtodo)

		if HasVEvent(cal) {
			t.Error("HasVEvent should return false for calendar without VEVENT")
		}
	})
}

func TestRoundTripVTODOTask(t *testing.T) {
	tests := []struct {
		name        string
		task        *taskstore.Task
		dueTimeMode string
	}{
		{
			name: "round trip minimal task",
			task: &taskstore.Task{
				TaskID: "id-123",
				Title:  sql.NullString{String: "Round Trip Task", Valid: true},
				Status: sql.NullString{String: "needsAction", Valid: true},
			},
			dueTimeMode: "preserve",
		},
		{
			name: "round trip task with all fields",
			task: &taskstore.Task{
				TaskID:       "id-456",
				Title:        sql.NullString{String: "Complex Task", Valid: true},
				Status:       sql.NullString{String: "completed", Valid: true},
				Detail:       sql.NullString{String: "Task details", Valid: true},
				Importance:   sql.NullString{String: "3", Valid: true},
				DueTime:      taskstore.TimeToMs(time.Date(2025, 5, 20, 10, 0, 0, 0, time.UTC)),
				LastModified: sql.NullInt64{Int64: taskstore.TimeToMs(time.Date(2025, 3, 17, 15, 0, 0, 0, time.UTC)), Valid: true},
			},
			dueTimeMode: "preserve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Task -> VTODO
			cal := TaskToVTODO(tt.task, tt.dueTimeMode)

			// VTODO -> Task
			resultTask, err := VTODOToTask(cal, tt.dueTimeMode)
			if err != nil {
				t.Fatalf("VTODOToTask failed: %v", err)
			}

			// Compare mapped fields
			if resultTask.TaskID != tt.task.TaskID {
				t.Errorf("TaskID mismatch after round trip")
			}
			if taskstore.NullStr(resultTask.Title) != taskstore.NullStr(tt.task.Title) {
				t.Errorf("Title mismatch after round trip")
			}
			if taskstore.NullStr(resultTask.Status) != taskstore.NullStr(tt.task.Status) {
				t.Errorf("Status mismatch after round trip")
			}
			if taskstore.NullStr(resultTask.Detail) != taskstore.NullStr(tt.task.Detail) {
				t.Errorf("Detail mismatch after round trip")
			}
			if taskstore.NullStr(resultTask.Importance) != taskstore.NullStr(tt.task.Importance) {
				t.Errorf("Importance mismatch after round trip")
			}
		})
	}
}

// Helper function to create a test calendar with VTODO properties
func createTestCalendar(props map[string]string) *ical.Calendar {
	cal := ical.NewCalendar()
	todo := ical.NewComponent("VTODO")

	for key, value := range props {
		todo.Props.SetText(key, value)
	}

	cal.Children = append(cal.Children, todo)
	return cal
}
