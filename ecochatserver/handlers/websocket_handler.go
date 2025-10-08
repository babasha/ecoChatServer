package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/middleware"
	"github.com/egor/ecochatserver/models"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// wsUpgrader апгрейдит HTTP→WebSocket с улучшенной проверкой Origin
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkOrigin,
}

// checkOrigin проверяет, разрешен ли Origin для подключения
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Разрешаем локальные подключения без Origin
		host := r.Host
		if strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") {
			return true
		}
		return false
	}

	// Получаем разрешенные origins из переменных окружения
	allowedOrigins := []string{}

	// Основной URL фронтенда
	if frontendURL := os.Getenv("FRONTEND_URL"); frontendURL != "" {
		allowedOrigins = append(allowedOrigins, frontendURL)
	}

	// Дополнительные разрешенные origins
	if additional := os.Getenv("ADDITIONAL_ALLOWED_ORIGINS"); additional != "" {
		for _, url := range strings.Split(additional, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				allowedOrigins = append(allowedOrigins, url)
			}
		}
	}

	// Проверяем, есть ли origin в списке разрешенных
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}

	// Для разработки можно разрешить все origins
	if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
		log.Printf("ВНИМАНИЕ: Разрешен origin %s (ALLOW_ALL_ORIGINS=true)", origin)
		return true
	}

	log.Printf("Отклонен origin: %s", origin)
	return false
}

// ServeWs обрабатывает WebSocket соединение
func ServeWs(c *gin.Context) {
	log.Printf("ServeWs: новое соединение от %s, origin: %s",
		c.ClientIP(), c.Request.Header.Get("Origin"))

	// Получаем параметры и токен
	token := c.Query("token")
	clientType := c.DefaultQuery("type", "admin")
	chatIDStr := c.Query("chat_id")

	// Для виджета chat_id необязателен - может быть создан позже
	if clientType == "widget" && chatIDStr == "" {
		log.Printf("ServeWs: виджет подключается без chat_id - чат будет создан при первом сообщении")
	}

	// Проверяем токен для админа
	var adminID, clientID, chatID uuid.UUID
	var err error

	if clientType == "admin" && token != "" {
		// Валидируем JWT токен
		claims, err := middleware.ValidateToken(token)
		if err != nil {
			log.Printf("ServeWs: ошибка валидации токена: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Неверный токен"})
			return
		}

		adminID, err = uuid.Parse(claims.AdminID)
		if err != nil {
			log.Printf("ServeWs: ошибка парсинга adminID: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
			return
		}

		clientID, err = uuid.Parse(claims.ClientID)
		if err != nil {
			log.Printf("ServeWs: ошибка парсинга clientID: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный clientID"})
			return
		}

		// Сохраняем данные в контексте для использования в обработчиках
		c.Set("adminID", claims.AdminID)
		c.Set("clientID", claims.ClientID)
		c.Set("role", claims.Role)

		log.Printf("ServeWs: аутентифицирован admin %s (client: %s)", adminID, clientID)
	} else if clientType == "widget" {
		// Для виджета парсим chatID только если он передан
		if chatIDStr != "" {
			chatID, err = uuid.Parse(chatIDStr)
			if err != nil {
				log.Printf("ServeWs: ошибка парсинга chatID: %v", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат chatID"})
				return
			}
		}

		// Получаем userID из токена для виджета (токен содержит строковый userID)
		userIDStr := token
		if userIDStr == "" {
			// Fallback на заголовок, если токен не передан
			userIDStr = c.GetHeader("X-Widget-User-ID")
		}

		if userIDStr != "" {
			// Преобразуем userID в UUID таким же образом, как в telegram_handler
			if parsedUUID, err := uuid.Parse(userIDStr); err == nil {
				adminID = parsedUUID
				log.Printf("ServeWs: UserID %s уже является валидным UUID", userIDStr)
			} else {
				adminID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(userIDStr))
				log.Printf("ServeWs: создан детерминированный UUID для userID %s: %s", userIDStr, adminID.String())
			}
		}

		log.Printf("ServeWs: подключение виджета, chatID: %s, userID: %s", chatID, adminID)
	} else {
		log.Printf("ServeWs: неверный тип клиента или отсутствует токен")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный тип клиента или отсутствует токен"})
		return
	}

	// Апгрейдим соединение
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ServeWs: ошибка апгрейда соединения: %v", err)
		return
	}

	// Создаем нового клиента
	client := websocketpkg.NewClient(WebSocketHub, conn, clientType, adminID, chatID)
	client.Context = c

	// Для виджета сохраняем исходный строковый userID
	if clientType == "widget" {
		// Используем токен как строковый userID
		userIDStr := token
		if userIDStr == "" {
			// Fallback на заголовок
			userIDStr = c.GetHeader("X-Widget-User-ID")
		}
		if userIDStr != "" {
			client.UserIDString = userIDStr
			log.Printf("ServeWs: сохранен строковый userID для виджета: %s", userIDStr)
		}
	}

	// Регистрируем клиента в хабе
	WebSocketHub.Register <- client

	// Запускаем горутины обработки
	go client.WritePump()
	go client.ReadPump(processWebSocketMessage)

	// Отправляем статус подключения
	WebSocketHub.SendConnectionStatus(client, true)

	log.Printf("ServeWs: клиент %s успешно подключен", client.ID)
}

// processWebSocketMessage обрабатывает входящие WebSocket сообщения
func processWebSocketMessage(client *websocketpkg.Client, raw []byte) {
	var msg websocketpkg.WebSocketMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		client.SendError("invalid_json", "Некорректный формат JSON")
		return
	}

	// Получаем данные из контекста Gin
	ginCtx := client.Context

	switch msg.Type {
	case "getChats":
		processGetChats(client, msg.Payload, ginCtx)
	case "getChatByID":
		processGetChatByID(client, msg.Payload, ginCtx)
	case "sendMessage":
		processSendMessage(client, msg.Payload, ginCtx)
	case "markAsRead":
		processMarkAsRead(client, msg.Payload, ginCtx)
	case "mark_read":
		// Для виджета: помечаем сообщения админа как прочитанные клиентом
		processMarkReadFromWidget(client, msg.Payload, ginCtx)
	case "typing":
		processTypingStatus(client, msg.Payload, ginCtx)
	case "getWidgetMessages":
		processGetWidgetMessages(client, msg.Payload, ginCtx)
	default:
		client.SendError("unknown_type", "Неизвестный тип сообщения: "+msg.Type)
	}
}

// processSendMessage обрабатывает отправку сообщений с автоответчиком
func processSendMessage(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID   string                 `json:"chatID"`
		Content  string                 `json:"content"`
		Type     string                 `json:"type"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для sendMessage")
		return
	}

	// Проверяем обязательные поля
	if p.ChatID == "" || p.Content == "" {
		client.SendError("missing_fields", "Необходимы поля chatID и content")
		return
	}

	// Проверяем размер сообщения в зависимости от типа клиента
	var maxLength int
	if client.ClientType == "admin" {
		maxLength = 2000 // Админ может писать более развернуто
	} else {
		maxLength = 1000 // Клиент (виджет) ограничен меньшим лимитом
	}

	if len(p.Content) > maxLength {
		client.SendError("message_too_long", fmt.Sprintf("Сообщение слишком длинное. Максимум %d символов, отправлено %d", maxLength, len(p.Content)))
		return
	}

	// Устанавливаем тип сообщения по умолчанию
	if p.Type == "" {
		p.Type = "text"
	}

	// Парсим chatID
	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	// Определяем отправителя в зависимости от типа клиента
	var senderID uuid.UUID
	var sender string

	if client.ClientType == "admin" {
		// Для админа берем ID из контекста аутентификации
		adminIDStr, exists := ginCtx.Get("adminID")
		if !exists {
			client.SendError("auth_error", "Не удалось получить ID администратора")
			return
		}
		adminID, err := uuid.Parse(adminIDStr.(string))
		if err != nil {
			client.SendError("invalid_uuid", "Некорректный adminID")
			return
		}
		senderID = adminID
		sender = "admin"

		// Переводим сообщение админа на язык клиента
		if Translator != nil {
			log.Printf("processSendMessage: попытка перевода сообщения админа")
			result, err := Translator.TranslateAdminMessage(ginCtx.Request.Context(), p.Content, chatID, adminID)
			if err != nil {
				log.Printf("processSendMessage: ошибка перевода: %v", err)
			} else if result != nil && result.WasTranslated {
				// Сохраняем перевод в metadata.translations
				if p.Metadata == nil {
					p.Metadata = make(map[string]interface{})
				}
				// Копируем metadata из результата
				for k, v := range result.Metadata {
					p.Metadata[k] = v
				}
				// Добавляем перевод
				translations := make(map[string]string)
				if targetLang, ok := result.Metadata["targetLanguage"].(string); ok {
					translations[targetLang] = result.Content
				}
				p.Metadata["translations"] = translations
				log.Printf("processSendMessage: сообщение админа переведено с %s на %s",
					result.Metadata["sourceLanguage"], result.Metadata["targetLanguage"])
			}
		}
	} else {
		// Для виджета используем ID пользователя
		senderID = client.ID
		sender = "user"
	}

	// Добавляем сообщение в базу с auto-generated UUID (оригинал в content, перевод в metadata)
	log.Printf("processSendMessage: добавление сообщения в чат %s от %s (%s): %s",
		chatID, sender, senderID, p.Content)

	message, err := database.AddMessage(
		chatID,
		p.Content,
		sender,
		senderID,
		p.Type,
		p.Metadata,
	)
	if err != nil {
		log.Printf("processSendMessage: ошибка добавления сообщения: %v", err)
		client.SendError("db_error", "Ошибка сохранения сообщения")
		return
	}

	// Быстро обновляем время чата
	updateChatTimestamp(chatID)

	// Если это сообщение от админа, очищаем состояние эскалации
	if sender == "admin" && AutoResponder != nil {
		AutoResponder.ClearEscalation(chatID.String())
		log.Printf("processSendMessage: очищена эскалация для чата %s (ответ админа)", chatID)
	}

	// ОБРАБОТКА АВТООТВЕТЧИКА
	if sender == "user" && AutoResponder != nil {
		go func() {
			// Асинхронная обработка автоответчика
			lightChat, err := queries.GetChatLightweight(database.DB, chatID)
			if err != nil {
				log.Printf("processSendMessage: ошибка загрузки чата для автоответчика: %v", err)
				return
			}

			botMsg, err := AutoResponder.ProcessMessage(ginCtx.Request.Context(), lightChat, message)
			if err != nil {
				log.Printf("processSendMessage: ошибка автоответчика: %v", err)
				return
			}

			if botMsg != nil {
				// Сохраняем автоответ
				saved, err := database.AddMessage(
					chatID,
					botMsg.Content,
					botMsg.Sender,
					botMsg.SenderID,
					botMsg.Type,
					botMsg.Metadata,
				)
				if err != nil {
					log.Printf("processSendMessage: ошибка сохранения автоответа: %v", err)
				} else {
					botMsg = saved

					// Обновляем время чата
					updateChatTimestamp(chatID)

					// Обновляем lightChat перед отправкой уведомления
					lightChat.Messages = append(lightChat.Messages, *message)

					// Создаем общую информацию о чате
					chatInfo := createChatInfo(lightChat)

					// Отправляем уведомление о сообщении пользователя
					userNotification := createMessageNotification(chatID, message, chatInfo)
					WebSocketHub.SendToChatAndAdmins(chatID.String(), userNotification)

					// Если есть автоответ бота, отправляем его отдельно
					if botMsg != nil {
						lightChat.Messages = append(lightChat.Messages, *botMsg)
						botNotification := createMessageNotification(chatID, botMsg, chatInfo)
						WebSocketHub.SendToChatAndAdmins(chatID.String(), botNotification)
					}
				}
			}
		}()
	} else {
		// Для админских сообщений нужно отправить перевод виджету
		widgetMessage := *message

		// Если сообщение от админа и есть перевод - используем его для виджета
		if sender == "admin" && message.Metadata != nil {
			if translations, ok := message.Metadata["translations"].(map[string]interface{}); ok {
				// Определяем язык клиента из чата
				clientLang, err := database.GetClientLanguageFromChat(chatID)
				if err == nil && clientLang != "" {
					if translation, exists := translations[clientLang]; exists {
						if translatedText, ok := translation.(string); ok && translatedText != "" {
							widgetMessage.Content = translatedText
							log.Printf("processSendMessage: для WebSocket используется перевод на %s", clientLang)
						}
					}
				}
			}
		}

		// Загружаем легковесную версию чата для уведомления
		lightChat, err := queries.GetChatLightweight(database.DB, chatID)
		if err != nil {
			log.Printf("processSendMessage: ошибка загрузки чата: %v", err)
			// Создаем минимальный объект чата
			lightChat = &models.Chat{
				ID:       chatID,
				Messages: []models.Message{widgetMessage},
			}
		} else {
			// Добавляем текущее сообщение к списку
			lightChat.Messages = append(lightChat.Messages, widgetMessage)
		}

		// Отправляем сообщение (с переводом для виджета)
		chatInfo := createChatInfo(lightChat)
		notification := createMessageNotification(chatID, &widgetMessage, chatInfo)
		WebSocketHub.SendToChatAndAdmins(chatID.String(), notification)
	}

	log.Printf("processSendMessage: сообщение успешно отправлено (ID=%s)", message.ID)

	// Отправляем подтверждение отправителю
	response := map[string]interface{}{
		"type": "messageSent",
		"payload": map[string]interface{}{
			"messageID": message.ID.String(),
			"timestamp": message.Timestamp,
			"status":    "delivered",
		},
	}

	if err := client.SendJSON(response); err != nil {
		log.Printf("processSendMessage: ошибка отправки подтверждения: %v", err)
	}
}

// Остальные обработчики остаются без изменений
func processGetChats(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для getChats")
		return
	}

	// Устанавливаем дефолтные значения
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 || p.PageSize > database.MaxPageSize {
		p.PageSize = database.DefaultPageSize
	}

	// Получаем ID из контекста
	adminIDStr, _ := ginCtx.Get("adminID")
	clientIDStr, _ := ginCtx.Get("clientID")

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный adminID")
		return
	}

	clientID, err := uuid.Parse(clientIDStr.(string))
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный clientID")
		return
	}

	// Получаем чаты
	log.Printf("processGetChats: запрос чатов для admin=%s, client=%s, page=%d, size=%d",
		adminID, clientID, p.Page, p.PageSize)

	chats, total, err := database.GetChats(clientID, adminID, p.Page, p.PageSize)
	if err != nil {
		log.Printf("processGetChats: ошибка получения чатов: %v", err)
		client.SendError("db_error", "Ошибка получения чатов: "+err.Error())
		return
	}

	// Рассчитываем общее количество страниц
	totalPages := (total + p.PageSize - 1) / p.PageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Формируем ответ
	response := map[string]interface{}{
		"type": "chatsList",
		"payload": models.ChatPaginationResponse{
			Chats:      chats,
			Page:       p.Page,
			PageSize:   p.PageSize,
			TotalItems: total,
			TotalPages: totalPages,
		},
	}

	log.Printf("processGetChats: найдено %d чатов из %d всего", len(chats), total)

	// Отправляем ответ
	if err := client.SendJSON(response); err != nil {
		log.Printf("processGetChats: ошибка отправки ответа: %v", err)
	}
}

func processGetChatByID(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID string `json:"chatID"`
		Limit  int    `json:"limit"`
		Before string `json:"before"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для getChatByID")
		return
	}

	// Устанавливаем дефолтные значения
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 25
	}

	// Парсим ID чата
	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	// Получаем чат и его сообщения
	log.Printf("processGetChatByID: запрос чата ID=%s, limit=%d, before=%s",
		chatID, p.Limit, p.Before)

	chat, total, err := database.GetChatByID(chatID, p.Limit, p.Before)
	if err != nil {
		log.Printf("processGetChatByID: ошибка получения чата: %v", err)
		client.SendError("db_error", "Ошибка получения чата: "+err.Error())
		return
	}

	// Переводим сообщения для админа (lazy caching)
	if client.ClientType == "admin" && Translator != nil {
		adminIDStr, exists := ginCtx.Get("adminID")
		if exists {
			adminID, err := uuid.Parse(adminIDStr.(string))
			if err == nil {
				settings, err := queries.GetAdminSettings(database.DB, adminID)
				if err == nil && settings.PreferredLanguage != "" {
					log.Printf("processGetChatByID: перевод сообщений для админа %s (язык: %s)", adminID, settings.PreferredLanguage)
					err = Translator.TranslateMessagesForAdmin(ginCtx.Request.Context(), chat.Messages, settings.PreferredLanguage)
					if err != nil {
						log.Printf("processGetChatByID: ошибка перевода сообщений: %v", err)
					}
				}
			}
		}
	}

	// Отмечаем сообщения как прочитанные
	if client.ClientType == "admin" {
		if err := database.MarkMessagesAsRead(chatID); err != nil {
			log.Printf("processGetChatByID: ошибка маркировки сообщений: %v", err)
		}
	}

	// Формируем ответ
	response := map[string]interface{}{
		"type": "chatDetails",
		"payload": map[string]interface{}{
			"chat":    chat,
			"total":   total,
			"hasMore": len(chat.Messages) >= p.Limit,
		},
	}

	log.Printf("processGetChatByID: найден чат с %d сообщениями", len(chat.Messages))

	// Отправляем ответ
	if err := client.SendJSON(response); err != nil {
		log.Printf("processGetChatByID: ошибка отправки ответа: %v", err)
	}
}

func processMarkAsRead(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID string `json:"chatID"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для markAsRead")
		return
	}

	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	log.Printf("processMarkAsRead: отметка сообщений как прочитанных в чате %s", chatID)

	if err := database.MarkMessagesAsRead(chatID); err != nil {
		log.Printf("processMarkAsRead: ошибка: %v", err)
		client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
		return
	}

	// Отправляем обновление всем клиентам чата о прочтении сообщений
	statusMsg, _ := websocketpkg.NewMessage("messagesRead", map[string]interface{}{
		"chatID": chatID.String(),
		"readBy": client.ID.String(),
	})

	// Отправляем статус другим клиентам этого чата
	WebSocketHub.SendToChat(chatID.String(), statusMsg)

	log.Printf("processMarkAsRead: успешно обновлен статус сообщений в чате %s", chatID)

	// Отправляем подтверждение отправителю запроса
	response := map[string]interface{}{
		"type": "markAsReadConfirmed",
		"payload": map[string]interface{}{
			"chatID": chatID.String(),
			"status": "success",
		},
	}

	if err := client.SendJSON(response); err != nil {
		log.Printf("processMarkAsRead: ошибка отправки подтверждения: %v", err)
	}
}

// processMarkReadFromWidget обрабатывает пометку сообщений админа как прочитанных (от виджета/клиента)
func processMarkReadFromWidget(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID string `json:"chatId"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для mark_read")
		return
	}

	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	log.Printf("processMarkReadFromWidget: клиент пометил сообщения админа как прочитанные в чате %s", chatID)

	// Помечаем сообщения админа как прочитанные
	if err := queries.MarkAdminMessagesAsRead(database.DB, chatID); err != nil {
		log.Printf("processMarkReadFromWidget: ошибка: %v", err)
		client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
		return
	}

	// Отправляем уведомление админам о том, что клиент прочитал сообщения
	statusMsg, _ := websocketpkg.NewMessage("messages_read", map[string]interface{}{
		"chatId": chatID.String(),
	})

	// Отправляем статус админам, подключенным к этому чату
	WebSocketHub.SendToChatAndAdmins(chatID.String(), statusMsg)

	log.Printf("processMarkReadFromWidget: успешно обновлен статус сообщений в чате %s", chatID)
}

func processTypingStatus(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID   string `json:"chatID"`
		IsTyping bool   `json:"isTyping"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для typing")
		return
	}

	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	// Определяем тип отправителя
	sender := "admin"
	if client.ClientType == "widget" {
		sender = "user"
	}

	// Создаем и отправляем сообщение о наборе текста
	typingMsg, err := websocketpkg.NewTypingMessage(chatID, p.IsTyping, sender)
	if err != nil {
		log.Printf("processTypingStatus: ошибка формирования сообщения: %v", err)
		return
	}

	// Отправляем только клиентам этого чата
	WebSocketHub.SendToChat(chatID.String(), typingMsg)

	log.Printf("processTypingStatus: отправлен статус typing=%v для чата %s от %s",
		p.IsTyping, chatID, sender)
}

// processGetWidgetMessages - новый метод для получения сообщений виджета через WebSocket
func processGetWidgetMessages(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID string `json:"chatID"`
		Limit  int    `json:"limit"`
		Before string `json:"before"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для getWidgetMessages")
		return
	}

	// Устанавливаем дефолтные значения
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 50
	}

	// Парсим ID чата
	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	// Проверяем, принадлежит ли чат этому пользователю (только если у клиента есть ChatID)
	if client.ClientType == "widget" && client.ChatID != uuid.Nil && client.ChatID != chatID {
		client.SendError("access_denied", "Доступ к чату запрещен")
		return
	}

	// Получаем сообщения
	chat, total, err := database.GetChatByID(chatID, p.Limit, p.Before)
	if err != nil {
		log.Printf("processGetWidgetMessages: ошибка получения сообщений: %v", err)
		client.SendError("db_error", "Ошибка получения сообщений: "+err.Error())
		return
	}

	// Переводим сообщения админа для клиента (lazy caching)
	if Translator != nil {
		clientLang, err := database.GetClientLanguageFromChat(chatID)
		if err == nil && clientLang != "" {
			log.Printf("processGetWidgetMessages: перевод сообщений для клиента (язык: %s)", clientLang)
			err = Translator.TranslateMessagesForWidget(ginCtx.Request.Context(), chat.Messages, clientLang)
			if err != nil {
				log.Printf("processGetWidgetMessages: ошибка перевода сообщений: %v", err)
			}
		}
	}

	// Если чат архивирован, возвращаем пустой список сообщений
	if chat.IsArchived {
		log.Printf("processGetWidgetMessages: чат %s архивирован, возвращаем пустой список", chatID)
		response := map[string]interface{}{
			"type": "widgetMessages",
			"payload": map[string]interface{}{
				"messages": []interface{}{},
				"status":   "archived",
				"message":  "Этот чат был архивирован",
			},
		}
		if err := client.SendJSON(response); err != nil {
			log.Printf("processGetWidgetMessages: ошибка отправки ответа: %v", err)
		}
		return
	}

	// Преобразуем сообщения в формат для виджета
	simplifiedMessages := make([]map[string]interface{}, 0, len(chat.Messages))
	for _, msg := range chat.Messages {
		simplifiedMessages = append(simplifiedMessages, map[string]interface{}{
			"id":        msg.ID.String(),
			"content":   msg.Content,
			"sender":    msg.Sender,
			"timestamp": msg.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			"type":      msg.Type,
		})
	}

	// Формируем ответ
	response := map[string]interface{}{
		"type": "widgetMessages",
		"payload": map[string]interface{}{
			"messages": simplifiedMessages,
			"total":    total,
			"hasMore":  len(chat.Messages) >= p.Limit,
			"chatId":   chat.ID.String(),
			"userId":   chat.User.ID.String(),
		},
	}

	log.Printf("processGetWidgetMessages: найдено %d сообщений", len(simplifiedMessages))

	// Отправляем ответ
	if err := client.SendJSON(response); err != nil {
		log.Printf("processGetWidgetMessages: ошибка отправки ответа: %v", err)
	}
}
