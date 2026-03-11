package queries

import (
	"database/sql"
	"time"
)

// DirectorChatMsg represents a persisted director chat message.
type DirectorChatMsg struct {
	ID        int
	AdminID   string
	Role      string
	Content   string
	CreatedAt time.Time
}

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
