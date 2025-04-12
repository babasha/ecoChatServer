package models

// User представляет собой структуру пользователя (клиента)
type User struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Source   string `json:"source,omitempty"` // Источник (telegram, whatsapp, etc.)
	SourceID string `json:"sourceId,omitempty"` // ID пользователя в источнике
}

// Admin представляет собой структуру администратора
type Admin struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash,omitempty"`
	Avatar       string `json:"avatar,omitempty"`
	Role         string `json:"role"` // "admin", "support", etc.
	ClientID     string `json:"clientId"` // ID клиента, на которого работает админ
	Active       bool   `json:"active"`
}

// Client представляет собой структуру клиента (компании)
type Client struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Subscription string `json:"subscription"` // Тип подписки
	Active      bool   `json:"active"`        // Активен ли клиент
}