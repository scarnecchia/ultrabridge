package chat

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Session represents a chat conversation session.
type Session struct {
	ID        int64
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message represents a single message in a chat session.
type Message struct {
	ID        int64
	SessionID int64
	Role      string // "user", "assistant", "system"
	Content   string
	CreatedAt time.Time
}

// Store handles chat session and message persistence.
type Store struct {
	db *sql.DB
}

// NewStore creates a new chat store.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateSession creates a new chat session with the given title.
func (s *Store) CreateSession(ctx context.Context, title string) (*Session, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (title, created_at, updated_at) VALUES (?, ?, ?)`,
		title, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	id, _ := result.LastInsertId()
	return &Session{ID: id, Title: title, CreatedAt: time.Unix(now, 0), UpdatedAt: time.Unix(now, 0)}, nil
}

// ListSessions returns all chat sessions ordered by updated_at descending.
func (s *Store) ListSessions(ctx context.Context) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, created_at, updated_at FROM chat_sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var createdAt, updatedAt int64
		if err := rows.Scan(&sess.ID, &sess.Title, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		sess.CreatedAt = time.Unix(createdAt, 0)
		sess.UpdatedAt = time.Unix(updatedAt, 0)
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// AddMessage adds a new message to a session and updates the session's updated_at timestamp.
func (s *Store) AddMessage(ctx context.Context, sessionID int64, role, content string) (*Message, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_messages (session_id, role, content, created_at) VALUES (?, ?, ?, ?)`,
		sessionID, role, content, now,
	)
	if err != nil {
		return nil, fmt.Errorf("add message: %w", err)
	}
	// Update session's updated_at
	s.db.ExecContext(ctx, `UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, now, sessionID)

	id, _ := result.LastInsertId()
	return &Message{ID: id, SessionID: sessionID, Role: role, Content: content, CreatedAt: time.Unix(now, 0)}, nil
}

// GetMessages returns all messages in a session ordered by created_at ascending.
func (s *Store) GetMessages(ctx context.Context, sessionID int64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, created_at FROM chat_messages WHERE session_id = ? ORDER BY created_at ASC`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteSession deletes a session and all its messages.
func (s *Store) DeleteSession(ctx context.Context, sessionID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_messages WHERE session_id = ?`, sessionID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = ?`, sessionID)
	return err
}
