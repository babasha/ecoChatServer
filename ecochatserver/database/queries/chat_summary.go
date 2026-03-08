package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertChatSummary saves a new conversation summary
func InsertChatSummary(db *sql.DB, chatID uuid.UUID, summary string, messagesFrom, messagesTo time.Time, messageCount int) (*models.ChatSummary, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	s := &models.ChatSummary{
		ID:           uuid.New(),
		ChatID:       chatID,
		Summary:      summary,
		MessagesFrom: messagesFrom,
		MessagesTo:   messagesTo,
		MessageCount: messageCount,
		CreatedAt:    time.Now(),
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO chat_summaries (id, chat_id, summary, messages_from, messages_to, message_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		s.ID, s.ChatID, s.Summary, s.MessagesFrom, s.MessagesTo, s.MessageCount, s.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert chat summary: %w", err)
	}

	return s, nil
}

// GetLatestChatSummary returns the most recent summary for a chat
func GetLatestChatSummary(db *sql.DB, chatID uuid.UUID) (*models.ChatSummary, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var s models.ChatSummary
	err := db.QueryRowContext(ctx, `
		SELECT id, chat_id, summary, messages_from, messages_to, message_count, created_at
		FROM chat_summaries
		WHERE chat_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, chatID,
	).Scan(&s.ID, &s.ChatID, &s.Summary, &s.MessagesFrom, &s.MessagesTo, &s.MessageCount, &s.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest chat summary: %w", err)
	}
	return &s, nil
}

// CountMessagesSince counts messages in a chat after a given timestamp
func CountMessagesSince(db *sql.DB, chatID uuid.UUID, since time.Time) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages
		WHERE chat_id = $1 AND timestamp > $2`,
		chatID, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages since: %w", err)
	}
	return count, nil
}

// CountAllMessages counts all messages in a chat
func CountAllMessages(db *sql.DB, chatID uuid.UUID) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM messages WHERE chat_id = $1`, chatID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count all messages: %w", err)
	}
	return count, nil
}

// GetMessagesSince returns messages after a given timestamp, ordered chronologically
func GetMessagesSince(db *sql.DB, chatID uuid.UUID, since time.Time, limit int) ([]models.Message, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, chat_id, content, sender, sender_id, timestamp, read, type
		FROM messages
		WHERE chat_id = $1 AND timestamp > $2
		ORDER BY timestamp ASC
		LIMIT $3`,
		chatID, since, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages since: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// GetRecentMessages returns the most recent messages for a chat, ordered chronologically
func GetRecentMessages(db *sql.DB, chatID uuid.UUID, limit int) ([]models.Message, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, chat_id, content, sender, sender_id, timestamp, read, type
		FROM (
			SELECT id, chat_id, content, sender, sender_id, timestamp, read, type
			FROM messages
			WHERE chat_id = $1
			ORDER BY timestamp DESC
			LIMIT $2
		) sub
		ORDER BY timestamp ASC`,
		chatID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// GetMessagesForSummary returns messages in a range for summarization
func GetMessagesForSummary(db *sql.DB, chatID uuid.UUID, since time.Time, limit int) ([]models.Message, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var rows *sql.Rows
	var err error

	if since.IsZero() {
		// First summary — get oldest messages
		rows, err = db.QueryContext(ctx, `
			SELECT id, chat_id, content, sender, sender_id, timestamp, read, type
			FROM messages
			WHERE chat_id = $1
			ORDER BY timestamp ASC
			LIMIT $2`,
			chatID, limit,
		)
	} else {
		// Subsequent summary — get messages after last summary
		rows, err = db.QueryContext(ctx, `
			SELECT id, chat_id, content, sender, sender_id, timestamp, read, type
			FROM messages
			WHERE chat_id = $1 AND timestamp > $2
			ORDER BY timestamp ASC
			LIMIT $3`,
			chatID, since, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get messages for summary: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func scanMessages(rows *sql.Rows) ([]models.Message, error) {
	var messages []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Content, &m.Sender, &m.SenderID, &m.Timestamp, &m.Read, &m.Type); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}
