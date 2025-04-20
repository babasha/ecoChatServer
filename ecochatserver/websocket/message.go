package websocket

import (
	"encoding/json"
	"ecochatserver/models"
)

// WebSocketMessage представляет сообщение для WebSocket
type WebSocketMessage struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// NewMessage создает новое сообщение с указанным типом и данными
func NewMessage(messageType string, payload interface{}) ([]byte, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	message := WebSocketMessage{
		Type:    messageType,
		Payload: payloadJSON,
	}

	return json.Marshal(message)
}

// NewChatMessage создает сообщение о новом сообщении в чате
func NewChatMessage(chat *models.Chat, message *models.Message) ([]byte, error) {
	payload := struct {
		Chat    *models.Chat    `json:"chat"`
		Message *models.Message `json:"message"`
	}{
		Chat:    chat,
		Message: message,
	}

	return NewMessage("new_message", payload)
}

// NewWidgetMessage создает сообщение для виджета
func NewWidgetMessage(message *models.Message) ([]byte, error) {
	// Упрощенный формат сообщения для виджета
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
		Timestamp: message.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		Metadata:  message.Metadata,
	}

	return NewMessage("message", payload)
}

// NewChatUpdatedMessage создает сообщение об обновлении чата
func NewChatUpdatedMessage(chat *models.Chat) ([]byte, error) {
	return NewMessage("chat_updated", chat)
}

// NewChatListMessage создает сообщение со списком чатов
func NewChatListMessage(chats []models.ChatResponse) ([]byte, error) {
	return NewMessage("chat_list", chats)
}

// NewErrorMessage создает сообщение об ошибке
func NewErrorMessage(errorText string) ([]byte, error) {
	payload := struct {
		Error string `json:"error"`
	}{
		Error: errorText,
	}

	return NewMessage("error", payload)
}

// NewTypingMessage создает сообщение о наборе текста
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