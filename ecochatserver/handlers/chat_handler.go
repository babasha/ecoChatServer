package handlers

import (
    "log"
    "net/http"
    "strconv"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/models"
    "github.com/egor/ecochatserver/websocket"
)

// PaginationResponse стандартная структура ответа с пагинацией
type PaginationResponse struct {
	Items      interface{} `json:"items"`
	Page       int         `json:"page"`
	PageSize   int         `json:"pageSize"`
	TotalItems int         `json:"totalItems"`
	TotalPages int         `json:"totalPages"`
}

// GetChats возвращает список всех чатов для админа
func GetChats(c *gin.Context) {
	// Получаем ID админа и клиента из токена аутентификации
	adminIDStr := c.GetString("adminID")
	clientIDStr := c.GetString("clientID")
	role := c.GetString("role")
	
	log.Printf("GetChats: начало, adminID=%s, clientID=%s, role=%s из токена", 
		adminIDStr, clientIDStr, role)
	
	if adminIDStr == "" || clientIDStr == "" {
		log.Printf("GetChats: ошибка авторизации: adminID или clientID отсутствуют")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}

	// Преобразуем строковые ID в UUID
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		log.Printf("GetChats: ошибка преобразования adminID в UUID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID администратора"})
		return
	}

	clientID, err := uuid.Parse(clientIDStr)
	if err != nil {
		log.Printf("GetChats: ошибка преобразования clientID в UUID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID клиента"})
		return
	}

	// Получаем параметры пагинации
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", strconv.Itoa(database.DefaultPageSize)))

	log.Printf("GetChats: запрос чатов для admin=%s, client=%s (страница: %d, размер: %d)", 
		adminIDStr, clientIDStr, page, pageSize)

	// Получаем список чатов из базы данных
	chats, totalItems, err := database.GetChats(clientID, adminID, page, pageSize)
	if err != nil {
		log.Printf("GetChats: ошибка получения чатов для admin=%s, client=%s: %v", 
			adminIDStr, clientIDStr, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения чатов: " + err.Error()})
		return
	}

	// Рассчитываем общее количество страниц
	totalPages := (totalItems + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Формируем ответ с пагинацией
	response := PaginationResponse{
		Items:      chats,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalItems,
		TotalPages: totalPages,
	}

	log.Printf("GetChats: успешно получено %d чатов для admin=%s (страница %d из %d)", 
		len(chats), adminIDStr, page, totalPages)
	
	// Подробно логируем каждый чат
	for i, chat := range chats {
		log.Printf("GetChats: чат %d: ID=%s, userID=%s, userName=%s, unread=%d, status=%s, createdAt=%v, updatedAt=%v", 
			i, chat.ID, chat.User.ID, chat.User.Name, chat.UnreadCount, chat.Status, chat.CreatedAt, chat.UpdatedAt)
		if chat.LastMessage != nil {
			log.Printf("GetChats: чат %d последнее сообщение: ID=%s, sender=%s, content=%s, timestamp=%v", 
				i, chat.LastMessage.ID, chat.LastMessage.Sender, chat.LastMessage.Content, chat.LastMessage.Timestamp)
		}
	}
	
	c.JSON(http.StatusOK, response)
}

// GetChatByID возвращает информацию о конкретном чате и его сообщениях
func GetChatByID(c *gin.Context) {
	chatIDStr := c.Param("id")
	adminIDStr := c.GetString("adminID")
	clientIDStr := c.GetString("clientID")
	
	log.Printf("GetChatByID: начало, chatID=%s, adminID=%s, clientID=%s", 
		chatIDStr, adminIDStr, clientIDStr)
	
	if adminIDStr == "" {
		log.Printf("GetChatByID: ошибка авторизации при получении чата %s: adminID отсутствует", chatIDStr)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}
	
	// Преобразуем строковый ID чата в UUID
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("GetChatByID: ошибка преобразования chatID в UUID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID чата"})
		return
	}
	
	// Получаем параметры пагинации для сообщений
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", strconv.Itoa(database.DefaultPageSize)))
	
	log.Printf("GetChatByID: запрос на получение чата %s от admin: %s (страница: %d, размер: %d)", 
		chatIDStr, adminIDStr, page, pageSize)
	
	// Получаем информацию о чате из базы данных
	chat, totalMessages, err := database.GetChatByID(chatID, page, pageSize)
	if err != nil {
		log.Printf("GetChatByID: ошибка получения чата %s: %v", chatIDStr, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден: " + err.Error()})
		return
	}
	
	log.Printf("GetChatByID: получен чат ID=%s, userID=%s, clientID=%s, status=%s", 
		chat.ID, chat.User.ID, chat.ClientID, chat.Status)
	
	// Отмечаем сообщения как прочитанные
	err = database.MarkMessagesAsRead(chatID)
	if err != nil {
		log.Printf("GetChatByID: ошибка при отметке сообщений как прочитанные: %v", err)
		c.Error(err)
	} else {
		log.Printf("GetChatByID: сообщения отмечены как прочитанные")
	}
	
	// Рассчитываем общее количество страниц
	totalPages := (totalMessages + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	
	// Формируем ответ с пагинацией
	response := struct {
		Chat       *models.Chat `json:"chat"`
		Page       int          `json:"page"`
		PageSize   int          `json:"pageSize"`
		TotalItems int          `json:"totalMessages"`
		TotalPages int          `json:"totalPages"`
	}{
		Chat:       chat,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: totalMessages,
		TotalPages: totalPages,
	}
	
	log.Printf("GetChatByID: успешно получен чат %s с %d сообщениями (страница %d из %d)", 
		chatIDStr, len(chat.Messages), page, totalPages)
	
	// Логируем сообщения
	for i, msg := range chat.Messages {
		log.Printf("GetChatByID: сообщение %d: ID=%s, sender=%s, content=%s, timestamp=%v", 
			i, msg.ID, msg.Sender, msg.Content, msg.Timestamp)
	}
	
	c.JSON(http.StatusOK, response)
}

// SendMessage отправляет сообщение в чат
func SendMessage(c *gin.Context) {
	chatIDStr := c.Param("id")
	adminIDStr := c.GetString("adminID")
	clientIDStr := c.GetString("clientID")
	
	log.Printf("SendMessage: начало, chatID=%s, adminID=%s, clientID=%s", 
		chatIDStr, adminIDStr, clientIDStr)
	
	if adminIDStr == "" {
		log.Printf("SendMessage: ошибка авторизации при отправке сообщения в чат %s: adminID отсутствует", chatIDStr)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}
	
	// Преобразуем строковый ID чата в UUID
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("SendMessage: ошибка преобразования chatID в UUID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID чата"})
		return
	}
	
	// Преобразуем строковый ID админа в UUID
	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		log.Printf("SendMessage: ошибка преобразования adminID в UUID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID администратора"})
		return
	}
	
	// Получаем данные сообщения
	var messageData struct {
		Content string `json:"content" binding:"required"`
		Type    string `json:"type"`
	}
	
	if err := c.ShouldBindJSON(&messageData); err != nil {
		log.Printf("SendMessage: ошибка в формате данных сообщения: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректные данные: " + err.Error()})
		return
	}
	
	log.Printf("SendMessage: содержимое сообщения: %s, тип: %s", messageData.Content, messageData.Type)
	
	// Если тип не указан, используем "text"
	messageType := "text"
	if messageData.Type != "" {
		messageType = messageData.Type
	}
	
	// Добавляем сообщение в базу данных
	message, err := database.AddMessage(chatID, messageData.Content, "admin", adminID, messageType, nil)
	if err != nil {
		log.Printf("SendMessage: ошибка при добавлении сообщения в БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка отправки сообщения: " + err.Error()})
		return
	}
	
	log.Printf("SendMessage: сообщение добавлено в БД, ID=%s", message.ID)
	
	// Получаем обновленный чат (первую страницу сообщений)
	chat, _, err := database.GetChatByID(chatID, 1, database.DefaultPageSize)
	if err != nil {
		log.Printf("SendMessage: не удалось получить обновленный чат: %v", err)
		c.Error(err)
	} else {
		log.Printf("SendMessage: получен обновленный чат для WebSocket уведомления")
		
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(chat, message)
		if err == nil {
			// Отправляем всем клиентам
			WebSocketHub.Broadcast(messageData)
			log.Printf("SendMessage: WebSocket уведомление отправлено (broadcast)")
		} else {
			log.Printf("SendMessage: ошибка при создании WebSocket сообщения: %v", err)
		}
	}
	
	// Здесь нужно добавить код для отправки сообщения через Telegram API
	// В зависимости от source в чате
	
	log.Printf("SendMessage: успешно отправлено сообщение с ID: %s", message.ID)
	c.JSON(http.StatusOK, message)
}