package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// ADKAutoResponderV2 - обновлённая версия с правильным ADK API и мульти-агентностью
type ADKAutoResponderV2 struct {
	storeClient *llm.StoreClient
	config      llm.AutoResponderConfig

	// Single-agent mode: Agent cache per chatID
	agents *syncCache[*SupportAgent]

	// Multi-agent mode: Orchestrator cache per chatID
	orchestrators *syncCache[*OrchestratorAgent]

	// Режим работы: true = мульти-агент, false = single-agent
	useMultiAgent bool

	// Supervisor agent для анализа запросов (опционально, для single-agent mode)
	supervisor    *SupervisorAgent
	useSupervisor bool

	// Эскалации (in-memory - при рестарте сбрасываются)
	escalations *syncCache[*EscalationState]
}

// NewADKAutoResponderV2 создаёт автоответчик на базе ADK V2 (single-agent mode)
func NewADKAutoResponderV2(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	storeClient := llm.NewStoreClient()

	ar := &ADKAutoResponderV2{
		storeClient:   storeClient,
		config:        cfg,
		agents:        newSyncCache[*SupportAgent](),
		orchestrators: newSyncCache[*OrchestratorAgent](),
		escalations:   newSyncCache[*EscalationState](),
		useMultiAgent: false, // По умолчанию single-agent
		useSupervisor: false,
	}

	log.Printf("[ADK_V2] Инициализирован (single-agent mode, lazy creation)")
	return ar, nil
}

// NewADKAutoResponderV2MultiAgent создаёт автоответчик с мульти-агентной архитектурой
func NewADKAutoResponderV2MultiAgent(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	storeClient := llm.NewStoreClient()

	ar := &ADKAutoResponderV2{
		storeClient:   storeClient,
		config:        cfg,
		agents:        newSyncCache[*SupportAgent](),
		orchestrators: newSyncCache[*OrchestratorAgent](),
		escalations:   newSyncCache[*EscalationState](),
		useMultiAgent: true, // Мульти-агентный режим
		useSupervisor: false,
	}

	log.Printf("[ADK_V2] Инициализирован (MULTI-AGENT mode, lazy creation)")
	log.Printf("[ADK_V2] 🤖 Orchestrator → [ProductExpert, OrderManager, SupportSpecialist]")
	return ar, nil
}

// EnableMultiAgent включает/выключает мульти-агентный режим
func (ar *ADKAutoResponderV2) EnableMultiAgent(enabled bool) {
	ar.useMultiAgent = enabled
	mode := "single-agent"
	if enabled {
		mode = "multi-agent"
	}
	log.Printf("[ADK_V2] Switched to %s mode", mode)
}

// NewADKAutoResponderV2WithSupervisor создаёт автоответчик с supervisor agent
func NewADKAutoResponderV2WithSupervisor(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Создаём supervisor agent
	supervisor, err := NewSupervisorAgent(ctx)
	if err != nil {
		log.Printf("[ADK_V2] Warning: failed to create supervisor agent: %v (continuing without supervisor)", err)
	} else {
		ar.supervisor = supervisor
		ar.useSupervisor = true
		log.Printf("[ADK_V2] Supervisor agent enabled")
	}

	return ar, nil
}

// EnableSupervisor включает/выключает supervisor agent
func (ar *ADKAutoResponderV2) EnableSupervisor(enabled bool) {
	ar.useSupervisor = enabled && ar.supervisor != nil
	log.Printf("[ADK_V2] Supervisor agent: %v", ar.useSupervisor)
}

// ProcessMessage - основной метод обработки сообщения пользователя
func (ar *ADKAutoResponderV2) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[ADK_V2] ProcessMessage: chatID=%s, mode=%s", chat.ID, ar.getMode())

	// Быстрые проверки: должен ли автоответчик отвечать?
	if !ar.config.Enabled || // Автоответчик выключен
		msg.Sender != "user" || // Сообщение не от пользователя
		(chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil) || // Чат назначен оператору
		!chat.AutoResponderEnabled { // Автоответчик отключен для чата
		return nil, nil
	}

	chatKey := chat.ID.String()

	// Проверка эскалации
	escalation, _ := ar.escalations.get(chatKey)
	if escalation != nil && escalation.ReturnedAt == nil {
		return nil, nil
	}

	// Определяем авторизацию
	userID := ar.getUserIDWithCache(ctx, chat)
	isAuthorized := userID > 0
	log.Printf("[ADK_V2] User context: userID=%d, authorized=%v", userID, isAuthorized)

	// Задержка (если настроена)
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

	// Запуск агента с timeout
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	var response string
	var err error
	var agentType string

	// Выбираем режим работы
	if ar.useMultiAgent {
		// MULTI-AGENT MODE: Orchestrator → [ProductExpert, OrderManager, SupportSpecialist]
		response, err = ar.processWithMultiAgent(genCtx, chatKey, msg.Content, userID, isAuthorized, clientLang)
		agentType = "multi-agent"
	} else {
		// SINGLE-AGENT MODE: один SupportAgent с 19 tools
		response, err = ar.processWithSingleAgent(genCtx, chatKey, msg.Content, userID, isAuthorized, clientLang)
		agentType = "single-agent"
	}

	if err != nil {
		log.Printf("[ADK_V2] Agent error (%s): %v", agentType, err)
		return nil, err
	}

	// Проверка эскалации
	if strings.Contains(response, "#escalate") {
		ar.escalations.set(chatKey, &EscalationState{
			EscalatedAt: time.Now(),
		})

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
			"agentMode":      agentType,
		},
	}

	// Добавляем информацию о режиме
	if ar.useMultiAgent {
		botMsg.Metadata["multiAgent"] = true
	}
	if ar.useSupervisor {
		botMsg.Metadata["supervisorEnabled"] = true
	}

	return botMsg, nil
}

// getMode возвращает текущий режим работы
func (ar *ADKAutoResponderV2) getMode() string {
	if ar.useMultiAgent {
		return "multi-agent"
	}
	return "single-agent"
}

// processWithMultiAgent обрабатывает сообщение через мульти-агентную систему
func (ar *ADKAutoResponderV2) processWithMultiAgent(ctx context.Context, chatKey, userMessage string, userID int, isAuthorized bool, clientLang string) (string, error) {
	// Получаем или создаём Orchestrator
	orchestrator, err := ar.getOrCreateOrchestrator(ctx, chatKey, userID, isAuthorized)
	if err != nil {
		return "", fmt.Errorf("failed to create orchestrator: %w", err)
	}

	// Обновляем userID если изменился
	orchestrator.SetUserID(userID)

	// Запускаем обработку
	return orchestrator.ProcessMessage(ctx, chatKey, userMessage, clientLang)
}

// processWithSingleAgent обрабатывает сообщение через single-agent
func (ar *ADKAutoResponderV2) processWithSingleAgent(ctx context.Context, chatKey, userMessage string, userID int, isAuthorized bool, clientLang string) (string, error) {
	// Получаем агента (с полным набором из 19 tools)
	agent, err := ar.getOrCreateAgent(ctx, chatKey, isAuthorized)
	if err != nil {
		return "", err
	}

	// Подготавливаем сообщение
	processedMessage := userMessage

	// Если supervisor включен, анализируем запрос и добавляем инструкции
	if ar.useSupervisor && ar.supervisor != nil {
		plan, err := ar.supervisor.AnalyzeRequest(ctx, userMessage)
		if err != nil {
			log.Printf("[ADK_V2] Supervisor analysis failed: %v (continuing without plan)", err)
		} else if plan != nil && plan.RequiredTool != "" {
			instruction := ar.formatSupervisorInstruction(plan)
			processedMessage = instruction + "\n\n" + userMessage
			log.Printf("[ADK_V2] Supervisor plan: intent=%s, required_tool=%s", plan.Intent, plan.RequiredTool)
		}
	}

	// Передаём язык клиента в агента (если определён)
	if clientLang != "" {
		return agent.ProcessMessage(ctx, chatKey, processedMessage, userID, clientLang)
	}
	return agent.ProcessMessage(ctx, chatKey, processedMessage, userID)
}

// getOrCreateOrchestrator создаёт или возвращает кэшированный Orchestrator для чата
func (ar *ADKAutoResponderV2) getOrCreateOrchestrator(ctx context.Context, chatID string, userID int, isAuthorized bool) (*OrchestratorAgent, error) {
	orch, err := ar.orchestrators.getOrCreate(chatID, func() (*OrchestratorAgent, error) {
		return NewOrchestratorAgent(ctx, MultiAgentConfig{
			StoreClient:  ar.storeClient,
			IsAuthorized: isAuthorized,
			UserID:       userID,
		})
	})
	if err != nil {
		return nil, err
	}

	authType := "UNAUTHORIZED"
	if isAuthorized {
		authType = "AUTHORIZED"
	}
	log.Printf("[ADK_V2] Created/reused %s orchestrator for chat %s (total: %d)", authType, chatID, ar.orchestrators.len())
	return orch, nil
}

// formatSupervisorInstruction форматирует инструкцию от supervisor для support agent
func (ar *ADKAutoResponderV2) formatSupervisorInstruction(plan *ExecutionPlan) string {
	if plan == nil || plan.RequiredTool == "" {
		return ""
	}

	instruction := fmt.Sprintf("[SUPERVISOR INSTRUCTION]\nIntent: %s\nREQUIRED: You MUST call tool '%s' first.\n",
		plan.Intent, plan.RequiredTool)

	if len(plan.Steps) > 0 {
		instruction += "Steps:\n"
		for i, step := range plan.Steps {
			instruction += fmt.Sprintf("%d. %s\n", i+1, step)
		}
	}

	return instruction
}

// ClearEscalation очищает эскалацию
func (ar *ADKAutoResponderV2) ClearEscalation(chatID string) {
	ar.escalations.remove(chatID)
}

// getOrCreateAgent creates or returns cached agent for specific chatID
// FIX: Each chat gets isolated agent to prevent userID race condition
func (ar *ADKAutoResponderV2) getOrCreateAgent(ctx context.Context, chatID string, isAuthorized bool) (*SupportAgent, error) {
	agent, err := ar.agents.getOrCreate(chatID, func() (*SupportAgent, error) {
		return NewSupportAgent(ctx, ar.storeClient, isAuthorized)
	})
	if err != nil {
		return nil, err
	}

	authType := "UNAUTHORIZED"
	if isAuthorized {
		authType = "AUTHORIZED"
	}
	log.Printf("[ADK_V2] Created/reused %s agent for chat %s (total agents: %d)", authType, chatID, ar.agents.len())
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

// RemoveAgentForChat removes cached agent for specific chat (memory cleanup)
func (ar *ADKAutoResponderV2) RemoveAgentForChat(chatID string) {
	if ar.agents.remove(chatID) {
		log.Printf("[ADK_V2] Removed agent for chat %s (total agents: %d)", chatID, ar.agents.len())
	}
}

// ClearAllAgents removes all cached agents (memory cleanup on restart/maintenance)
func (ar *ADKAutoResponderV2) ClearAllAgents() {
	count := ar.agents.clear()
	log.Printf("[ADK_V2] Cleared all agents (removed %d agents)", count)
}

// GetAgentCacheStats returns statistics about cached agents
func (ar *ADKAutoResponderV2) GetAgentCacheStats() (total int, chatIDs []string) {
	return ar.agents.len(), ar.agents.keys()
}
