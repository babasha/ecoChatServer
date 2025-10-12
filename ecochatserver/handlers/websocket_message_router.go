package handlers

import (
	"encoding/json"

	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// processWebSocketMessage обрабатывает входящие WebSocket сообщения
func processWebSocketMessage(client *websocketpkg.Client, raw []byte) {
	var msg websocketpkg.WebSocketMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		client.SendError("invalid_json", "Некорректный формат JSON")
		return
	}

	// Получаем данные из контекста Gin
	ginCtx := client.Context

	switch msg.Type {
	case "getChats":
		processGetChats(client, msg.Payload, ginCtx)
	case "getChatByID":
		processGetChatByID(client, msg.Payload, ginCtx)
	case "sendMessage":
		processSendMessage(client, msg.Payload, ginCtx)
	case "markAsRead":
		processMarkAsRead(client, msg.Payload, ginCtx)
	case "mark_read":
		// Для виджета: помечаем сообщения админа как прочитанные клиентом
		processMarkReadFromWidget(client, msg.Payload, ginCtx)
	case "typing":
		processTypingStatus(client, msg.Payload, ginCtx)
	case "getWidgetMessages":
		processGetWidgetMessages(client, msg.Payload, ginCtx)
	case "subscribeToLogs":
		processSubscribeToLogs(client, msg.Payload, ginCtx)
	case "unsubscribeFromLogs":
		processUnsubscribeFromLogs(client, msg.Payload, ginCtx)
	default:
		client.SendError("unknown_type", "Неизвестный тип сообщения: "+msg.Type)
	}
}
