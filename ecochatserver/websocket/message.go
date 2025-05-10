package websocket

import (
    "encoding/json"
    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
)

// WebSocketMessage — общая обёртка для всех JSON-сообщений по WS.
type WebSocketMessage struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload"`
}

// NewMessage упаковывает любой payload в JSON вида:
// { "type": "...", "payload": { ... } }
func NewMessage(msgType string, payload interface{}) ([]byte, error) {
    raw, err := json.Marshal(payload)
    if err != nil {
        return nil, err
    }
    envelope := WebSocketMessage{
        Type:    msgType,
        Payload: raw,
    }
    return json.Marshal(envelope)
}

// NewChatMessage строит сообщение о новом чате или сообщении.
func NewChatMessage(chat *models.Chat, message *models.Message) ([]byte, error) {
    payload := struct {
        ChatID      uuid.UUID        `json:"chatId"`
        Message     *models.Message  `json:"message"`
        UnreadCount int              `json:"unreadCount,omitempty"`
    }{
        ChatID:      message.ChatID,
        Message:     message,
    }
    
    // Если чат есть, добавляем счетчик непрочитанных
    if chat != nil {
        // Вычисляем непрочитанные сообщения
        unread := 0
        for _, msg := range chat.Messages {
            if msg.Sender == "user" && !msg.Read {
                unread++
            }
        }
        payload.UnreadCount = unread
    }
    
    return NewMessage("new_message", payload)
}

// NewWidgetMessage создает сообщение для отправки виджету
func NewWidgetMessage(message *models.Message) ([]byte, error) {
    // Упрощенная версия сообщения для виджета
    payload := struct {
        ID        string    `json:"id"`
        ChatID    string    `json:"chatId"`
        Content   string    `json:"content"`
        Sender    string    `json:"sender"`
        Timestamp string    `json:"timestamp"`
        Type      string    `json:"type,omitempty"`
    }{
        ID:        message.ID.String(),
        ChatID:    message.ChatID.String(),
        Content:   message.Content,
        Sender:    message.Sender,
        Timestamp: message.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
        Type:      message.Type,
    }
    
    return NewMessage("widget_message", payload)
}

// NewTypingMessage уведомляет, что пользователь печатает.
func NewTypingMessage(chatID uuid.UUID, isTyping bool, sender string) ([]byte, error) {
    payload := struct {
        ChatID   string `json:"chatId"`
        IsTyping bool   `json:"isTyping"`
        Sender   string `json:"sender"`
    }{
        ChatID:   chatID.String(),
        IsTyping: isTyping,
        Sender:   sender,
    }
    return NewMessage("typing", payload)
}

// NewErrorMessage формирует ошибку на WS-канале.
func NewErrorMessage(code, text string) ([]byte, error) {
    payload := struct {
        Code string `json:"code"`
        Text string `json:"text"`
    }{
        Code: code,
        Text: text,
    }
    return NewMessage("error", payload)
}