package tasksync

import "context"

// ChangeType represents the kind of sync change.
type ChangeType int

const (
	ChangeCreate ChangeType = iota
	ChangeUpdate
	ChangeDelete
)

// RemoteTask is an adapter-neutral representation of a task from a remote device.
type RemoteTask struct {
	RemoteID      string
	Title         string
	Detail        string
	Status        string // "needsAction" or "completed" (Supernote convention)
	Importance    string
	DueTime       int64  // millisecond UTC unix, 0 = unset
	CompletedTime int64  // millisecond UTC unix — Supernote quirk: holds creation time
	Recurrence    string
	IsReminderOn  string
	Links         string
	ETag          string // opaque hash for change detection
}

// Change describes a single sync operation to push to a remote adapter.
type Change struct {
	Type     ChangeType
	TaskID   string     // local task ID
	RemoteID string     // remote task ID (empty for creates)
	Remote   RemoteTask // task data to push
}

// PushResult reports the outcome of a single push operation.
type PushResult struct {
	TaskID   string // local task ID from the Change
	RemoteID string // server-assigned remote ID (relevant for creates)
}

// SyncStatus reports the current state of the sync engine.
type SyncStatus struct {
	LastSyncAt    int64  // millisecond UTC unix
	NextSyncAt    int64  // millisecond UTC unix (0 = not scheduled)
	InProgress    bool
	LastError     string
	AdapterID     string
	AdapterActive bool
}

// DeviceAdapter is the interface all device sync adapters must implement.
type DeviceAdapter interface {
	// ID returns a unique identifier for this adapter (e.g., "supernote").
	ID() string

	// Start initializes the adapter (e.g., authenticates). Called at
	// engine start, and retried at the top of each cycle if it fails, so
	// implementations must be safe to call more than once.
	Start(ctx context.Context) error

	// Stop cleanly shuts down the adapter.
	Stop() error

	// Pull fetches remote tasks changed since the given sync token.
	// Returns the remote tasks, a new sync token, and any error.
	// An empty since token means "fetch all".
	Pull(ctx context.Context, since string) ([]RemoteTask, string, error)

	// Push applies local changes (creates, updates, deletes) to the remote device.
	// Returns a PushResult for each successfully pushed change (with server-assigned RemoteIDs for creates).
	Push(ctx context.Context, changes []Change) ([]PushResult, error)
}
