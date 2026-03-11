package database

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// EmbedFunc is a callback for generating embeddings, set externally to avoid import cycle.
// Signature: func(ctx context.Context, text string) ([]float64, error)
var EmbedFunc func(ctx context.Context, text string) ([]float64, error)

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
// Director Memory (persistent long-term memory)
// ============================================================================

// UpsertMemory saves a memory, auto-generating embedding if available.
func UpsertMemory(m *models.DirectorMemory) error {
	// Auto-generate embedding if not set and callback is available
	if len(m.Embedding) == 0 && EmbedFunc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		emb, err := EmbedFunc(ctx, m.Content)
		cancel()
		if err == nil {
			m.Embedding = emb
		} else {
			log.Printf("[MEMORY] Embedding generation skipped: %v", err)
		}
	}
	return queries.UpsertMemory(DB, m)
}

// RecallMemories searches memories (FTS only, legacy).
func RecallMemories(query string, category string, limit int) ([]models.DirectorMemory, error) {
	return queries.RecallMemories(DB, query, category, limit)
}

// RecallMemoriesHybrid performs hybrid FTS + vector search with MMR diversity.
// Automatically generates query embedding if embedding client is available.
func RecallMemoriesHybrid(query string, category string, limit int) ([]models.DirectorMemory, error) {
	var queryEmbedding []float64

	if EmbedFunc != nil && query != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		emb, err := EmbedFunc(ctx, query)
		cancel()
		if err == nil {
			queryEmbedding = emb
		} else {
			log.Printf("[MEMORY] Query embedding failed, using FTS only: %v", err)
		}
	}

	return queries.RecallMemoriesHybrid(DB, query, queryEmbedding, category, limit)
}

func DeleteMemory(category, key string) error {
	return queries.DeleteMemory(DB, category, key)
}

func ListMemories(category string, limit int) ([]models.DirectorMemory, error) {
	return queries.ListMemories(DB, category, limit)
}

func GetHotMemories(limit int) ([]models.DirectorMemory, error) {
	return queries.GetHotMemories(DB, limit)
}

func GetMemoryStats() (map[string]int, error) {
	return queries.GetMemoryStats(DB)
}

func DecayMemories() (int64, int64, error) {
	return queries.DecayMemories(DB)
}

func SaveAutoMemory(category, key, content string, tags []string, expiresAt *time.Time) error {
	return queries.SaveAutoMemory(DB, category, key, content, tags, expiresAt)
}

func SearchReports(query string, limit int) ([]models.DirectorReport, error) {
	return queries.SearchReports(DB, query, limit)
}

func GetReportsByDateRange(from, to time.Time, limit int) ([]models.DirectorReport, error) {
	return queries.GetReportsByDateRange(DB, from, to, limit)
}

// ============================================================================
// Directive Outcomes (self-reflection feedback loop)
// ============================================================================

func InsertDirectiveOutcome(reportID uuid.UUID, directiveType, instruction string,
	escRateBefore, emptyRateBefore, avgMsBefore float64) error {
	return queries.InsertDirectiveOutcome(DB, reportID, directiveType, instruction,
		escRateBefore, emptyRateBefore, avgMsBefore)
}

func GetPendingDirectiveOutcomes() ([]models.DirectiveOutcome, error) {
	return queries.GetPendingDirectiveOutcomes(DB)
}

func EvaluateDirectiveOutcome(id uuid.UUID, effectiveness, notes string,
	escRateAfter, emptyRateAfter, avgMsAfter float64) error {
	return queries.EvaluateDirectiveOutcome(DB, id, effectiveness, notes,
		escRateAfter, emptyRateAfter, avgMsAfter)
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

// ============================================================================
// Deep Search & Timeline (unified search + historical browsing)
// ============================================================================

func DeepSearch(query string, sourceType string, timeRange string, limit int) ([]models.DeepSearchResult, error) {
	return queries.DeepSearch(DB, query, sourceType, timeRange, limit)
}

func GetTimelineData(from, to time.Time, detail string) (*models.TimelineData, error) {
	return queries.GetTimelineData(DB, from, to, detail)
}

func InsertDigest(d *models.DirectorDigest) error {
	return queries.InsertDigest(DB, d)
}

func GetDigestForPeriod(periodType string, periodStart time.Time) (*models.DirectorDigest, error) {
	return queries.GetDigestForPeriod(DB, periodType, periodStart)
}

func ListDigests(periodType string, limit int) ([]models.DirectorDigest, error) {
	return queries.ListDigests(DB, periodType, limit)
}

func CountChatsInPeriod(from, to time.Time) (int, error) {
	return queries.CountChatsInPeriod(DB, from, to)
}

func ParsePeriod(period string) (time.Time, time.Time, error) {
	return queries.ParsePeriod(period)
}

// ============================================================================
// Director Skills (self-created tools)
// ============================================================================

func CreateSkill(skill *models.DirectorSkill) error {
	return queries.CreateSkill(DB, skill)
}

func GetEnabledSkills() ([]models.DirectorSkill, error) {
	return queries.GetEnabledSkills(DB)
}

func GetAllSkills() ([]models.DirectorSkill, error) {
	return queries.GetAllSkills(DB)
}

func GetSkillByName(name string) (*models.DirectorSkill, error) {
	return queries.GetSkillByName(DB, name)
}

func UpdateSkill(name string, description, parameters, code *string, enabled *bool, tags []string) error {
	return queries.UpdateSkill(DB, name, description, parameters, code, enabled, tags)
}

func DeleteSkill(name string) error {
	return queries.DeleteSkill(DB, name)
}

func RecordSkillUsage(name string, lastError string) error {
	return queries.RecordSkillUsage(DB, name, lastError)
}

func ToggleSkill(name string, enabled bool) error {
	return queries.ToggleSkill(DB, name, enabled)
}

func LogDirectorToolCall(toolName, argsJSON string, resultLen int, success bool) error {
	return queries.LogDirectorToolCall(DB, toolName, argsJSON, resultLen, success)
}

func GetRepeatingToolPatterns(since time.Time, minCount int) ([]models.ToolPattern, error) {
	return queries.GetRepeatingToolPatterns(DB, since, minCount)
}

func CountAutoCreatedSkills() (int, error) {
	return queries.CountAutoCreatedSkills(DB)
}

func GetNegativeOutcomesSince(since time.Time) ([]models.DirectiveOutcome, error) {
	return queries.GetNegativeOutcomesSince(DB, since)
}

func GetNegativeOutcomesByType(since time.Time) (map[string]int, error) {
	return queries.GetNegativeOutcomesByType(DB, since)
}

// ============================================================================
// Director Chat Messages (persistent conversation history)
// ============================================================================

func SaveDirectorChatMessage(adminID, role, content string) error {
	return queries.SaveDirectorChatMessage(DB, adminID, role, content)
}

func LoadDirectorChatHistory(adminID string, limit int) ([]queries.DirectorChatMsg, error) {
	return queries.LoadDirectorChatHistory(DB, adminID, limit)
}

func ClearDirectorChatHistory(adminID string) error {
	return queries.ClearDirectorChatHistory(DB, adminID)
}

// ============================================================================
// Director Identity (personality/soul system) — stored in UsersDB (ballast)
// ============================================================================

func GetIdentity(key string) (*models.DirectorIdentity, error) {
	return queries.GetIdentity(UsersDB, key)
}

func GetAllIdentity() ([]models.DirectorIdentity, error) {
	return queries.GetAllIdentity(UsersDB)
}

func UpsertIdentity(key, content, updatedBy, reason string) error {
	return queries.UpsertIdentity(UsersDB, key, content, updatedBy, reason)
}

func GetIdentityHistory(key string, limit int) ([]models.DirectorIdentityHistory, error) {
	return queries.GetIdentityHistory(UsersDB, key, limit)
}

func RollbackIdentity(key string, targetVersion int) error {
	return queries.RollbackIdentity(UsersDB, key, targetVersion)
}

func IsIdentityBootstrapped() (bool, error) {
	return queries.IsIdentityBootstrapped(UsersDB)
}

func GetIdentityForPrompt() (map[string]string, error) {
	return queries.GetIdentityForPrompt(UsersDB)
}

func SeedIdentity() error {
	return queries.SeedIdentity(UsersDB)
}

func GetIdentityUpdatedSince(since time.Time) ([]models.DirectorIdentity, error) {
	return queries.GetIdentityUpdatedSince(UsersDB, since)
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
