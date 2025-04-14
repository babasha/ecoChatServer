package handlers

import (
	"ecochatserver/database"
	"ecochatserver/models"
	"ecochatserver/websocket"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
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
	adminID := c.GetString("adminID")
	clientID := c.GetString("clientID")
	
	if adminID == "" || clientID == "" {
		log.Printf("Ошибка авторизации: adminID или clientID отсутствуют")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}

	// Получаем параметры пагинации
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", strconv.Itoa(database.DefaultPageSize)))

	log.Printf("Запрос на получение чатов от admin: %s, client: %s (страница: %d, размер: %d)", 
		adminID, clientID, page, pageSize)

	// Получаем список чатов из базы данных
	chats, totalItems, err := database.GetChats(clientID, adminID, page, pageSize)
	if err != nil {
		log.Printf("Ошибка получения чатов для admin: %s, client: %s: %v", adminID, clientID, err)
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

	log.Printf("Успешно получено %d чатов для admin: %s (страница %d из %d)", 
		len(chats), adminID, page, totalPages)
	c.JSON(http.StatusOK, response)
}

// GetChatByID возвращает информацию о конкретном чате и его сообщениях
func GetChatByID(c *gin.Context) {
	chatID := c.Param("id")
	adminID := c.GetString("adminID")
	
	if adminID == "" {
		log.Printf("Ошибка авторизации при получении чата %s: adminID отсутствует", chatID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}
	
	// Получаем параметры пагинации для сообщений
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", strconv.Itoa(database.DefaultPageSize)))
	
	log.Printf("Запрос на получение чата %s от admin: %s (страница: %d, размер: %d)", 
		chatID, adminID, page, pageSize)
	
	// Получаем информацию о чате из базы данных
	chat, totalMessages, err := database.GetChatByID(chatID, page, pageSize)
	if err != nil {
		log.Printf("Ошибка получения чата %s: %v", chatID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден: " + err.Error()})
		return
	}
	
	// Отмечаем сообщения как прочитанные
	err = database.MarkMessagesAsRead(chatID)
	if err != nil {
		// Логируем ошибку, но продолжаем
		log.Printf("Предупреждение: ошибка при отметке сообщений как прочитанные: %v", err)
		c.Error(err)
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
	
	log.Printf("Успешно получен чат %s с %d сообщениями (страница %d из %d)", 
		chatID, len(chat.Messages), page, totalPages)
	c.JSON(http.StatusOK, response)
}

// SendMessage отправляет сообщение в чат
func SendMessage(c *gin.Context) {
	chatID := c.Param("id")
	adminID := c.GetString("adminID")
	
	if adminID == "" {
		log.Printf("Ошибка авторизации при отправке сообщения в чат %s: adminID отсутствует", chatID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Необходима авторизация"})
		return
	}
	
	log.Printf("Запрос на отправку сообщения в чат %s от admin: %s", chatID, adminID)
	
	// Получаем данные сообщения
	var messageData struct {
		Content string `json:"content" binding:"required"`
		Type    string `json:"type"`
	}
	
	if err := c.ShouldBindJSON(&messageData); err != nil {
		log.Printf("Ошибка в формате данных сообщения: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректные данные: " + err.Error()})
		return
	}
	
	log.Printf("Содержимое сообщения: %s, тип: %s", messageData.Content, messageData.Type)
	
	// Если тип не указан, используем "text"
	messageType := "text"
	if messageData.Type != "" {
		messageType = messageData.Type
	}
	
	// Добавляем сообщение в базу данных
	message, err := database.AddMessage(chatID, messageData.Content, "admin", adminID, messageType, nil)
	if err != nil {
		log.Printf("Ошибка при добавлении сообщения в БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка отправки сообщения: " + err.Error()})
		return
	}
	
	// Получаем обновленный чат (первую страницу сообщений)
	chat, _, err := database.GetChatByID(chatID, 1, database.DefaultPageSize)
	if err != nil {
		log.Printf("Предупреждение: не удалось получить обновленный чат: %v", err)
		c.Error(err)
	} else {
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(chat, message)
		if err == nil {
			// Отправляем всем клиентам или конкретному админу
			WebSocketHub.Broadcast(messageData)
			log.Printf("Отправлено уведомление по WebSocket")
		} else {
			log.Printf("Ошибка при создании WebSocket сообщения: %v", err)
		}
	}
	
	// Здесь нужно добавить код для отправки сообщения через Telegram API
	// В зависимости от source в чате
	
	log.Printf("Сообщение успешно отправлено с ID: %s", message.ID)
	c.JSON(http.StatusOK, message)
}