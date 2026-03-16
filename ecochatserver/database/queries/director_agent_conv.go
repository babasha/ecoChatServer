package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DirectorAgentMessage represents one exchange in a Director↔Agent conversation.
type DirectorAgentMessage struct {
	ID        uuid.UUID `json:"id"`
	ChatID    string    `json:"chat_id"`
	Direction string    `json:"direction"` // "director_to_agent" or "agent_to_director"
	Sender    string    `json:"sender"`
	Message   string    `json:"message"`
	Response  string    `json:"response"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	CreatedAt time.Time `json:"created_at"`
}

// InsertDirectorAgentConversation logs one Director↔Agent exchange.
func InsertDirectorAgentConversation(db *sql.DB, chatID, direction, sender, message, response string, tokensIn, tokensOut int) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO director_agent_conversations
			(chat_id, direction, sender, message, response, tokens_in, tokens_out)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		chatID, direction, sender, message, response, tokensIn, tokensOut,
	)
	if err != nil {
		return fmt.Errorf("insert director-agent conversation: %w", err)
	}
	return nil
}

// GetDirectorAgentConversations returns recent Director↔Agent exchanges.
func GetDirectorAgentConversations(db *sql.DB, limit int) ([]DirectorAgentMessage, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, chat_id, direction, sender, message, response, tokens_in, tokens_out, created_at
		FROM director_agent_conversations
		ORDER BY created_at DESC
		LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get director-agent conversations: %w", err)
	}
	defer rows.Close()

	var msgs []DirectorAgentMessage
	for rows.Next() {
		var m DirectorAgentMessage
		if err := rows.Scan(&m.ID, &m.ChatID, &m.Direction, &m.Sender, &m.Message, &m.Response, &m.TokensIn, &m.TokensOut, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan director-agent conv: %w", err)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}
