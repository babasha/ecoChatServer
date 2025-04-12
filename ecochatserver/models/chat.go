package models

import (
	"time"
)

// Chat представляет собой структуру чата
type Chat struct {
	ID        string    `json:"id"`
	User      User      `json:"user"`
	Messages  []Message `json:"messages"`
	LastMessage *Message  `json:"lastMessage,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Status    string    `json:"status"` // "active", "closed", "pending"
	Source    string    `json:"source"` // Источник (например, "telegram", "whatsapp")
	BotID     string    `json:"botId"`  // ID бота, через который пришло сообщение
	ClientID  string    `json:"clientId"` // ID клиента, которому принадлежит бот
	AssignedTo *string   `json:"assignedTo,omitempty"` // ID сотрудника, которому назначен чат
}

// ChatResponse для отправки на фронтенд
type ChatResponse struct {
	ID          string    `json:"id"`
	User        User      `json:"user"`
	LastMessage *Message  `json:"lastMessage,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Status      string    `json:"status"`
	UnreadCount int       `json:"unreadCount"`
}