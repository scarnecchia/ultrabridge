package sync

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockWebSocketServer represents a mock Engine.IO v3 server
type mockWebSocketServer struct {
	upgrader          websocket.Upgrader
	messages          []string
	mu                sync.Mutex
	connectedChan     chan struct{}
	pingResponseChan  chan struct{}
	startsyncRecvChan chan string
}

func newMockWebSocketServer() *mockWebSocketServer {
	return &mockWebSocketServer{
		messages:          []string{},
		connectedChan:     make(chan struct{}, 1),
		pingResponseChan:  make(chan struct{}, 10),
		startsyncRecvChan: make(chan string, 10),
	}
}

func (m *mockWebSocketServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Send Engine.IO open packet (type 0)
	openPacket := `0{"sid":"test-sid","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(openPacket)); err != nil {
		return
	}

	// Read Socket.IO connect (expect "40")
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return
	}
	if string(msg) != "40" {
		return
	}

	m.mu.Lock()
	select {
	case m.connectedChan <- struct{}{}:
	default:
	}
	m.mu.Unlock()

	// Read loop: handle Socket.IO messages and respond to pings
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		msgStr := string(msg)
		m.mu.Lock()
		m.messages = append(m.messages, msgStr)
		m.mu.Unlock()

		// Handle ping
		if msgStr == "2" {
			conn.WriteMessage(websocket.TextMessage, []byte("3"))
			m.mu.Lock()
			select {
			case m.pingResponseChan <- struct{}{}:
			default:
			}
			m.mu.Unlock()
		}

		// Handle STARTSYNC message (42[...])
		if strings.HasPrefix(msgStr, "42[") {
			m.mu.Lock()
			select {
			case m.startsyncRecvChan <- msgStr:
			default:
			}
			m.mu.Unlock()
		}
	}
}

// TestNotifierConnect verifies Engine.IO v3 connection handshake
func TestNotifierConnect(t *testing.T) {
	// Create mock server
	mockServer := newMockWebSocketServer()
	server := httptest.NewServer(mockServer)
	defer server.Close()

	// Convert HTTP URL to WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if !strings.Contains(wsURL, "/socket.io/") {
		wsURL += "/socket.io/"
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := NewNotifier(wsURL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	notifier.Connect(ctx)

	// Wait for connection to establish
	select {
	case <-mockServer.connectedChan:
		// Connection successful
	case <-ctx.Done():
		t.Fatalf("connection handshake timeout")
	}

	notifier.Close()
}

// TestNotifierPingPong verifies ping/pong keepalive handling
func TestNotifierPingPong(t *testing.T) {
	mockServer := newMockWebSocketServer()
	// We need a custom handler to send ping from server side
	testDone := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := mockServer.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send Engine.IO open packet (type 0)
		openPacket := `0{"sid":"test-sid","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(openPacket)); err != nil {
			return
		}

		// Read Socket.IO connect (expect "40")
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if string(msg) != "40" {
			return
		}

		// Signal connection established
		mockServer.mu.Lock()
		select {
		case mockServer.connectedChan <- struct{}{}:
		default:
		}
		mockServer.mu.Unlock()

		// Send a ping from server and wait for pong response
		if err := conn.WriteMessage(websocket.TextMessage, []byte("2")); err != nil {
			return
		}

		// Wait for pong response
		_, msg, err = conn.ReadMessage()
		if err != nil {
			return
		}
		if string(msg) == "3" {
			// Pong received correctly
			mockServer.mu.Lock()
			select {
			case mockServer.pingResponseChan <- struct{}{}:
			default:
			}
			mockServer.mu.Unlock()
		}

		close(testDone)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if !strings.Contains(wsURL, "/socket.io/") {
		wsURL += "/socket.io/"
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := NewNotifier(wsURL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	notifier.Connect(ctx)

	// Wait for connection
	select {
	case <-mockServer.connectedChan:
	case <-ctx.Done():
		t.Fatalf("connection timeout")
	}

	// Wait for pong response from server side
	select {
	case <-mockServer.pingResponseChan:
		// Pong received
	case <-ctx.Done():
		t.Fatalf("pong response timeout")
	}

	notifier.Close()
	<-testDone
}

// TestNotifySuccess verifies STARTSYNC message is sent with correct format (AC3.5)
func TestNotifySuccess(t *testing.T) {
	mockServer := newMockWebSocketServer()
	server := httptest.NewServer(mockServer)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if !strings.Contains(wsURL, "/socket.io/") {
		wsURL += "/socket.io/"
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := NewNotifier(wsURL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	notifier.Connect(ctx)

	// Wait for connection
	select {
	case <-mockServer.connectedChan:
	case <-ctx.Done():
		t.Fatalf("connection timeout")
	}

	// Call Notify
	err := notifier.Notify(ctx)
	if err != nil {
		t.Fatalf("Notify failed: %v", err)
	}

	// Wait for STARTSYNC message to be received by server
	select {
	case msg := <-mockServer.startsyncRecvChan:
		// Verify message format: 42["ServerMessage","{\\"code\\":\\"200\\",...}"]
		// The JSON is escaped when put into the Socket.IO message
		if !strings.HasPrefix(msg, `42["ServerMessage",`) {
			t.Errorf("invalid message format: %q", msg)
		}

		// Parse as Socket.IO message: we have a text string that looks like:
		// 42["ServerMessage","{\"code\":\"200\",...}"]
		// This is a JSON array with two elements: a string and a string
		var socketMsg []interface{}
		err := json.Unmarshal([]byte(msg[2:]), &socketMsg) // Skip "42" prefix
		if err != nil {
			t.Errorf("failed to parse socket.io message: %v", err)
		}

		if len(socketMsg) < 2 {
			t.Errorf("socket.io message has wrong length: %d", len(socketMsg))
		}

		// Second element should be the payload JSON string
		payloadStr, ok := socketMsg[1].(string)
		if !ok {
			t.Errorf("payload is not a string: %v", socketMsg[1])
		}

		// Parse the payload JSON
		var payload map[string]interface{}
		err = json.Unmarshal([]byte(payloadStr), &payload)
		if err != nil {
			t.Errorf("failed to parse payload JSON: %v, got: %q", err, payloadStr)
		}

		// Verify required fields
		if payload["code"] != "200" {
			t.Errorf("code: got %v, want 200", payload["code"])
		}
		if payload["msgType"] != "FILE-SYN" {
			t.Errorf("msgType: got %v, want FILE-SYN", payload["msgType"])
		}
		if _, hasTimestamp := payload["timestamp"]; !hasTimestamp {
			t.Errorf("timestamp field missing")
		}

		data, ok := payload["data"].([]interface{})
		if !ok || len(data) == 0 {
			t.Errorf("data field invalid or empty: %v", payload["data"])
		} else {
			dataItem, ok := data[0].(map[string]interface{})
			if !ok {
				t.Errorf("data[0] not a map: %v", data[0])
			} else {
				if dataItem["messageType"] != "STARTSYNC" {
					t.Errorf("messageType: got %v, want STARTSYNC", dataItem["messageType"])
				}
				if dataItem["equipmentNo"] != "ultrabridge" {
					t.Errorf("equipmentNo: got %v, want ultrabridge", dataItem["equipmentNo"])
				}
			}
		}

	case <-ctx.Done():
		t.Fatalf("STARTSYNC message timeout")
	}

	notifier.Close()
}

// TestNotifyNotConnected verifies Notify returns error when not connected (AC3.6)
func TestNotifyNotConnected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create notifier without connecting
	notifier := NewNotifier("ws://localhost:9999/socket.io/", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := notifier.Notify(ctx)
	if err == nil {
		t.Errorf("Notify should return error when not connected")
	}
}

// TestNotifyNotConnectedDoesNotPanic verifies Notify doesn't panic when not connected (AC3.6)
func TestNotifyNotConnectedDoesNotPanic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := NewNotifier("ws://localhost:9999/socket.io/", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Notify panicked: %v", r)
		}
	}()

	_ = notifier.Notify(ctx)
}

// TestNotifierReconnect verifies notifier reconnects after connection drop
func TestNotifierReconnect(t *testing.T) {
	mockServer := newMockWebSocketServer()
	connCount := 0
	connCountMu := sync.Mutex{}
	reconnectChan := make(chan struct{}, 5)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCountMu.Lock()
		connCount++
		connCountMu.Unlock()

		conn, err := mockServer.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Send Engine.IO open packet (type 0)
		openPacket := `0{"sid":"test-sid","upgrades":[],"pingInterval":25000,"pingTimeout":60000}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(openPacket)); err != nil {
			return
		}

		// Read Socket.IO connect (expect "40")
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if string(msg) != "40" {
			return
		}

		// Signal connection established
		mockServer.mu.Lock()
		select {
		case mockServer.connectedChan <- struct{}{}:
		default:
		}
		select {
		case reconnectChan <- struct{}{}:
		default:
		}
		mockServer.mu.Unlock()

		// On first connection, close immediately to simulate drop
		connCountMu.Lock()
		isFirstConn := connCount == 1
		connCountMu.Unlock()

		if isFirstConn {
			return // Close connection immediately
		}

		// On reconnection, stay open and read messages
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	if !strings.Contains(wsURL, "/socket.io/") {
		wsURL += "/socket.io/"
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := NewNotifier(wsURL, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	notifier.Connect(ctx)

	// Wait for first connection
	select {
	case <-reconnectChan:
	case <-ctx.Done():
		t.Fatalf("initial connection timeout")
	}

	// Wait for reconnection (should happen within ~5 seconds due to reconnect delay)
	select {
	case <-reconnectChan:
		// Reconnection successful
	case <-ctx.Done():
		t.Fatalf("reconnect timeout")
	}

	connCountMu.Lock()
	if connCount < 2 {
		t.Errorf("notifier did not reconnect: connCount=%d, want >= 2", connCount)
	}
	connCountMu.Unlock()

	notifier.Close()
}
