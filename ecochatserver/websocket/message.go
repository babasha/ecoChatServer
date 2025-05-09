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
        ChatID     uuid.UUID        `json:"chatId"`
        Message    *models.Message  `json:"message"`
        UnreadCount int             `json:"unreadCount"`
    }{
        ChatID:      chat.ID,
        Message:     message,
        UnreadCount: chat.UnreadCount,
    }
    return NewMessage("new_message", payload)
}

// NewTypingMessage уведомляет, что пользователь печатает.
func NewTypingMessage(chatID uuid.UUID, isTyping bool, sender string) ([]byte, error) {
    payload := struct {
        ChatID   uuid.UUID `json:"chatId"`
        IsTyping bool      `json:"isTyping"`
        Sender   string    `json:"sender"`
    }{
        ChatID:   chatID,
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