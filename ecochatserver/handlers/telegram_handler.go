package handlers

import (
	"ecochatserver/database"
	"ecochatserver/middleware"
	"ecochatserver/models"
	"ecochatserver/websocket"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// TelegramWebhook обрабатывает входящие запросы от Telegram API
func TelegramWebhook(c *gin.Context) {
	var incomingMessage models.IncomingMessage
	
	if err := c.ShouldBindJSON(&incomingMessage); err != nil {
		log.Printf("Ошибка парсинга JSON из Telegram webhook: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	log.Printf("Получено входящее сообщение от пользователя %s (source: %s)", incomingMessage.UserName, incomingMessage.Source)
	
	// Проверяем наличие обязательных полей
	if incomingMessage.UserID == "" || incomingMessage.ClientID == "" {
		log.Printf("Ошибка в запросе: отсутствуют обязательные поля UserID или ClientID")
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
		log.Printf("Ошибка создания/получения чата: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания чата: " + err.Error()})
		return
	}
	
	log.Printf("Чат получен/создан с ID: %s", chat.ID)
	
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
		log.Printf("Ошибка добавления сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка добавления сообщения: " + err.Error()})
		return
	}
	
	log.Printf("Добавлено сообщение с ID: %s в чат %s", message.ID, chat.ID)
	
	// Обновляем чат (получаем первую страницу сообщений)
	updatedChat, _, err := database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
	if err != nil {
		log.Printf("Предупреждение: не удалось получить обновленный чат: %v", err)
		c.Error(err)
	} else {
		// Отправляем уведомление по WebSocket
		messageData, err := websocket.NewChatMessage(updatedChat, message)
		if err == nil {
			WebSocketHub.Broadcast(messageData)
			log.Printf("Отправлено уведомление по WebSocket")
		} else {
			log.Printf("Ошибка при создании WebSocket сообщения: %v", err)
		}
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "message processed", "message_id": message.ID, "chat_id": chat.ID})
}

// Login обрабатывает авторизацию админов
func Login(c *gin.Context) {
	var credentials struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&credentials); err != nil {
		log.Printf("Ошибка парсинга данных для авторизации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	log.Printf("Попытка авторизации для пользователя: %s", credentials.Email)
	
	// Аутентификация пользователя
	token, err := middleware.Authenticate(credentials.Email, credentials.Password)
	if err != nil {
		log.Printf("Ошибка аутентификации для %s: %v", credentials.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	
	// Получаем данные администратора
	admin, err := database.GetAdmin(credentials.Email)
	if err != nil {
		log.Printf("Ошибка получения данных администратора %s: %v", credentials.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения данных пользователя"})
		return
	}
	
	// Скрываем чувствительные данные
	admin.PasswordHash = ""
	
	log.Printf("Успешная авторизация администратора: %s (ID: %s)", admin.Email, admin.ID)
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": admin,
	})
}