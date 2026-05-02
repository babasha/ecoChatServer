package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// processSendMessage обрабатывает отправку сообщений с автоответчиком
func processSendMessage(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID    string                 `json:"chatId"`
		ChatIDOld string                 `json:"chatID"` // обратная совместимость
		Content   string                 `json:"content"`
		Type      string                 `json:"type"`
		Metadata  map[string]interface{} `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для sendMessage")
		return
	}

	// Обратная совместимость: поддерживаем и chatId, и chatID
	if p.ChatID == "" && p.ChatIDOld != "" {
		p.ChatID = p.ChatIDOld
	}

	// Проверяем обязательные поля
	if p.ChatID == "" || p.Content == "" {
		client.SendError("missing_fields", "Необходимы поля chatId и content")
		return
	}

	// Проверяем размер сообщения в зависимости от типа клиента
	var maxLength int
	switch client.ClientType {
	case "admin":
		maxLength = 2000 // Админ может писать более развернуто
	case websocketpkg.ClientTypeDriver, websocketpkg.ClientTypeMoClient:
		maxLength = 1500
	default:
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

	// SECURITY: для авторизованных подключений к конкретному чату (driver, mo_client)
	// chatID из payload должен совпадать с chatID, к которому привязано WS-соединение.
	// Иначе одно подключение могло бы писать в любой чат.
	if (client.ClientType == websocketpkg.ClientTypeDriver ||
		client.ClientType == websocketpkg.ClientTypeMoClient) &&
		client.ChatID != uuid.Nil && client.ChatID != chatID {
		log.Printf("processSendMessage[security]: %s попытался писать в чат %s (его чат: %s)",
			client.ClientType, chatID, client.ChatID)
		client.SendError("forbidden", "Это не ваш чат")
		return
	}

	// Определяем отправителя в зависимости от типа клиента
	var senderID uuid.UUID
	var sender string

	// moooving-чаты не используют translator/autoresponder/external dispatch
	isMoooving := client.ClientType == websocketpkg.ClientTypeDriver ||
		client.ClientType == websocketpkg.ClientTypeMoClient

	switch client.ClientType {
	case "admin":
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

		// Переводим сообщение админа на язык клиента (не для moooving — там фикс. язык)
		if Translator != nil && !isMooovingChat(chatID) {
			log.Printf("processSendMessage: попытка перевода сообщения админа")
			result, err := Translator.TranslateAdminMessage(ginCtx.Request.Context(), p.Content, chatID, adminID)
			if err != nil {
				log.Printf("processSendMessage: ошибка перевода: %v", err)
			} else if result != nil && result.WasTranslated {
				// Копируем metadata из результата (уже содержит detectedLanguage + translations)
				if p.Metadata == nil {
					p.Metadata = make(map[string]interface{})
				}
				for k, v := range result.Metadata {
					p.Metadata[k] = v
				}
				log.Printf("processSendMessage: сообщение админа переведено, detectedLang=%s",
					result.DetectedLanguage)
			}
		}
	case websocketpkg.ClientTypeDriver:
		// moooving: водитель пишет клиенту по заказу
		senderID = client.UserID
		sender = "driver"
	case websocketpkg.ClientTypeMoClient:
		// moooving: авторизованный клиент пишет водителю по заказу
		senderID = client.UserID
		sender = "user"
	default:
		// Виджет (анонимный клиент) — старый flow
		senderID = client.UserID
		sender = "user"
	}

	// Перевод для moooving (двусторонний): driver↔client. detectedLanguage отправителя
	// сохраняется всегда; translations[receiverLang] заполняется когда язык получателя
	// уже известен из его прошлых сообщений.
	if isMoooving && Translator != nil {
		result, terr := Translator.TranslateMooovingMessage(ginCtx.Request.Context(), p.Content, chatID, sender)
		if terr != nil {
			log.Printf("processSendMessage[moooving]: ошибка перевода: %v", terr)
		} else if result != nil && len(result.Metadata) > 0 {
			if p.Metadata == nil {
				p.Metadata = make(map[string]interface{})
			}
			for k, v := range result.Metadata {
				p.Metadata[k] = v
			}
		}
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

	// Увеличиваем счетчик реальных сообщений чата
	WebSocketHub.IncrementChatMessage()

	// Отправляем сообщение во внешние каналы (например, Instagram) для админов.
	// Для moooving-чатов внешних каналов нет.
	if sender == "admin" && !isMoooving {
		go dispatchExternalMessage(chatID, message)
	}

	// Если это сообщение от админа, очищаем состояние эскалации
	if sender == "admin" && AutoResponder != nil && !isMoooving {
		AutoResponder.ClearEscalation(chatID.String())
		log.Printf("processSendMessage: очищена эскалация для чата %s (ответ админа)", chatID)
	}

	// ОБРАБОТКА АВТООТВЕТЧИКА (только для виджет-чатов, не для moooving)
	if sender == "user" && AutoResponder != nil && !isMoooving {
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
					go dispatchExternalMessage(chatID, botMsg)

					// Увеличиваем счетчик реальных сообщений чата (включая бота)
					WebSocketHub.IncrementChatMessage()

					// Notify only about bot response (user message was already notified via widget WebSocket)
					notifyNewMessages(lightChat, nil, botMsg)
				}
			}
		}()
	} else if isMoooving {
		// moooving: персонализированная маршрутизация driver ↔ mo_client.
		// Каждой стороне отдаём перевод на её язык (если язык известен), иначе оригинал.
		lightChat, err := queries.GetChatLightweight(database.DB, chatID)
		if err != nil {
			log.Printf("processSendMessage[moooving]: ошибка загрузки чата: %v", err)
			lightChat = &models.Chat{ID: chatID, Messages: []models.Message{*message}}
		} else {
			lightChat.Messages = append(lightChat.Messages, *message)
		}
		chatInfo := createChatInfo(lightChat)

		// Получатель противоположной стороны.
		// Если получатель НЕ подключён по WS — кидаем web push (если у него есть подписка).
		var recipientLang string
		var pushTargetSource string
		var pushTargetExtID *int64
		var deliveredViaWS bool
		switch sender {
		case "driver":
			recipientLang, _ = database.GetClientLanguageFromChat(chatID)
			recipientLang = strings.TrimSpace(recipientLang)
			recipientMessage := message
			if recipientLang != "" {
				recipientMessage = applyTranslationForWidget(message, chatID, recipientLang)
			}
			notif := createMessageNotification(chatID, recipientMessage, chatInfo)
			deliveredViaWS = WebSocketHub.SendToMoClientInChat(chatID.String(), notif)
			pushTargetSource = database.MooovingClientSource
			pushTargetExtID = lightChat.ClientIDExt
		case "user":
			recipientLang, _ = database.GetDetectedLanguageBySender(chatID, "driver")
			recipientLang = strings.TrimSpace(recipientLang)
			recipientMessage := message
			if recipientLang != "" {
				recipientMessage = applyTranslationForWidget(message, chatID, recipientLang)
			}
			notif := createMessageNotification(chatID, recipientMessage, chatInfo)
			deliveredViaWS = WebSocketHub.SendToDriverInChat(chatID.String(), notif)
			pushTargetSource = database.MooovingDriverSource
			pushTargetExtID = lightChat.DriverIDExt
		default:
			// admin пишет в moooving чат как наблюдатель — отправим обоим оригинал
			notif := createMessageNotification(chatID, message, chatInfo)
			WebSocketHub.SendToDriverInChat(chatID.String(), notif)
			WebSocketHub.SendToMoClientInChat(chatID.String(), notif)
		}

		if !deliveredViaWS && pushTargetSource != "" && pushTargetExtID != nil {
			// Получатель оффлайн — кидаем push в фоне.
			extID := *pushTargetExtID
			source := pushTargetSource

			// Title/Body учитывают перевод: получатель видит на своём языке.
			// Если перевода нет (язык неизвестен) — оригинал.
			var pushBody string
			if recipientLang != "" {
				if translated, ok := getTranslation(message.Metadata, recipientLang); ok {
					pushBody = translated
				}
			}
			if pushBody == "" {
				pushBody = message.Content
			}
			// Ограничиваем длину — push-сервисы (FCM/Mozilla) ограничивают payload ~4KB
			if len(pushBody) > 200 {
				pushBody = pushBody[:200] + "…"
			}

			titleKey := "Mensagem do motorista"
			if source == database.MooovingDriverSource {
				titleKey = "Mensagem do cliente"
			}

			data := map[string]interface{}{
				"chatId": chatID.String(),
			}
			if lightChat.OrderID != nil {
				data["orderId"] = *lightChat.OrderID
				data["url"] = "/?orderId=" + fmt.Sprint(*lightChat.OrderID)
			}

			payload := &PushPayload{
				Title: titleKey,
				Body:  pushBody,
				Tag:   "moooving-chat-" + chatID.String(),
				Data:  data,
			}

			go func() {
				subs, err := database.ListMooovingPushSubscriptions(source, extID)
				if err != nil || len(subs) == 0 {
					return
				}
				SendMooovingPushToSubscriptions(subs, payload)
			}()
		}

		// Админы видят оригинал
		adminNotification := createMessageNotification(chatID, message, chatInfo)
		WebSocketHub.SendToAllAdmins(adminNotification)
	} else {
		// Для админских сообщений нужно отправить перевод виджету (не для moooving)
		widgetMessage := message
		if sender == "admin" {
			clientLang, _ := database.GetClientLanguageFromChat(chatID)
			widgetMessage = applyTranslationForWidget(message, chatID, clientLang)
		}

		// Загружаем легковесную версию чата для уведомления
		lightChat, err := queries.GetChatLightweight(database.DB, chatID)
		if err != nil {
			log.Printf("processSendMessage: ошибка загрузки чата: %v", err)
			// Создаем минимальный объект чата
			lightChat = &models.Chat{
				ID:       chatID,
				Messages: []models.Message{*widgetMessage},
			}
		} else {
			// Добавляем текущее сообщение к списку
			lightChat.Messages = append(lightChat.Messages, *widgetMessage)
		}

		chatInfo := createChatInfo(lightChat)

		// Все участники чата (виджет) — через SendToChat
		widgetNotification := createMessageNotification(chatID, widgetMessage, chatInfo)
		WebSocketHub.SendToChat(chatID.String(), widgetNotification)

		// Админам — персонализированный перевод для user-сообщений, оригинал иначе.
		if sender == "user" {
			broadcastToAdminsPersonalized(chatID, message, chatInfo)
		} else {
			adminNotification := createMessageNotification(chatID, message, chatInfo)
			WebSocketHub.SendToAllAdmins(adminNotification)
		}
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

// processGetChats получает список чатов с пагинацией
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

// processGetChatByID получает конкретный чат по ID
func processGetChatByID(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID    string `json:"chatId"`
		ChatIDOld string `json:"chatID"` // обратная совместимость
		Limit     int    `json:"limit"`
		Before    string `json:"before"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для getChatByID")
		return
	}

	if p.ChatID == "" && p.ChatIDOld != "" {
		p.ChatID = p.ChatIDOld
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

	if !enforceMooovingChatScope(client, chatID) {
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
				adminLang := getAdminLanguage(adminID)
				if adminLang != "" {
					log.Printf("processGetChatByID: перевод сообщений для админа %s (язык: %s)", adminID, adminLang)
					err = Translator.TranslateMessagesForAdmin(ginCtx.Request.Context(), chat.Messages, adminLang)
					if err != nil {
						log.Printf("processGetChatByID: ошибка перевода сообщений: %v", err)
					}
				}
			}
		}
	}

	// Перевод истории для moooving driver / mo_client
	if Translator != nil {
		var viewerLang string
		switch client.ClientType {
		case websocketpkg.ClientTypeDriver:
			viewerLang, _ = database.GetDetectedLanguageBySender(chatID, "driver")
		case websocketpkg.ClientTypeMoClient:
			viewerLang, _ = database.GetClientLanguageFromChat(chatID)
		}
		viewerLang = strings.TrimSpace(viewerLang)
		if viewerLang != "" {
			log.Printf("processGetChatByID[moooving]: перевод истории на %s для %s", viewerLang, client.ClientType)
			if err := Translator.TranslateMessagesForWidget(ginCtx.Request.Context(), chat.Messages, viewerLang); err != nil {
				log.Printf("processGetChatByID[moooving]: ошибка перевода истории: %v", err)
			}
		}
	}

	// НЕ отмечаем сообщения автоматически - админ сам отметит видимые через markAsRead
	// Это позволяет помечать только реально просмотренные сообщения

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

// processTypingStatus обрабатывает статус набора текста
func processTypingStatus(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID    string `json:"chatId"`
		ChatIDOld string `json:"chatID"` // обратная совместимость
		IsTyping  bool   `json:"isTyping"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для typing")
		return
	}

	if p.ChatID == "" && p.ChatIDOld != "" {
		p.ChatID = p.ChatIDOld
	}

	chatID, err := uuid.Parse(p.ChatID)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный формат chatID")
		return
	}

	if !enforceMooovingChatScope(client, chatID) {
		return
	}

	// Определяем тип отправителя
	sender := "admin"
	switch client.ClientType {
	case "widget", websocketpkg.ClientTypeMoClient:
		sender = "user"
	case websocketpkg.ClientTypeDriver:
		sender = "driver"
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

// processGetWidgetMessages получает сообщения виджета через WebSocket
func processGetWidgetMessages(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
	var p struct {
		ChatID    string `json:"chatId"`
		ChatIDOld string `json:"chatID"` // обратная совместимость
		Limit     int    `json:"limit"`
		Before    string `json:"before"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		client.SendError("invalid_payload", "Некорректный формат данных для getWidgetMessages")
		return
	}

	if p.ChatID == "" && p.ChatIDOld != "" {
		p.ChatID = p.ChatIDOld
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
		m := map[string]interface{}{
			"id":        msg.ID.String(),
			"content":   msg.Content,
			"sender":    msg.Sender,
			"timestamp": msg.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
			"type":      msg.Type,
			"read":      msg.Read,
		}
		if msg.Metadata != nil {
			m["metadata"] = msg.Metadata
		}
		simplifiedMessages = append(simplifiedMessages, m)
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
