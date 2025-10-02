package handlers

import (
	"log"
	"net/http"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetChats возвращает список чатов для админки
func GetChats(c *gin.Context) {
	// Получаем параметры пагинации
	page, size := parsePagination(c)

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

	chats, total, err := database.GetChats(clientID, adminID, page, size)
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
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Получаем параметры пагинации для сообщений
	page, size := parsePagination(c)

	chat, total, err := database.GetChatByID(chatID, page, size)
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
	chatID, err := parseChatID(c)
	if err != nil {
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

	log.Printf("SendMessageToChat: отправка сообщения в чат %s от админа", chatID)

	// Получаем реальный ID админа из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	// Переводим сообщение админа на язык клиента
	var messageContent = request.Content
	var messageMetadata map[string]interface{}

	if Translator != nil {
		log.Printf("SendMessageToChat: попытка перевода сообщения админа")
		result, err := Translator.TranslateAdminMessage(c.Request.Context(), request.Content, chatID, adminID)
		if err != nil {
			log.Printf("SendMessageToChat: ошибка перевода сообщения: %v", err)
			// Продолжаем с оригинальным текстом
		} else if result != nil {
			// Используем переведенный текст для клиента
			messageContent = result.Content
			messageMetadata = result.Metadata
			if result.WasTranslated {
				log.Printf("SendMessageToChat: сообщение переведено с %s на %s",
					result.Metadata["sourceLanguage"], result.Metadata["targetLanguage"])
			} else {
				log.Printf("SendMessageToChat: перевод не требуется")
			}
		}
	} else {
		log.Printf("SendMessageToChat: сервис перевода недоступен")
	}

	// Добавляем сообщение в базу данных (с переведенным текстом)
	message, err := database.AddMessage(
		chatID,
		messageContent,
		"admin",
		adminID,
		"text",
		messageMetadata,
	)
	if err != nil {
		log.Printf("SendMessageToChat: ошибка добавления сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сохранения сообщения"})
		return
	}

	log.Printf("SendMessageToChat: сообщение сохранено: ID=%s", message.ID)

	// Обновляем время чата
	updateChatTimestamp(chatID)

	// Отправляем WebSocket уведомление в формате совместимом с виджетом и админкой
	messagePayload := createMessagePayload(message, chatID)

	payload := map[string]interface{}{
		"chatId":  chatID.String(), // для админки
		"message": messagePayload,
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
	chatID, err := parseChatID(c)
	if err != nil {
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

	log.Printf("ToggleAutoResponder: обновление автоответчика для чата %s, enabled=%t", chatID, request.Enabled)

	// Обновляем статус автоответчика в базе данных
	if err := queries.UpdateAutoResponder(database.DB, chatID, request.Enabled); err != nil {
		log.Printf("ToggleAutoResponder: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления автоответчика"})
		return
	}

	log.Printf("ToggleAutoResponder: автоответчик успешно обновлен для чата %s", chatID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"enabled": request.Enabled,
		"message": "Автоответчик успешно обновлен",
	})
}

// GetAdminSettings возвращает настройки админа
func GetAdminSettings(c *gin.Context) {
	// Получаем adminID из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	log.Printf("GetAdminSettings: получение настроек для админа %s", adminID)

	settings, err := queries.GetAdminSettings(database.DB, adminID)
	if err != nil {
		log.Printf("GetAdminSettings: ошибка получения настроек: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения настроек"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"preferredLanguage": settings.PreferredLanguage,
	})
}

// UpdateAdminSettings обновляет настройки админа
func UpdateAdminSettings(c *gin.Context) {
	// Получаем adminID из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	// Парсим тело запроса
	var request struct {
		PreferredLanguage string `json:"preferredLanguage" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("UpdateAdminSettings: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	// Валидируем код языка (опционально, можно расширить список)
	validLanguages := map[string]bool{
		"ru": true, "en": true, "pl": true, "de": true, "fr": true,
		"es": true, "it": true, "uk": true, "be": true, "cs": true,
		"sk": true, "lt": true, "lv": true, "et": true,
	}

	if !validLanguages[request.PreferredLanguage] {
		log.Printf("UpdateAdminSettings: недопустимый код языка: %s", request.PreferredLanguage)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Недопустимый код языка"})
		return
	}

	log.Printf("UpdateAdminSettings: обновление языка для админа %s на %s", adminID, request.PreferredLanguage)

	// Обновляем настройки
	if err := queries.UpsertAdminSettings(database.DB, adminID, request.PreferredLanguage); err != nil {
		log.Printf("UpdateAdminSettings: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления настроек"})
		return
	}

	log.Printf("UpdateAdminSettings: настройки успешно обновлены для админа %s", adminID)

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"preferredLanguage": request.PreferredLanguage,
		"message":           "Настройки успешно обновлены",
	})
}

// MarkChatMessagesAsRead помечает все сообщения в чате как прочитанные
func MarkChatMessagesAsRead(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	log.Printf("MarkChatMessagesAsRead: пометка сообщений в чате %s как прочитанных", chatID)

	// Помечаем сообщения как прочитанные
	if err := database.MarkMessagesAsRead(chatID); err != nil {
		log.Printf("MarkChatMessagesAsRead: ошибка пометки сообщений: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка пометки сообщений"})
		return
	}

	log.Printf("MarkChatMessagesAsRead: сообщения в чате %s успешно помечены как прочитанные", chatID)

	// Отправляем WebSocket уведомление о прочтении сообщений
	readNotification := map[string]interface{}{
		"chatId": chatID.String(),
	}

	wsMessage, _ := websocket.NewMessage("messages_read", readNotification)
	totalSent := WebSocketHub.SendToChatAndAdmins(chatID.String(), wsMessage)
	log.Printf("MarkChatMessagesAsRead: WebSocket уведомление отправлено %d клиентам", totalSent)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Сообщения помечены как прочитанные",
	})
}
