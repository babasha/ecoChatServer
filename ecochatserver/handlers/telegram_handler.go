package handlers

import (
	"ecochatserver/models"
	"ecochatserver/websocket"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TelegramWebhook обрабатывает входящие запросы от Telegram API
func TelegramWebhook(c *gin.Context) {
	var incomingMessage models.IncomingMessage
	
	if err := c.ShouldBindJSON(&incomingMessage); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Проверяем наличие клиента и его подписки
	// (В реальном коде здесь будет проверка из БД)
	if incomingMessage.ClientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Client ID is required"})
		return
	}
	
	// Проверяем наличие существующего чата для пользователя
	var existingChatID string
	for id, chat := range chats {
		if chat.User.SourceID == incomingMessage.UserID && 
			chat.Source == incomingMessage.Source && 
			chat.BotID == incomingMessage.BotID {
			existingChatID = id
			break
		}
	}
	
	now := time.Now()
	messageID := uuid.New().String()
	
	// Создаем сообщение
	message := models.Message{
		ID:        messageID,
		Content:   incomingMessage.Content,
		Sender:    "user",
		SenderID:  incomingMessage.UserID,
		Timestamp: now,
		Read:      false,
		Type:      incomingMessage.MessageType,
		Metadata:  incomingMessage.Metadata,
	}
	
	var chat models.Chat
	
	if existingChatID != "" {
		// Добавляем сообщение в существующий чат
		chat = chats[existingChatID]
		message.ChatID = existingChatID
		chat.Messages = append(chat.Messages, message)
		chat.LastMessage = &message
		chat.UpdatedAt = now
		chat.Status = "active" // Обновляем статус, если был "closed"
		chats[existingChatID] = chat
	} else {
		// Создаем нового пользователя
		user := models.User{
			ID:       uuid.New().String(),
			Name:     incomingMessage.UserName,
			Email:    incomingMessage.UserEmail,
			Source:   incomingMessage.Source,
			SourceID: incomingMessage.UserID,
		}
		
		// Создаем новый чат
		chatID := uuid.New().String()
		message.ChatID = chatID
		
		chat = models.Chat{
			ID:        chatID,
			User:      user,
			Messages:  []models.Message{message},
			LastMessage: &message,
			CreatedAt: now,
			UpdatedAt: now,
			Status:    "active",
			Source:    incomingMessage.Source,
			BotID:     incomingMessage.BotID,
			ClientID:  incomingMessage.ClientID,
		}
		
		// Здесь можно добавить логику назначения чата конкретному сотруднику
		// chat.AssignedTo = "some-admin-id"
		
		chats[chatID] = chat
	}
	
	// Отправляем уведомление по WebSocket всем подключенным админам
	outgoingMessage := models.OutgoingMessage{
		Type: "new_message",
		Payload: map[string]interface{}{
			"chat":    chat,
			"message": message,
		},
	}
	
	// Здесь должна быть отправка через WebSocket hub
	// websocket.Hub.Broadcast(outgoingMessage)
	
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
	
	// Проверка учетных данных (в реальном приложении проверка из БД)
	// Здесь просто для примера:
	if credentials.Email != "admin@example.com" || credentials.Password != "password" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}
	
	// Генерация JWT-токена
	// В реальном приложении здесь будет настоящая генерация JWT
	token := "example-jwt-token"
	
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": models.Admin{
			ID:       "admin-1",
			Name:     "Администратор",
			Email:    credentials.Email,
			Role:     "admin",
			ClientID: "client-1",
			Active:   true,
		},
	})
}