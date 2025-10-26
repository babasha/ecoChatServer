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
// Constants
// ---------------------------------------------------------------------------

const (
	// Максимальное количество сообщений для загрузки из БД
	MaxMessagesLoad = 20
	// Таймаут эскалации (время ожидания ответа админа)
	EscalationTimeout = 5 * time.Minute
	// Максимальный возраст завершенной эскалации перед очисткой
	EscalationTTL = 1 * time.Hour
	// Максимальное количество попыток неавторизованного доступа
	MaxUnauthorizedAttempts = 3
	// Время блокировки после превышения лимита попыток
	UnauthorizedBlockDuration = 5 * time.Minute
	// Период очистки старых записей попыток доступа
	UnauthorizedCleanupInterval = 10 * time.Minute
	// Максимальный возраст записи попытки доступа
	UnauthorizedAttemptTTL = 1 * time.Hour
	// Период проверки эскалаций
	EscalationCheckInterval = 1 * time.Minute
	// Значения конфигурации по умолчанию
	DefaultBotName         = "Автоответчик"
	DefaultDelaySeconds    = 1
	DefaultIdleTimeMinutes = 5
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
		BotName:         DefaultBotName,
		DelaySeconds:    DefaultDelaySeconds,
		IdleTimeMinutes: DefaultIdleTimeMinutes,
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
	log.Printf("[AUTORESPONDER] История будет автоматически обрезаться до %d сообщений (~4000 токенов)", MaxMessagesLoad)

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
	ar.mu.RLock() // Используем RLock для чтения (не блокируем параллельные запросы)
	escalation := ar.escalations[chatKey]
	ar.mu.RUnlock()

	// Если чат эскалирован, проверяем нужно ли вернуть LLM
	if escalation != nil {
		return ar.handleEscalatedChat(ctx, chat, msg, escalation)
	}

	// ── ПОЛУЧАЕМ USER_ID ОДИН РАЗ ───────────────────────────────
	// Это оптимизация: получаем userID в начале для использования во всех проверках
	userID := ar.getUserIDWithCache(ctx, chat)
	if userID > 0 {
		log.Printf("[AUTORESPONDER] Определен user_id магазина: %d для чата %s", userID, chat.ID)
	}

	// ── ИНИЦИАЛИЗАЦИЯ ИСТОРИИ (ДО проверки заказов!) ──────────
	// Проверяем нужна ли инициализация (без lock для быстрой проверки)
	ar.mu.RLock()
	hist := ar.history[chatKey]
	needsInit := len(hist) == 0
	ar.mu.RUnlock()

	// Если история пуста в памяти, пытаемся загрузить из БД
	if needsInit {
		log.Printf("[AUTORESPONDER] История пуста в памяти для чата %s, загружаем из БД", chatKey)
		if err := ar.LoadChatHistory(ctx, chatKey); err != nil {
			log.Printf("[AUTORESPONDER] Ошибка загрузки истории из БД для чата %s: %v", chatKey, err)
			// Продолжаем с пустой историей - будет инициализирована ниже
		} else {
			// Проверяем еще раз - могла загрузиться история из БД
			ar.mu.RLock()
			hist = ar.history[chatKey]
			needsInit = len(hist) == 0
			ar.mu.RUnlock()

			if !needsInit {
				log.Printf("[AUTORESPONDER] История успешно загружена из БД: %d сообщений", len(hist))

				// 🔒 SECURITY: Проверяем соответствие system prompt уровню авторизации
				// Если пользователь добавил email после создания истории, нужно обновить промпт
				if len(hist) > 0 && hist[0].Role == "system" {
					expectedPrompt := systemPromptUnauthorized
					if userID > 0 {
						expectedPrompt = systemPromptAuthorized
					}

					// Если промпт не соответствует текущему статусу авторизации - обновляем
					if hist[0].Content != expectedPrompt {
						ar.mu.Lock()
						if len(ar.history[chatKey]) > 0 {
							ar.history[chatKey][0].Content = expectedPrompt
							hist = ar.history[chatKey]
							log.Printf("[AUTORESPONDER] [SECURITY] System prompt обновлен в соответствии с авторизацией (userID=%d)", userID)
						}
						ar.mu.Unlock()

						// Асинхронно сохраняем обновленный промпт в БД
						go func() {
							if err := ar.SaveChatHistory(context.Background(), chatKey, nil); err != nil {
								log.Printf("[AUTORESPONDER] [SECURITY] Ошибка сохранения обновленного промпта в БД: %v", err)
							} else {
								log.Printf("[AUTORESPONDER] [SECURITY] Обновленный промпт сохранен в БД для чата %s", chatKey)
							}
						}()
					}
				}
			}
		}
	}

	// Если история все еще пуста, инициализируем с правильным промптом
	if needsInit {
		var selectedPrompt string

		if userID > 0 {
			selectedPrompt = systemPromptAuthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с АВТОРИЗОВАННЫМ промптом для user_id=%d", userID)
		} else {
			selectedPrompt = systemPromptUnauthorized
			log.Printf("[AUTORESPONDER] [SECURITY] Инициализация истории с НЕАВТОРИЗОВАННЫМ промптом (нет доступа к заказам)")
		}

		// Сохраняем с lock
		ar.mu.Lock()
		// Проверяем еще раз - мог другой поток инициализировать
		if len(ar.history[chatKey]) == 0 {
			ar.history[chatKey] = []Message{{Role: "system", Content: selectedPrompt}}
		}
		hist = ar.history[chatKey]
		ar.mu.Unlock()
	}

	// ── проверка запросов о заказах ──────────────────────────
	// Теперь передаем уже известный userID, избегая повторных HTTP запросов
	if orderQuery := DetectOrderQuery(msg.Content); orderQuery != nil {
		return ar.handleSecurityCheck(ctx, chat, msg, orderQuery, chatKey, userID)
	}

	// Получаем финальную копию истории для работы
	ar.mu.RLock()
	hist = ar.history[chatKey]
	hist = ar.historyMgr.TrimHistory(hist)
	// Создаем независимую копию для работы (защита от race conditions)
	histForLLM := make([]Message, len(hist))
	copy(histForLLM, hist)
	ar.mu.RUnlock()

	// Определяем язык клиента по метаданным последнего сообщения или БД
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
			// Логируем только реальные ошибки, игнорируем отсутствие записи
			log.Printf("[AUTORESPONDER] Ошибка получения языка клиента из БД для чата %s: %v", chat.ID, err)
		}
	}

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

	// Оптимизация: добавляем языковой контекст в историю только если язык определён
	histWithLang := histForLLM
	if customerLang != "" && customerLang != "unknown" {
		// Обновляем только system prompt (первое сообщение)
		// histForLLM уже является копией, можем модифицировать напрямую
		if len(histWithLang) > 0 && histWithLang[0].Role == "system" {
			langInstruction := fmt.Sprintf("\n\n🌍 CRITICAL: Customer's language is '%s'. You MUST respond in %s.", customerLang, customerLang)
			histWithLang[0].Content = histWithLang[0].Content + langInstruction
		}
	}

	response, err := ar.provider.GenerateWithTools(genCtx, msg.Content, histWithLang, tools, &GenerateOptions{
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
		// Используем histWithLang чтобы сохранить языковой контекст
		finalResponse, err := ar.provider.ContinueWithFunctionResult(
			genCtx,
			histWithLang,
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

	// 🌍 ОПТИМИЗАЦИЯ: Убран автоматический DetectAndTranslate
	// LLM уже получил инструкцию "You MUST respond in {customerLang}" в system prompt (строка 294)
	// DetectAndTranslate = второй LLM call (2x стоимость, 2x латентность)
	//
	// Если LLM не ответил на нужном языке:
	// 1. Это означает что промпт недостаточно строгий
	// 2. Или модель игнорирует инструкции
	// 3. В обоих случаях нужно улучшать промпт, а не добавлять костыль
	//
	// Для compatibility оставляем метаданные без перевода:
	var translationMeta map[string]interface{}
	if customerLang != "" && customerLang != "unknown" {
		translationMeta = map[string]interface{}{
			"detectedLanguage": customerLang, // Предполагаем что LLM ответил правильно
			"targetLanguage":   customerLang,
			"translatedText":   clean,
			"isTranslated":     false, // LLM уже ответил на нужном языке
			"translations": map[string]interface{}{
				customerLang: clean,
			},
		}
	}

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
	botMetadata := map[string]interface{}{
		"isAutoResponse": true,
		"botName":        ar.config.BotName,
		"needEscalation": escalate,
	}

	if translationMeta != nil {
		for k, v := range translationMeta {
			botMetadata[k] = v
		}
	} else if customerLang != "" && customerLang != "unknown" {
		botMetadata["detectedLanguage"] = customerLang
		botMetadata["targetLanguage"] = customerLang
		botMetadata["translatedText"] = clean
		botMetadata["isTranslated"] = false
		botMetadata["translations"] = map[string]interface{}{
			customerLang: clean,
		}
	}

	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   clean,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata:  botMetadata,
	}

	// сохраняем в локальную историю
	// Используем одну критическую секцию для атомарного обновления
	ar.mu.Lock()
	currentHist := ar.history[chatKey]
	// Добавляем сообщение пользователя (оригинальное, без префикса)
	currentHist = append(currentHist, Message{Role: "user", Content: msg.Content})
	// Добавляем ответ assistant
	currentHist = append(currentHist, Message{Role: "assistant", Content: clean})

	// Обрезаем историю после добавления ответа
	currentHist = ar.historyMgr.TrimHistory(currentHist)

	ar.history[chatKey] = currentHist
	ar.mu.Unlock()

	// Асинхронно сохраняем историю в БД для персистентности
	go func() {
		if err := ar.SaveChatHistory(context.Background(), chatKey, nil); err != nil {
			log.Printf("[AUTORESPONDER] Ошибка сохранения истории в БД для чата %s: %v", chatKey, err)
		} else {
			log.Printf("[AUTORESPONDER] История сохранена в БД для чата %s (%d сообщений)", chatKey, len(currentHist))
		}
	}()

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

// ---------------------------------------------------------------------------
// Helper Methods
// ---------------------------------------------------------------------------

// getUserIDWithCache получает userID с кешированием в метаданных чата
// Это избегает повторных HTTP запросов к API магазина
func (ar *AutoResponder) getUserIDWithCache(ctx context.Context, chat *models.Chat) int {
	// 1. Проверяем кеш в метаданных чата
	if chat.Metadata != nil {
		if userID, ok := chat.Metadata["store_user_id"].(int); ok && userID > 0 {
			return userID
		}
		if userID, ok := chat.Metadata["store_user_id"].(float64); ok && userID > 0 {
			return int(userID)
		}
	}

	// 2. Кеша нет - делаем HTTP запрос
	userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	// 3. Если нашли - сохраняем в кеш (в память)
	if userID > 0 {
		if chat.Metadata == nil {
			chat.Metadata = make(map[string]interface{})
		}
		chat.Metadata["store_user_id"] = userID

		// 4. Асинхронно сохраняем в БД для персистентности
		go ar.saveUserIDToDatabase(ctx, chat.ID, userID)
	}

	return userID
}

// saveUserIDToDatabase сохраняет userID в метаданных чата в БД
func (ar *AutoResponder) saveUserIDToDatabase(ctx context.Context, chatID uuid.UUID, userID int) {
	query := `
		UPDATE chats
		SET metadata = jsonb_set(
			COALESCE(metadata, '{}'::jsonb),
			'{store_user_id}',
			$1::text::jsonb
		)
		WHERE id = $2
	`

	_, err := database.DB.ExecContext(ctx, query, userID, chatID)
	if err != nil {
		log.Printf("[AUTORESPONDER] Ошибка сохранения store_user_id в БД для чата %s: %v", chatID, err)
	} else {
		log.Printf("[AUTORESPONDER] store_user_id=%d сохранен в БД для чата %s", userID, chatID)
	}
}
