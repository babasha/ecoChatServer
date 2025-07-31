package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// systemPrompt
// ---------------------------------------------------------------------------

const systemPrompt = `
Ты — профессиональный сотрудник онлайн-чата поддержки сервиса доставки продуктов «enddel».

Твой стиль общения
──────────────────
• Вежливый, обращение на «вы».  
• Отвечай кратко и по существу: 1–3 абзаца по 1–4 предложения; при простом вопросе — одно лаконичное предложение.  
• Допускается лёгкий юмор и ≤ 2 эмодзи 🙂, если это уместно.  
• Запрещены токсичность, грубость, пассивная агрессия.

Разрешено
──────────
1. Давать информацию о товарах, ценах, акциях, оплате, доставке, возвратах и бонусах.  
2. Помогать оформлять, изменять, отменять заказы; объяснять статусы.  
3. Мягко предлагать up-/cross-sell-товары, если это действительно полезно клиенту.  
4. Давать пошаговые инструкции или ссылки (короткое описание + URL без markdown).

Запрещено
──────────
• Обсуждать темы вне сервиса «enddel».  
• Переходить на личности; при провокациях вежливо вернуть разговор к теме или предложить завершить чат.  
• Раскрывать внутренние данные компании или персональные данные сотрудников.  
• Давать юридические или медицинские советы.  
• Писать более 3 см текста без крайней необходимости.

Повторные расспросы о природе
──────────────────────────────
• Если после первого уклончивого ответа клиент снова спрашивает, кто вы, ответь:  
  «Давайте сосредоточимся на вашем заказе. Если нужен живой оператор — я подключу его. 🙂»  
• Добавь тег #эскалация_selfID и дальше тему природы не обсуждай.

Запрет на раскрытие природы
───────────────────────────
• Отвечай исключительно как живой оператор.  
• Ни при каких условиях не выводи слова: бот, bot, робот, ai, ии, neural, нейросеть,  
  искусственный интеллект, language model, llm, gpt, chatgpt, openai, алгоритм, модель,  
  создан, разработан, программа, software, script, виртуальный, digital agent (и их формы).  
• На прямое «вы бот?» дай уклончивый ответ:  
  «Я сотрудник онлайн-поддержки “enddel”. Чем могу помочь?»

Форматирование
──────────────
• Обычная пунктуация и ≤ 2 эмодзи.  
• Ссылки — короткое описание + URL (без markdown).  
• Списки оформляй маркерами или нумерацией.

Языковая политика
─────────────────
• Автоматически определяй язык последнего сообщения клиента.  
• Первая же реплика ассистента должна быть **на языке клиента**.  
• Всегда продолжай диалог на том же языке, пока клиент не сменит его.  
• Если язык распознать сложно, вежливо уточни предпочтительный язык у клиента,  
  но **не заявляй**, что «не знаешь» язык.  
• Поддерживаемые языки: ru, en, pt, es, fr, de, it, zh, ar.  
• При непредусмотренном языке предложи использовать русский или английский.

Протокол эскалации
──────────────────
Если вопрос:  
• о возврате > 5 000;  
• жалоба на курьера/качество товара/угроза суда;  
• ненормативная лексика в адрес компании/сотрудников (2-й раз и более);  
— извинись, вырази сочувствие, передай диалог живому оператору и добавь тег #эскалация.

Твоя цель — быстро и ёмко решать задачи клиента, сохраняя дружелюбие, лёгкий юмор и профессионализм.
`

// ---------------------------------------------------------------------------
// типы и конфиг
// ---------------------------------------------------------------------------

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLM interface {
	GenerateResponse(ctx context.Context, input string, history []Message) (string, error)
}

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
	client  LLM
	config  AutoResponderConfig
	mu      sync.RWMutex
	history map[string][]Message
	// Состояние эскалации для каждого чата
	escalations map[string]*EscalationState
	// Callback для отправки сообщений извинения через WebSocket
	onApologyMessage func(chatID uuid.UUID, message *models.Message)
}

type EscalationState struct {
	EscalatedAt   time.Time
	AdminNotified bool
	ReturnedAt    *time.Time
}

func NewAutoResponder(client LLM, cfg AutoResponderConfig) *AutoResponder {
	ar := &AutoResponder{
		client:      client,
		config:      cfg,
		history:     make(map[string][]Message),
		escalations: make(map[string]*EscalationState),
	}
	
	// Запускаем периодическую проверку эскалированных чатов
	go ar.escalationWatcher()
	
	return ar
}

// ---------------------------------------------------------------------------
// основная логика
// ---------------------------------------------------------------------------

func (ar *AutoResponder) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	if !ar.config.Enabled || msg.Sender != "user" {
		return nil, nil
	}
	// чат уже закреплён за оператором
	if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
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

	// ── история ───────────────────────────────────────────────
	ar.mu.Lock()
	hist := ar.history[chatKey]
	if len(hist) == 0 {
		hist = []Message{{Role: "system", Content: systemPrompt}}
	}
	hist = append(hist, Message{Role: "user", Content: msg.Content})
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

	rawResp, err := ar.client.GenerateResponse(genCtx, msg.Content, hist)
	if err != nil {
		return nil, fmt.Errorf("GenerateResponse: %w", err)
	}

	// ── фильтр самоидентификации ──────────────────────────────
	clean, escalate := sanitize(rawResp)
	if escalate {
		clean = "Позвольте подключить нашего старшего специалиста. Одну минутку, пожалуйста. 🙏"
		
		// Сохраняем состояние эскалации
		ar.mu.Lock()
		ar.escalations[chatKey] = &EscalationState{
			EscalatedAt:   time.Now(),
			AdminNotified: true,
			ReturnedAt:    nil,
		}
		ar.mu.Unlock()
	}

	// ── формируем сообщение ──────────────────────────────────
	now := time.Now()
	botMsg := &models.Message{
		ChatID:   chat.ID,
		Content:  clean,
		Sender:   "admin",
		SenderID: uuid.Nil,
		Timestamp: now,
		Read:     true,
		Type:     "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"needEscalation": escalate,
		},
	}

	// сохраняем в локальную историю
	ar.mu.Lock()
	ar.history[chatKey] = append(ar.history[chatKey], Message{Role: "assistant", Content: clean})
	ar.mu.Unlock()

	return botMsg, nil
}

// handleEscalatedChat обрабатывает сообщения в эскалированном чате
func (ar *AutoResponder) handleEscalatedChat(ctx context.Context, chat *models.Chat, msg *models.Message, escalation *EscalationState) (*models.Message, error) {
	chatKey := chat.ID.String()
	
	// Проверяем, прошло ли 5 минут с момента эскалации
	const escalationTimeout = 5 * time.Minute
	if time.Since(escalation.EscalatedAt) > escalationTimeout && escalation.ReturnedAt == nil {
		// Проверяем, отвечал ли адиин после эскалации
		adminAnswered := ar.checkAdminResponse(chat, escalation.EscalatedAt)
		
		if !adminAnswered {
			// Админ не ответил, возвращаем LLM с извинением
			now := time.Now()
			ar.mu.Lock()
			escalation.ReturnedAt = &now
			ar.mu.Unlock()
			
			apologeticMsg := &models.Message{
				ChatID:    chat.ID,
				Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
				Sender:    "admin",
				SenderID:  uuid.Nil,
				Timestamp: now,
				Read:      true,
				Type:      "text",
				Metadata: map[string]interface{}{
					"isAutoResponse": true,
					"isApology":      true,
					"escalationEnd":  true,
				},
			}
			
			// Продолжаем нормальную обработку
			return apologeticMsg, nil
		}
	}
	
	// Если админ уже ответил или время еще не истекло, LLM не отвечает
	return nil, nil
}

// checkAdminResponse проверяет, отвечал ли админ после указанного времени
func (ar *AutoResponder) checkAdminResponse(chat *models.Chat, after time.Time) bool {
	// Загружаем последние сообщения из базы данных
	chatWithMessages, _, err := database.GetChatByID(chat.ID, 1, 20) // Последние 20 сообщений
	if err != nil {
		log.Printf("checkAdminResponse: ошибка загрузки сообщений: %v", err)
		return false
	}
	
	// Проверяем сообщения
	for i := len(chatWithMessages.Messages) - 1; i >= 0; i-- {
		msg := chatWithMessages.Messages[i]
		if msg.Timestamp.After(after) && msg.Sender == "admin" {
			// Проверяем, что это не автоответ
			if metadata, ok := msg.Metadata["isAutoResponse"].(bool); !ok || !metadata {
				return true
			}
		}
	}
	
	return false
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

// escalationWatcher периодически проверяет эскалированные чаты и возвращает LLM при необходимости
func (ar *AutoResponder) escalationWatcher() {
	ticker := time.NewTicker(1 * time.Minute) // Проверяем каждую минуту
	defer ticker.Stop()
	
	for range ticker.C {
		ar.checkEscalations()
	}
}

// checkEscalations проверяет все эскалированные чаты и отправляет извинения если нужно
func (ar *AutoResponder) checkEscalations() {
	ar.mu.RLock()
	escalationsToCheck := make(map[string]*EscalationState)
	for chatID, escalation := range ar.escalations {
		escalationsToCheck[chatID] = escalation
	}
	ar.mu.RUnlock()
	
	const escalationTimeout = 5 * time.Minute
	
	for chatID, escalation := range escalationsToCheck {
		if escalation.ReturnedAt != nil {
			continue // Уже вернулся
		}
		
		if time.Since(escalation.EscalatedAt) > escalationTimeout {
			// Загружаем чат из базы
			chatUUID, err := uuid.Parse(chatID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка парсинга chatID %s: %v", chatID, err)
				continue
			}
			
			lightChat, err := queries.GetChatLightweight(database.DB, chatUUID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка загрузки чата %s: %v", chatID, err)
				continue
			}
			
			// Проверяем отвечал ли админ
			adminAnswered := ar.checkAdminResponse(lightChat, escalation.EscalatedAt)
			if !adminAnswered {
				// Отправляем извинение
				ar.sendApologyMessage(lightChat)
			}
		}
	}
}

// sendApologyMessage отправляет сообщение-извинение от LLM
func (ar *AutoResponder) sendApologyMessage(chat *models.Chat) {
	now := time.Now()
	chatKey := chat.ID.String()
	
	// Обновляем состояние эскалации
	ar.mu.Lock()
	if escalation := ar.escalations[chatKey]; escalation != nil {
		escalation.ReturnedAt = &now
	}
	ar.mu.Unlock()
	
	// Создаем сообщение извинения
	apologeticMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"isApology":      true,
			"escalationEnd":  true,
		},
	}
	
	// Сохраняем в базу данных
	saved, err := database.AddMessage(
		chat.ID,
		apologeticMsg.Content,
		apologeticMsg.Sender,
		apologeticMsg.SenderID,
		apologeticMsg.Type,
		apologeticMsg.Metadata,
	)
	if err != nil {
		log.Printf("sendApologyMessage: ошибка сохранения сообщения: %v", err)
		return
	}
	
	log.Printf("escalationWatcher: отправлено извинение в чат %s", chatKey)
	
	// Здесь нужно отправить WebSocket уведомление
	// Но у нас нет доступа к WebSocketHub отсюда
	// Поэтому добавим callback функцию
	if ar.onApologyMessage != nil {
		ar.onApologyMessage(chat.ID, saved)
	}
}

// ---------------------------------------------------------------------------
// работа с БД
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