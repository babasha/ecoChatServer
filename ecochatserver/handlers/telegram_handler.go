package handlers

import (
	"ecochatserver/database"
	"ecochatserver/middleware"
	"ecochatserver/models"
	"ecochatserver/websocket"
	"net/http"

	"github.com/gin-gonic/gin"
)


// TelegramWebhook обрабатывает входящие запросы от Telegram API
func TelegramWebhook(c *gin.Context) {
	var incomingMessage models.IncomingMessage
	
	if err := c.ShouldBindJSON(&incomingMessage); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Проверяем наличие обязательных полей
	if incomingMessage.UserID == "" || incomingMessage.ClientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "UserID и ClientID обязательны"})
		return
	}
	
	// Создаем или получаем существующий чат
	chat, _, err := database.CreateOrGetChat(
		incomingMessage.UserID,
		incomingMessage.UserName,
		incomingMessage.UserEmail,
		incomingMessage.Source,
		incomingMessage.UserID,
		incomingMessage.BotID,
		incomingMessage.ClientID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания чата: " + err.Error()})
		return
	}
	
	// Добавляем новое сообщение в чат
	messageType := "text"
	if incomingMessage.MessageType != "" {
		messageType = incomingMessage.MessageType
	}
	
	message, err := database.AddMessage(
		chat.ID,
		incomingMessage.Content,
		"user",
		incomingMessage.UserID,
		messageType,
		incomingMessage.Metadata,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка добавления сообщения: " + err.Error()})
		return
	}
	
	// Обновляем чат
	updatedChat, err := database.GetChatByID(chat.ID)
	if err != nil {
		c.Error(err)
	} else {
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(updatedChat, message)
		if err == nil {
			websocketHub.Broadcast(messageData)
		}
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "message processed"})
}

// Login обрабатывает авторизацию админов
func Login(c *gin.Context) {
	var credentials struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&credentials); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Аутентификация пользователя
	token, err := middleware.Authenticate(credentials.Email, credentials.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	
	// Получаем данные администратора
	admin, err := database.GetAdmin(credentials.Email)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения данных пользователя"})
		return
	}
	
	// Скрываем чувствительные данные
	admin.PasswordHash = ""
	
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": admin,
	})
}

// Инициализация WebSocket хаба в main.go
func SetWebSocketHub(hub *websocket.Hub) {
	websocketHub = hub
}