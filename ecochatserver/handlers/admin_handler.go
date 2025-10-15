package handlers

import (
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Кеш для настроек админов (чтобы не лезть в БД при каждом запросе)
type adminSettingsCache struct {
	settings  map[uuid.UUID]*queries.AdminSettings
	expiresAt map[uuid.UUID]time.Time
	mu        sync.RWMutex
	ttl       time.Duration
}

var settingsCache = &adminSettingsCache{
	settings:  make(map[uuid.UUID]*queries.AdminSettings),
	expiresAt: make(map[uuid.UUID]time.Time),
	ttl:       5 * time.Minute, // Кешируем на 5 минут
}

// getAdminSettings получает настройки админа с кешированием
func (cache *adminSettingsCache) getAdminSettings(adminID uuid.UUID) (*queries.AdminSettings, error) {
	// Проверяем кеш
	cache.mu.RLock()
	if settings, exists := cache.settings[adminID]; exists {
		if time.Now().Before(cache.expiresAt[adminID]) {
			cache.mu.RUnlock()
			log.Printf("getAdminSettings: используем кеш для админа %s", adminID)
			return settings, nil
		}
	}
	cache.mu.RUnlock()

	// Кеш устарел или отсутствует - загружаем из БД
	settings, err := queries.GetAdminSettings(database.DB, adminID)
	if err != nil {
		return nil, err
	}

	// Сохраняем в кеш
	cache.mu.Lock()
	cache.settings[adminID] = settings
	cache.expiresAt[adminID] = time.Now().Add(cache.ttl)
	cache.mu.Unlock()

	log.Printf("getAdminSettings: загружено из БД и закешировано для админа %s", adminID)
	return settings, nil
}

// invalidateAdminSettings инвалидирует кеш настроек админа
func (cache *adminSettingsCache) invalidateAdminSettings(adminID uuid.UUID) {
	cache.mu.Lock()
	delete(cache.settings, adminID)
	delete(cache.expiresAt, adminID)
	cache.mu.Unlock()
	log.Printf("invalidateAdminSettings: кеш инвалидирован для админа %s", adminID)
}

// GetChats возвращает список чатов для админки
func GetChats(c *gin.Context) {
	// Получаем параметры пагинации
	page, size := parsePagination(c)

	// Для админки получаем ВСЕ чаты всех клиентов
	// Используем специальное значение для обозначения "все клиенты"
	var clientID uuid.UUID // будет nil UUID (00000000-0000-0000-0000-000000000000)

	// Получаем adminID из JWT токена (опционально, для фильтрации)
	adminIDStr, exists := c.Get("adminID")
	var adminID uuid.UUID
	if exists {
		adminID, _ = uuid.Parse(adminIDStr.(string))
	} else {
		adminID = uuid.Nil
	}

	chats, total, err := database.GetChats(clientID, adminID, page, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Ошибка получения чатов",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chats": chats,
		"pagination": gin.H{
			"page":  page,
			"size":  size,
			"total": total,
			"pages": (total + size - 1) / size,
		},
	})
}

// GetChatByID возвращает конкретный чат с сообщениями
func GetChatByID(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Получаем параметры пагинации для сообщений (infinite scroll)
	limitStr := c.DefaultQuery("limit", "25")
	before := c.Query("before")

	limit, _ := strconv.Atoi(limitStr)
	if limit < 1 || limit > 100 {
		limit = 25
	}

	chat, _, err := database.GetChatByID(chatID, limit, before)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Ошибка получения чата",
			"details": err.Error(),
		})
		return
	}

	// Переводим сообщения для админа (lazy caching)
	if Translator != nil {
		adminID, err := getAdminID(c)
		if err == nil {
			settings, err := settingsCache.getAdminSettings(adminID)
			if err == nil && settings.PreferredLanguage != "" {
				log.Printf("GetChatByID: перевод сообщений для админа %s (язык: %s)", adminID, settings.PreferredLanguage)
				err = Translator.TranslateMessagesForAdmin(c.Request.Context(), chat.Messages, settings.PreferredLanguage)
				if err != nil {
					log.Printf("GetChatByID: ошибка перевода сообщений: %v", err)
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"chat": chat})
}

// SendMessageToChat отправляет сообщение от админа в чат
func SendMessageToChat(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Парсим тело запроса
	var request struct {
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("SendMessageToChat: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	if request.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Содержимое сообщения не может быть пустым"})
		return
	}

	// Проверяем размер сообщения
	const maxMessageLength = 2000
	if len(request.Content) > maxMessageLength {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":         "Сообщение слишком длинное",
			"details":       "Максимальная длина сообщения - 2000 символов",
			"maxLength":     maxMessageLength,
			"currentLength": len(request.Content),
		})
		return
	}

	log.Printf("SendMessageToChat: отправка сообщения в чат %s от админа", chatID)

	// Получаем реальный ID админа из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	// Переводим сообщение админа на язык клиента
	var messageContent = request.Content // Сохраняем оригинал
	var messageMetadata map[string]interface{}

	if Translator != nil {
		log.Printf("SendMessageToChat: попытка перевода сообщения админа")
		result, err := Translator.TranslateAdminMessage(c.Request.Context(), request.Content, chatID, adminID)
		if err != nil {
			log.Printf("SendMessageToChat: ошибка перевода сообщения: %v", err)
			// Продолжаем с оригинальным текстом
		} else if result != nil {
			// ВАЖНО: Сохраняем ОРИГИНАЛ в content, перевод в metadata
			messageContent = request.Content  // Оставляем оригинальный текст!
			messageMetadata = result.Metadata // metadata уже содержит translations

			if result.WasTranslated {
				log.Printf("SendMessageToChat: сообщение переведено с %s на %s",
					result.Metadata["detectedLanguage"], result.Metadata["targetLanguage"])
			} else if result.Metadata["translationFailed"] == true {
				// Перевод не удался - сохраняем оригинал как fallback
				if targetLang, ok := result.Metadata["targetLanguage"].(string); ok {
					translations := make(map[string]interface{})
					translations[targetLang] = request.Content // Оригинал как fallback
					if messageMetadata == nil {
						messageMetadata = make(map[string]interface{})
					}
					messageMetadata["translations"] = translations
					log.Printf("SendMessageToChat: перевод не удался, сохранён оригинал как fallback для языка %s", targetLang)
				}
			} else {
				log.Printf("SendMessageToChat: перевод не требуется")
			}
		}
	} else {
		log.Printf("SendMessageToChat: сервис перевода недоступен")
	}

	// Добавляем сообщение в базу данных (с оригинальным текстом)
	message, err := database.AddMessage(
		chatID,
		messageContent,
		"admin",
		adminID,
		"text",
		messageMetadata,
	)
	if err != nil {
		log.Printf("SendMessageToChat: ошибка добавления сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сохранения сообщения"})
		return
	}

	log.Printf("SendMessageToChat: сообщение сохранено: ID=%s", message.ID)

	// Обновляем время чата
	updateChatTimestamp(chatID)

	// Инвалидируем Redis кеш (lastMessage изменилось)
	database.InvalidateChatsCacheForAll()

	// Отправляем сообщение во внешние каналы (например, Instagram)
	go dispatchExternalMessage(chatID, message)

	// Для WebSocket нужно отправить переведенное сообщение клиенту
	// Создаем копию сообщения для виджета с переводом
	widgetMessage := *message

	// Если есть перевод - используем его для виджета
	if message.Metadata != nil {
		log.Printf("SendMessageToChat: metadata != nil, содержимое: %+v", message.Metadata)
		log.Printf("SendMessageToChat: тип translations: %T", message.Metadata["translations"])
		if translations, ok := message.Metadata["translations"].(map[string]interface{}); ok {
			log.Printf("SendMessageToChat: найдены translations: %+v", translations)
			// Определяем язык клиента из чата
			clientLang, err := database.GetClientLanguageFromChat(chatID)
			log.Printf("SendMessageToChat: GetClientLanguageFromChat вернул lang=%s, err=%v", clientLang, err)
			if err == nil && clientLang != "" {
				if translation, exists := translations[clientLang]; exists {
					if translatedText, ok := translation.(string); ok && translatedText != "" {
						widgetMessage.Content = translatedText
						log.Printf("SendMessageToChat: для WebSocket используется перевод на %s", clientLang)
					}
				} else {
					log.Printf("SendMessageToChat: перевод для языка %s не найден в translations", clientLang)
				}
			}
		} else {
			log.Printf("SendMessageToChat: translations не найдены в metadata")
		}
	} else {
		log.Printf("SendMessageToChat: metadata == nil")
	}

	// Отправляем WebSocket уведомление в формате совместимом с виджетом и админкой
	messagePayload := createMessagePayload(&widgetMessage, chatID)

	payload := map[string]interface{}{
		"chatId":  chatID.String(), // для админки
		"message": messagePayload,
		"chat": map[string]interface{}{ // для виджета
			"id": chatID.String(),
		},
	}

	wsMessage, _ := websocket.NewMessage("new_message", payload)
	totalSent := WebSocketHub.SendToChatAndAdmins(chatID.String(), wsMessage)
	log.Printf("SendMessageToChat: WebSocket уведомление отправлено %d клиентам", totalSent)

	// Возвращаем только статус успеха
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Сообщение отправлено",
	})
}

// ToggleAutoResponder включает/выключает автоответчик для чата
func ToggleAutoResponder(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Парсим тело запроса
	var request struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("ToggleAutoResponder: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	log.Printf("ToggleAutoResponder: обновление автоответчика для чата %s, enabled=%t", chatID, request.Enabled)

	// Обновляем статус автоответчика в базе данных
	if err := queries.UpdateAutoResponder(database.DB, chatID, request.Enabled); err != nil {
		log.Printf("ToggleAutoResponder: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления автоответчика"})
		return
	}

	log.Printf("ToggleAutoResponder: автоответчик успешно обновлен для чата %s", chatID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"enabled": request.Enabled,
		"message": "Автоответчик успешно обновлен",
	})
}

// GetAdminSettings возвращает настройки админа
func GetAdminSettings(c *gin.Context) {
	// Получаем adminID из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	log.Printf("GetAdminSettings: получение настроек для админа %s", adminID)

	settings, err := queries.GetAdminSettings(database.DB, adminID)
	if err != nil {
		log.Printf("GetAdminSettings: ошибка получения настроек: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения настроек"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"preferredLanguage": settings.PreferredLanguage,
	})
}

// UpdateAdminSettings обновляет настройки админа
func UpdateAdminSettings(c *gin.Context) {
	// Получаем adminID из JWT токена
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	// Парсим тело запроса
	var request struct {
		PreferredLanguage string `json:"preferredLanguage" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("UpdateAdminSettings: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных", "details": err.Error()})
		return
	}

	// Валидируем код языка (опционально, можно расширить список)
	validLanguages := map[string]bool{
		"ru": true, "en": true, "pl": true, "de": true, "fr": true,
		"es": true, "it": true, "uk": true, "be": true, "cs": true,
		"sk": true, "lt": true, "lv": true, "et": true,
	}

	if !validLanguages[request.PreferredLanguage] {
		log.Printf("UpdateAdminSettings: недопустимый код языка: %s", request.PreferredLanguage)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Недопустимый код языка"})
		return
	}

	log.Printf("UpdateAdminSettings: обновление языка для админа %s на %s", adminID, request.PreferredLanguage)

	// Обновляем настройки
	if err := queries.UpsertAdminSettings(database.DB, adminID, request.PreferredLanguage); err != nil {
		log.Printf("UpdateAdminSettings: ошибка обновления: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка обновления настроек"})
		return
	}

	// Инвалидируем кеш настроек админа
	settingsCache.invalidateAdminSettings(adminID)

	log.Printf("UpdateAdminSettings: настройки успешно обновлены для админа %s", adminID)

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"preferredLanguage": request.PreferredLanguage,
		"message":           "Настройки успешно обновлены",
	})
}

// MarkChatMessagesAsRead помечает все сообщения в чате как прочитанные
func MarkChatMessagesAsRead(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	// Парсим тело запроса с массивом ID сообщений
	var request struct {
		MessageIDs []string `json:"messageIds"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		log.Printf("MarkChatMessagesAsRead: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
		return
	}

	if len(request.MessageIDs) == 0 {
		log.Printf("MarkChatMessagesAsRead: пустой массив messageIds")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Массив messageIds не может быть пустым"})
		return
	}

	// Конвертируем строки в UUID
	messageUUIDs := make([]uuid.UUID, 0, len(request.MessageIDs))
	for _, idStr := range request.MessageIDs {
		msgUUID, err := uuid.Parse(idStr)
		if err != nil {
			log.Printf("MarkChatMessagesAsRead: некорректный UUID: %s", idStr)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат messageId"})
			return
		}
		messageUUIDs = append(messageUUIDs, msgUUID)
	}

	log.Printf("MarkChatMessagesAsRead: пометка %d сообщений в чате %s", len(messageUUIDs), chatID)

	// Помечаем конкретные сообщения как прочитанные
	markedCount, err := database.MarkSpecificMessagesAsRead(messageUUIDs)
	if err != nil {
		log.Printf("MarkChatMessagesAsRead: ошибка пометки сообщений: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка пометки сообщений"})
		return
	}

	log.Printf("MarkChatMessagesAsRead: успешно помечено %d сообщений из %d", markedCount, len(messageUUIDs))

	// Отправляем WebSocket уведомление о прочтении сообщений ТОЛЬКО если что-то изменилось
	if markedCount > 0 {
		readNotification := map[string]interface{}{
			"chatId":     chatID.String(),
			"messageIds": request.MessageIDs,
			"count":      markedCount,
		}

		wsMessage, _ := websocket.NewMessage("messages_read", readNotification)
		totalSent := WebSocketHub.SendToChatAndAdmins(chatID.String(), wsMessage)
		log.Printf("MarkChatMessagesAsRead: WebSocket уведомление отправлено %d клиентам", totalSent)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Сообщения помечены как прочитанные",
		"marked":  markedCount,
	})
}
