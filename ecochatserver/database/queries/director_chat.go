package queries

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// DirectorChatMsg represents a persisted director chat message.
type DirectorChatMsg struct {
	ID        int
	AdminID   string
	Role      string
	Content   string
	CreatedAt time.Time
}

// DirectorChatCompaction represents a summarized block of old chat messages.
type DirectorChatCompaction struct {
	ID                   uuid.UUID
	AdminID              string
	Summary              string
	MessagesFrom         time.Time
	MessagesTo           time.Time
	MessageCount         int
	SourceMessages       json.RawMessage
	PreviousCompactionID *uuid.UUID
	CreatedAt            time.Time
}

// ============================================================================
// Chat Messages CRUD
// ============================================================================

// SaveDirectorChatMessage inserts a single message into director_chat_messages.
func SaveDirectorChatMessage(db *sql.DB, adminID, role, content string) error {
	ctx, cancel := WithDBContext()
	defer cancel()
	_, err := db.ExecContext(ctx,
		`INSERT INTO director_chat_messages (admin_id, role, content) VALUES ($1, $2, $3)`,
		adminID, role, content)
	return err
}

// LoadDirectorChatHistory loads the last N messages for an admin, ordered by created_at.
func LoadDirectorChatHistory(db *sql.DB, adminID string, limit int) ([]DirectorChatMsg, error) {
	ctx, cancel := WithDBContext()
	defer cancel()
	rows, err := db.QueryContext(ctx,
		`SELECT id, admin_id, role, content, created_at
		 FROM director_chat_messages
		 WHERE admin_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`, adminID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []DirectorChatMsg
	for rows.Next() {
		var m DirectorChatMsg
		if err := rows.Scan(&m.ID, &m.AdminID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to get chronological order (oldest first)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// ClearDirectorChatHistory deletes all messages for an admin.
func ClearDirectorChatHistory(db *sql.DB, adminID string) error {
	ctx, cancel := WithDBContext()
	defer cancel()
	_, err := db.ExecContext(ctx,
		`DELETE FROM director_chat_messages WHERE admin_id = $1`, adminID)
	return err
}

// DeleteOldDirectorChatMessages removes messages that were already compacted.
// Keeps only messages newer than the cutoff timestamp.
func DeleteOldDirectorChatMessages(db *sql.DB, adminID string, olderThan time.Time) (int64, error) {
	ctx, cancel := WithDBContext()
	defer cancel()
	res, err := db.ExecContext(ctx,
		`DELETE FROM director_chat_messages WHERE admin_id = $1 AND created_at <= $2`,
		adminID, olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ============================================================================
// Chat Compactions CRUD
// ============================================================================

// SaveCompaction inserts a new compaction summary.
func SaveCompaction(db *sql.DB, adminID, summary string, messagesFrom, messagesTo time.Time,
	messageCount int, sourceMessages json.RawMessage, previousID *uuid.UUID) (uuid.UUID, error) {
	ctx, cancel := WithDBContext()
	defer cancel()
	id := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO director_chat_compactions
		 (id, admin_id, summary, messages_from, messages_to, message_count, source_messages, previous_compaction_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, adminID, summary, messagesFrom, messagesTo, messageCount, sourceMessages, previousID)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GetLatestCompaction returns the most recent compaction for an admin.
func GetLatestCompaction(db *sql.DB, adminID string) (*DirectorChatCompaction, error) {
	ctx, cancel := WithDBContext()
	defer cancel()
	row := db.QueryRowContext(ctx,
		`SELECT id, admin_id, summary, messages_from, messages_to, message_count,
		        source_messages, previous_compaction_id, created_at
		 FROM director_chat_compactions
		 WHERE admin_id = $1
		 ORDER BY created_at DESC LIMIT 1`, adminID)

	var c DirectorChatCompaction
	var srcMsgs sql.NullString
	var prevID *uuid.UUID

	err := row.Scan(&c.ID, &c.AdminID, &c.Summary, &c.MessagesFrom, &c.MessagesTo,
		&c.MessageCount, &srcMsgs, &prevID, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if srcMsgs.Valid {
		c.SourceMessages = json.RawMessage(srcMsgs.String)
	}
	c.PreviousCompactionID = prevID
	return &c, nil
}

// DeleteCompactionsForAdmin removes all compactions for an admin (used when clearing chat).
func DeleteCompactionsForAdmin(db *sql.DB, adminID string) error {
	ctx, cancel := WithDBContext()
	defer cancel()
	_, err := db.ExecContext(ctx,
		`DELETE FROM director_chat_compactions WHERE admin_id = $1`, adminID)
	return err
}

// DeleteOldCompactions removes compactions older than retentionDays.
// Returns the number of deleted rows. If retentionDays <= 0, does nothing.
func DeleteOldCompactions(db *sql.DB, retentionDays int) (int64, error) {
	if retentionDays <= 0 {
		return 0, nil // infinite retention
	}
	ctx, cancel := WithDBContext()
	defer cancel()
	res, err := db.ExecContext(ctx,
		`DELETE FROM director_chat_compactions WHERE created_at < NOW() - ($1 || ' days')::INTERVAL`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
