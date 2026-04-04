# CalDAV-Native Task Store — Phase 4: Supernote Adapter

**Goal:** Outbound adapter that syncs tasks between UltraBridge's SQLite store and Supernote Private Cloud (SPC) via its REST API.

**Architecture:** New `internal/tasksync/supernote/` package implements the `DeviceAdapter` interface from Phase 3. Three files: `client.go` (HTTP client for SPC REST endpoints with JWT auth), `mapping.go` (field translation between UB task model and SPC wire format, isolating all Supernote quirks), and `adapter.go` (DeviceAdapter implementation that composes the client and mapping). The adapter authenticates via SPC's challenge-response JWT flow, pulls tasks via the task list endpoint, and pushes creates/updates/deletes via the corresponding task API. After pushing changes, it triggers STARTSYNC via the existing `sync.Notifier` so the device picks up changes. Config vars follow the established `UB_` prefix pattern.

**Tech Stack:** Go, `net/http`, `crypto/sha256`, `encoding/json`

**Scope:** 4 of 7 phases from original design

**Codebase verified:** 2026-04-04

**Development environment:** Code is written locally at `/home/jtd/ultrabridge`. Testing requires SSH to `sysop@192.168.9.52` where Go is installed and the running instance lives at `~/src/ultrabridge`.

---

## Acceptance Criteria Coverage

This phase implements and tests:

### caldav-native-taskstore.AC2: Supernote sync adapter
- **caldav-native-taskstore.AC2.1 Success:** Task created in UltraBridge appears on Supernote device after sync cycle + STARTSYNC push
- **caldav-native-taskstore.AC2.2 Success:** Task completed in UltraBridge sets status=completed and updates lastModified on SPC side
- **caldav-native-taskstore.AC2.3 Success:** Task created on Supernote device appears in UltraBridge after sync cycle
- **caldav-native-taskstore.AC2.4 Success:** Task edited on Supernote device (title change) reflected in UltraBridge after sync
- **caldav-native-taskstore.AC2.5 Success:** Conflict (both sides edited) resolves with UltraBridge version winning and pushing back to SPC
- **caldav-native-taskstore.AC2.6 Success:** Adapter authenticates via SPC challenge-response, re-authenticates on 401
- **caldav-native-taskstore.AC2.7 Failure:** SPC unreachable — sync cycle logs warning, retries next interval, task store continues working
- **caldav-native-taskstore.AC2.8 Failure:** SPC auth failure (wrong password) — logged as error, sync disabled until next restart or config change

---

<!-- START_TASK_1 -->
### Task 1: Add Supernote sync config vars

**Files:**
- Modify: `internal/config/config.go:11-60` (Config struct) and `internal/config/config.go:62-100` (Load function)

**Implementation:**

Add fields to the Config struct (after the existing TaskDBPath field):

```go
// Supernote sync
SNSyncEnabled  bool
SNSyncInterval int    // seconds
SNAPIURL       string
SNPassword     string
```

In `Load()`, after the TaskDBPath line, add:

```go
cfg.SNSyncEnabled  = envBoolOrDefault("UB_SN_SYNC_ENABLED", false)
cfg.SNSyncInterval = envIntOrDefault("UB_SN_SYNC_INTERVAL", 300) // 5 minutes
cfg.SNAPIURL       = envOrDefault("UB_SN_API_URL", "http://localhost:9000")
cfg.SNPassword     = os.Getenv("UB_SN_PASSWORD")
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./...
```

Expected: Builds without errors.

**Commit:** `feat(config): add Supernote sync env vars (UB_SN_SYNC_ENABLED, UB_SN_SYNC_INTERVAL, UB_SN_API_URL, UB_SN_PASSWORD)`

<!-- END_TASK_1 -->

<!-- START_SUBCOMPONENT_A (tasks 2-3) -->
<!-- START_TASK_2 -->
### Task 2: Create `internal/tasksync/supernote/client.go` — SPC REST API client

**Files:**
- Create: `internal/tasksync/supernote/client.go`

**Implementation:**

HTTP client for SPC task endpoints. Follows the OCRClient pattern from `/home/jtd/ultrabridge/internal/processor/ocrclient.go:28-50` (struct with `http.Client`, timeout, auth headers).

The SPC REST API for tasks uses these endpoints (documented in design plan):
- `POST /api/official/user/query/random/code` → returns `{randomCode, timestamp}`
- `POST /api/official/user/account/login/equipment` → returns JWT
- `GET /api/file/schedule/task/group/list` → returns task groups
- `GET /api/file/schedule/task/list?groupId=...` → returns tasks in a group
- `POST /api/file/schedule/task/create` → creates a task
- `POST /api/file/schedule/task/update` → bulk updates tasks
- `POST /api/file/schedule/task/delete` → deletes a task

Auth flow (challenge-response JWT):
1. POST to random/code endpoint → get `randomCode` and `timestamp`
2. Compute: `SHA256(password + randomCode)`
3. POST to login endpoint with `{password: sha256hex, randomCode, timestamp}` → get JWT
4. All subsequent requests include header `x-access-token: {jwt}`
5. On 401, re-authenticate automatically

```go
package supernote

// pattern: Imperative Shell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Client is an HTTP client for the Supernote Private Cloud REST API.
type Client struct {
	apiURL   string
	password string
	logger   *slog.Logger
	client   http.Client

	mu    sync.Mutex
	token string
}

// NewClient creates a new SPC REST API client.
func NewClient(apiURL, password string, logger *slog.Logger) *Client {
	return &Client{
		apiURL:   apiURL,
		password: password,
		logger:   logger,
		client:   http.Client{Timeout: 30 * time.Second},
	}
}

// Login performs the challenge-response JWT authentication flow.
func (c *Client) Login(ctx context.Context) error {
	// Step 1: Get random code
	var codeResp struct {
		Success    bool   `json:"success"`
		RandomCode string `json:"randomCode"`
		Timestamp  int64  `json:"timestamp"`
	}
	if err := c.postJSON(ctx, "/api/official/user/query/random/code", nil, &codeResp, false); err != nil {
		return fmt.Errorf("get random code: %w", err)
	}

	// Step 2: Hash password with challenge
	hash := sha256.Sum256([]byte(c.password + codeResp.RandomCode))
	hashedPW := fmt.Sprintf("%x", hash)

	// Step 3: Login
	loginBody := map[string]any{
		"password":   hashedPW,
		"randomCode": codeResp.RandomCode,
		"timestamp":  codeResp.Timestamp,
	}
	var loginResp struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	if err := c.postJSON(ctx, "/api/official/user/account/login/equipment", loginBody, &loginResp, false); err != nil {
		return fmt.Errorf("login: %w", err)
	}

	c.mu.Lock()
	c.token = loginResp.Token
	c.mu.Unlock()

	c.logger.Info("SPC login successful")
	return nil
}

// FetchTasks returns all tasks from all groups in SPC.
func (c *Client) FetchTasks(ctx context.Context) ([]SPCTask, error) {
	// Fetch groups first
	var groupsResp struct {
		Success bool `json:"success"`
		Data    []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/file/schedule/task/group/list", &groupsResp); err != nil {
		return nil, fmt.Errorf("fetch groups: %w", err)
	}

	// Fetch tasks from each group
	var allTasks []SPCTask
	for _, group := range groupsResp.Data {
		var tasksResp struct {
			Success bool      `json:"success"`
			Data    []SPCTask `json:"data"`
		}
		url := fmt.Sprintf("/api/file/schedule/task/list?groupId=%s", group.ID)
		if err := c.getJSON(ctx, url, &tasksResp); err != nil {
			c.logger.Warn("fetch tasks for group failed", "group_id", group.ID, "error", err)
			continue
		}
		allTasks = append(allTasks, tasksResp.Data...)
	}

	return allTasks, nil
}

// CreateTask creates a single task on SPC.
func (c *Client) CreateTask(ctx context.Context, task SPCTask) error {
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/create", task, &resp, true)
}

// UpdateTasks performs a bulk update of tasks on SPC.
func (c *Client) UpdateTasks(ctx context.Context, tasks []SPCTask) error {
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/update", tasks, &resp, true)
}

// DeleteTask deletes a task on SPC.
func (c *Client) DeleteTask(ctx context.Context, taskID string) error {
	body := map[string]string{"id": taskID}
	var resp struct{ Success bool `json:"success"` }
	return c.postJSON(ctx, "/api/file/schedule/task/delete", body, &resp, true)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, result any, auth bool) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.apiURL+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth {
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		req.Header.Set("x-access-token", token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && auth {
		// Re-authenticate and retry once
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-auth failed: %w", err)
		}
		return c.postJSON(ctx, path, body, result, false) // false to prevent infinite loop
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SPC %s returned %d: %s", path, resp.StatusCode, errBody)
	}

	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, result any) error {
	return c.doGetJSON(ctx, path, result, false)
}

func (c *Client) doGetJSON(ctx context.Context, path string, result any, retried bool) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.apiURL+path, nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	req.Header.Set("x-access-token", token)

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retried {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-auth failed: %w", err)
		}
		return c.doGetJSON(ctx, path, result, true)
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SPC %s returned %d: %s", path, resp.StatusCode, errBody)
	}

	return json.NewDecoder(resp.Body).Decode(result)
}

// SPCTask is the wire format for tasks in the SPC REST API.
type SPCTask struct {
	ID            string `json:"id"`
	TaskListID    string `json:"taskListId,omitempty"`
	Title         string `json:"title"`
	Detail        string `json:"detail,omitempty"`
	Status        string `json:"status"`
	Importance    string `json:"importance,omitempty"`
	DueTime       int64  `json:"dueTime"`
	CompletedTime int64  `json:"completedTime"` // Supernote quirk: holds creation time
	LastModified  int64  `json:"lastModified"`  // Supernote quirk: holds completion time when completed
	Recurrence    string `json:"recurrence,omitempty"`
	IsReminderOn  string `json:"isReminderOn"`
	Links         string `json:"links,omitempty"`
	IsDeleted     string `json:"isDeleted"`
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/supernote/
```

Expected: Builds without errors.

**Commit:** `feat(supernote): add SPC REST API client with JWT challenge-response auth`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Create `internal/tasksync/supernote/mapping.go` — field translation

**Files:**
- Create: `internal/tasksync/supernote/mapping.go`

**Implementation:**

Isolates all Supernote-specific field quirks in one place. Converts between `SPCTask` (wire format) and `tasksync.RemoteTask` (adapter-neutral format). Key quirks:
- `completedTime` holds creation time (not completion)
- `lastModified` holds actual completion time when status is "completed"
- Task IDs are MD5(title + timestamp)

```go
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
	if remoteID == "" {
		now := time.Now().UnixMilli()
		remoteID = fmt.Sprintf("%x", md5.Sum([]byte(rt.Title+fmt.Sprint(now))))
	}
	return SPCTask{
		ID:            remoteID,
		Title:         rt.Title,
		Detail:        rt.Detail,
		Status:        rt.Status,
		Importance:    rt.Importance,
		DueTime:       rt.DueTime,
		CompletedTime: rt.CompletedTime,
		Recurrence:    rt.Recurrence,
		IsReminderOn:  rt.IsReminderOn,
		Links:         rt.Links,
		IsDeleted:     "N",
	}
}

// computeSPCETag generates an opaque hash for change detection from SPC task fields.
func computeSPCETag(spc SPCTask) string {
	data := fmt.Sprintf("%s|%s|%s|%d|%d",
		spc.Title, spc.Status, spc.Detail, spc.DueTime, spc.LastModified)
	return fmt.Sprintf("%x", md5.Sum([]byte(data)))
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/supernote/
```

Expected: Builds without errors.

**Commit:** `feat(supernote): add field mapping between SPC wire format and RemoteTask`

<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-5) -->
<!-- START_TASK_4 -->
### Task 4: Create `internal/tasksync/supernote/adapter.go` — DeviceAdapter implementation

**Files:**
- Create: `internal/tasksync/supernote/adapter.go`

**Implementation:**

Implements `tasksync.DeviceAdapter`. Composes the SPC Client for HTTP operations and mapping functions for field translation. After pushing changes, calls `sync.Notifier.Notify()` to trigger STARTSYNC.

```go
package supernote

// pattern: Imperative Shell

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/sysop/ultrabridge/internal/tasksync"
)

// SyncNotifier triggers device sync. Matches caldav.SyncNotifier.
type SyncNotifier interface {
	Notify(ctx context.Context) error
}

// Adapter implements tasksync.DeviceAdapter for Supernote devices via SPC REST API.
type Adapter struct {
	client   *Client
	notifier SyncNotifier
	logger   *slog.Logger
}

// NewAdapter creates a Supernote sync adapter.
func NewAdapter(apiURL, password string, notifier SyncNotifier, logger *slog.Logger) *Adapter {
	return &Adapter{
		client:   NewClient(apiURL, password, logger),
		notifier: notifier,
		logger:   logger,
	}
}

func (a *Adapter) ID() string { return "supernote" }

func (a *Adapter) Start(ctx context.Context) error {
	return a.client.Login(ctx)
}

func (a *Adapter) Stop() error { return nil }

func (a *Adapter) Pull(ctx context.Context, since string) ([]tasksync.RemoteTask, string, error) {
	spcTasks, err := a.client.FetchTasks(ctx)
	if err != nil {
		return nil, since, fmt.Errorf("fetch SPC tasks: %w", err)
	}

	var remote []tasksync.RemoteTask
	for _, spc := range spcTasks {
		if spc.IsDeleted == "Y" {
			continue
		}
		remote = append(remote, SPCTaskToRemote(spc))
	}

	// SPC doesn't support sync tokens — return empty token.
	// Change detection is done via ETag comparison in the sync engine.
	return remote, "", nil
}

func (a *Adapter) Push(ctx context.Context, changes []tasksync.Change) ([]tasksync.PushResult, error) {
	var updateBatch []SPCTask
	var results []tasksync.PushResult

	for _, c := range changes {
		switch c.Type {
		case tasksync.ChangeCreate:
			spc := RemoteToSPCTask(c.Remote, "")
			if err := a.client.CreateTask(ctx, spc); err != nil {
				a.logger.Warn("push create failed", "task_id", c.TaskID, "error", err)
				continue
			}
			results = append(results, tasksync.PushResult{
				TaskID:   c.TaskID,
				RemoteID: spc.ID, // Server-assigned (generated by RemoteToSPCTask)
			})

		case tasksync.ChangeUpdate:
			spc := RemoteToSPCTask(c.Remote, c.RemoteID)
			updateBatch = append(updateBatch, spc)
			results = append(results, tasksync.PushResult{
				TaskID:   c.TaskID,
				RemoteID: c.RemoteID,
			})

		case tasksync.ChangeDelete:
			if err := a.client.DeleteTask(ctx, c.RemoteID); err != nil {
				a.logger.Warn("push delete failed", "task_id", c.TaskID, "error", err)
			}
		}
	}

	// Bulk update
	if len(updateBatch) > 0 {
		if err := a.client.UpdateTasks(ctx, updateBatch); err != nil {
			return results, fmt.Errorf("bulk update: %w", err)
		}
	}

	// Trigger STARTSYNC so device picks up changes
	if a.notifier != nil {
		if err := a.notifier.Notify(ctx); err != nil {
			a.logger.Warn("STARTSYNC notification failed", "error", err)
		}
	}

	return results, nil
}
```

**Verification:**

```bash
# On remote server:
go build -C ~/src/ultrabridge ./internal/tasksync/supernote/
```

Expected: Builds without errors.

**Commit:** `feat(supernote): implement DeviceAdapter with Pull, Push, and STARTSYNC notification`

<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Tests for Supernote adapter

**Verifies:** caldav-native-taskstore.AC2.1, caldav-native-taskstore.AC2.2, caldav-native-taskstore.AC2.3, caldav-native-taskstore.AC2.4, caldav-native-taskstore.AC2.5, caldav-native-taskstore.AC2.6

**Files:**
- Create: `internal/tasksync/supernote/adapter_test.go`

**Testing:**

Tests use `httptest.NewServer` to mock the SPC REST API, following the pattern used by auth and handler tests in the project. No mocking libraries — hand-roll the mock HTTP server.

The mock SPC server should:
- Handle the challenge-response login flow (random code → login → JWT)
- Serve task list/group endpoints with configurable responses
- Accept create/update/delete calls and record what was received
- Return 401 on expired token to test re-auth

Tests must verify:
- **caldav-native-taskstore.AC2.1:** Create an adapter, call Push with a ChangeCreate. Verify the mock SPC server received the create request with correct field mapping. Verify STARTSYNC notification was sent (mock notifier).

- **caldav-native-taskstore.AC2.2:** Push a ChangeUpdate with status="completed". Verify the mock SPC server received the update with correct status field.

- **caldav-native-taskstore.AC2.3:** Configure mock SPC to return a task from Pull. Verify the RemoteTask has correct fields mapped from the SPCTask wire format.

- **caldav-native-taskstore.AC2.4:** Configure mock SPC to return a task with a different title from a previous Pull. Verify the RemoteTask reflects the new title.

- **caldav-native-taskstore.AC2.5:** This is tested in Phase 3's engine tests (conflict resolution is engine-level). Verify here that Push correctly sends back UB's version.

- **caldav-native-taskstore.AC2.6:** Start adapter (triggers login). Verify mock SPC received challenge-response flow. Then make the mock return 401 on next request. Verify the adapter re-authenticates and retries.

Additional test cases:
- **Mapping round-trip:** SPCTask → RemoteTask → SPCTask preserves fields correctly (including all fields from I-8 fix: Importance, CompletedTime, Recurrence, Links, IsReminderOn)
- **ETag stability:** Same SPCTask input produces same ETag
- **caldav-native-taskstore.AC2.7 (SPC unreachable):** Mock SPC server is shut down. Call Pull/Push. Verify adapter returns error. Verify task store is unaffected. This simulates the engine logging a warning and retrying next interval.
- **caldav-native-taskstore.AC2.8 (auth failure):** Configure mock SPC to always reject login (return 401 for login endpoint). Call adapter.Start(). Verify it returns an error. The sync engine (Phase 3) handles this by not starting the sync loop — verify at engine level that Start failure prevents sync cycles from running.

**Verification:**

```bash
# On remote server:
go test -C ~/src/ultrabridge ./internal/tasksync/supernote/ -v
```

Expected: All tests pass.

**Commit:** `test(supernote): add adapter tests with mock SPC server`

<!-- END_TASK_5 -->
<!-- END_SUBCOMPONENT_B -->

<!-- START_TASK_6 -->
### Task 6: Add CLAUDE.md for tasksync/supernote package

**Files:**
- Create: `internal/tasksync/supernote/CLAUDE.md`

**Implementation:**

```markdown
# Supernote Sync Adapter

Last verified: 2026-04-04

## Purpose
Outbound sync adapter for Supernote devices via the Supernote Private Cloud (SPC) REST API. Implements the `tasksync.DeviceAdapter` interface.

## Contracts
- **Exposes**: `Adapter` (implements DeviceAdapter), `Client` (SPC REST API), `MigrateFromSPC` (first-run import)
- **Guarantees**: JWT auth with auto-retry on 401. Field mapping isolates Supernote quirks. STARTSYNC push after changes. Migration preserves original SPC task IDs.
- **Expects**: SPC API URL, password, and optional SyncNotifier.

## Dependencies
- **Uses**: `tasksync` (DeviceAdapter interface, types), `taskstore` (Task model, helpers)
- **Used by**: `cmd/ultrabridge` (adapter registration and migration)
- **Boundary**: Only package that knows SPC wire format. No CalDAV or web imports.

## Key Decisions
- Challenge-response JWT: SHA-256(password + randomCode)
- Re-auth on 401 with retry guard (max 1 retry per request)
- MD5 task IDs generated for creates (matches device convention)
- CompletedTime quirk: SPC completedTime = creation time, lastModified = completion time

## Invariants
- All SPC timestamps are millisecond UTC unix
- Deleted tasks filtered (isDeleted='Y') during Pull
```

**Verification:** Documentation only.

**Commit:** `docs(supernote): add CLAUDE.md for Supernote adapter package`

<!-- END_TASK_6 -->
