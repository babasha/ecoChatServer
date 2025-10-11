package websocket

import (
	"encoding/json"
	"github.com/google/uuid"
)

// WebSocketMessage — общая обёртка для всех JSON-сообщений по WS.
type WebSocketMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// NewMessage  упаковывает любой payload в JSON вида:
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

// NewErrorMessage формир ует ошибку на WS-канале.
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
