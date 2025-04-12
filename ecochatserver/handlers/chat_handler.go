package handlers

import (
	"ecochatserver/database"
	"ecochatserver/websocket"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetChats возвращает список всех чатов для админа
func GetChats(c *gin.Context) {
	// Получаем ID админа и клиента из токена аутентификации
	adminID := c.GetString("adminID")
	clientID := c.GetString("clientID")
	
	if adminID == "" || clientID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Получаем список чатов из базы данных
	chats, err := database.GetChats(clientID, adminID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения чатов"})
		return
	}

	c.JSON(http.StatusOK, chats)
}

// GetChatByID возвращает информацию о конкретном чате и его сообщениях
func GetChatByID(c *gin.Context) {
	chatID := c.Param("id")
	adminID := c.GetString("adminID")
	
	if adminID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	
	// Получаем информацию о чате из базы данных
	chat, err := database.GetChatByID(chatID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден"})
		return
	}
	
	// Отмечаем сообщения как прочитанные
	err = database.MarkMessagesAsRead(chatID)
	if err != nil {
		// Логируем ошибку, но продолжаем
		c.Error(err)
	}
	
	c.JSON(http.StatusOK, chat)
}

// SendMessage отправляет сообщение в чат
func SendMessage(c *gin.Context) {
	chatID := c.Param("id")
	adminID := c.GetString("adminID")
	
	if adminID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	
	// Получаем данные сообщения
	var messageData struct {
		Content string `json:"content" binding:"required"`
		Type    string `json:"type"`
	}
	
	if err := c.ShouldBindJSON(&messageData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Если тип не указан, используем "text"
	messageType := "text"
	if messageData.Type != "" {
		messageType = messageData.Type
	}
	
	// Добавляем сообщение в базу данных
	message, err := database.AddMessage(chatID, messageData.Content, "admin", adminID, messageType, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка отправки сообщения"})
		return
	}
	
	// Получаем обновленный чат
	chat, err := database.GetChatByID(chatID)
	if err != nil {
		c.Error(err)
	} else {
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(chat, message)
		if err == nil {
			// Отправляем всем клиентам или конкретному админу
			// В идеале нужно отправлять только админам, связанным с этим чатом
			websocketHub.Broadcast(messageData)
		}
	}
	
	// Здесь нужно добавить код для отправки сообщения через Telegram API
	// В зависимости от source в чате
	
	c.JSON(http.StatusOK, message)
}

// Глобальная переменная для доступа к WebSocket хабу
var websocketHub *websocket.Hub