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

// getTranslation извлекает перевод на указанный язык из metadata сообщения.
// Возвращает переведённый текст и true, если перевод найден.
func getTranslation(metadata map[string]interface{}, lang string) (string, bool) {
	if metadata == nil || lang == "" {
		return "", false
	}
	translations, ok := metadata["translations"].(map[string]interface{})
	if !ok {
		return "", false
	}
	translation, exists := translations[lang]
	if !exists {
		return "", false
	}
	translatedText, ok := translation.(string)
	if !ok || translatedText == "" {
		return "", false
	}
	return translatedText, true
}

// applyTranslationForWidget создаёт копию сообщения с переводом для виджета.
// Если перевод на язык клиента существует, заменяет content.
func applyTranslationForWidget(message *models.Message, chatID uuid.UUID) *models.Message {
	widgetMessage := *message
	if message.Metadata == nil {
		return &widgetMessage
	}

	clientLang, err := database.GetClientLanguageFromChat(chatID)
	if err != nil || clientLang == "" {
		return &widgetMessage
	}

	if translatedText, ok := getTranslation(message.Metadata, clientLang); ok {
		widgetMessage.Content = translatedText
		log.Printf("applyTranslationForWidget: применён перевод на %s", clientLang)
	}

	return &widgetMessage
}

// applyTranslationForAdmin создаёт копию сообщения с переводом для админа.
// Если перевод на язык админа существует, заменяет content.
func applyTranslationForAdmin(message *models.Message, adminLang string) *models.Message {
	adminMessage := *message
	if message.Metadata == nil || adminLang == "" {
		return &adminMessage
	}

	if translatedText, ok := getTranslation(message.Metadata, adminLang); ok {
		adminMessage.Content = translatedText
		log.Printf("applyTranslationForAdmin: применён перевод на %s", adminLang)
	}

	return &adminMessage
}

// getAdminLanguage получает язык админа из кеша настроек.
// Возвращает пустую строку если язык не найден.
func getAdminLanguage(adminID uuid.UUID) string {
	settings, err := settingsCache.getAdminSettings(adminID)
	if err != nil || settings.PreferredLanguage == "" {
		return ""
	}
	return settings.PreferredLanguage
}
