package service

import (
	"context"
	"io"
	"time"

	"github.com/sysop/ultrabridge/internal/booxpipeline"
)

// TaskStatus is a type-safe status for tasks.
type TaskStatus string

const (
	StatusNeedsAction TaskStatus = "needsAction"
	StatusCompleted   TaskStatus = "completed"
)

// Task represents a unified task entity.
type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      TaskStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	DueAt       *time.Time `json:"due_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Detail      *string    `json:"detail,omitempty"`
	Links       *TaskLink  `json:"links,omitempty"`
}

type TaskLink struct {
	AppName  string `json:"app_name"`
	FilePath string `json:"file_path"`
	Page     int    `json:"page"`
}

// NoteFile represents a notebook file (Supernote or Boox).
type NoteFile struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	RelPath    string    `json:"rel_path"`
	IsDir      bool      `json:"is_dir"`
	FileType   string    `json:"file_type"` // note, pdf, epub, other
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
	Source     string    `json:"source"`      // supernote, boox
	DeviceInfo *string   `json:"device_info"` // e.g. "A5 X"
	JobStatus  string    `json:"job_status"`  // pending, in_progress, done, failed, skipped
	LastError  *string   `json:"last_error"`
}

// SyncStatus represents the CalDAV sync state.
type SyncStatus struct {
	AdapterID     string     `json:"adapter_id"`
	AdapterActive bool       `json:"adapter_active"`
	InProgress    bool       `json:"in_progress"`
	LastSyncAt    *time.Time `json:"last_sync_at"`
	NextSyncAt    *time.Time `json:"next_sync_at"`
	LastError     *string    `json:"last_error"`
}

// EmbeddingJobStatus represents the background processing state.
type EmbeddingJobStatus struct {
	Running        bool                     `json:"running"`
	PendingCount   int                      `json:"pending_count"`
	InFlightCount  int                      `json:"in_flight_count"`
	ProcessedCount int                      `json:"processed_count"`
	FailedCount    int                      `json:"failed_count"`
	ActiveTask     *ActiveTask              `json:"active_task,omitempty"`
	Boox           *booxpipeline.QueueStatus `json:"boox,omitempty"`
}

type ActiveTask struct {
	Path      string    `json:"path"`
	StartedAt time.Time `json:"started_at"`
}

// SyncStatusProvider is an interface for accessing sync status and triggering sync.
type SyncStatusProvider interface {
	Status() SyncStatus
	TriggerSync()
}

// TaskService manages task-related operations.
type TaskService interface {
	List(ctx context.Context) ([]Task, error)
	Create(ctx context.Context, title string, dueAt *time.Time) (Task, error)
	Complete(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	PurgeCompleted(ctx context.Context) error
	BulkComplete(ctx context.Context, ids []string) error
	BulkDelete(ctx context.Context, ids []string) error
}

// NoteService manages note files and background processing.
type NoteService interface {
	ListFiles(ctx context.Context, path string, sort, order string, page, perPage int) ([]NoteFile, int, error)
	GetNoteDetails(ctx context.Context, path string) (interface{}, error) // history/job info
	GetContent(ctx context.Context, path string) (interface{}, error)     // OCR text and page metadata
	RenderPage(ctx context.Context, path string, page int) (io.ReadCloser, string, error) // image stream, content-type
	
	ScanFiles(ctx context.Context) error
	Enqueue(ctx context.Context, path string, force bool) error
	Skip(ctx context.Context, path, reason string) error
	Unskip(ctx context.Context, path string) error
	RetryFailed(ctx context.Context) error
	DeleteNote(ctx context.Context, path string) error
	BulkDelete(ctx context.Context, paths []string) error
	
	// Source Presence
	HasSupernoteSource() bool
	HasBooxSource() bool
	ListVersions(ctx context.Context, path string) (interface{}, error)
	
	// Pipeline Control
	StartProcessor(ctx context.Context) error
	StopProcessor(ctx context.Context) error
	GetProcessorStatus(ctx context.Context) (EmbeddingJobStatus, error)
	
	// Import (Boox specific)
	ImportFiles(ctx context.Context) error
	MigrateImports(ctx context.Context) error
}

// SearchResult represents a single search match.
type SearchResult struct {
	Path    string `json:"path"`
	Page    int    `json:"page"`
	Snippet string `json:"snippet"`
	Score   float32 `json:"score"`
}

// SearchService manages search and chat interactions.
type SearchService interface {
	Search(ctx context.Context, query, folder string) ([]SearchResult, error)
	
	// Chat (SSE stream)
	Ask(ctx context.Context, question string, sessionID int) (<-chan ChatResponse, error)
	ListSessions(ctx context.Context) (interface{}, error)
	GetMessages(ctx context.Context, sessionID int) (interface{}, error)

	// Embeddings
	TriggerBackfill(ctx context.Context) error
	GetEmbeddingCount(ctx context.Context) int
	HasEmbeddingPipeline() bool
}

type ChatResponse struct {
	Type    string      `json:"type"` // session, content, error
	Content string      `json:"content,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ConfigService manages system configuration and sources.
type ConfigService interface {
	GetConfig(ctx context.Context) (interface{}, error)
	UpdateConfig(ctx context.Context, config interface{}) error
	IsRestartRequired() bool
	
	ListSources(ctx context.Context) (interface{}, error)
	AddSource(ctx context.Context, source interface{}) error
	UpdateSource(ctx context.Context, id string, source interface{}) error
	DeleteSource(ctx context.Context, id string) error
	
	GetSyncStatus(ctx context.Context) (SyncStatus, error)
	TriggerSync(ctx context.Context) error
	HasSyncProvider() bool
}
