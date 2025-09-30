package models

import (
	"github.com/google/uuid"
	"time"
)

// Message представляет собой структуру сообщения
type Message struct {
	ID        uuid.UUID              `json:"id"`
	ChatID    uuid.UUID              `json:"chatId"`
	Content   string                 `json:"content"`
	Sender    string                 `json:"sender"` // "user" или "admin"
	SenderID  uuid.UUID              `json:"senderId,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Read      bool                   `json:"read"`
	ReadAt    *time.Time             `json:"readAt,omitempty"`   // Время прочтения сообщения
	Type      string                 `json:"type,omitempty"`     // "text", "image", "file", etc.
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // Дополнительные данные
}

// UniversalMessage - универсальная структура для всех типов сообщений
type UniversalMessage struct {
	// Основные поля Message
	ID        uuid.UUID              `json:"id,omitempty"`
	ChatID    uuid.UUID              `json:"chatId,omitempty"`
	Content   string                 `json:"content"`
	Sender    string                 `json:"sender,omitempty"`
	SenderID  uuid.UUID              `json:"senderId,omitempty"`
	Timestamp time.Time              `json:"timestamp,omitempty"`
	Read      bool                   `json:"read,omitempty"`
	ReadAt    *time.Time             `json:"readAt,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`

	// Поля для входящих сообщений
	UserID    string `json:"userId,omitempty"`
	UserName  string `json:"userName,omitempty"`
	UserEmail string `json:"userEmail,omitempty"`
	Source    string `json:"source,omitempty"` // "telegram", "whatsapp", etc.
	BotID     string `json:"botId,omitempty"`
	ClientID  string `json:"clientId,omitempty"`

	// Поля для исходящих сообщений
	MessageType string      `json:"messageType,omitempty"`
	Payload     interface{} `json:"payload,omitempty"`
}

// ToMessage конвертирует UniversalMessage в Message
func (um *UniversalMessage) ToMessage() *Message {
	return &Message{
		ID:        um.ID,
		ChatID:    um.ChatID,
		Content:   um.Content,
		Sender:    um.Sender,
		SenderID:  um.SenderID,
		Timestamp: um.Timestamp,
		Read:      um.Read,
		ReadAt:    um.ReadAt,
		Type:      um.Type,
		Metadata:  um.Metadata,
	}
}

// IncomingMessage представляет собой входящее сообщение от API Telegram
// Deprecated: используйте UniversalMessage
type IncomingMessage struct {
	UserID      string                 `json:"userId"`
	UserName    string                 `json:"userName"`
	UserEmail   string                 `json:"userEmail,omitempty"`
	Content     string                 `json:"content"`
	Source      string                 `json:"source"` // "telegram", "whatsapp", etc.
	BotID       string                 `json:"botId"`
	ClientID    string                 `json:"clientId"`
	MessageType string                 `json:"messageType,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// OutgoingMessage представляет собой исходящее сообщение в WebSocket
// Deprecated: используйте UniversalMessage
type OutgoingMessage struct {
	Type    string      `json:"type"` // "new_message", "chat_updated", etc.
	Payload interface{} `json:"payload"`
}
