package database

import (
	"encoding/json"
	"log"
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
	// Используем UsersDB для админов (БД ballast)
	return queries.GetAdmin(UsersDB, email)
}

func GetAdminByID(adminID string) (*models.Admin, error) {
	// Используем UsersDB для админов (БД ballast)
	return queries.GetAdminByID(UsersDB, adminID)
}

func VerifyPassword(pw, hash string) error {
	return queries.VerifyPassword(pw, hash)
}

func GetChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
	// Пробуем получить из Redis кеша
	if chats, total, found := GetCachedChats(clientID, adminID, page, size); found {
		return chats, total, nil
	}

	// Кеша нет - загружаем из БД
	chats, total, err := queries.GetChats(DB, clientID, adminID, page, size)
	if err != nil {
		return nil, 0, err
	}

	// Сохраняем в кеш
	SetCachedChats(clientID, adminID, page, size, chats, total)

	return chats, total, nil
}

// InvalidateChatsCache инвалидирует кеш чатов
func InvalidateChatsCache() {
	InvalidateChatsCacheForAll()
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
	source string,
) (*models.Message, error) {
	return queries.AddMessageWithID(DB, messageID, chatID, content, sender, senderID, timestamp, msgType, meta, source)
}

func MarkSpecificMessagesAsRead(messageIDs []uuid.UUID) (int, error) {
	return queries.MarkSpecificMessagesAsRead(DB, messageIDs)
}

func GetOrCreateChat(
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
	chat, err := queries.GetOrCreateChat(DB, userID, userName, userEmail, source, sourceID, botID, clientAPIKey)
	if err != nil {
		return nil, err
	}

	// Инвалидируем кеш ТОЛЬКО для новых чатов
	if chat.IsNewChat {
		InvalidateChatsCache()
		log.Printf("GetOrCreateChat: новый чат %s, кеш инвалидирован", chat.ID)
	}

	return chat, nil
}

func GetOrCreateChatMetadata(
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
	chat, err := queries.GetOrCreateChatMetadata(DB, userID, userName, userEmail, source, sourceID, botID, clientAPIKey)
	if err != nil {
		return nil, err
	}

	// Инвалидируем кеш ТОЛЬКО для новых чатов
	if chat.IsNewChat {
		InvalidateChatsCache()
		log.Printf("GetOrCreateChatMetadata: новый чат %s, кеш инвалидирован", chat.ID)
	}

	return chat, nil
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

// Push subscription helpers (stored in UsersDB)
func SavePushSubscription(adminID uuid.UUID, endpoint, p256dh, auth string, subscription []byte) error {
	return queries.UpsertPushSubscription(UsersDB, adminID, endpoint, p256dh, auth, json.RawMessage(subscription))
}

func RemovePushSubscription(adminID uuid.UUID, endpoint string) error {
	return queries.DeletePushSubscription(UsersDB, adminID, endpoint)
}

func RemovePushSubscriptionByEndpoint(endpoint string) error {
	return queries.DeletePushSubscriptionByEndpoint(UsersDB, endpoint)
}

func ListPushSubscriptions(adminID uuid.UUID) ([]models.PushSubscription, error) {
	return queries.ListPushSubscriptions(UsersDB, adminID)
}

func TouchPushSubscription(endpoint string) error {
	return queries.TouchPushSubscription(UsersDB, endpoint)
}
