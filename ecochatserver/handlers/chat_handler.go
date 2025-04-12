package handlers

import (
	"ecochatserver/models"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Временное хранилище чатов (в реальном проекте будет база данных)
var chats = make(map[string]models.Chat)

// GetChats возвращает список всех чатов для админа
func GetChats(c *gin.Context) {
	// Получаем ID админа из токена аутентификации
	adminID := c.GetString("adminID")
	if adminID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Получаем список чатов из базы данных
	// Здесь должна быть логика получения чатов из БД
	// Пока используем временное хранилище
	chatList := []models.ChatResponse{}
	
	for _, chat := range chats {
		// Проверяем, назначен ли чат этому админу
		if chat.AssignedTo == adminID || chat.AssignedTo == "" {
			unreadCount := 0
			for _, msg := range chat.Messages {
				if !msg.Read && msg.Sender == "user" {
					unreadCount++
				}
			}
			
			chatResponse := models.ChatResponse{
				ID:          chat.ID,
				User:        chat.User,
				LastMessage: chat.LastMessage,
				CreatedAt:   chat.CreatedAt,
				UpdatedAt:   chat.UpdatedAt,
				Status:      chat.Status,
				UnreadCount: unreadCount,
			}
			
			chatList = append(chatList, chatResponse)
		}
	}

	c.JSON(http.StatusOK, chatList)
}

// GetChatByID возвращает информацию о конкретном чате и его сообщениях
func GetChatByID(c *gin.Context) {
	chatID := c.Param("id")
	
	// Проверяем наличие чата
	chat, exists := chats[chatID]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}
	
	// Обновляем статус прочтения сообщений
	for i := range chat.Messages {
		if chat.Messages[i].Sender == "user" && !chat.Messages[i].Read {
			chat.Messages[i].Read = true
		}
	}
	chats[chatID] = chat
	
	c.JSON(http.StatusOK, chat)
}

// SendMessage отправляет сообщение в чат
func SendMessage(c *gin.Context) {
	chatID := c.Param("id")
	
	// Проверяем наличие чата
	chat, exists := chats[chatID]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}
	
	// Получаем данные сообщения
	var messageData struct {
		Content string `json:"content" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&messageData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	// Создаем новое сообщение
	adminID := c.GetString("adminID")
	now := time.Now()
	message := models.Message{
		ID:        uuid.New().String(),
		ChatID:    chatID,
		Content:   messageData.Content,
		Sender:    "admin",
		SenderID:  adminID,
		Timestamp: now,
		Read:      false,
		Type:      "text",
	}
	
	// Добавляем сообщение в чат
	chat.Messages = append(chat.Messages, message)
	chat.LastMessage = &message
	chat.UpdatedAt = now
	chats[chatID] = chat
	
	// Здесь нужно отправить сообщение через Telegram API
	// ... (код для отправки сообщения пользователю)
	
	// Отправляем уведомление всем подключенным клиентам через WebSocket
	// Должна быть интеграция с WebSocket Hub
	
	c.JSON(http.StatusOK, message)
}