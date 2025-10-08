package database

import (
	"time"

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
	return queries.GetAdmin(DB, email)
}

func VerifyPassword(pw, hash string) error {
	return queries.VerifyPassword(pw, hash)
}

func GetChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
	return queries.GetChats(DB, clientID, adminID, page, size)
}

func GetChatByID(chatID uuid.UUID, limit int, beforeTimestamp string) (*models.Chat, int, error) {
	return queries.GetChatByID(DB, chatID, limit, beforeTimestamp)
}

func AddMessage(
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	msgType string,
	meta map[string]any,
) (*models.Message, error) {
	return queries.AddMessage(DB, chatID, content, sender, senderID, msgType, meta)
}

// AddMessageWithID добавляет сообщение с заданным ID и временной меткой
func AddMessageWithID(
	messageID uuid.UUID,
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	timestamp time.Time,
	msgType string,
	meta map[string]any,
) (*models.Message, error) {
	return queries.AddMessageWithID(DB, messageID, chatID, content, sender, senderID, timestamp, msgType, meta)
}

func MarkSpecificMessagesAsRead(messageIDs []uuid.UUID) (int, error) {
	return queries.MarkSpecificMessagesAsRead(DB, messageIDs)
}

func GetOrCreateChat(
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
	return queries.GetOrCreateChat(DB, userID, userName, userEmail, source, sourceID, botID, clientAPIKey)
}

func EnsureClientWithAPIKey(apiKey, clientName string) (uuid.UUID, error) {
	return queries.EnsureClientWithAPIKey(DB, apiKey, clientName)
}

// Экспортируем новые оптимизированные функции
func GetChatLightweight(chatID uuid.UUID) (*models.Chat, error) {
	return queries.GetChatLightweight(DB, chatID)
}

func UpdateChatTimestamp(chatID uuid.UUID) error {
	return queries.UpdateChatTimestamp(DB, chatID)
}

func GetClientLanguageFromChat(chatID uuid.UUID) (string, error) {
	return queries.GetClientLanguageFromChat(DB, chatID)
}

// Функции для работы с переводами
func SaveTranslation(messageID uuid.UUID, language string, translation string) error {
	return queries.SaveTranslation(DB, messageID, language, translation)
}

func SaveTranslationsBatch(translations map[uuid.UUID]map[string]string) error {
	return queries.SaveTranslationsBatch(DB, translations)
}

func GetTranslation(messageID uuid.UUID, language string) (string, error) {
	return queries.GetTranslation(DB, messageID, language)
}
