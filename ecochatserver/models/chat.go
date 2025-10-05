package models

import (
	"github.com/google/uuid"
	"time"
)

//  Chat представляет собой структуру чата
type Chat struct {
	ID                    uuid.UUID              `json:"id"`
	User                  User                   `json:"user"`
	Messages              []Message              `json:"messages"`
	LastMessage           *Message               `json:"lastMessage,omitempty"`
	CreatedAt             time.Time              `json:"createdAt"`
	UpdatedAt             time.Time              `json:"updatedAt"`
	Status                string                 `json:"status"`                         // "active", "closed", "pending"
	Source                string                 `json:"source"`                         // Источник (например, "telegram", "whatsapp")
	BotID                 string                 `json:"botId"`                          // ID бота, через который пришло сообщение
	ClientID              uuid.UUID              `json:"clientId"`                       // ID клиента, которому принадлежит бот
	AssignedTo            *uuid.UUID             `json:"assignedTo,omitempty"`           // ID сотрудника, которому назначен чат
	AutoResponderEnabled  bool                   `json:"autoResponderEnabled"`           // Включен ли автоответчик для этого чата
	ResolvedAt            *time.Time             `json:"resolvedAt,omitempty"`           // Время решения вопроса
	AutoArchiveAt         *time.Time             `json:"autoArchiveAt,omitempty"`        // Время автоархивации (для LLM-решенных чатов)
	IsArchived            bool                   `json:"isArchived"`                     // Архивирован ли чат
	ArchiveTimerPaused    bool                   `json:"archiveTimerPaused"`             // Приостановлен ли таймер архивации
	Metadata              map[string]interface{} `json:"metadata,omitempty"`             // Метаданные чата, включая историю LLM
}

// ChatResponse для отправки на фронтенд
type ChatResponse struct {
	ID                   uuid.UUID              `json:"id"`
	User                 User                   `json:"user"`
	LastMessage          *Message               `json:"lastMessage,omitempty"`
	CreatedAt            time.Time              `json:"createdAt"`
	UpdatedAt            time.Time              `json:"updatedAt"`
	Status               string                 `json:"status"`
	ClientID             uuid.UUID              `json:"clientId"`
	UnreadCount          int                    `json:"unreadCount"`
	AutoResponderEnabled bool                   `json:"autoResponderEnabled"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
}

// ChatPaginationResponse для ответа с пагинацией
type ChatPaginationResponse struct {
	Chats      []ChatResponse `json:"chats"`
	Page       int            `json:"page"`
	PageSize   int            `json:"pageSize"`
	TotalItems int            `json:"totalItems"`
	TotalPages int            `json:"totalPages"`
}

// ArchivedChat представляет архивный чат
type ArchivedChat struct {
	ID                uuid.UUID              `json:"id"`
	ChatID            uuid.UUID              `json:"chatId"`
	ClientID          uuid.UUID              `json:"clientId"`
	UserID            uuid.UUID              `json:"userId"`
	ArchivedAt        time.Time              `json:"archivedAt"`
	DeleteAt          time.Time              `json:"deleteAt"`
	DeleteTimerPaused bool                   `json:"deleteTimerPaused"`
	Messages          []Message              `json:"messages"`
	Metadata          map[string]interface{} `json:"metadata,omitempty"`
}

// ArchivedChatSummary краткая информация об архивном чате для календаря
type ArchivedChatSummary struct {
	ID           uuid.UUID `json:"id"`
	ChatID       uuid.UUID `json:"chatId"`
	ArchivedAt   time.Time `json:"archivedAt"`
	DeleteAt     time.Time `json:"deleteAt"`
	MessageCount int       `json:"messageCount"`
	UserName     string    `json:"userName"`
	UserEmail    string    `json:"userEmail"`
}
