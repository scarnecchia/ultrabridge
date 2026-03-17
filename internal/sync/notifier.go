package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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

	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}

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
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, n.url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}

	// Read open packet (type 0)
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("read open: %w", err)
	}
	if len(msg) == 0 || msg[0] != '0' {
		conn.Close()
		return fmt.Errorf("unexpected open packet: %s", msg)
	}
	n.logger.Info("socket.io connected", "handshake", string(msg[1:]))

	// Send Socket.IO connect
	if err := conn.WriteMessage(websocket.TextMessage, []byte("40")); err != nil {
		conn.Close()
		return fmt.Errorf("send connect: %w", err)
	}

	n.mu.Lock()
	n.conn = conn
	n.mu.Unlock()

	// Read loop: handle pings and messages
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			n.mu.Lock()
			n.conn = nil
			n.mu.Unlock()
			return fmt.Errorf("read: %w", err)
		}
		if len(msg) > 0 && msg[0] == '2' {
			// Ping — respond with pong
			if err := conn.WriteMessage(websocket.TextMessage, []byte("3")); err != nil {
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

	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
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
		n.conn.Close()
		n.conn = nil
	}
}
