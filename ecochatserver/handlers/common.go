package handlers

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/egor/ecochatserver/websocket"
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
// clientLang передаётся вызывающим кодом для избежания повторных DB запросов.
func applyTranslationForWidget(message *models.Message, chatID uuid.UUID, clientLang string) *models.Message {
	widgetMessage := *message
	if message.Metadata == nil || clientLang == "" {
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

// truncateForLog обрезает строку для вывода в лог
func truncateForLog(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...(truncated)"
}

// firstNotEmpty возвращает первое непустое значение из списка
func firstNotEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// deterministicUUID создаёт детерминированный UUID из строки (или парсит, если строка — валидный UUID)
func deterministicUUID(value string) uuid.UUID {
	if parsed, err := uuid.Parse(value); err == nil {
		return parsed
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(value))
}

// maskIdentifier маскирует идентификатор, оставляя последние 4 символа
func maskIdentifier(id string) string {
	if len(id) <= 4 {
		return id
	}
	return id[len(id)-4:]
}

// createChatInfo создает информацию о чате для WebSocket уведомлений
func createChatInfo(chat *models.Chat) map[string]interface{} {
	return map[string]interface{}{
		"id":                   chat.ID.String(),
		"user":                 chat.User,
		"status":               chat.Status,
		"source":               chat.Source,
		"clientId":             chat.ClientID.String(),
		"createdAt":            chat.CreatedAt.Format(time.RFC3339),
		"updatedAt":            chat.UpdatedAt.Format(time.RFC3339),
		"unreadCount":          0,
		"autoResponderEnabled": chat.AutoResponderEnabled,
	}
}

// createMessageNotification создает WebSocket уведомление для одного сообщения
func createMessageNotification(chatID uuid.UUID, message *models.Message, chatInfo map[string]interface{}) []byte {
	payload := map[string]interface{}{
		"chatId":  chatID.String(),
		"message": createMessagePayload(message, chatID),
		"chat":    chatInfo,
	}
	msg, _ := websocket.NewMessage("new_message", payload)
	return msg
}

// createEscalationNotification создает уведомление об эскалации для админов
func createEscalationNotification(chatID uuid.UUID, userMsg *models.Message) []byte {
	payload := map[string]interface{}{
		"type":   "escalation",
		"chatId": chatID.String(),
		"message": map[string]interface{}{
			"id":        userMsg.ID.String(),
			"content":   userMsg.Content,
			"sender":    userMsg.Sender,
			"timestamp": userMsg.Timestamp.Format(time.RFC3339),
		},
		"sound":  "notification",
		"urgent": true,
	}
	msg, _ := websocket.NewMessage("escalation_alert", payload)
	return msg
}

// runAutoResponder обрабатывает сообщение через автоответчик, сохраняет ответ,
// обрабатывает эскалацию и (опционально) отправляет во внешние каналы.
func runAutoResponder(ctx context.Context, chat *models.Chat, userMsg *models.Message, dispatchExternal bool) *models.Message {
	if AutoResponder == nil || !chat.AutoResponderEnabled {
		return nil
	}

	lightChat, err := queries.GetChatLightweight(database.DB, chat.ID)
	if err != nil {
		log.Printf("runAutoResponder: ошибка загрузки чата: %v", err)
		lightChat = chat
	}

	botMsg, err := AutoResponder.ProcessMessage(ctx, lightChat, userMsg)
	if err != nil {
		log.Printf("runAutoResponder: ошибка: %v", err)
		if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "overloaded") {
			botMsg = &models.Message{
				ChatID:    chat.ID,
				Content:   "Извините, сервис временно перегружен. Попробуйте повторить запрос через несколько секунд.",
				Sender:    "admin",
				SenderID:  uuid.MustParse("00000000-0000-0000-0000-000000000000"),
				Type:      "text",
				Timestamp: time.Now(),
				Metadata:  make(map[string]interface{}),
			}
		}
	}

	if botMsg == nil {
		return nil
	}

	saved, err := database.AddMessage(
		chat.ID,
		botMsg.Content,
		botMsg.Sender,
		botMsg.SenderID,
		botMsg.Type,
		botMsg.Metadata,
	)
	if err != nil {
		log.Printf("runAutoResponder: ошибка сохранения: %v", err)
		return nil
	}

	botMsg = saved

	if dispatchExternal {
		go dispatchExternalMessage(chat.ID, botMsg)
	}

	if needEscalation, ok := botMsg.Metadata["needEscalation"].(bool); ok && needEscalation {
		escalation := createEscalationNotification(chat.ID, userMsg)
		sent := WebSocketHub.SendToAllAdmins(escalation)
		log.Printf("runAutoResponder: уведомление об эскалации → %d админов", sent)
	}

	return botMsg
}

// notifyNewMessages отправляет WebSocket уведомления о новых сообщениях (user + bot)
func notifyNewMessages(chat *models.Chat, userMsg *models.Message, botMsg *models.Message) {
	chatInfo := createChatInfo(chat)

	userNotification := createMessageNotification(chat.ID, userMsg, chatInfo)
	WebSocketHub.SendToChatAndAdmins(chat.ID.String(), userNotification)

	if botMsg != nil {
		botNotification := createMessageNotification(chat.ID, botMsg, chatInfo)
		WebSocketHub.SendToChatAndAdmins(chat.ID.String(), botNotification)
	}
}
