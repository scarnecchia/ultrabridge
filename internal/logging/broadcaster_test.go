package logging

import (
	"testing"
	"time"
)

// TestSubscribeReturnsChannel verifies that Subscribe returns a channel that receives entries
func TestSubscribeReturnsChannel(t *testing.T) {
	broadcaster := NewLogBroadcaster()

	id, ch := broadcaster.Subscribe()
	if id < 0 {
		t.Errorf("Subscribe returned invalid ID: %d", id)
	}
	if ch == nil {
		t.Errorf("Subscribe returned nil channel")
	}
	broadcaster.Unsubscribe(id)
}

// TestBroadcastSingleSubscriber verifies entry broadcast to a single subscriber
func TestBroadcastSingleSubscriber(t *testing.T) {
	broadcaster := NewLogBroadcaster()
	id, ch := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(id)

	// Broadcast an entry
	broadcaster.Broadcast("[INFO] test message")

	// Verify subscriber receives it
	select {
	case msg := <-ch:
		if msg != "[INFO] test message" {
			t.Errorf("Got message %q, want %q", msg, "[INFO] test message")
		}
	case <-time.After(1 * time.Second):
		t.Errorf("Did not receive message within timeout")
	}
}

// TestBroadcastMultipleSubscribers verifies that all subscribers receive the same entry
func TestBroadcastMultipleSubscribers(t *testing.T) {
	broadcaster := NewLogBroadcaster()

	// Create 3 subscribers
	id1, ch1 := broadcaster.Subscribe()
	id2, ch2 := broadcaster.Subscribe()
	id3, ch3 := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(id1)
	defer broadcaster.Unsubscribe(id2)
	defer broadcaster.Unsubscribe(id3)

	// Broadcast an entry
	testMessage := "[WARN] multiple subscribers"
	broadcaster.Broadcast(testMessage)

	// Verify all subscribers receive it
	timeout := 1 * time.Second
	for i, ch := range []<-chan string{ch1, ch2, ch3} {
		select {
		case msg := <-ch:
			if msg != testMessage {
				t.Errorf("Subscriber %d got message %q, want %q", i+1, msg, testMessage)
			}
		case <-time.After(timeout):
			t.Errorf("Subscriber %d did not receive message within timeout", i+1)
		}
	}
}

// TestUnsubscribe verifies that Unsubscribe stops delivery to that subscriber
func TestUnsubscribe(t *testing.T) {
	broadcaster := NewLogBroadcaster()

	// Subscribe and get IDs
	id1, ch1 := broadcaster.Subscribe()
	id2, ch2 := broadcaster.Subscribe()

	// Broadcast first message
	broadcaster.Broadcast("[INFO] first message")

	// Consume from both
	timeout := 1 * time.Second
	select {
	case msg := <-ch1:
		if msg != "[INFO] first message" {
			t.Errorf("ch1 got unexpected message: %q", msg)
		}
	case <-time.After(timeout):
		t.Errorf("ch1 should receive first message")
	}

	select {
	case msg := <-ch2:
		if msg != "[INFO] first message" {
			t.Errorf("ch2 got unexpected message: %q", msg)
		}
	case <-time.After(timeout):
		t.Errorf("ch2 should receive first message")
	}

	// Unsubscribe ch1
	broadcaster.Unsubscribe(id1)

	// Broadcast second message
	broadcaster.Broadcast("[INFO] second message")

	// Verify ch2 receives it but ch1 does not
	timeout = 100 * time.Millisecond
	select {
	case msg := <-ch2:
		if msg != "[INFO] second message" {
			t.Errorf("ch2 got unexpected message: %q", msg)
		}
	case <-time.After(timeout):
		t.Errorf("ch2 should receive second message")
	}

	// ch1 should be closed now
	select {
	case msg, ok := <-ch1:
		if ok {
			t.Errorf("ch1 should be closed after unsubscribe, got message: %q", msg)
		}
		// Channel is closed, which is expected
	case <-time.After(timeout):
		// Expected: ch1 is closed and won't receive
	}

	broadcaster.Unsubscribe(id2)
}

// TestBroadcasterMultipleMessages verifies broadcasting several messages in sequence
func TestBroadcasterMultipleMessages(t *testing.T) {
	broadcaster := NewLogBroadcaster()
	id, ch := broadcaster.Subscribe()
	defer broadcaster.Unsubscribe(id)

	// Broadcast multiple messages quickly
	messages := []string{"[INFO] msg1", "[INFO] msg2", "[INFO] msg3"}
	for _, msg := range messages {
		broadcaster.Broadcast(msg)
	}

	// Collect all messages
	received := 0
	timeout := 1 * time.Second
	for {
		select {
		case msg := <-ch:
			if msg != "" {
				received++
			}
			if received >= len(messages) {
				return
			}
		case <-time.After(timeout):
			t.Errorf("Expected %d messages, received %d", len(messages), received)
			return
		}
	}
}
