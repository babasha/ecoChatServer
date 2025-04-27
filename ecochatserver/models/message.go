package models

import (
	"time"
	"github.com/google/uuid"
)

// Message представляет собой структуру сообщения
type Message struct {
	ID        uuid.UUID `json:"id"`
	ChatID    uuid.UUID `json:"chatId"`
	Content   string    `json:"content"`
	Sender    string    `json:"sender"` // "user" или "admin"
	SenderID  uuid.UUID `json:"senderId,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
	Type      string    `json:"type,omitempty"` // "text", "image", "file", etc.
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // Дополнительные данные
}

// IncomingMessage представляет собой входящее сообщение от API Telegram
type IncomingMessage struct {
	UserID      string `json:"userId"`
	UserName    string `json:"userName"`
	UserEmail   string `json:"userEmail,omitempty"`
	Content     string `json:"content"`
	Source      string `json:"source"` // "telegram", "whatsapp", etc.
	BotID       string `json:"botId"`
	ClientID    string `json:"clientId"`
	MessageType string `json:"messageType,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// OutgoingMessage представляет собой исходящее сообщение в WebSocket
type OutgoingMessage struct {
	Type    string      `json:"type"` // "new_message", "chat_updated", etc.
	Payload interface{} `json:"payload"`
}