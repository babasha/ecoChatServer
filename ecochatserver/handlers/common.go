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

// isMooovingChat возвращает true, если чат принадлежит интеграции moooving.
// Используется чтобы пропускать перевод/AutoResponder/external dispatch.
func isMooovingChat(chatID uuid.UUID) bool {
	chat, err := queries.GetChatLightweight(database.DB, chatID)
	if err != nil || chat == nil {
		return false
	}
	return chat.Source == database.MooovingChatSource
}

// enforceMooovingChatScope гарантирует, что moooving driver/mo_client
// работает только с тем chatID, к которому привязано его WS-соединение.
// Возвращает true, если запрос разрешён.
func enforceMooovingChatScope(client *websocket.Client, chatID uuid.UUID) bool {
	if client.ClientType != websocket.ClientTypeDriver &&
		client.ClientType != websocket.ClientTypeMoClient {
		return true
	}
	if client.ChatID == uuid.Nil || client.ChatID == chatID {
		return true
	}
	log.Printf("enforceMooovingChatScope[security]: %s обратился к чату %s (его чат: %s)",
		client.ClientType, chatID, client.ChatID)
	client.SendError("forbidden", "Это не ваш чат")
	return false
}

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
// Если перевод на язык админа существует, заменяет content и ставит metadata-флаги
// которые фронтенд использует для отображения (isTranslated, originalText, translatedText).
func applyTranslationForAdmin(message *models.Message, adminLang string) *models.Message {
	adminMessage := *message
	if message.Metadata == nil || adminLang == "" {
		return &adminMessage
	}

	if translatedText, ok := getTranslation(message.Metadata, adminLang); ok {
		// Копируем metadata чтобы не мутировать оригинал
		newMetadata := make(map[string]interface{})
		for k, v := range message.Metadata {
			newMetadata[k] = v
		}
		newMetadata["isTranslated"] = true
		newMetadata["originalText"] = message.Content
		newMetadata["translatedText"] = translatedText

		adminMessage.Content = translatedText
		adminMessage.Metadata = newMetadata
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

// broadcastToAdminsPersonalized отправляет user-сообщение каждому админу
// с переводом на его язык. Если переводов нет — отправляет оригинал.
func broadcastToAdminsPersonalized(chatID uuid.UUID, message *models.Message, chatInfo map[string]interface{}) {
	fallback := createMessageNotification(chatID, message, chatInfo)

	if message.Metadata != nil {
		if _, ok := message.Metadata["translations"]; ok {
			WebSocketHub.SendToAllAdminsWithCallback(func(adminClient *websocket.Client) []byte {
				adminLang := getAdminLanguage(adminClient.UserID)
				personalizedMsg := applyTranslationForAdmin(message, adminLang)
				return createMessageNotification(chatID, personalizedMsg, chatInfo)
			})
			return
		}
	}
	WebSocketHub.SendToAllAdmins(fallback)
}

// notifyNewMessages отправляет WebSocket уведомления о новых сообщениях (user + bot).
// Either userMsg or botMsg can be nil — only non-nil messages are broadcast.
func notifyNewMessages(chat *models.Chat, userMsg *models.Message, botMsg *models.Message) {
	chatInfo := createChatInfo(chat)

	if userMsg != nil {
		// Виджетам — оригинал
		widgetNotification := createMessageNotification(chat.ID, userMsg, chatInfo)
		WebSocketHub.SendToChat(chat.ID.String(), widgetNotification)

		// Админам — персонализированный перевод
		if userMsg.Sender == "user" {
			broadcastToAdminsPersonalized(chat.ID, userMsg, chatInfo)
		} else {
			WebSocketHub.SendToAllAdmins(widgetNotification)
		}
	}

	if botMsg != nil {
		botNotification := createMessageNotification(chat.ID, botMsg, chatInfo)
		WebSocketHub.SendToChatAndAdmins(chat.ID.String(), botNotification)
	}
}
