package handlers

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// parsePagination извлекает и валидирует параметры пагинации из query string
func parsePagination(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ = strconv.Atoi(c.DefaultQuery("size", "50"))

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}

	return page, size
}

// parseChatID извлекает и парсит chatID из URL параметра
func parseChatID(c *gin.Context) (uuid.UUID, error) {
	chatIDStr := c.Param("id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("parseChatID: неверный UUID чата: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID чата"})
		return uuid.Nil, err
	}
	return chatID, nil
}

// getAdminID извлекает и парсит adminID из контекста Gin (JWT)
func getAdminID(c *gin.Context) (uuid.UUID, error) {
	adminIDStr, exists := c.Get("adminID")
	if !exists {
		log.Printf("getAdminID: adminID отсутствует в контексте")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Не авторизован"})
		return uuid.Nil, http.ErrNoCookie
	}

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		log.Printf("getAdminID: неверный UUID админа: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID админа"})
		return uuid.Nil, err
	}

	return adminID, nil
}

// updateChatTimestamp обновляет timestamp чата с логированием ошибки
func updateChatTimestamp(chatID uuid.UUID) {
	if err := queries.UpdateChatTimestamp(database.DB, chatID); err != nil {
		log.Printf("updateChatTimestamp: ошибка обновления времени чата %s: %v", chatID, err)
	}
}

// createMessagePayload создает payload для WebSocket уведомления о сообщении
func createMessagePayload(message *models.Message, chatID uuid.UUID) map[string]interface{} {
	payload := map[string]interface{}{
		"id":        message.ID.String(),
		"chatId":    chatID.String(),
		"content":   message.Content,
		"sender":    message.Sender,
		"senderId":  message.SenderID.String(),
		"timestamp": message.Timestamp.Format(time.RFC3339),
		"read":      false,
		"type":      message.Type,
	}

	// Добавляем metadata, если есть
	if message.Metadata != nil {
		payload["metadata"] = message.Metadata
	}

	return payload
}
