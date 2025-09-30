package handlers

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetChats возвращает список чатов для админки
func GetChats(c *gin.Context) {
	// Получаем параметры пагинации
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}

	// Для админки получаем ВСЕ чаты всех клиентов
	// Используем специальное значение для обозначения "все клиенты"
	var clientID uuid.UUID // будет nil UUID (00000000-0000-0000-0000-000000000000)

	// Получаем adminID из JWT токена (опционально, для фильтрации)
	adminIDStr, exists := c.Get("adminID")
	var adminID uuid.UUID
	if exists {
		adminID, _ = uuid.Parse(adminIDStr.(string))
	} else {
		adminID = uuid.Nil
	}

	chats, total, err := database.GetChats(clientID, adminID, page-1, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Ошибка получения чатов",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chats": chats,
		"pagination": gin.H{
			"page":  page,
			"size":  size,
			"total": total,
			"pages": (total + size - 1) / size,
		},
	})
}

// GetChatByID возвращает конкретный чат с сообщениями
func GetChatByID(c *gin.Context) {
	chatIDStr := c.Param("id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID чата",
		})
		return
	}

	// Получаем параметры пагинации для сообщений
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}

	chat, total, err := database.GetChatByID(chatID, page-1, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Ошибка получения чата",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chat": chat,
		"pagination": gin.H{
			"page":  page,
			"size":  size,
			"total": total,
			"pages": (total + size - 1) / size,
		},
	})
}

// SendMessageToChat отправляет сообщение от админа в чат
func SendMessageToChat(c *gin.Context) {
	chatIDStr := c.Param("id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("SendMessageToChat: неверный UUID чата: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID чата"})
		return
	}

	// Парсим тело запроса
	var request struct {
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("SendMessageToChat: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	if request.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Содержимое сообщения не может быть пустым"})
		return
	}

	log.Printf("SendMessageToChat: отправка сообщения в чат %s от админа", chatIDStr)

	// Получаем реальный ID админа из JWT токена
	adminIDStr, exists := c.Get("adminID")
	if !exists {
		log.Printf("SendMessageToChat: adminID не найден в контексте")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Не авторизован"})
		return
	}

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		log.Printf("SendMessageToChat: ошибка парсинга adminID: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Некорректный ID администратора"})
		return
	}

	// Добавляем сообщение в базу данных
	message, err := database.AddMessage(
		chatID,
		request.Content,
		"admin",
		adminID,
		"text",
		nil, // metadata
	)
	if err != nil {
		log.Printf("SendMessageToChat: ошибка добавления сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сохранения сообщения"})
		return
	}

	log.Printf("SendMessageToChat: сообщение сохранено: ID=%s", message.ID)

	// Обновляем время чата
	if err := queries.UpdateChatTimestamp(database.DB, chatID); err != nil {
		log.Printf("SendMessageToChat: ошибка обновления времени чата: %v", err)
	}

	// Отправляем WebSocket уведомление в формате совместимом с виджетом и админкой
	payload := map[string]interface{}{
		"chatId": chatID.String(), // для админки
		"message": map[string]interface{}{
			"id":        message.ID.String(),
			"chatId":    chatID.String(),
			"content":   message.Content,
			"sender":    message.Sender,
			"senderId":  message.SenderID.String(),
			"timestamp": message.Timestamp.Format(time.RFC3339),
			"read":      false,
			"type":      message.Type,
		},
		"chat": map[string]interface{}{ // для виджета
			"id": chatID.String(),
		},
	}

	wsMessage, _ := websocket.NewMessage("new_message", payload)
	totalSent := WebSocketHub.SendToChatAndAdmins(chatID.String(), wsMessage)
	log.Printf("SendMessageToChat: WebSocket уведомление отправлено %d клиентам", totalSent)

	// Возвращаем только статус успеха
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Сообщение отправлено",
	})
}

// ToggleAutoResponder включает/выключает автоответчик для чата
func ToggleAutoResponder(c *gin.Context) {
	chatIDStr := c.Param("id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("ToggleAutoResponder: неверный UUID чата: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID чата"})
		return
	}

	// Парсим тело запроса
	var request struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("ToggleAutoResponder: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	log.Printf("ToggleAutoResponder: обновление автоответчика для чата %s, enabled=%t", chatIDStr, request.Enabled)

	// Обновляем статус автоответчика в базе данных
	if err := queries.UpdateAutoResponder(database.DB, chatID, request.Enabled); err != nil {
		log.Printf("ToggleAutoResponder: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления автоответчика"})
		return
	}

	log.Printf("ToggleAutoResponder: автоответчик успешно обновлен для чата %s", chatIDStr)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"enabled": request.Enabled,
		"message": "Автоответчик успешно обновлен",
	})
}
