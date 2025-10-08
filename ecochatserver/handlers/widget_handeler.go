package handlers

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/egor/ecochatserver/database"
)

// GetWidgetChatMessages возвращает историю сообщений чата для виджета
func GetWidgetChatMessages(c *gin.Context) {
	userIDStr := c.GetHeader("X-Widget-User-ID")
	apiKey := c.GetHeader("X-API-Key")

	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID пользователя не указан"})
		return
	}

	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API ключ не указан"})
		return
	}

	// Парсим chatID
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Получаем параметры пагинации (infinite scroll)
	limitStr := c.DefaultQuery("limit", "50")
	before := c.Query("before")

	limit, _ := strconv.Atoi(limitStr)
	if limit < 1 || limit > 100 {
		limit = 50
	}

	log.Printf("GetWidgetChatMessages: загрузка истории чата %s для пользователя %s (limit=%d, before=%s)", chatID, userIDStr, limit, before)

	// Получаем чат из базы данных
	chat, totalMessages, err := database.GetChatByID(chatID, limit, before)
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

	// Если чат архивирован, возвращаем пустой список сообщений
	if chat.IsArchived {
		log.Printf("GetWidgetChatMessages: чат %s архивирован, возвращаем пустой список", chatID)
		response := gin.H{
			"chatId":   chat.ID.String(),
			"messages": []interface{}{},
			"status":   "archived",
			"message":  "Этот чат был архивирован",
		}
		c.JSON(http.StatusOK, response)
		return
	}

	log.Printf("GetWidgetChatMessages: найден чат с %d сообщениями (всего: %d)", len(chat.Messages), totalMessages)

	// Переводим сообщения админа для клиента (lazy caching)
	if Translator != nil {
		// Получаем язык клиента из чата
		clientLang, err := database.GetClientLanguageFromChat(chatID)
		if err == nil && clientLang != "" {
			log.Printf("GetWidgetChatMessages: перевод сообщений для клиента (язык: %s)", clientLang)
			err = Translator.TranslateMessagesForWidget(c.Request.Context(), chat.Messages, clientLang)
			if err != nil {
				log.Printf("GetWidgetChatMessages: ошибка перевода сообщений: %v", err)
				// Продолжаем с оригинальными сообщениями
			}
		}
	}

	// Формируем ответ в формате совместимом с виджетом
	response := gin.H{
		"chatId":   chat.ID.String(),
		"messages": chat.Messages,
		"total":    totalMessages,
		"hasMore":  len(chat.Messages) >= limit,
		"status":   "success",
	}

	c.JSON(http.StatusOK, response)
}
