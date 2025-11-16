package adkagent

import (
	"context"
	"database/sql"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// ADKAutoResponderV2 - обновлённая версия с правильным ADK API
type ADKAutoResponderV2 struct {
	storeClient *llm.StoreClient
	config      llm.AutoResponderConfig

	// Кэш агентов V2
	agentsMu            sync.RWMutex
	authorizedAgentV2   *SupportAgentV2
	unauthorizedAgentV2 *SupportAgentV2

	// Эскалации
	escalationsMu sync.RWMutex
	escalations   map[string]*EscalationState
}

// NewADKAutoResponderV2 создаёт автоответчик на базе ADK V2
func NewADKAutoResponderV2(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	storeClient := llm.NewStoreClient()

	ar := &ADKAutoResponderV2{
		storeClient: storeClient,
		config:      cfg,
		escalations: make(map[string]*EscalationState),
	}

	log.Printf("[ADK_V2] Инициализирован (lazy agent creation)")
	return ar, nil
}

// ProcessMessage - основной метод (совместим со старым интерфейсом)
func (ar *ADKAutoResponderV2) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[ADK_V2] ProcessMessage: chatID=%s", chat.ID)

	// Проверки
	if !ar.config.Enabled || msg.Sender != "user" {
		return nil, nil
	}

	if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
		return nil, nil
	}

	if !chat.AutoResponderEnabled {
		return nil, nil
	}

	chatKey := chat.ID.String()

	// Проверка эскалации
	ar.escalationsMu.RLock()
	escalation := ar.escalations[chatKey]
	ar.escalationsMu.RUnlock()

	if escalation != nil && escalation.ReturnedAt == nil {
		return nil, nil
	}

	// Определяем авторизацию
	userID := ar.getUserIDWithCache(ctx, chat)
	isAuthorized := userID > 0

	// Получаем агента
	agentV2, err := ar.getOrCreateAgentV2(ctx, isAuthorized)
	if err != nil {
		log.Printf("[ADK_V2] Ошибка создания агента: %v", err)
		return nil, err
	}

	// Задержка
	if ar.config.DelaySeconds > 0 {
		time.Sleep(time.Duration(ar.config.DelaySeconds) * time.Second)
	}

	// Запуск агента
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	response, err := agentV2.ProcessMessage(genCtx, chatKey, msg.Content, userID)
	if err != nil {
		log.Printf("[ADK_V2] Ошибка агента: %v", err)
		return nil, err
	}

	// Проверка эскалации
	if agentV2.IsEscalationNeeded(response) {
		ar.escalationsMu.Lock()
		ar.escalations[chatKey] = &EscalationState{
			EscalatedAt: time.Now(),
		}
		ar.escalationsMu.Unlock()

		response = strings.ReplaceAll(response, "#escalate", "")
		response = strings.TrimSpace(response)
	}

	// Формируем ответ
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   response,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: time.Now(),
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"provider":       "adk-v2",
		},
	}

	return botMsg, nil
}

// ClearEscalation очищает эскалацию
func (ar *ADKAutoResponderV2) ClearEscalation(chatID string) {
	ar.escalationsMu.Lock()
	defer ar.escalationsMu.Unlock()
	delete(ar.escalations, chatID)
}

// getOrCreateAgentV2 получает или создаёт агента
func (ar *ADKAutoResponderV2) getOrCreateAgentV2(ctx context.Context, isAuthorized bool) (*SupportAgentV2, error) {
	ar.agentsMu.RLock()
	if isAuthorized && ar.authorizedAgentV2 != nil {
		ar.agentsMu.RUnlock()
		return ar.authorizedAgentV2, nil
	}
	if !isAuthorized && ar.unauthorizedAgentV2 != nil {
		ar.agentsMu.RUnlock()
		return ar.unauthorizedAgentV2, nil
	}
	ar.agentsMu.RUnlock()

	ar.agentsMu.Lock()
	defer ar.agentsMu.Unlock()

	// Double-check
	if isAuthorized && ar.authorizedAgentV2 != nil {
		return ar.authorizedAgentV2, nil
	}
	if !isAuthorized && ar.unauthorizedAgentV2 != nil {
		return ar.unauthorizedAgentV2, nil
	}

	// Создаём
	agent, err := NewSupportAgentV2(ctx, ar.storeClient, isAuthorized)
	if err != nil {
		return nil, err
	}

	if isAuthorized {
		ar.authorizedAgentV2 = agent
	} else {
		ar.unauthorizedAgentV2 = agent
	}

	return agent, nil
}

// getUserIDWithCache получает user_id
func (ar *ADKAutoResponderV2) getUserIDWithCache(ctx context.Context, chat *models.Chat) int {
	if chat.Metadata != nil {
		if userID, ok := chat.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chat.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	userID := llm.ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	if userID > 0 {
		if chat.Metadata == nil {
			chat.Metadata = make(map[string]interface{})
		}
		chat.Metadata["store_user_id"] = userID
	}

	return userID
}

// detectLanguage определяет язык (не используется в V2, но оставим для совместимости)
func (ar *ADKAutoResponderV2) detectLanguage(msg *models.Message, chat *models.Chat) string {
	customerLang := ""

	if msg.Metadata != nil {
		if detected, ok := msg.Metadata["detectedLanguage"].(string); ok {
			customerLang = strings.ToLower(strings.TrimSpace(detected))
		}
	}

	if customerLang == "" || customerLang == "unknown" {
		if lang, err := database.GetClientLanguageFromChat(chat.ID); err == nil {
			customerLang = strings.ToLower(strings.TrimSpace(lang))
		} else if err != sql.ErrNoRows {
			log.Printf("[ADK_V2] Ошибка получения языка: %v", err)
		}
	}

	return customerLang
}
