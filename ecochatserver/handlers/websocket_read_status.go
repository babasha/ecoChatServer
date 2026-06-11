package handlers

import (
	"encoding/json"
	"log"

	"github.com/gin-gonic/gin"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// processMarkAsRead помечает сообщения как прочитанные (от админа)
func processMarkAsRead(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID     string   `json:"chatId"`
		ChatIDOld  string   `json:"chatID"` // обратная совместимость
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для markAsRead")
		return
	}

	chatID, ok := resolveChatID(client, p.ChatID, p.ChatIDOld)
	if !ok {
		return
	}

	if !enforceChatScope(client, chatID) {
		return
	}

	if len(p.MessageIDs) == 0 {
		client.SendError("invalid_payload", "Массив messageIds не может быть пустым")
		return
	}

	// Конвертируем строки в UUID
	messageUUIDs, ok := parseMessageIDs(client, p.MessageIDs)
	if !ok {
		return
	}

	log.Printf("processMarkAsRead: отметка %d сообщений как прочитанных в чате %s", len(messageUUIDs), chatID)

	markedCount, err := database.MarkSpecificMessagesAsRead(messageUUIDs)
	if err != nil {
		log.Printf("processMarkAsRead: ошибка: %v", err)
		client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
		return
	}

	log.Printf("processMarkAsRead: успешно помечено %d сообщений из %d", markedCount, len(messageUUIDs))

	// Отправляем обновление всем клиентам чата о прочтении сообщений ТОЛЬКО если что-то изменилось
	if markedCount > 0 {
		statusMsg, _ := websocketpkg.NewMessage("messages_read", map[string]interface{}{
			"chatId":     chatID.String(),
			"messageIds": p.MessageIDs,
			"count":      markedCount,
			"readBy":     client.ID,
		})

		// Отправляем статус клиентам чата и всем админам
		WebSocketHub.SendToChatAndAdmins(chatID.String(), statusMsg)
	}

	// Отправляем подтверждение отправителю запроса
	response := map[string]interface{}{
		"type": "markAsReadConfirmed",
		"payload": map[string]interface{}{
			"chatId": chatID.String(),
			"status": "success",
			"marked": markedCount,
		},
	}

	if err := client.SendJSON(response); err != nil {
		log.Printf("processMarkAsRead: ошибка отправки подтверждения: %v", err)
	}
}

// processMarkReadFromWidget обрабатывает пометку сообщений админа как прочитанных (от виджета/клиента)
func processMarkReadFromWidget(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID     string   `json:"chatId"`
		MessageIDs []string `json:"messageIds"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для mark_read")
		return
	}

	chatID, ok := resolveChatID(client, p.ChatID, "")
	if !ok {
		return
	}

	if !enforceChatScope(client, chatID) {
		return
	}

	// Если messageIds не передан или пустой - используем старый метод (помечаем все)
	if len(p.MessageIDs) == 0 {
		log.Printf("processMarkReadFromWidget: клиент пометил ВСЕ сообщения админа как прочитанные в чате %s (fallback)", chatID)

		if err := queries.MarkAdminMessagesAsRead(database.DB, chatID); err != nil {
			log.Printf("processMarkReadFromWidget: ошибка: %v", err)
			client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
			return
		}

		// Отправляем уведомление админам (без messageIds для обратной совместимости)
		statusMsg, _ := websocketpkg.NewMessage("messages_read", map[string]interface{}{
			"chatId": chatID.String(),
		})
		WebSocketHub.SendToChatAndAdmins(chatID.String(), statusMsg)

		log.Printf("processMarkReadFromWidget: успешно обновлен статус всех сообщений в чате %s", chatID)
		return
	}

	// Новый метод: помечаем только конкретные сообщения
	log.Printf("processMarkReadFromWidget: клиент пометил %d конкретных сообщений админа как прочитанные в чате %s", len(p.MessageIDs), chatID)

	// Конвертируем строки в UUID
	messageUUIDs, ok := parseMessageIDs(client, p.MessageIDs)
	if !ok {
		return
	}

	// Помечаем конкретные сообщения как прочитанные
	markedCount, err := database.MarkSpecificMessagesAsRead(messageUUIDs)
	if err != nil {
		log.Printf("processMarkReadFromWidget: ошибка: %v", err)
		client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
		return
	}

	log.Printf("processMarkReadFromWidget: успешно помечено %d сообщений из %d", markedCount, len(messageUUIDs))

	// Отправляем уведомление админам о том, что клиент прочитал сообщения ТОЛЬКО если что-то изменилось
	if markedCount > 0 {
		statusMsg, _ := websocketpkg.NewMessage("messages_read", map[string]interface{}{
			"chatId":     chatID.String(),
			"messageIds": p.MessageIDs,
			"count":      markedCount,
		})

		// Отправляем статус админам, подключенным к этому чату
		WebSocketHub.SendToChatAndAdmins(chatID.String(), statusMsg)
	}

	log.Printf("processMarkReadFromWidget: успешно обновлен статус сообщений в чате %s", chatID)
}
