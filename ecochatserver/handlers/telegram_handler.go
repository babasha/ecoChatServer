package handlers

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/egor/ecochatserver/websocket"
)

// AutoResponder — единственный экземпляр автоответчика
var AutoResponder *llm.AutoResponder

// Translator — сервис для перевода сообщений
var Translator *TranslationService

// InitAutoResponder инициализирует автоответчик (LLMклиент + конфиг)
func InitAutoResponder() {
	// Проверяем включен ли автоответчик (из БД с fallback на ENV)
	enabled := database.GetSettingBool("ENABLE_AUTO_RESPONDER", true)
	if !enabled {
		log.Println("Автоответчик отключен в настройках БД/ENV")
		return
	}
	log.Println("Автоответчик включен, инициализируем...")

	// 🔧 ОПТИМИЗАЦИЯ: Создаём ОДИН провайдер и переиспользуем его
	// Это экономит ресурсы - один HTTP клиент вместо двух
	provider, err := llm.NewProvider(nil)
	if err != nil {
		log.Fatalf("InitAutoResponder: не удалось создать провайдера: %v", err)
	}
	log.Printf("Провайдер LLM инициализирован: %s", provider.GetName())

	// Создаём AutoResponder с переиспользованием провайдера
	cfg := llm.GetDefaultConfig()
	AutoResponder = llm.NewAutoResponder(provider, cfg)

	// Инициализируем сервис перевода с ТЕМ ЖЕ провайдером (переиспользование!)
	Translator = NewTranslationService(provider)
	log.Printf("Сервис перевода инициализирован (переиспользует провайдер)")

	// Устанавливаем callback для отправки сообщений извинения
	AutoResponder.SetApologyCallback(func(chatID uuid.UUID, message *models.Message) {
		// Загружаем легковесную версию чата для уведомления
		lightChat, err := queries.GetChatLightweight(database.DB, chatID)
		if err != nil {
			log.Printf("ApologyCallback: ошибка загрузки чата: %v", err)
			// Создаем минимальный объект чата
			lightChat = &models.Chat{
				ID:       chatID,
				Messages: []models.Message{*message},
			}
		} else {
			// Добавляем текущее сообщение к списку
			lightChat.Messages = append(lightChat.Messages, *message)
		}

		// Отправляем WebSocket уведомление о сообщении извинения
		if WebSocketHub != nil {
			chatInfo := createChatInfo(lightChat)
			notification := createMessageNotification(chatID, message, chatInfo)
			totalSent := WebSocketHub.SendToChatAndAdmins(chatID.String(), notification)
			log.Printf("TelegramWebhook: уведомление о сообщении извинения отправлено %d клиентам", totalSent)
		}
	})

	log.Println("Автоответчик успешно инициализирован")
}

// TelegramWebhook обрабатывает вебхук Telegram и виджета
func TelegramWebhook(c *gin.Context) {
	log.Printf("TelegramWebhook: %s %s from %s", c.Request.Method, c.FullPath(), c.ClientIP())

	// OPTIONS для CORS
	if c.Request.Method == http.MethodOptions {
		handleCORS(c)
		c.Status(http.StatusOK)
		return
	}
	handleCORS(c)

	// Проверяем Content-Type
	if !strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		log.Printf("TelegramWebhook: неверный Content-Type: %s", c.GetHeader("Content-Type"))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type должен быть application/json"})
		return
	}

	// Парсим входящее сообщение
	var in models.IncomingMessage
	if err := c.ShouldBindJSON(&in); err != nil {
		log.Printf("TelegramWebhook: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: получено сообщение: %+v", in)

	if in.UserID == "" {
		log.Printf("TelegramWebhook: отсутствует UserID")
		c.JSON(http.StatusBadRequest, gin.H{"error": "UserID обязателен"})
		return
	}
	if in.ClientID == "" {
		in.ClientID = "test_client_id"
		log.Printf("TelegramWebhook: ClientID не указан, используем: %s", in.ClientID)
	} else {
		log.Printf("TelegramWebhook: используем ClientID: %s", in.ClientID)
	}

	// Создаём или получаем чат (БЕЗ загрузки истории сообщений для оптимизации)
	log.Printf("TelegramWebhook: создаем/получаем чат для user=%s, source=%s, botID=%s, clientID=%s",
		in.UserID, in.Source, in.BotID, in.ClientID)

	chat, err := database.GetOrCreateChatMetadata(
		in.UserID, in.UserName, in.UserEmail,
		in.Source, in.UserID, in.BotID, in.ClientID,
	)
	if err != nil {
		log.Printf("TelegramWebhook: GetOrCreateChatMetadata error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: получен чат: ID=%s, ClientID=%s, UserID=%s",
		chat.ID, chat.ClientID, chat.User.ID)

	// ВАЖНО: Обновляем chat_id для виджета, если он подключен с временным ID
	// Передаем chat.User.SourceID для проверки безопасности
	if WebSocketHub.UpdateWidgetChatID(in.UserID, chat.ID, chat.User.SourceID) {
		log.Printf("TelegramWebhook: виджет %s успешно обновлен с chat_id=%s", in.UserID, chat.ID)
	}

	// Переводим сообщение пользователя, если включен переводчик
	messageContent := in.Content
	messageMetadata := in.Metadata
	if messageMetadata == nil {
		messageMetadata = make(map[string]interface{})
	}

	// Сохраняем оригинальный текст для LLM
	originalContent := in.Content

	if Translator != nil {
		log.Printf("TelegramWebhook: перевод сообщения пользователя")
		result, err := Translator.TranslateUserMessage(c.Request.Context(), in.Content, chat.ID)
		if err != nil {
			log.Printf("TelegramWebhook: ошибка перевода сообщения: %v", err)
			// Продолжаем с оригинальным текстом
		} else if result != nil {
			// ВАЖНО: Сохраняем ОРИГИНАЛ в content, перевод в metadata
			messageContent = originalContent // Оставляем оригинальный текст!

			// result.Metadata уже содержит translations - просто добавляем все метаданные
			for k, v := range result.Metadata {
				messageMetadata[k] = v
			}

			log.Printf("TelegramWebhook: перевод завершен, wasTranslated=%v, detectedLang=%s",
				result.WasTranslated, result.DetectedLanguage)
		}
	}

	// Создаем детерминированный UUID для отправителя
	var userUUID uuid.UUID
	if parsedUUID, err := uuid.Parse(in.UserID); err == nil {
		userUUID = parsedUUID
		log.Printf("TelegramWebhook: UserID %s уже является валидным UUID", in.UserID)
	} else {
		userUUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(in.UserID))
		log.Printf("TelegramWebhook: создан детерминированный UUID для userID %s: %s", in.UserID, userUUID.String())
	}

	// Добавляем сообщение пользователя
	msgType := "text"
	if in.MessageType != "" {
		msgType = in.MessageType
	}

	log.Printf("TelegramWebhook: добавляем сообщение в чат %s от пользователя %s",
		chat.ID, userUUID)

	userMsg, err := database.AddMessage(
		chat.ID,
		messageContent,    // Сохраняем оригинальный текст пользователя
		"user",
		userUUID,
		msgType,
		messageMetadata,   // Метаданные содержат translations для админа
	)
	if err != nil {
		log.Printf("TelegramWebhook: AddMessage error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: сообщение добавлено: ID=%s", userMsg.ID)

	// Быстро обновляем время чата
	updateChatTimestamp(chat.ID)

	// Генерируем автоответ, если включено
	var botMsg *models.Message
	if AutoResponder != nil && chat.AutoResponderEnabled {
		log.Printf("TelegramWebhook: генерируем автоответ")

		// Загружаем минимальную информацию о чате для автоответчика
		lightChat, err := queries.GetChatLightweight(database.DB, chat.ID)
		if err != nil {
			log.Printf("TelegramWebhook: ошибка загрузки чата: %v", err)
			lightChat = chat // Используем уже загруженный чат
		}

		// LLM получает оригинальный текст (он уже в userMsg.Content)
		// Теперь content всегда содержит оригинал, не нужно подменять
		botMsg, err = AutoResponder.ProcessMessage(
			c.Request.Context(),
			lightChat,
			userMsg,
		)
		if err != nil {
			log.Printf("TelegramWebhook: AutoResponder.ProcessMessage error: %v", err)

			// Если ошибка от Gemini API (перегрузка, таймаут и т.д.) - отправляем извинение
			if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "overloaded") {
				botMsg = &models.Message{
					ChatID:    chat.ID,
					Content:   "Извините, сервис временно перегружен 😔 Попробуйте повторить запрос через несколько секунд.",
					Sender:    "admin",
					SenderID:  uuid.MustParse("00000000-0000-0000-0000-000000000000"),
					Type:      "text",
					Timestamp: time.Now(),
					Metadata:  make(map[string]interface{}),
				}
			}
		}

		if botMsg != nil {
			log.Printf("TelegramWebhook: автоответ сгенерирован, сохраняем в БД")
			botUUID := botMsg.SenderID

			saved, err := database.AddMessage(
				chat.ID,
				botMsg.Content,
				botMsg.Sender,
				botUUID,
				botMsg.Type,
				botMsg.Metadata,
			)
			if err != nil {
				log.Printf("TelegramWebhook: ошибка сохранения автоответа: %v", err)
			} else {
				botMsg = saved
				log.Printf("TelegramWebhook: автоответ сохранен: ID=%s", botMsg.ID)

				// Обновляем время чата
				updateChatTimestamp(chat.ID)

				// Проверяем нужна ли эскалация
				if needEscalation, ok := botMsg.Metadata["needEscalation"].(bool); ok {
					log.Printf("TelegramWebhook: проверка эскалации для чата %s: needEscalation=%v", chat.ID, needEscalation)
					if needEscalation {
						log.Printf("TelegramWebhook: требуется эскалация для чата %s", chat.ID)
						// Отправляем уведомление админам об эскалации
						escalationNotification := createEscalationNotification(chat.ID, userMsg)
						totalSent := WebSocketHub.SendToAllAdmins(escalationNotification)
						log.Printf("TelegramWebhook: уведомление об эскалации отправлено %d админам", totalSent)
					}
				} else {
					log.Printf("TelegramWebhook: поле needEscalation отсутствует в метаданных для чата %s", chat.ID)
				}
			}
		} else {
			log.Printf("TelegramWebhook: автоответ не сгенерирован (botMsg == nil)")
		}
	} else {
		log.Printf("TelegramWebhook: автоответчик не активен")
	}

	// ВАЖНО: Отправляем WebSocket уведомления
	if userMsg != nil {
		// Создаем общую информацию о чате (используется в обоих уведомлениях)
		chatInfo := createChatInfo(chat)

		// Отправляем уведомление о сообщении пользователя
		userNotification := createMessageNotification(chat.ID, userMsg, chatInfo)
		totalSent := WebSocketHub.SendToChatAndAdmins(chat.ID.String(), userNotification)
		log.Printf("TelegramWebhook: уведомление о сообщении пользователя отправлено %d клиентам", totalSent)

		// Если есть автоответ бота, отправляем его отдельно
		if botMsg != nil {
			botNotification := createMessageNotification(chat.ID, botMsg, chatInfo)
			totalSent := WebSocketHub.SendToChatAndAdmins(chat.ID.String(), botNotification)
			log.Printf("TelegramWebhook: уведомление о сообщении бота отправлено %d клиентам", totalSent)
		}
	}

	// Ответ клиенту
	response := gin.H{
		"success":    true,
		"message":    "Сообщение обработано",
		"message_id": userMsg.ID.String(),
		"chat_id":    chat.ID.String(),
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	log.Printf("TelegramWebhook: отправляем ответ: %+v", response)
	c.JSON(http.StatusOK, response)
}

// createChatInfo создает информацию о чате для WebSocket уведомлений (используется повторно)
func createChatInfo(chat *models.Chat) map[string]interface{} {
	// ВАЖНО: unreadCount НЕ используется фронтендом из WebSocket!
	// Фронтенд сам инкрементирует счётчик локально при получении нового сообщения.
	// Источник правды для unreadCount - это GetChats с SQL COUNT из БД.
	// Здесь отправляем 0 для совместимости, но фронтенд это значение игнорирует.

	return map[string]interface{}{
		"id":                   chat.ID.String(),
		"user":                 chat.User,
		"status":               chat.Status,
		"clientId":             chat.ClientID.String(),
		"createdAt":            chat.CreatedAt.Format(time.RFC3339),
		"updatedAt":            chat.UpdatedAt.Format(time.RFC3339),
		"unreadCount":          0, // Фронтенд не использует это значение
		"autoResponderEnabled": chat.AutoResponderEnabled,
	}
}

// createMessageNotification создает WebSocket уведомление для одного сообщения
func createMessageNotification(chatID uuid.UUID, message *models.Message, chatInfo map[string]interface{}) []byte {
	payload := map[string]interface{}{
		"chatId": chatID.String(),
		"message": map[string]interface{}{
			"id":        message.ID.String(),
			"chatId":    chatID.String(),
			"content":   message.Content,
			"sender":    message.Sender,
			"timestamp": message.Timestamp.Format(time.RFC3339),
			"read":      false,
			"type":      message.Type,
			"metadata":  message.Metadata,
		},
		"chat": chatInfo, // Переиспользуем созданную информацию о чате
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
		"sound":  "notification", // Флаг для звукового уведомления
		"urgent": true,
	}

	msg, _ := websocket.NewMessage("escalation_alert", payload)
	log.Printf("createEscalationNotification: создано уведомление об эскалации: %s", string(msg))
	return msg
}

// handleCORS выставляет стандартные CORS заголовки
func handleCORS(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if origin == "" {
		origin = "*"
	}
	c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
	c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
	c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
	c.Writer.Header().Set("Access-Control-Max-Age", "86400")
}
