package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
)

// GetWidgetChatMessages возвращает историю сообщений чата для виджета
func GetWidgetChatMessages(c *gin.Context) {
	chatIDStr := c.Param("id")
	userIDStr := c.GetHeader("X-Widget-User-ID")
	apiKey := c.GetHeader("X-API-Key")

	if chatIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID чата не указан"})
		return
	}

	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID пользователя не указан"})
		return
	}

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API ключ не указан"})
		return
	}

	// Парсим chatID
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		log.Printf("GetWidgetChatMessages: неверный UUID чата: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат ID чата"})
		return
	}

	// Получаем параметры пагинации
	pageStr := c.DefaultQuery("page", "1")
	sizeStr := c.DefaultQuery("size", "50")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	size, err := strconv.Atoi(sizeStr)
	if err != nil || size < 1 || size > 100 {
		size = 50
	}

	log.Printf("GetWidgetChatMessages: загрузка истории чата %s для пользователя %s", chatIDStr, userIDStr)

	// Получаем чат из базы данных
	chat, totalMessages, err := database.GetChatByID(chatID, page-1, size)
	if err != nil {
		log.Printf("GetWidgetChatMessages: ошибка получения чата: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден"})
		return
	}

	// Проверяем права доступа - пользователь должен быть владельцем чата
	if chat.User.SourceID != userIDStr {
		log.Printf("GetWidgetChatMessages: доступ запрещен - пользователь %s не является владельцем чата (owner: %s)", userIDStr, chat.User.SourceID)
		c.JSON(http.StatusForbidden, gin.H{"error": "Доступ запрещен"})
		return
	}

	log.Printf("GetWidgetChatMessages: найден чат с %d сообщениями (всего: %d)", len(chat.Messages), totalMessages)

	// Формируем ответ в формате совместимом с виджетом
	response := gin.H{
		"chatId":        chat.ID.String(),
		"messages":      chat.Messages,
		"totalMessages": totalMessages,
		"page":          page,
		"pageSize":      size,
		"status":        "success",
	}

	c.JSON(http.StatusOK, response)
}
