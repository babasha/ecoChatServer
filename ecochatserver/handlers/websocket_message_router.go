package handlers

import (
	"encoding/json"
	"log"
	"runtime/debug"

	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// allowedMessagesForClientType определяет, какие message types разрешены
// для каждого ClientType. Отсутствие записи (admin) = все разрешены.
//
// ВАЖНО: widget ОБЯЗАН быть в whitelist — иначе анонимный виджет может вызвать
// admin-only команды. Например getChats делает adminIDStr.(string) по контексту,
// которого у widget нет → panic в горутине ReadPump → падение всего процесса
// (recover есть в processWebSocketMessage, но whitelist — первый барьер).
var allowedMessagesForClientType = map[string]map[string]bool{
	websocketpkg.ClientTypeDriver: {
		"sendMessage": true,
		"getChatByID": true,
		"markAsRead":  true,
		"mark_read":   true,
		"typing":      true,
	},
	websocketpkg.ClientTypeMoClient: {
		"sendMessage": true,
		"getChatByID": true,
		"markAsRead":  true,
		"mark_read":   true,
		"typing":      true,
	},
	websocketpkg.ClientTypeMoradaVisitor: {
		"sendMessage": true,
		"getChatByID": true,
		"markAsRead":  true,
		"mark_read":   true,
		"typing":      true,
	},
	websocketpkg.ClientTypeMoradaAgent: {
		"sendMessage": true,
		"getChatByID": true,
		"markAsRead":  true,
		"mark_read":   true,
		"typing":      true,
	},
	websocketpkg.ClientTypeWidget: {
		"sendMessage":       true,
		"getChatByID":       true,
		"getWidgetMessages": true,
		"mark_read":         true,
		"typing":            true,
	},
}

// processWebSocketMessage обрабатывает входящие WebSocket сообщения
func processWebSocketMessage(client *websocketpkg.Client, raw []byte) {
	// Паника в обработчике не должна ронять весь процесс: эта функция вызывается
	// синхронно из горутины ReadPump одного соединения. recover() изолирует сбой
	// до одного сообщения, логирует стек и сообщает клиенту об ошибке.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("processWebSocketMessage: PANIC recovered (client=%s, type=%s): %v\n%s",
				client.ID, client.ClientType, r, debug.Stack())
			client.SendError("internal_error", "Внутренняя ошибка обработки сообщения")
		}
	}()

	var msg websocketpkg.WebSocketMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		client.SendError("invalid_json", "Некорректный формат JSON")
		return
	}

	// Whitelist для moooving driver/mo_client — закрываем доступ к командам,
	// которые требуют admin-контекста (могут привести к panic) или утечке данных.
	if allowed, ok := allowedMessagesForClientType[client.ClientType]; ok {
		if !allowed[msg.Type] {
			client.SendError("forbidden", "Тип сообщения недоступен для этого клиента: "+msg.Type)
			return
		}
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
