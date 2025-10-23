package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Types and Config
// ---------------------------------------------------------------------------

type AutoResponderConfig struct {
	Enabled         bool   `json:"enabled"`
	BotName         string `json:"botName"`
	DelaySeconds    int    `json:"delaySeconds"`
	IdleTimeMinutes int    `json:"idleTimeMinutes"`
}

func GetDefaultConfig() AutoResponderConfig {
	return AutoResponderConfig{
		Enabled:         true,
		BotName:         "Автоответчик",
		DelaySeconds:    1,
		IdleTimeMinutes: 5,
	}
}

type AutoResponder struct {
	provider    Provider // Универсальный провайдер LLM
	storeClient *StoreClient
	config      AutoResponderConfig
	mu          sync.RWMutex
	history     map[string][]Message
	historyMgr  *HistoryManager
	// Состояние эскалации для каждого чата
	escalations map[string]*EscalationState
	// Callback для отправки сообщений извинения через WebSocket
	onApologyMessage func(chatID uuid.UUID, message *models.Message)
	// 🔒 SECURITY: Счетчик попыток несанкционированного доступа
	unauthorizedAttempts map[string]*UnauthorizedAttemptTracker
}

type EscalationState struct {
	EscalatedAt   time.Time
	AdminNotified bool
	ReturnedAt    *time.Time
}

// NewAutoResponder создаёт новый AutoResponder с указанным провайдером
func NewAutoResponder(provider Provider, cfg AutoResponderConfig) *AutoResponder {
	ar := &AutoResponder{
		provider:             provider,
		storeClient:          NewStoreClient(),
		config:               cfg,
		history:              make(map[string][]Message),
		historyMgr:           NewHistoryManager(),
		escalations:          make(map[string]*EscalationState),
		unauthorizedAttempts: make(map[string]*UnauthorizedAttemptTracker),
	}

	// Запускаем периодическую проверку эскалированных чатов
	go ar.escalationWatcher()

	// Запускаем очистку старых записей попыток доступа
	go ar.cleanupUnauthorizedAttempts()

	log.Printf("[AUTORESPONDER] [SECURITY] Инициализирован с провайдером: %s", provider.GetName())
	log.Println("[AUTORESPONDER] [SECURITY] Усиленная защита доступа к заказам активна")
	log.Println("[AUTORESPONDER] История будет автоматически обрезаться до 20 сообщений (~4000 токенов)")

	return ar
}

// NewAutoResponderWithConfig создаёт AutoResponder используя конфигурацию из env
func NewAutoResponderWithConfig(cfg AutoResponderConfig) (*AutoResponder, error) {
	// Создаём провайдера из переменных окружения
	provider, err := NewProvider(nil) // nil = использовать LoadConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}

	return NewAutoResponder(provider, cfg), nil
}

// ---------------------------------------------------------------------------
// Main Logic
// ---------------------------------------------------------------------------

func (ar *AutoResponder) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[AUTORESPONDER] ProcessMessage START: chatID=%s, sender=%s, content=%s", chat.ID, msg.Sender, msg.Content)
	log.Printf("[AUTORESPONDER] Config: enabled=%v, chat.AutoResponderEnabled=%v, chat.AssignedTo=%v", ar.config.Enabled, chat.AutoResponderEnabled, chat.AssignedTo)

	if !ar.config.Enabled || msg.Sender != "user" {
		log.Printf("[AUTORESPONDER] EARLY RETURN: config.Enabled=%v, sender=%s", ar.config.Enabled, msg.Sender)
		return nil, nil
	}
	// чат уже закреплён за оператором
	if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
		log.Printf("[AUTORESPONDER] EARLY RETURN: чат назначен оператору %v", *chat.AssignedTo)
		return nil, nil
	}
	// автоответчик отключен для этого чата
	if !chat.AutoResponderEnabled {
		log.Printf("[AUTORESPONDER] EARLY RETURN: auto_responder_enabled=false")
		return nil, nil
	}

	chatKey := chat.ID.String()

	// Проверяем состояние эскалации
	ar.mu.Lock()
	escalation := ar.escalations[chatKey]
	ar.mu.Unlock()

	// Если чат эскалирован, проверяем нужно ли вернуть LLM
	if escalation != nil {
		return ar.handleEscalatedChat(ctx, chat, msg, escalation)
	}

	// ── проверка запросов о заказах ──────────────────────────
	if orderQuery := DetectOrderQuery(msg.Content); orderQuery != nil {
		return ar.handleSecurityCheck(ctx, chat, msg, orderQuery, chatKey)
	}

	// ── история ───────────────────────────────────────────────
	ar.mu.Lock()
	hist := ar.history[chatKey]

	// 🔒 ВЫБОР ПРОМПТА: Используем разные промпты для авторизованных и неавторизованных
	if len(hist) == 0 {
		// Проверяем авторизацию для выбора правильного системного промпта
		userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)
		var selectedPrompt string

		if userID > 0 {
			selectedPrompt = systemPromptAuthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с АВТОРИЗОВАННЫМ промптом для user_id=%d", userID)
		} else {
			selectedPrompt = systemPromptUnauthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с НЕАВТОРИЗОВАННЫМ промптом (нет доступа к заказам)")
		}

		hist = []Message{{Role: "system", Content: selectedPrompt}}
	}

	// ВАЖНО: НЕ добавляем сообщение в hist здесь - buildMessages сделает это
	// Обрезаем историю до разумного размера
	hist = ar.historyMgr.TrimHistory(hist)

	ar.history[chatKey] = hist
	ar.mu.Unlock()

	// имитация «печатает…»
	if ar.config.DelaySeconds > 0 {
		select {
		case <-time.After(time.Duration(ar.config.DelaySeconds) * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	// 🔧 FUNCTION CALLING: Используем универсальный интерфейс с поддержкой tools
	tools := GetUniversalStoreFunctionTools()
	log.Printf("[AUTORESPONDER] Отправляем запрос в провайдер %s с %d tools", ar.provider.GetName(), len(tools))

	// Добавляем префикс-напоминание про язык клиента к сообщению
	userMessageWithReminder := fmt.Sprintf("[Respond in customer's language] %s", msg.Content)

	response, err := ar.provider.GenerateWithTools(genCtx, userMessageWithReminder, hist, tools, &GenerateOptions{
		Temperature: 0.7,
		MaxTokens:   1000,
	})
	if err != nil {
		log.Printf("[AUTORESPONDER] Ошибка GenerateWithTools: %v", err)
		return nil, fmt.Errorf("provider GenerateWithTools: %w", err)
	}

	log.Printf("[AUTORESPONDER] Получен ответ: funcCall=%v, text=%s", response.FunctionCall != nil, response.Text)

	var rawResp string

	// Если провайдер хочет вызвать функцию - выполняем её
	if response.FunctionCall != nil {
		log.Printf("[FUNCTION_CALLING] Провайдер вызывает функцию: %s с аргументами: %+v",
			response.FunctionCall.Name, response.FunctionCall.Arguments)

		// Выполняем функцию
		toolCall := ToolCall{
			Name:      response.FunctionCall.Name,
			Arguments: response.FunctionCall.Arguments,
		}
		funcResult, err := ExecuteTool(genCtx, ar.storeClient, toolCall)
		if err != nil {
			log.Printf("[FUNCTION_CALLING] Ошибка выполнения функции %s: %v", response.FunctionCall.Name, err)
			funcResult = fmt.Sprintf("Error executing function: %v", err)
		}

		log.Printf("[FUNCTION_CALLING] Результат функции %s: %s", response.FunctionCall.Name, funcResult)

		// Отправляем результат обратно провайдеру для финального ответа
		finalResponse, err := ar.provider.ContinueWithFunctionResult(
			genCtx,
			hist,
			response.FunctionCall,
			funcResult,
			&GenerateOptions{Temperature: 0.7, MaxTokens: 1000},
		)
		if err != nil {
			return nil, fmt.Errorf("provider ContinueWithFunctionResult: %w", err)
		}

		rawResp = finalResponse.Text
		log.Printf("[FUNCTION_CALLING] Финальный ответ провайдера: %s", rawResp)
	} else {
		// Обычный текстовый ответ без function call
		rawResp = response.Text
	}

	// ── фильтр самоидентификации и проверка тегов эскалации ──
	clean, escalate := sanitize(rawResp)

	// Дополнительная проверка: ищем теги эскалации в тексте
	if !escalate && strings.Contains(clean, "#эскалация") {
		escalate = true
	}

	if escalate {
		// Проверяем, не эскалирован ли уже этот чат
		ar.mu.Lock()
		existingEscalation := ar.escalations[chatKey]
		ar.mu.Unlock()

		if existingEscalation != nil && existingEscalation.ReturnedAt == nil {
			// Чат уже эскалирован и LLM еще не вернулся
			log.Printf("ProcessMessage: чат %s уже эскалирован, пропускаем повторную эскалацию", chatKey)
			escalate = false // Не эскалируем повторно
			clean = "Ваш запрос уже передан нашему специалисту. Ожидайте ответа."
		} else {
			// Сохраняем состояние эскалации
			ar.mu.Lock()
			ar.escalations[chatKey] = &EscalationState{
				EscalatedAt:   time.Now(),
				AdminNotified: true,
				ReturnedAt:    nil,
			}
			ar.mu.Unlock()
		}
	}

	// ── формируем сообщение ──────────────────────────────────
	now := time.Now()
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   clean,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"needEscalation": escalate,
		},
	}

	// сохраняем в локальную историю
	ar.mu.Lock()
	histForSave := ar.history[chatKey]
	// Добавляем сообщение пользователя (оригинальное, без префикса)
	histForSave = append(histForSave, Message{Role: "user", Content: msg.Content})
	// Добавляем ответ assistant
	histForSave = append(histForSave, Message{Role: "assistant", Content: clean})

	// Обрезаем историю после добавления ответа
	histForSave = ar.historyMgr.TrimHistory(histForSave)

	ar.history[chatKey] = histForSave
	ar.mu.Unlock()

	return botMsg, nil
}

// ---------------------------------------------------------------------------
// Database Operations
// ---------------------------------------------------------------------------

func (ar *AutoResponder) SaveChatHistory(ctx context.Context, chatID string, tx *sql.Tx) error {
	ar.mu.RLock()
	hist := ar.history[chatID]
	ar.mu.RUnlock()
	if len(hist) == 0 {
		return nil
	}
	raw, err := json.Marshal(hist)
	if err != nil {
		return fmt.Errorf("SaveChatHistory: marshal: %w", err)
	}

	query := `
		UPDATE chats
		SET metadata = jsonb_set(coalesce(metadata, '{}'::jsonb), '{llmHistory}', $1)
		WHERE id = $2
	`
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, raw, chatID)
	} else {
		_, err = database.DB.ExecContext(ctx, query, raw, chatID)
	}
	return err
}

func (ar *AutoResponder) LoadChatHistory(ctx context.Context, chatID string) error {
	var raw []byte
	query := `SELECT metadata->'llmHistory' FROM chats WHERE id = $1`
	if err := database.DB.QueryRowContext(ctx, query, chatID).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("LoadChatHistory: scan: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	var hist []Message
	if err := json.Unmarshal(raw, &hist); err != nil {
		return fmt.Errorf("LoadChatHistory: unmarshal: %w", err)
	}
	ar.mu.Lock()
	ar.history[chatID] = hist
	ar.mu.Unlock()
	return nil
}

func (ar *AutoResponder) ClearChatHistory(chatID string) {
	ar.mu.Lock()
	delete(ar.history, chatID)
	ar.mu.Unlock()
}

// ClearEscalation очищает состояние эскалации для чата
func (ar *AutoResponder) ClearEscalation(chatID string) {
	ar.mu.Lock()
	delete(ar.escalations, chatID)
	ar.mu.Unlock()
}

// SetApologyCallback устанавливает callback для отправки сообщений извинения
func (ar *AutoResponder) SetApologyCallback(callback func(chatID uuid.UUID, message *models.Message)) {
	ar.onApologyMessage = callback
}
