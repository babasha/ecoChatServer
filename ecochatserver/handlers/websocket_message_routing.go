package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
)

// mergeMetadata сливает src в dst (создавая dst при необходимости) и возвращает результат.
// Пустой src возвращает dst без изменений.
func mergeMetadata(dst, src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]interface{})
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// applyAndPersistTranslation сливает метаданные перевода в message (in-memory,
// чтобы немедленная рассылка получателям использовала перевод) и персистит
// detectedLanguage + переводы в БД (чтобы история и будущие определения языка
// собеседника видели их). Пустой meta — no-op.
func applyAndPersistTranslation(message *models.Message, meta map[string]interface{}) {
	if message == nil || len(meta) == 0 {
		return
	}
	message.Metadata = mergeMetadata(message.Metadata, meta)

	if dl, ok := meta["detectedLanguage"].(string); ok && dl != "" {
		if err := database.SaveDetectedLanguage(message.ID, dl); err != nil {
			log.Printf("applyAndPersistTranslation: SaveDetectedLanguage: %v", err)
		}
	}
	if translations, ok := meta["translations"].(map[string]interface{}); ok {
		for lang, v := range translations {
			text, ok := v.(string)
			if !ok || text == "" {
				continue
			}
			if err := database.SaveTranslation(message.ID, lang, text); err != nil {
				log.Printf("applyAndPersistTranslation: SaveTranslation[%s]: %v", lang, err)
			}
		}
	}
}

// deliverAdminMessage выполняет ОТЛОЖЕННУЮ доставку admin-сообщения: перевод на
// язык клиента (LLM), отправку во внешние каналы и рассылку участникам/админам.
// Запускается в отдельной горутине, поэтому LLM-перевод не блокирует ни
// подтверждение отправителю, ни обработку следующих сообщений в ReadPump.
func deliverAdminMessage(lightChat *models.Chat, chatID uuid.UUID, message *models.Message, chatIsMoooving bool, adminID uuid.UUID) {
	if Translator != nil && !chatIsMoooving {
		tctx, tcancel := newWSContext()
		result, err := Translator.TranslateAdminMessage(tctx, message.Content, chatID, adminID)
		tcancel()
		if err != nil {
			log.Printf("deliverAdminMessage: ошибка перевода: %v", err)
		} else if result != nil && result.WasTranslated {
			applyAndPersistTranslation(message, result.Metadata)
			log.Printf("deliverAdminMessage: сообщение админа переведено, detectedLang=%s", result.DetectedLanguage)
		}
	}

	// Внешние каналы (Instagram/WhatsApp). dispatchExternalMessage сам фильтрует
	// по source чата, поэтому для moooving-чата это no-op.
	go dispatchExternalMessage(chatID, message)

	// Ответ админа снимает состояние эскалации автоответчика.
	if AutoResponder != nil {
		AutoResponder.ClearEscalation(chatID.String())
		log.Printf("deliverAdminMessage: очищена эскалация для чата %s (ответ админа)", chatID)
	}

	routeWidgetOrAdminMessage(lightChat, chatID, message, "admin")
}

// deliverMooovingMessage выполняет отложенную доставку moooving-сообщения:
// двусторонний перевод (driver↔client) и персонализированную рассылку с push.
// Запускается в горутине — LLM-перевод не блокирует ReadPump/подтверждение.
func deliverMooovingMessage(lightChat *models.Chat, chatID uuid.UUID, message *models.Message, sender string) {
	if Translator != nil {
		tctx, tcancel := newWSContext()
		result, terr := Translator.TranslateMooovingMessage(tctx, message.Content, chatID, sender)
		tcancel()
		if terr != nil {
			log.Printf("deliverMooovingMessage: ошибка перевода: %v", terr)
		} else if result != nil && len(result.Metadata) > 0 {
			applyAndPersistTranslation(message, result.Metadata)
		}
	}
	routeMooovingMessage(lightChat, chatID, message, sender)
}

// runWidgetAutoResponder асинхронно обрабатывает сообщение виджет-клиента через
// автоответчик, сохраняет ответ бота и рассылает уведомление.
//
// ВНИМАНИЕ: это намеренно НЕ то же самое, что runAutoResponder() — здесь нет
// gate по chat.AutoResponderEnabled, обработки 503 и эскалации. Унификация с
// runAutoResponder возможна, но изменит поведение WS-виджета (добавит эскалацию
// и 503-fallback), поэтому требует продуктового подтверждения и теста.
func runWidgetAutoResponder(lightChat *models.Chat, chatID uuid.UUID, userMsg *models.Message) {
	if lightChat == nil {
		log.Printf("runWidgetAutoResponder: чат %s не загружен, пропускаем автоответчик", chatID)
		return
	}

	// Фоновый контекст: горутина переживает HTTP-запрос, а сам AutoResponder
	// внутри ProcessMessage применяет собственный таймаут.
	botMsg, err := AutoResponder.ProcessMessage(context.Background(), lightChat, userMsg)
	if err != nil {
		log.Printf("runWidgetAutoResponder: ошибка автоответчика: %v", err)
		return
	}
	if botMsg == nil {
		return
	}

	saved, err := database.AddMessage(chatID, botMsg.Content, botMsg.Sender, botMsg.SenderID, botMsg.Type, botMsg.Metadata)
	if err != nil {
		log.Printf("runWidgetAutoResponder: ошибка сохранения автоответа: %v", err)
		return
	}

	go dispatchExternalMessage(chatID, saved)
	WebSocketHub.IncrementChatMessage()
	// Уведомляем только об ответе бота (сообщение пользователя уже доставлено через widget WS).
	notifyNewMessages(lightChat, nil, saved)
}

// routeMooovingMessage маршрутизирует сообщение в moooving-чате между driver и
// mo_client: каждой стороне отдаётся перевод на её язык (если известен), а если
// получатель оффлайн — отправляется web push. Админы видят оригинал.
func routeMooovingMessage(lightChat *models.Chat, chatID uuid.UUID, message *models.Message, sender string) {
	if lightChat == nil {
		lightChat = &models.Chat{ID: chatID, Messages: []models.Message{*message}}
	} else {
		lightChat.Messages = append(lightChat.Messages, *message)
	}
	chatInfo := createChatInfo(lightChat)

	// Получатель противоположной стороны. Если он НЕ подключён по WS — push.
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
		// admin пишет в moooving чат как наблюдатель — отправим обоим оригинал.
		notif := createMessageNotification(chatID, message, chatInfo)
		WebSocketHub.SendToDriverInChat(chatID.String(), notif)
		WebSocketHub.SendToMoClientInChat(chatID.String(), notif)
	}

	if !deliveredViaWS && pushTargetSource != "" && pushTargetExtID != nil {
		sendMooovingOfflinePush(chatID, message, lightChat, pushTargetSource, *pushTargetExtID, recipientLang)
	}

	// Админы видят оригинал.
	WebSocketHub.SendToAllAdmins(createMessageNotification(chatID, message, chatInfo))
}

// sendMooovingOfflinePush отправляет web push получателю moooving-сообщения,
// который не подключён по WebSocket.
func sendMooovingOfflinePush(chatID uuid.UUID, message *models.Message, lightChat *models.Chat, source string, extID int64, recipientLang string) {
	// Body учитывает перевод: получатель видит на своём языке, иначе оригинал.
	pushBody := message.Content
	if recipientLang != "" {
		if translated, ok := getTranslation(message.Metadata, recipientLang); ok {
			pushBody = translated
		}
	}
	// push-сервисы (FCM/Mozilla) ограничивают payload ~4KB.
	if len(pushBody) > 200 {
		pushBody = pushBody[:200] + "…"
	}

	titleKey := "Mensagem do motorista"
	if source == database.MooovingDriverSource {
		titleKey = "Mensagem do cliente"
	}

	data := map[string]interface{}{"chatId": chatID.String()}
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

// routeWidgetOrAdminMessage рассылает сообщение в обычном (не moooving) чате:
// виджету — перевод на язык клиента (для admin-сообщений), админам —
// персонализированный перевод для user-сообщений и оригинал иначе.
func routeWidgetOrAdminMessage(lightChat *models.Chat, chatID uuid.UUID, message *models.Message, sender string) {
	// Виджету админские сообщения уходят переведёнными на язык клиента.
	widgetMessage := message
	if sender == "admin" {
		clientLang, _ := database.GetClientLanguageFromChat(chatID)
		widgetMessage = applyTranslationForWidget(message, chatID, clientLang)
	}

	if lightChat == nil {
		lightChat = &models.Chat{ID: chatID, Messages: []models.Message{*widgetMessage}}
	} else {
		lightChat.Messages = append(lightChat.Messages, *widgetMessage)
	}
	chatInfo := createChatInfo(lightChat)

	// Все участники чата (виджет) — через SendToChat.
	WebSocketHub.SendToChat(chatID.String(), createMessageNotification(chatID, widgetMessage, chatInfo))

	// Админам — персонализированный перевод для user-сообщений, оригинал иначе.
	if sender == "user" {
		broadcastToAdminsPersonalized(chatID, message, chatInfo)
	} else {
		WebSocketHub.SendToAllAdmins(createMessageNotification(chatID, message, chatInfo))
	}
}
