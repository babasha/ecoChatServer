package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
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

	// SECURITY: соединение, привязанное к конкретному чату (driver, mo_client, widget),
	// может писать только в свой чат. admin/непривязанные — без ограничений.
	if !enforceChatScope(client, chatID) {
		return
	}

	// Загружаем чат ОДИН раз и переиспользуем для определения типа чата и для
	// маршрутизации уведомлений (раньше admin-сообщение грузило чат дважды —
	// при проверке типа чата и повторно в route-функции).
	lightChat, lightErr := database.GetChatLightweight(chatID)
	if lightErr != nil {
		// Не фатально (рассылка переживёт nil-чат), но без него отключаются
		// автоответчик/push/определение типа чата — логируем явно.
		log.Printf("processSendMessage: GetChatLightweight(%s) ошибка: %v", chatID, lightErr)
	}
	chatIsMoooving := lightChat != nil && lightChat.Source == database.MooovingChatSource

	// Определяем отправителя в зависимости от типа клиента
	var senderID uuid.UUID
	var sender string
	var adminID uuid.UUID

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
		parsed, perr := uuid.Parse(adminIDStr.(string))
		if perr != nil {
			client.SendError("invalid_uuid", "Некорректный adminID")
			return
		}
		adminID = parsed
		senderID = parsed
		sender = "admin"
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

	// Сохраняем сообщение с ОРИГИНАЛЬНЫМ content немедленно. Перевод (LLM, до 30с)
	// больше не на критическом пути: он выполняется асинхронно в deliver*-функциях
	// ниже и дописывается в metadata.translations через SaveTranslation. До этого
	// история отдаётся с ленивым переводом при чтении — корректность сохраняется.
	log.Printf("processSendMessage: добавление сообщения в чат %s от %s (%s)", chatID, sender, senderID)

	message, err := database.AddMessage(chatID, p.Content, sender, senderID, p.Type, p.Metadata)
	if err != nil {
		log.Printf("processSendMessage: ошибка добавления сообщения: %v", err)
		client.SendError("db_error", "Ошибка сохранения сообщения")
		return
	}

	// Увеличиваем счетчик реальных сообщений чата
	WebSocketHub.IncrementChatMessage()

	// Подтверждение отправителю — СРАЗУ, не дожидаясь перевода и рассылки.
	confirmation := map[string]interface{}{
		"type": "messageSent",
		"payload": map[string]interface{}{
			"messageID": message.ID.String(),
			"timestamp": message.Timestamp,
			"status":    "delivered",
		},
	}
	if err := client.SendJSON(confirmation); err != nil {
		log.Printf("processSendMessage: ошибка отправки подтверждения: %v", err)
	}

	// Доставка получателям (перевод + внешние каналы + рассылка) — асинхронно,
	// чтобы LLM-перевод не блокировал ReadPump. Порядок у получателя держится по
	// timestamp в payload; DB-порядок гарантирован синхронным AddMessage.
	switch {
	case sender == "user" && AutoResponder != nil && !isMoooving:
		// Виджет-чат с автоответчиком: ответ бота обрабатывается асинхронно.
		go runWidgetAutoResponder(lightChat, chatID, message)
	case isMoooving:
		// moooving: двусторонний перевод + персонализированная маршрутизация + push.
		go deliverMooovingMessage(lightChat, chatID, message, sender)
	case sender == "admin":
		// admin: перевод на язык клиента + внешние каналы + рассылка.
		go deliverAdminMessage(lightChat, chatID, message, chatIsMoooving, adminID)
	default:
		// Виджет-user без автоответчика.
		go routeWidgetOrAdminMessage(lightChat, chatID, message, sender)
	}

	log.Printf("processSendMessage: сообщение принято (ID=%s)", message.ID)
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

	// Получаем ID из контекста. type assertion делаем безопасно (с ok), иначе
	// соединение без admin-контекста (не должно сюда попадать после whitelist,
	// но защищаемся) вызвало бы panic на nil.(string).
	adminIDRaw, okAdmin := ginCtx.Get("adminID")
	clientIDRaw, okClient := ginCtx.Get("clientID")
	adminIDStr, _ := adminIDRaw.(string)
	clientIDStr, _ := clientIDRaw.(string)
	if !okAdmin || !okClient {
		client.SendError("auth_error", "Команда доступна только администратору")
		return
	}

	adminID, err := uuid.Parse(adminIDStr)
	if err != nil {
		client.SendError("invalid_uuid", "Некорректный adminID")
		return
	}

	clientID, err := uuid.Parse(clientIDStr)
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

	// Устанавливаем дефолтные значения
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 25
	}

	// Парсим ID чата (с обратной совместимостью chatId/chatID)
	chatID, ok := resolveChatID(client, p.ChatID, p.ChatIDOld)
	if !ok {
		return
	}

	if !enforceChatScope(client, chatID) {
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
					tctx, tcancel := newWSContext()
					err = Translator.TranslateMessagesForAdmin(tctx, chat.Messages, adminLang)
					tcancel()
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
			tctx, tcancel := newWSContext()
			terr := Translator.TranslateMessagesForWidget(tctx, chat.Messages, viewerLang)
			tcancel()
			if terr != nil {
				log.Printf("processGetChatByID[moooving]: ошибка перевода истории: %v", terr)
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

	chatID, ok := resolveChatID(client, p.ChatID, p.ChatIDOld)
	if !ok {
		return
	}

	if !enforceChatScope(client, chatID) {
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

	// Устанавливаем дефолтные значения
	if p.Limit < 1 || p.Limit > 100 {
		p.Limit = 50
	}

	// Парсим ID чата (с обратной совместимостью chatId/chatID)
	chatID, ok := resolveChatID(client, p.ChatID, p.ChatIDOld)
	if !ok {
		return
	}

	// Проверяем, принадлежит ли чат этому пользователю (admin/непривязанные — без ограничений)
	if !enforceChatScope(client, chatID) {
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
			tctx, tcancel := newWSContext()
			err = Translator.TranslateMessagesForWidget(tctx, chat.Messages, clientLang)
			tcancel()
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
