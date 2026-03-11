package database

import (
	"encoding/json"
	"log"
	"time"

	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Director Reports (Level 2 agent)
// ============================================================================

func InsertDirectorReport(id uuid.UUID, reportDate time.Time, reportType, triggerEvent string, summaryCount int, analysis string, directives, stats, customerComplaints, keyObservations, promptChanges json.RawMessage, expectations string) error {
	return queries.InsertDirectorReport(DB, id, reportDate, reportType, triggerEvent, summaryCount, analysis, directives, stats, customerComplaints, keyObservations, promptChanges, expectations)
}

func GetLatestDirectorReport() (*models.DirectorReport, error) {
	return queries.GetLatestDirectorReport(DB)
}

func UpdateDirectorReportPromptChanges(id uuid.UUID, promptChanges json.RawMessage) error {
	return queries.UpdateDirectorReportPromptChanges(DB, id, promptChanges)
}

func GetChatSummariesSince(since time.Time) ([]models.ChatSummary, error) {
	return queries.GetChatSummariesSince(DB, since)
}

// ============================================================================
// Agent Prompts (versioned prompt management)
// ============================================================================

func GetActivePrompt(agentName string) (*models.AgentPrompt, error) {
	return queries.GetActivePrompt(DB, agentName)
}

func GetPromptHistory(agentName string, limit int) ([]models.AgentPrompt, error) {
	return queries.GetPromptHistory(DB, agentName, limit)
}

func InsertPrompt(agentName string, version int, prompt, createdBy string, parentVersion *int, notes string) (*models.AgentPrompt, error) {
	return queries.InsertPrompt(DB, agentName, version, prompt, createdBy, parentVersion, notes)
}

func ActivatePrompt(agentName string, version int) error {
	return queries.ActivatePrompt(DB, agentName, version)
}

func UpdatePromptMetrics(agentName string, version int, metrics *models.PromptMetrics) error {
	return queries.UpdatePromptMetrics(DB, agentName, version, metrics)
}

func GetNextPromptVersion(agentName string) (int, error) {
	return queries.GetNextVersion(DB, agentName)
}

// ============================================================================
// Interaction Metrics (per-agent/per-tool analytics)
// ============================================================================

func InsertInteractionMetric(m *models.InteractionMetric) error {
	return queries.InsertInteractionMetric(DB, m)
}

func GetAgentStatsSince(since time.Time) ([]models.AgentStats, error) {
	return queries.GetAgentStatsSince(DB, since)
}

func GetToolStatsSince(since time.Time) ([]models.ToolStats, error) {
	return queries.GetToolStatsSince(DB, since)
}

func GetToolStatsByAgentSince(agentName string, since time.Time) ([]models.ToolStats, error) {
	return queries.GetToolStatsByAgentSince(DB, agentName, since)
}

func GetPromptVersionMetrics(agentName string, version int) (*models.PromptMetrics, error) {
	return queries.GetPromptVersionMetrics(DB, agentName, version)
}

// ============================================================================
// Chat Summaries (session pruning)
// ============================================================================

func InsertChatSummary(chatID uuid.UUID, summary string, messagesFrom, messagesTo time.Time, messageCount int) (*models.ChatSummary, error) {
	return queries.InsertChatSummary(DB, chatID, summary, messagesFrom, messagesTo, messageCount)
}

func GetLatestChatSummary(chatID uuid.UUID) (*models.ChatSummary, error) {
	return queries.GetLatestChatSummary(DB, chatID)
}

func CountMessagesSince(chatID uuid.UUID, since time.Time) (int, error) {
	return queries.CountMessagesSince(DB, chatID, since)
}

func CountAllMessages(chatID uuid.UUID) (int, error) {
	return queries.CountAllMessages(DB, chatID)
}

func GetMessagesSince(chatID uuid.UUID, since time.Time, limit int) ([]models.Message, error) {
	return queries.GetMessagesSince(DB, chatID, since, limit)
}

func GetRecentMessages(chatID uuid.UUID, limit int) ([]models.Message, error) {
	return queries.GetRecentMessages(DB, chatID, limit)
}

func GetMessagesForSummary(chatID uuid.UUID, since time.Time, limit int) ([]models.Message, error) {
	return queries.GetMessagesForSummary(DB, chatID, since, limit)
}

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

// GetRecentChatsForDirector returns recent chats (including archived) without Redis cache.
func GetRecentChatsForDirector(limit int) ([]models.ChatResponse, int, error) {
	return queries.GetRecentChatsForDirector(DB, limit)
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
	widgetBusinessID *string,
) (*models.Chat, error) {
	chat, err := queries.GetOrCreateChat(DB, userID, userName, userEmail, source, sourceID, botID, clientAPIKey, widgetBusinessID)
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
	widgetBusinessID *string,
) (*models.Chat, error) {
	chat, err := queries.GetOrCreateChatMetadata(DB, userID, userName, userEmail, source, sourceID, botID, clientAPIKey, widgetBusinessID)
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

func GetLastUserMessage(chatID uuid.UUID) (*models.Message, error) {
	return queries.GetLastUserMessage(DB, chatID)
}

func UpdateUserProfile(userID uuid.UUID, avatar, profileURL string) error {
	return queries.UpdateUserProfile(DB, userID, avatar, profileURL)
}

// Функции для системы контроля доступа админов
func GetAdminLanguagesForChat(chatID uuid.UUID) ([]queries.AdminLanguageInfo, error) {
	return queries.GetAdminLanguagesForChat(DB, chatID)
}

func GetAdminLanguagesForChatOnlineOnly(chatID uuid.UUID, onlineAdminIDs []uuid.UUID) ([]queries.AdminLanguageInfo, error) {
	return queries.GetAdminLanguagesForChatOnlineOnly(DB, chatID, onlineAdminIDs)
}

func CheckAdminAccessToChat(adminID uuid.UUID, adminRole string, chatID uuid.UUID) (bool, error) {
	return queries.CheckAdminAccessToChat(DB, adminID, adminRole, chatID)
}

func AddSupervisorSourceAccess(supervisorID, clientID uuid.UUID, sourceType string, sourceID *string) error {
	return queries.AddSupervisorSourceAccess(DB, supervisorID, clientID, sourceType, sourceID)
}

// Функции для работы с переводами
func SaveTranslation(messageID uuid.UUID, language string, translation string) error {
	return queries.SaveTranslation(DB, messageID, language, translation)
}

func SaveDetectedLanguage(messageID uuid.UUID, language string) error {
	return queries.SaveDetectedLanguage(DB, messageID, language)
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

// FindChatByUserSourceID ищет чат по source_id пользователя и source
// Используется ТОЛЬКО для Instagram demo, не затрагивает widget систему
func FindChatByUserSourceID(userSourceID, source string) (*models.Chat, error) {
	return queries.FindChatByUserSourceID(DB, userSourceID, source)
}
