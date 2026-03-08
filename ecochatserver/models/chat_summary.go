package models

import (
	"time"

	"github.com/google/uuid"
)

// ChatSummary represents a periodic conversation summary for AI context
type ChatSummary struct {
	ID           uuid.UUID `json:"id"`
	ChatID       uuid.UUID `json:"chatId"`
	Summary      string    `json:"summary"`
	MessagesFrom time.Time `json:"messagesFrom"`
	MessagesTo   time.Time `json:"messagesTo"`
	MessageCount int       `json:"messageCount"`
	CreatedAt    time.Time `json:"createdAt"`
}
