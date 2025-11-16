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

	// Кэш агентов (2 экземпляра: для авторизованных и гостей)
	agentsMu            sync.RWMutex
	authorizedAgent     *SupportAgent   // Agent для залогиненных пользователей (19 tools)
	unauthorizedAgent   *SupportAgent   // Agent для гостей (13 public tools)

	// Эскалации (in-memory - при рестарте сбрасываются)
	// NOTE: Это intentional - эскалация это ephemeral state.
	// При рестарте сервера автоответчик снова начнёт отвечать, что правильно.
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

// ProcessMessage - основной метод обработки сообщения пользователя
func (ar *ADKAutoResponderV2) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[ADK_V2] ProcessMessage: chatID=%s", chat.ID)

	// Быстрые проверки: должен ли автоответчик отвечать?
	if !ar.config.Enabled ||                              // Автоответчик выключен
		msg.Sender != "user" ||                            // Сообщение не от пользователя
		(chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil) || // Чат назначен оператору
		!chat.AutoResponderEnabled {                       // Автоответчик отключен для чата
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
	log.Printf("[ADK_V2] User context: userID=%d, authorized=%v", userID, isAuthorized)

	// Получаем агента (с полным набором из 19 tools)
	agent, err := ar.getOrCreateAgent(ctx, isAuthorized)
	if err != nil {
		log.Printf("[ADK_V2] Ошибка создания агента: %v", err)
		return nil, err
	}

	// Задержка
	if ar.config.DelaySeconds > 0 {
		time.Sleep(time.Duration(ar.config.DelaySeconds) * time.Second)
	}

	// Получаем язык клиента из метаданных сообщения (если есть)
	var clientLang string
	if msg.Metadata != nil {
		if detectedLang, ok := msg.Metadata["detectedLanguage"].(string); ok && detectedLang != "" {
			clientLang = detectedLang
			log.Printf("[ADK_V2] Client language detected from metadata: %s", clientLang)
		}
	}

	// Запуск агента
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	// Передаём язык клиента в агента (если определён)
	var response string
	if clientLang != "" {
		response, err = agent.ProcessMessage(genCtx, chatKey, msg.Content, userID, clientLang)
	} else {
		response, err = agent.ProcessMessage(genCtx, chatKey, msg.Content, userID)
	}
	if err != nil {
		log.Printf("[ADK_V2] Ошибка агента: %v", err)
		return nil, err
	}

	// Проверка эскалации
	if agent.IsEscalationNeeded(response) {
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

// getOrCreateAgent создаёт или возвращает закешированный агент V3
func (ar *ADKAutoResponderV2) getOrCreateAgent(ctx context.Context, isAuthorized bool) (*SupportAgent, error) {
	ar.agentsMu.RLock()
	if isAuthorized && ar.authorizedAgent != nil {
		ar.agentsMu.RUnlock()
		return ar.authorizedAgent, nil
	}
	if !isAuthorized && ar.unauthorizedAgent != nil {
		ar.agentsMu.RUnlock()
		return ar.unauthorizedAgent, nil
	}
	ar.agentsMu.RUnlock()

	ar.agentsMu.Lock()
	defer ar.agentsMu.Unlock()

	// Double-check
	if isAuthorized && ar.authorizedAgent != nil {
		return ar.authorizedAgent, nil
	}
	if !isAuthorized && ar.unauthorizedAgent != nil {
		return ar.unauthorizedAgent, nil
	}

	// Создаём V3 агента с полным набором из 19 tools
	agent, err := NewSupportAgent(ctx, ar.storeClient, isAuthorized)
	if err != nil {
		return nil, err
	}

	if isAuthorized {
		ar.authorizedAgent = agent
		log.Printf("[ADK_V2] Created AUTHORIZED V3 agent with 19 tools")
	} else {
		ar.unauthorizedAgent = agent
		log.Printf("[ADK_V2] Created UNAUTHORIZED V3 agent with 19 tools")
	}

	return agent, nil
}

// getUserIDWithCache получает user_id из metadata или через Store API
func (ar *ADKAutoResponderV2) getUserIDWithCache(ctx context.Context, chat *models.Chat) int {
	// Пытаемся получить из cache (metadata)
	if chat.Metadata != nil {
		if userID, ok := chat.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chat.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	// Если не закешировано - получаем через Store API
	userID := llm.ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	// NOTE: Не модифицируем chat.Metadata здесь - это должно делаться на уровне выше
	// где chat создаётся/загружается из БД, чтобы избежать race conditions

	return userID
}
