package database

import (
    "github.com/egor/ecochatserver/database/queries"
    "github.com/egor/ecochatserver/models"
    "github.com/google/uuid"
)

// Экспортируем константы
const (
    DefaultPageSize = queries.DefaultPageSize
    MaxPageSize     = queries.MaxPageSize
)

// Прокси-функции для внешнего использования
func GetAdmin(email string) (*models.Admin, error) {
    return queries.GetAdmin(email)
}

func VerifyPassword(pw, hash string) error {
    return queries.VerifyPassword(pw, hash)
}

func GetChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
    return queries.GetChats(clientID, adminID, page, size)
}

func GetChatByID(chatID uuid.UUID, page, size int) (*models.Chat, int, error) {
    return queries.GetChatByID(chatID, page, size)
}

func AddMessage(
    chatID uuid.UUID,
    content, sender string,
    senderID uuid.UUID,
    msgType string,
    meta map[string]any,
) (*models.Message, error) {
    return queries.AddMessage(chatID, content, sender, senderID, msgType, meta)
}

func MarkMessagesAsRead(chatID uuid.UUID) error {
    return queries.MarkMessagesAsRead(chatID)
}

func GetOrCreateChat(
    userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
    return queries.GetOrCreateChat(userID, userName, userEmail, source, sourceID, botID, clientAPIKey)
}

func EnsureClientWithAPIKey(apiKey, clientName string) (uuid.UUID, error) {
    return queries.EnsureClientWithAPIKey(apiKey, clientName)
}