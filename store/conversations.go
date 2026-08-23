package store

import (
	"context"
	"fmt"
	"time"
)

// Conversation is a saved chat session's metadata.
type Conversation struct {
	ID        int64
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message is a single turn in a saved conversation.
type Message struct {
	ID        int64
	Role      string
	Content   string
	CreatedAt time.Time
}

// CreateConversation inserts a new conversation titled title and returns
// its ID.
func (s *Store) CreateConversation(ctx context.Context, title string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO conversations (title) VALUES (?)`, title)
	if err != nil {
		return 0, fmt.Errorf("store: creating conversation: %w", err)
	}
	return res.LastInsertId()
}

// AddMessage appends a message to a conversation and bumps the
// conversation's updated_at, so ListConversations can order by recency.
func (s *Store) AddMessage(ctx context.Context, conversationID int64, role, content string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: adding message: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `INSERT INTO messages (conversation_id, role, content) VALUES (?, ?, ?)`, conversationID, role, content); err != nil {
		return fmt.Errorf("store: adding message: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, conversationID); err != nil {
		return fmt.Errorf("store: updating conversation timestamp: %w", err)
	}

	return tx.Commit()
}

// ListConversations returns every saved conversation, most recently
// updated first.
func (s *Store) ListConversations(ctx context.Context) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, created_at, updated_at FROM conversations
		ORDER BY updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("store: listing conversations: %w", err)
	}
	defer rows.Close()

	var convos []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning conversation: %w", err)
		}
		convos = append(convos, c)
	}
	return convos, rows.Err()
}

// GetMessages returns a conversation's messages, oldest first.
func (s *Store) GetMessages(ctx context.Context, conversationID int64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, role, content, created_at FROM messages
		WHERE conversation_id = ?
		ORDER BY id ASC
	`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("store: getting messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scanning message: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// DeleteConversation removes a conversation and (via ON DELETE CASCADE) its
// messages.
func (s *Store) DeleteConversation(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM conversations WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting conversation %d: %w", id, err)
	}
	return nil
}
