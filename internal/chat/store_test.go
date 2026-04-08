package chat

import (
	"context"
	"testing"
	"time"

	"github.com/sysop/ultrabridge/internal/notedb"
)

func TestStoreCreateSession(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, err := store.CreateSession(ctx, "Test Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if session.ID == 0 {
		t.Errorf("session.ID = 0, want > 0")
	}
	if session.Title != "Test Session" {
		t.Errorf("session.Title = %q, want %q", session.Title, "Test Session")
	}
	if session.CreatedAt.IsZero() {
		t.Errorf("session.CreatedAt is zero")
	}
	if session.UpdatedAt.IsZero() {
		t.Errorf("session.UpdatedAt is zero")
	}
}

func TestStoreListSessionsEmpty(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(sessions) != 0 {
		t.Errorf("len(sessions) = %d, want 0", len(sessions))
	}
}

func TestStoreListSessionsOrdered(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	// Create three sessions
	sess1, _ := store.CreateSession(ctx, "Session 1")
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	sess2, _ := store.CreateSession(ctx, "Session 2")
	time.Sleep(10 * time.Millisecond)
	sess3, _ := store.CreateSession(ctx, "Session 3")

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	if len(sessions) != 3 {
		t.Errorf("len(sessions) = %d, want 3", len(sessions))
	}

	// Sessions should be ordered by updated_at descending (most recent first)
	if sessions[0].ID != sess3.ID || sessions[1].ID != sess2.ID || sessions[2].ID != sess1.ID {
		t.Errorf("sessions not in descending updated_at order")
	}
}

func TestStoreAddMessage(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, _ := store.CreateSession(ctx, "Test Session")

	message, err := store.AddMessage(ctx, session.ID, "user", "Hello, world!")
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}

	if message.ID == 0 {
		t.Errorf("message.ID = 0, want > 0")
	}
	if message.SessionID != session.ID {
		t.Errorf("message.SessionID = %d, want %d", message.SessionID, session.ID)
	}
	if message.Role != "user" {
		t.Errorf("message.Role = %q, want %q", message.Role, "user")
	}
	if message.Content != "Hello, world!" {
		t.Errorf("message.Content = %q, want %q", message.Content, "Hello, world!")
	}
	if message.CreatedAt.IsZero() {
		t.Errorf("message.CreatedAt is zero")
	}
}

func TestStoreGetMessages(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, _ := store.CreateSession(ctx, "Test Session")

	// Add multiple messages
	store.AddMessage(ctx, session.ID, "user", "First message")
	time.Sleep(10 * time.Millisecond)
	store.AddMessage(ctx, session.ID, "assistant", "Second message")
	time.Sleep(10 * time.Millisecond)
	store.AddMessage(ctx, session.ID, "user", "Third message")

	messages, err := store.GetMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	if len(messages) != 3 {
		t.Errorf("len(messages) = %d, want 3", len(messages))
	}

	// Messages should be ordered by created_at ascending
	if messages[0].Content != "First message" {
		t.Errorf("messages[0].Content = %q, want %q", messages[0].Content, "First message")
	}
	if messages[1].Content != "Second message" {
		t.Errorf("messages[1].Content = %q, want %q", messages[1].Content, "Second message")
	}
	if messages[2].Content != "Third message" {
		t.Errorf("messages[2].Content = %q, want %q", messages[2].Content, "Third message")
	}

	// Verify roles
	if messages[0].Role != "user" {
		t.Errorf("messages[0].Role = %q, want %q", messages[0].Role, "user")
	}
	if messages[1].Role != "assistant" {
		t.Errorf("messages[1].Role = %q, want %q", messages[1].Role, "assistant")
	}
}

func TestStoreGetMessagesEmpty(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, _ := store.CreateSession(ctx, "Test Session")

	messages, err := store.GetMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	if len(messages) != 0 {
		t.Errorf("len(messages) = %d, want 0", len(messages))
	}
}

func TestStoreAddMessageUpdatesSessionUpdatedAt(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, _ := store.CreateSession(ctx, "Test Session")
	originalUpdatedAt := session.UpdatedAt

	time.Sleep(50 * time.Millisecond)

	store.AddMessage(ctx, session.ID, "user", "A message")

	// Retrieve the session again to check updated_at
	sessions, _ := store.ListSessions(ctx)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	updatedSession := sessions[0]
	if updatedSession.UpdatedAt.Before(originalUpdatedAt) {
		t.Errorf("session.UpdatedAt was not updated. original: %v, new: %v", originalUpdatedAt, updatedSession.UpdatedAt)
	}
}

func TestStoreDeleteSession(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	session, _ := store.CreateSession(ctx, "Test Session")
	store.AddMessage(ctx, session.ID, "user", "Message 1")
	store.AddMessage(ctx, session.ID, "assistant", "Message 2")

	// Verify messages were added
	messages, _ := store.GetMessages(ctx, session.ID)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// Delete the session
	err = store.DeleteSession(ctx, session.ID)
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// Verify session is deleted
	sessions, _ := store.ListSessions(ctx)
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}

	// Verify messages are also deleted
	messages, _ = store.GetMessages(ctx, session.ID)
	if len(messages) != 0 {
		t.Errorf("expected 0 messages after session deletion, got %d", len(messages))
	}
}

func TestStoreMultipleSessions(t *testing.T) {
	db, err := notedb.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("notedb.Open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()

	// Create two sessions with messages
	sess1, _ := store.CreateSession(ctx, "Session 1")
	store.AddMessage(ctx, sess1.ID, "user", "Message in session 1")

	sess2, _ := store.CreateSession(ctx, "Session 2")
	store.AddMessage(ctx, sess2.ID, "user", "Message in session 2")

	// Verify messages are isolated by session
	msgs1, _ := store.GetMessages(ctx, sess1.ID)
	msgs2, _ := store.GetMessages(ctx, sess2.ID)

	if len(msgs1) != 1 {
		t.Errorf("expected 1 message in session 1, got %d", len(msgs1))
	}
	if len(msgs2) != 1 {
		t.Errorf("expected 1 message in session 2, got %d", len(msgs2))
	}

	if msgs1[0].Content != "Message in session 1" {
		t.Errorf("session 1 message content wrong: %q", msgs1[0].Content)
	}
	if msgs2[0].Content != "Message in session 2" {
		t.Errorf("session 2 message content wrong: %q", msgs2[0].Content)
	}
}
