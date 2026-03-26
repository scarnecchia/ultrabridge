# NoteBridge Phase 3: Socket.IO + Event Bus

**Goal:** Real-time bidirectional communication and event-driven processing triggers.

**Architecture:** Engine.IO v3 WebSocket server at `/socket.io/`. In-process event bus (pub/sub with goroutine dispatch). Client registry tracks connected devices per user. File uploads publish events → bus → Socket.IO pushes ServerMessage to other devices.

**Tech Stack:** Go 1.24, golang.org/x/net/websocket, Engine.IO v3 framing

**Scope:** Phase 3 of 8 from original design

**Codebase verified:** 2026-03-22

---

## Acceptance Criteria Coverage

This phase implements and tests:

### notebridge-spc-replacement.AC3: Socket.IO
- **AC3.1 Success:** Tablet establishes Socket.IO connection with JWT, receives handshake
- **AC3.2 Success:** Ping/pong keepalive maintains connection
- **AC3.3 Success:** ServerMessage pushed to connected tablets when files change
- **AC3.4 Failure:** Invalid JWT on Socket.IO connect returns error frame

---

<!-- START_SUBCOMPONENT_A (tasks 1-3) -->
## Subcomponent A: Event Bus

<!-- START_TASK_1 -->
### Task 1: Event bus implementation

**Files:**
- Create: `/home/sysop/src/notebridge/internal/events/bus.go`
- Create: `/home/sysop/src/notebridge/internal/events/types.go`

**Implementation:**

**types.go — Event definitions:**

```go
type Event struct {
    Type   string // "file.uploaded", "file.modified", "file.deleted"
    FileID int64
    UserID int64
    Path   string
}
```

Constants for event types:
- `FileUploaded  = "file.uploaded"`
- `FileModified  = "file.modified"`
- `FileDeleted   = "file.deleted"`

**bus.go — EventBus implementation:**

```go
type EventBus struct {
    mu       sync.RWMutex
    handlers map[string][]func(Event)
}
```

Methods:
- `NewEventBus() *EventBus`
- `Subscribe(eventType string, handler func(Event))` — appends handler to list for eventType (mutex write lock)
- `Publish(ctx context.Context, event Event)` — reads handlers for event type (mutex read lock), dispatches each in a separate goroutine (fire-and-forget). Log panics from handlers but don't propagate.

The bus is in-process only — no network transport, no persistence. Goroutine dispatch means handlers execute concurrently and publishing never blocks.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add in-process event bus`
<!-- END_TASK_1 -->

<!-- START_TASK_2 -->
### Task 2: Event bus tests

**Files:**
- Create: `/home/sysop/src/notebridge/internal/events/bus_test.go`

**Testing:**

- Subscribe + Publish: handler receives event with correct fields
- Multiple subscribers: all handlers called for same event type
- Different event types: handler only receives subscribed type
- Publish with no subscribers: no panic, no error
- Concurrent publish: multiple goroutines publishing simultaneously, all events delivered
- Handler panic: does not crash the bus or affect other handlers

Use channels with timeouts for async verification (e.g., `select { case <-received: case <-time.After(time.Second): t.Fatal("timeout") }`).

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/events/
```

**Commit:** `test: add event bus tests`
<!-- END_TASK_2 -->

<!-- START_TASK_3 -->
### Task 3: Wire event bus to upload finish

**Files:**
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — add EventBus field
- Modify: `/home/sysop/src/notebridge/internal/sync/handlers_upload.go` — publish FileUploadedEvent after upload/finish
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go` — create EventBus, pass to Server

**Implementation:**

Add `eventBus *events.EventBus` field to sync.Server. Update `NewServer` constructor to accept it.

In `handleUploadFinish`, after successfully creating/updating the file entry in syncdb, publish:
```go
s.eventBus.Publish(ctx, events.Event{
    Type:   events.FileUploaded,
    FileID: fileEntry.ID,
    UserID: userID,
    Path:   storageKey,
})
```

Similarly, in delete handler publish `FileDeleted`, and in move/copy handlers publish `FileModified`.

In main.go: create `events.NewEventBus()`, pass to `sync.NewServer(...)`.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire event bus to file upload/delete/move handlers`
<!-- END_TASK_3 -->
<!-- END_SUBCOMPONENT_A -->

<!-- START_SUBCOMPONENT_B (tasks 4-7) -->
## Subcomponent B: Socket.IO Server

<!-- START_TASK_4 -->
### Task 4: Engine.IO frame encoding/decoding

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/engineio.go`

**Implementation:**

Engine.IO v3 frame encoding and decoding. The Supernote tablet uses Engine.IO v3 (EIO=3 query param), which prefixes frames with a single-digit packet type.

**Packet types (constants):**
- `PacketOpen    = '0'` — server → client, JSON payload with session info
- `PacketPing    = '2'` — server → client (or bidirectional)
- `PacketPong    = '3'` — client → server (response to ping)
- `PacketMessage = '4'` — Socket.IO layer prefix

**Socket.IO message sub-types (after the '4' prefix):**
- `MessageConnect   = "40"` — connection acknowledgment
- `MessageEvent     = "42"` — event frame: `42["eventName", arg1, ...]`

**Functions:**

`EncodeOpenPacket(sid string, pingInterval, pingTimeout int) string`
- Returns: `0{"sid":"<sid>","upgrades":[],"pingInterval":25000,"pingTimeout":5000}`

`EncodeEvent(eventName string, data any) (string, error)`
- Marshal data to JSON
- Returns: `42["<eventName>",<jsonData>]`

`DecodeFrame(raw string) (packetType byte, payload string)`
- Returns first byte as type, rest as payload

`DecodeEvent(payload string) (eventName string, data string, err error)`
- Expects format after "42" prefix: `["eventName", ...]`
- Returns event name and the raw JSON of the remaining args

Add `golang.org/x/net/websocket` dependency:
```bash
go get golang.org/x/net/websocket
```

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add Engine.IO v3 frame encoding/decoding`
<!-- END_TASK_4 -->

<!-- START_TASK_5 -->
### Task 5: Socket.IO WebSocket handler

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/socketio.go`
- Create: `/home/sysop/src/notebridge/internal/sync/notify.go`

**Implementation:**

**notify.go — Client registry:**

```go
type wsClient struct {
    userID     int64
    deviceType string
    send       chan string // buffered, size 16
    done       chan struct{}
}

type NotifyManager struct {
    mu      sync.RWMutex
    clients map[int64][]*wsClient // key: userID
}
```

Methods:
- `NewNotifyManager() *NotifyManager`
- `Register(client *wsClient)` — appends to user's client list
- `Unregister(client *wsClient)` — removes from user's client list, closes done channel
- `NotifyUser(userID int64, event, data string)` — sends to all clients for userID except the originator. Non-blocking: skip if send channel full.
- `NotifyAll(event, data string)` — sends to all connected clients

**socketio.go — WebSocket handler:**

`SocketIOHandler(auth *AuthService, notifier *NotifyManager, logger *slog.Logger) websocket.Handler`

Handler flow:
1. Read `token` and `type` (deviceType) query params from URL
2. Validate JWT via auth.ValidateJWTToken
3. If invalid: send error frame `44{"message":"Authentication failed"}` and close (AC3.4)
4. Generate random sid (16-byte hex nonce)
5. Send open packet: `0{"sid":"<sid>","upgrades":[],"pingInterval":25000,"pingTimeout":5000}` (AC3.1)
6. Send connect: `40`
7. Register client with notifier
8. Start write goroutine: reads from client.send channel, writes to WebSocket
9. Enter read loop:
   - Frame type '2' (ping): respond with '3' (pong) (AC3.2)
   - Frame "42" prefix: decode event
     - `ratta_ping`: respond with `42["ratta_ping","Received"]`
     - `ClientMessage` with "status": respond with `42["ClientMessage","true"]`
     - Other events: log and ignore
   - Read error: unregister client, exit loop
10. Defer: unregister client, close connection

The handler is mounted at the sync server (port 19071), matching SPC's Socket.IO endpoint.

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./...
```

**Commit:** `feat: add Socket.IO WebSocket handler and client registry`
<!-- END_TASK_5 -->

<!-- START_TASK_6 -->
### Task 6: Wire Socket.IO to event bus and server

**Files:**
- Modify: `/home/sysop/src/notebridge/internal/sync/server.go` — add NotifyManager, mount Socket.IO handler
- Modify: `/home/sysop/src/notebridge/cmd/notebridge/main.go` — create NotifyManager, subscribe to events

**Implementation:**

Add `notifier *NotifyManager` field to sync.Server. Mount Socket.IO handler:

```go
// In server.go Handler() method:
mux.Handle("/socket.io/", websocket.Handler(SocketIOHandler(s.auth, s.notifier, s.logger)))
```

In main.go, subscribe the notifier to file events:

```go
notifier := sync.NewNotifyManager()
eventBus.Subscribe(events.FileUploaded, func(e events.Event) {
    payload := buildServerMessage(e)
    notifier.NotifyUser(e.UserID, "ServerMessage", payload)
})
eventBus.Subscribe(events.FileModified, func(e events.Event) {
    payload := buildServerMessage(e)
    notifier.NotifyUser(e.UserID, "ServerMessage", payload)
})
eventBus.Subscribe(events.FileDeleted, func(e events.Event) {
    payload := buildServerMessage(e)
    notifier.NotifyUser(e.UserID, "ServerMessage", payload)
})
```

`buildServerMessage` creates the payload matching the SPC format:
```json
{
  "code": "200",
  "timestamp": <millis>,
  "msgType": "FILE-SYN",
  "data": [{"messageType": "STARTSYNC", "equipmentNo": "notebridge", "timestamp": <millis>}]
}
```

This triggers connected tablets to start a sync cycle (AC3.3).

**Verification:**

```bash
go build -C /home/sysop/src/notebridge ./cmd/notebridge/
```

**Commit:** `feat: wire Socket.IO to event bus for file change notifications`
<!-- END_TASK_6 -->

<!-- START_TASK_7 -->
### Task 7: Socket.IO and event integration tests

**Verifies:** AC3.1 (handshake), AC3.2 (ping/pong), AC3.3 (ServerMessage push), AC3.4 (invalid JWT rejection)

**Files:**
- Create: `/home/sysop/src/notebridge/internal/sync/socketio_test.go`
- Create: `/home/sysop/src/notebridge/internal/sync/engineio_test.go`

**Testing:**

**engineio_test.go — Frame encoding/decoding unit tests:**
- EncodeOpenPacket produces correct format with sid, intervals
- EncodeEvent with "ServerMessage" and JSON data produces `42["ServerMessage",{...}]`
- DecodeFrame correctly splits type byte from payload
- DecodeEvent extracts event name and data from `["name", data]` format
- Round-trip: EncodeEvent → strip "42" prefix → DecodeEvent recovers original

**socketio_test.go — Socket.IO handler integration tests:**

Use `httptest.NewServer` with the full sync server handler. Connect via `golang.org/x/net/websocket` as client.

Test helper: `connectSocketIO(t, serverURL, token string) *websocket.Conn` — dials `/socket.io/?token=<token>&type=test&EIO=3&transport=websocket`.

- AC3.1 handshake: connect with valid token, read first frame → type '0' with sid, pingInterval=25000, pingTimeout=5000. Read second frame → "40" (connect ack).

- AC3.2 ping/pong: after handshake, send "2" (ping), read response → "3" (pong)

- AC3.3 ServerMessage push:
  1. Connect device A via Socket.IO
  2. Upload a file via HTTP (triggers FileUploadedEvent → event bus → notifier)
  3. Read from device A's WebSocket → receives `42["ServerMessage",{...}]` with msgType "FILE-SYN"

- AC3.4 invalid JWT: connect with `token=garbage`, read response → error frame with auth failure message, connection closed

- ratta_ping: send `42["ratta_ping",{}]`, read response → `42["ratta_ping","Received"]`

- ClientMessage status: send `42["ClientMessage",{"status":"query"}]`, read response → `42["ClientMessage","true"]`

- Multiple devices: connect two clients for same user, upload file → both receive ServerMessage

- Disconnect cleanup: connect, disconnect, verify client removed from registry (no panic on subsequent NotifyUser)

**Verification:**

```bash
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestSocket
go test -C /home/sysop/src/notebridge ./internal/sync/ -run TestEngineIO
```

Expected: All tests pass.

**Commit:** `test: add Socket.IO and Engine.IO tests`
<!-- END_TASK_7 -->
<!-- END_SUBCOMPONENT_B -->
