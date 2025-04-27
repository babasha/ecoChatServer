package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
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
}

func NewAutoResponder(client LLM, cfg AutoResponderConfig) *AutoResponder {
	return &AutoResponder{
		client:  client,
		config:  cfg,
		history: make(map[string][]Message),
	}
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