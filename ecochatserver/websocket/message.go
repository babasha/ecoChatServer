package websocket

import (
    "encoding/json"
    "time"

    "github.com/egor/ecochatserver/models"
)

type WebSocketMessage struct {
    Type    string          `json:"type"`
    Payload json.RawMessage `json:"payload"`
}

func NewMessage(messageType string, payload interface{}) ([]byte, error) {
    p, err := json.Marshal(payload)
    if err != nil {
        return nil, err
    }
    msg := WebSocketMessage{
        Type:    messageType,
        Payload: p,
    }
    return json.Marshal(msg)
}

func NewChatMessage(chat *models.Chat, message *models.Message) ([]byte, error) {
    payload := struct {
        Chat    *models.Chat    `json:"chat"`
        Message *models.Message `json:"message"`
    }{Chat: chat, Message: message}
    return NewMessage("new_message", payload)
}

func NewWidgetMessage(message *models.Message) ([]byte, error) {
    payload := struct {
        ID        string                 `json:"id"`
        Content   string                 `json:"content"`
        Sender    string                 `json:"sender"`
        Timestamp string                 `json:"timestamp"`
        Metadata  map[string]interface{} `json:"metadata,omitempty"`
    }{
        ID:        message.ID,
        Content:   message.Content,
        Sender:    message.Sender,
        Timestamp: message.Timestamp.Format(time.RFC3339),
        Metadata:  message.Metadata,
    }
    return NewMessage("message", payload)
}

func NewChatUpdatedMessage(chat *models.Chat) ([]byte, error) {
    return NewMessage("chat_updated", chat)
}

func NewChatListMessage(chats []models.ChatResponse) ([]byte, error) {
    return NewMessage("chat_list", chats)
}

func NewErrorMessage(errorText string) ([]byte, error) {
    payload := struct {
        Error string `json:"error"`
    }{Error: errorText}
    return NewMessage("error", payload)
}

func NewTypingMessage(chatID string, isTyping bool, sender string) ([]byte, error) {
    payload := struct {
        ChatID   string `json:"chatId"`
        IsTyping bool   `json:"isTyping"`
        Sender   string `json:"sender"`
    }{
        ChatID:   chatID,
        IsTyping: isTyping,
        Sender:   sender,
    }
    return NewMessage("typing", payload)
}