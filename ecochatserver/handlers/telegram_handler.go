package handlers

import (
	"log"
	"net/http"
	"os"
	"strconv"
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
	raw := os.Getenv("ENABLE_AUTO_RESPONDER")
	if raw == "" {
		raw = "true"
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		log.Printf(
			"InitAutoResponder: неверное значение ENABLE_AUTO_RESPONDER=%q: %v — включаем по умолчанию",
			raw, err,
		)
		enabled = true
	}
	if !enabled {
		log.Println("Автоответчик отключен в настройках")
		return
	}

	client := llm.NewLLMClient()
	cfg := llm.GetDefaultConfig()
	AutoResponder = llm.NewAutoResponder(client, cfg)

	// Инициализируем сервис перевода
	Translator = NewTranslationService(client)
	log.Println("Сервис перевода успешно инициализирован")

	// Устанавливаем callback для отправки сообщений извинения
	AutoResponder.SetApologyCallback(func(chatID uuid.UUID, message *models.Message) {
		// Отправляем WebSocket уведомление о сообщении извинения
		notification := createChatNotification(chatID, message, nil)
		if WebSocketHub != nil {
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

	messageTime := time.Now()

	// Создаём или получаем чат
	log.Printf("TelegramWebhook: создаем/получаем чат для user=%s, source=%s, botID=%s, clientID=%s",
		in.UserID, in.Source, in.BotID, in.ClientID)

	chat, err := database.GetOrCreateChat(
		in.UserID, in.UserName, in.UserEmail,
		in.Source, in.UserID, in.BotID, in.ClientID,
	)
	if err != nil {
		log.Printf("TelegramWebhook: GetOrCreateChat error: %v", err)
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

	if Translator != nil {
		log.Printf("TelegramWebhook: перевод сообщения пользователя")
		result, err := Translator.TranslateUserMessage(c.Request.Context(), in.Content, chat.ID)
		if err != nil {
			log.Printf("TelegramWebhook: ошибка перевода сообщения: %v", err)
			// Продолжаем с оригинальным текстом
		} else if result != nil {
			// Используем переведенный текст (или оригинал, если перевод не нужен)
			messageContent = result.Content
			// Добавляем метаданные перевода
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

	// Создаем детерминированный ID для сообщения (дедупликация)
	// Используем оригинальный content для ID, чтобы избежать дублей при переводе
	messageID := generateMessageID(chat.ID, userUUID, in.Content, messageTime)

	// Добавляем сообщение пользователя с детерминированным ID
	msgType := "text"
	if in.MessageType != "" {
		msgType = in.MessageType
	}

	log.Printf("TelegramWebhook: добавляем сообщение в чат %s от пользователя %s с ID %s",
		chat.ID, userUUID, messageID)

	userMsg, err := database.AddMessageWithID(
		messageID,
		chat.ID,
		messageContent,    // Используем переведенный контент
		"user",
		userUUID,
		messageTime,
		msgType,
		messageMetadata,   // Используем обогащенные метаданные
	)
	if err != nil {
		log.Printf("TelegramWebhook: AddMessage error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: сообщение добавлено: ID=%s", userMsg.ID)

	// Быстро обновляем время чата
	if err := queries.UpdateChatTimestamp(database.DB, chat.ID); err != nil {
		log.Printf("TelegramWebhook: ошибка обновления времени: %v", err)
	}

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

		botMsg, err = AutoResponder.ProcessMessage(
			c.Request.Context(),
			lightChat,
			userMsg,
		)
		if err != nil {
			log.Printf("TelegramWebhook: AutoResponder.ProcessMessage error: %v", err)
		} else if botMsg != nil {
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
				if err := queries.UpdateChatTimestamp(database.DB, chat.ID); err != nil {
					log.Printf("TelegramWebhook: ошибка обновления времени: %v", err)
				}

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

	// ВАЖНО: Отправляем только ОДНО комплексное WebSocket сообщение
	if userMsg != nil {
		notification := createChatNotification(chat.ID, userMsg, botMsg)
		// Отправляем уведомление как виджету, так и всем админам
		totalSent := WebSocketHub.SendToChatAndAdmins(chat.ID.String(), notification)
		log.Printf("TelegramWebhook: комплексное WebSocket уведомление отправлено %d клиентам", totalSent)
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

// createChatNotification создает комплексное уведомление для WebSocket
func createChatNotification(chatID uuid.UUID, userMsg, botMsg *models.Message) []byte {
	// Формируем структуру, совместимую с admin interface
	// Админка ожидает chatId и message на верхнем уровне
	payload := map[string]interface{}{
		"chatId": chatID.String(),
		"message": map[string]interface{}{
			"id":        userMsg.ID.String(),
			"chatId":    chatID.String(),
			"content":   userMsg.Content,
			"sender":    userMsg.Sender,
			"timestamp": userMsg.Timestamp.Format(time.RFC3339),
			"read":      false,
			"type":      userMsg.Type,
			"metadata":  userMsg.Metadata,
		},
	}

	// Если есть автоответ бота, отправляем его отдельно
	if botMsg != nil {
		// Для ответа бота создаем отдельное уведомление
		botPayload := map[string]interface{}{
			"chatId": chatID.String(),
			"message": map[string]interface{}{
				"id":        botMsg.ID.String(),
				"chatId":    chatID.String(),
				"content":   botMsg.Content,
				"sender":    botMsg.Sender,
				"timestamp": botMsg.Timestamp.Format(time.RFC3339),
				"read":      false,
				"type":      botMsg.Type,
				"metadata":  botMsg.Metadata,
			},
			"chat": map[string]interface{}{
				"id": chatID.String(),
			},
		}

		// Отправляем уведомление о боте отдельно
		botNotification, _ := websocket.NewMessage("new_message", botPayload)
		WebSocketHub.SendToChatAndAdmins(chatID.String(), botNotification)
		log.Printf("TelegramWebhook: отправлено WebSocket уведомление о сообщении бота")
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
