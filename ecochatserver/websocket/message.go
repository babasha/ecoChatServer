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