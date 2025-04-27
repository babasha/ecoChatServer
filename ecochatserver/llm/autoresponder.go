package llm

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "sync"
    "time"

    "github.com/google/uuid"
    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/models"
)

// systemPrompt — первоначальный промпт для LLM
const systemPrompt = `Ты вежливый и полезный ассистент, который отвечает на вопросы клиентов. Твои ответы должны быть краткими, информативными и дружелюбными.`

// Message — локальный тип для истории диалога с LLM
type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// LLM — интерфейс клиента LLM
type LLM interface {
    GenerateResponse(ctx context.Context, input string, history []Message) (string, error)
}

// AutoResponderConfig содержит настройки автоответчика
type AutoResponderConfig struct {
    Enabled         bool   `json:"enabled"`
    BotName         string `json:"botName"`
    DelaySeconds    int    `json:"delaySeconds"`
    IdleTimeMinutes int    `json:"idleTimeMinutes"`
}

// GetDefaultConfig возвращает конфиг автоответчика по умолчанию
func GetDefaultConfig() AutoResponderConfig {
    return AutoResponderConfig{
        Enabled:         true,
        BotName:         "Автоответчик",
        DelaySeconds:    1,
        IdleTimeMinutes: 5,
    }
}

// AutoResponder представляет автоответчик на базе LLM
type AutoResponder struct {
    client  LLM
    config  AutoResponderConfig
    mu      sync.RWMutex
    history map[string][]Message
}

// NewAutoResponder создаёт новый экземпляр автоответчика
func NewAutoResponder(client LLM, cfg AutoResponderConfig) *AutoResponder {
    return &AutoResponder{
        client:  client,
        config:  cfg,
        history: make(map[string][]Message),
    }
}

// ProcessMessage обрабатывает входящее сообщение и, при необходимости, генерирует ответ
func (ar *AutoResponder) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
    if !ar.config.Enabled || msg.Sender != "user" {
        return nil, nil
    }
    // Если чат уже назначен оператору, автоответчик не вмешивается
    if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
        return nil, nil
    }

    // Используем строковое представление chat.ID как ключ для истории
    chatKey := chat.ID.String()

    // Получаем существующую историю или инициализируем новую
    ar.mu.Lock()
    hist := ar.history[chatKey]
    if len(hist) == 0 {
        hist = []Message{
            {Role: "system", Content: systemPrompt},
        }
    }
    // Добавляем последнее сообщение пользователя
    hist = append(hist, Message{Role: "user", Content: msg.Content})
    ar.history[chatKey] = hist
    ar.mu.Unlock()

    // Симулируем задержку набора (не блокируя через sleep без select)
    if ar.config.DelaySeconds > 0 {
        select {
        case <-time.After(time.Duration(ar.config.DelaySeconds) * time.Second):
        case <-ctx.Done():
            return nil, ctx.Err()
        }
    }

    // Генерируем ответ с учётом таймаута IdleTimeMinutes
    genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
    defer cancel()
    response, err := ar.client.GenerateResponse(genCtx, msg.Content, hist)
    if err != nil {
        return nil, fmt.Errorf("GenerateResponse: %w", err)
    }

    now := time.Now()
    botMsg := &models.Message{
        ChatID:    chat.ID,           // uuid.UUID
        Content:   response,
        Sender:    "admin",
        SenderID:  uuid.Nil,          // пустой UUID
        Timestamp: now,
        Read:      true,
        Type:      "text",
        Metadata: map[string]interface{}{
            "isAutoResponse": true,
            "botName":        ar.config.BotName,
        },
    }

    // Сохраняем ответ бота в локальной истории
    ar.mu.Lock()
    ar.history[chatKey] = append(ar.history[chatKey], Message{Role: "assistant", Content: response})
    ar.mu.Unlock()

    return botMsg, nil
}

// SaveChatHistory сохраняет историю LLM в поле metadata чата в БД.
// Если tx != nil — используется транзакция, иначе — прямой запрос.
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

// LoadChatHistory загружает историю LLM из metadata чата и сохраняет её в память.
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

// ClearChatHistory очищает локальную историю диалога для данного чата.
func (ar *AutoResponder) ClearChatHistory(chatID string) {
    ar.mu.Lock()
    delete(ar.history, chatID)
    ar.mu.Unlock()
}