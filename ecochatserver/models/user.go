package models

import (
	"github.com/google/uuid"
)

// User представляет собой структуру пользователя (клиента)
type User struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Email    string    `json:"email,omitempty"`
	Avatar   *string   `json:"avatar,omitempty"`
	Source   string    `json:"source,omitempty"`   // Источник (telegram, whatsapp, etc.)
	SourceID string    `json:"sourceId,omitempty"` // ID пользователя в источнике
}

// Admin представляет собой структуру администратора
// Маппится на таблицу users с соответствующими ролями
type Admin struct {
	ID           uuid.UUID  `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"` // display_name в БД
	PasswordHash string     `json:"password_hash,omitempty"`
	Avatar       *string    `json:"avatar,omitempty"` // avatar_url в БД
	Role         string     `json:"role"`             // из таблицы roles через role_id
	RoleID       *uuid.UUID `json:"role_id,omitempty"`
	Status       string     `json:"status"`       // active, inactive, suspended, banned
	EmailVerified bool      `json:"email_verified"`
}

// Client представляет собой структуру клиента (компании)
type Client struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Subscription string    `json:"subscription"` // Тип подписки
	Active       bool      `json:"active"`       // Активен ли клиент
}
