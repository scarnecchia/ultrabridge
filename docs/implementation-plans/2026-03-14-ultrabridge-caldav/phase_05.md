# UltraBridge CalDAV — Phase 5: Socket.io Sync Notifier

**Goal:** Push STARTSYNC to connected Supernote devices after task writes via Engine.IO v3 WebSocket.

**Architecture:** Raw Engine.IO v3 client over WebSocket (no third-party socket.io library — none are compatible with Go 1.22 and Engine.IO v3). The notifier connects to Ratta's socket.io service, handles handshake/keepalive, and exposes a `Notify()` method called after task store writes. Graceful degradation: if connection fails, log warning and continue.

**Tech Stack:** Go 1.22, `github.com/coder/websocket` (or `gorilla/websocket`)

**Scope:** 8 phases from original design (phase 5 of 8)

**Codebase verified:** 2026-03-17 (Engine.IO v3 protocol from `/mnt/supernote/FINDINGS.md`)

---

## Acceptance Criteria Coverage

This phase implements and tests:

### ultrabridge-caldav.AC3: Bidirectional task sync
- **ultrabridge-caldav.AC3.5 Success:** After CalDAV write, socket.io STARTSYNC message is sent and device syncs within seconds
- **ultrabridge-caldav.AC3.6 Failure:** If socket.io is unreachable, DB write still succeeds and warning is logged (graceful degradation)

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
<!-- START_TASK_1 -->
### Task 1: Engine.IO v3 client

**Files:**
- Create: `internal/sync/notifier.go`

**Implementation:**

A `Notifier` struct that manages an Engine.IO v3 WebSocket connection to Ratta's socket.io service. The Engine.IO v3 protocol over WebSocket is:

1. Connect to `ws://host:port/socket.io/?EIO=3&transport=websocket`
2. Receive open packet: `0{"sid":"xxx","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`
3. Send Socket.IO connect: `40`
4. Receive connect ack: `40{"sid":"xxx"}`
5. Keepalive: server sends `2` (ping), client responds `3` (pong) — OR client sends ping depending on Engine.IO v3 spec (the Supernote server uses `ratta_ping`/`ratta_pong` custom events alongside standard pings)
6. Send messages: `42["ServerMessage","{...json...}"]`

The STARTSYNC message format (from FINDINGS.md):
```
42["ServerMessage","{\"code\":\"200\",\"timestamp\":<now_ms>,\"msgType\":\"FILE-SYN\",\"data\":[{\"messageType\":\"STARTSYNC\",\"equipmentNo\":\"ultrabridge\",\"timestamp\":<now_ms>}]}"]
```

```go
package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type Notifier struct {
	url    string
	logger *slog.Logger

	mu   sync.Mutex
	conn *websocket.Conn
}

func NewNotifier(socketIOURL string, logger *slog.Logger) *Notifier {
	// Convert ws://host:port/socket.io/ to ws://host:port/socket.io/?EIO=3&transport=websocket
	u, _ := url.Parse(socketIOURL)
	q := u.Query()
	q.Set("EIO", "3")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()

	return &Notifier{
		url:    u.String(),
		logger: logger,
	}
}

// Connect establishes the Engine.IO v3 WebSocket connection.
// Non-blocking — returns immediately. Connection errors are logged, not fatal.
func (n *Notifier) Connect(ctx context.Context) {
	go n.connectLoop(ctx)
}

func (n *Notifier) connectLoop(ctx context.Context) {
	for {
		if err := n.dial(ctx); err != nil {
			n.logger.Warn("socket.io connect failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			// Reconnect after delay
		}
	}
}

func (n *Notifier) dial(ctx context.Context) error {
	conn, _, err := websocket.Dial(ctx, n.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	// Read open packet (type 0)
	_, msg, err := conn.Read(ctx)
	if err != nil {
		conn.Close(websocket.StatusNormalClosure, "")
		return fmt.Errorf("read open: %w", err)
	}
	if len(msg) == 0 || msg[0] != '0' {
		conn.Close(websocket.StatusNormalClosure, "")
		return fmt.Errorf("unexpected open packet: %s", msg)
	}
	n.logger.Info("socket.io connected", "handshake", string(msg[1:]))

	// Send Socket.IO connect
	if err := conn.Write(ctx, websocket.MessageText, []byte("40")); err != nil {
		conn.Close(websocket.StatusNormalClosure, "")
		return fmt.Errorf("send connect: %w", err)
	}

	n.mu.Lock()
	n.conn = conn
	n.mu.Unlock()

	// Read loop: handle pings and messages
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			n.mu.Lock()
			n.conn = nil
			n.mu.Unlock()
			return fmt.Errorf("read: %w", err)
		}
		if len(msg) > 0 && msg[0] == '2' {
			// Ping — respond with pong
			if err := conn.Write(ctx, websocket.MessageText, []byte("3")); err != nil {
				return fmt.Errorf("send pong: %w", err)
			}
		}
	}
}

// Notify sends a STARTSYNC message to trigger device sync.
// Returns nil on success. If not connected, logs warning and returns error
// (caller should NOT fail the DB write — graceful degradation).
func (n *Notifier) Notify(ctx context.Context) error {
	n.mu.Lock()
	conn := n.conn
	n.mu.Unlock()

	if conn == nil {
		n.logger.Warn("socket.io not connected, skipping STARTSYNC")
		return fmt.Errorf("not connected")
	}

	now := time.Now().UnixMilli()
	payload := fmt.Sprintf(
		`{"code":"200","timestamp":%d,"msgType":"FILE-SYN","data":[{"messageType":"STARTSYNC","equipmentNo":"ultrabridge","timestamp":%d}]}`,
		now, now,
	)

	// Socket.IO event format: 42["EventName","payload"]
	payloadJSON, _ := json.Marshal(payload)
	msg := fmt.Sprintf(`42["ServerMessage",%s]`, payloadJSON)

	if err := conn.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
		n.logger.Warn("STARTSYNC send failed", "error", err)
		return fmt.Errorf("send STARTSYNC: %w", err)
	}

	n.logger.Info("STARTSYNC sent")
	return nil
}

// Close shuts down the notifier connection.
func (n *Notifier) Close() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn != nil {
		n.conn.Close(websocket.StatusNormalClosure, "shutting down")
		n.conn = nil
	}
}
```

**Step 2: Add dependency**

```bash
go get github.com/coder/websocket
```

**Verification:**

```bash
go build ./internal/sync/
```

Expected: Compiles.

**Commit:** `feat: add Engine.IO v3 sync notifier for STARTSYNC push`

<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Wire notifier into CalDAV backend

**Files:**
- Modify: `internal/caldav/backend.go`
- Modify: `cmd/ultrabridge/main.go`

**Implementation:**

Add an optional `Notifier` interface to the CalDAV backend. After successful `PutCalendarObject` and `DeleteCalendarObject`, call `Notify()`. If notify fails, log warning but do not fail the CalDAV operation (graceful degradation per AC3.6).

Add to `internal/caldav/backend.go`:

```go
// SyncNotifier is called after task writes to trigger device sync.
type SyncNotifier interface {
	Notify(ctx context.Context) error
}
```

Add `notifier SyncNotifier` field to `Backend` struct. In `PutCalendarObject` and `DeleteCalendarObject`, after successful store operations:

```go
	if b.notifier != nil {
		if err := b.notifier.Notify(ctx); err != nil {
			// Log warning but don't fail the operation
			// (logging will be wired in Phase 6)
		}
	}
```

Update `NewBackend` to accept optional notifier parameter.

In `cmd/ultrabridge/main.go`, create the notifier and pass it to the backend:

```go
	notifier := sync.NewNotifier(cfg.SocketIOURL, slog.Default())
	notifier.Connect(context.Background())
	defer notifier.Close()

	backend := ubcaldav.NewBackend(store, "/caldav", cfg.CalDAVCollectionName, cfg.DueTimeMode, notifier)
```

**Verification:**

```bash
go build ./cmd/ultrabridge/
```

Expected: Compiles.

**Commit:** `feat: wire sync notifier into CalDAV backend`

<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Notifier tests

**Verifies:** ultrabridge-caldav.AC3.5, ultrabridge-caldav.AC3.6

**Files:**
- Create: `internal/sync/notifier_test.go`

**Testing:**

Tests must verify each AC:
- **ultrabridge-caldav.AC3.5:** `Notify()` sends correctly formatted STARTSYNC message. Test with a mock WebSocket server that receives the message and validates format: `42["ServerMessage","{...STARTSYNC...}"]` with valid JSON payload containing `messageType`, `equipmentNo`, `timestamp`, `msgType`, `code` fields.
- **ultrabridge-caldav.AC3.6:** When notifier has no connection (`conn == nil`), `Notify()` returns error but does not panic. The CalDAV backend test should verify that a failed `Notify()` does not cause `PutCalendarObject` to fail.

Additional tests:
- Connection handshake: mock server sends `0{...}`, client sends `40`, connection established
- Ping/pong: mock server sends `2`, client responds `3`
- Reconnect: after connection drops, client reconnects

Use `net/http/httptest` to create a test WebSocket server. The test server should:
1. Upgrade to WebSocket
2. Send Engine.IO open packet
3. Read Socket.IO connect (`40`)
4. Exchange messages

Follow Go standard testing patterns.

**Verification:**

```bash
go test ./internal/sync/ -v
```

Expected: All tests pass.

**Commit:** `test: add sync notifier tests with mock WebSocket server`

<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->
