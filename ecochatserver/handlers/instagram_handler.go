package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
)

const (
	instagramSource             = "instagram"
	defaultInstagramClientKey   = "instagram_default_client"
	instagramSignatureHeader    = "X-Hub-Signature-256"
	instagramMessagingProduct   = "instagram"
	instagramVerifyTokenSetting = "INSTAGRAM_VERIFY_TOKEN"
	instagramAppSecretSetting   = "INSTAGRAM_APP_SECRET"
	instagramClientKeySetting   = "INSTAGRAM_CLIENT_API_KEY"
	instagramBusinessIDSetting  = "INSTAGRAM_BUSINESS_ACCOUNT_ID"
	instagramAccessTokenSetting = "INSTAGRAM_ACCESS_TOKEN"
	instagramAPIVersionSetting  = "INSTAGRAM_API_VERSION"
	defaultInstagramAPIVersion  = "v18.0"
)

var instagramHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// instagramConversation содержит информацию о диалоге
type instagramConversation struct {
	ID           string `json:"id"`
	Participants struct {
		Data []struct {
			ID       string `json:"id"`
			Username string `json:"username,omitempty"`
			Name     string `json:"name,omitempty"`
		} `json:"data"`
	} `json:"participants"`
}

// instagramConversationsResponse содержит список диалогов
type instagramConversationsResponse struct {
	Data []instagramConversation `json:"data"`
}

// fetchInstagramConversations получает список conversations для бизнес-аккаунта
func fetchInstagramConversations(businessAccountID string) ([]instagramConversation, error) {
	accessToken := database.GetSetting(instagramAccessTokenSetting, "")
	if accessToken == "" {
		return nil, fmt.Errorf("instagram access token not configured")
	}

	apiVersion := database.GetSetting(instagramAPIVersionSetting, defaultInstagramAPIVersion)

	// Пробуем разные endpoints, т.к. в dev mode может быть ограничение
	endpoints := []string{
		fmt.Sprintf("https://graph.facebook.com/%s/%s?fields=id,username,name&access_token=%s", apiVersion, businessAccountID, accessToken),
		fmt.Sprintf("https://graph.facebook.com/%s/me?access_token=%s", apiVersion, accessToken),
	}

	for _, url := range endpoints {
		log.Printf("fetchInstagramConversations: trying %s", truncateForLog(url, 120))

		resp, err := instagramHTTPClient.Get(url)
		if err != nil {
			log.Printf("fetchInstagramConversations: HTTP error: %v", err)
			continue
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		log.Printf("fetchInstagramConversations: status=%d, response=%s", resp.StatusCode, truncateForLog(string(bodyBytes), 500))

		if resp.StatusCode == http.StatusOK {
			log.Printf("fetchInstagramConversations: SUCCESS with endpoint %s", url)
			break
		}
	}

	// Основной запрос к conversations
	url := fmt.Sprintf("https://graph.facebook.com/%s/%s/conversations?platform=instagram&fields=id,participants{id,username,name}&limit=10&access_token=%s",
		apiVersion, businessAccountID, accessToken)

	log.Printf("fetchInstagramConversations: fetching conversations from %s", truncateForLog(url, 120))

	resp, err := instagramHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch conversations: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Printf("fetchInstagramConversations: status=%d, response=%s", resp.StatusCode, truncateForLog(string(bodyBytes), 800))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("instagram API returned status %d", resp.StatusCode)
	}

	var response instagramConversationsResponse
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return nil, fmt.Errorf("failed to decode conversations: %w", err)
	}

	return response.Data, nil
}

// fetchMessageText получает текст сообщения по MID через Graph API
func fetchMessageText(messageID string) (string, error) {
	accessToken := database.GetSetting(instagramAccessTokenSetting, "")
	if accessToken == "" {
		return "", fmt.Errorf("instagram access token not configured")
	}

	apiVersion := database.GetSetting(instagramAPIVersionSetting, defaultInstagramAPIVersion)
	url := fmt.Sprintf("https://graph.facebook.com/%s/%s?fields=message&access_token=%s",
		apiVersion, messageID, accessToken)

	log.Printf("fetchMessageText: fetching from %s", truncateForLog(url, 120))

	resp, err := instagramHTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch message: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Printf("fetchMessageText: status=%d, response=%s", resp.StatusCode, truncateForLog(string(bodyBytes), 500))

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("instagram API returned status %d", resp.StatusCode)
	}

	var messageData struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(bodyBytes, &messageData); err != nil {
		return "", fmt.Errorf("failed to decode message: %w", err)
	}

	return messageData.Message, nil
}

// getSenderFromConversations пытается найти отправителя среди недавних conversations
func getSenderFromConversations(businessAccountID string) (string, string, error) {
	conversations, err := fetchInstagramConversations(businessAccountID)
	if err != nil {
		return "", "", err
	}

	log.Printf("getSenderFromConversations: found %d conversations", len(conversations))

	// Берем первый conversation (самый свежий) и ищем участника, который не является business account
	for _, conv := range conversations {
		for _, participant := range conv.Participants.Data {
			if participant.ID != businessAccountID {
				log.Printf("getSenderFromConversations: found sender %s (username=%s)", participant.ID, participant.Username)
				return participant.ID, participant.Username, nil
			}
		}
	}

	return "", "", fmt.Errorf("sender not found in recent conversations")
}

// InstagramWebhookVerify обрабатывает GET-запрос для подтверждения вебхука
func InstagramWebhookVerify(c *gin.Context) {
	mode := c.Query("hub.mode")
	challenge := c.Query("hub.challenge")
	verifyToken := c.Query("hub.verify_token")

	expectedToken := database.GetSetting(instagramVerifyTokenSetting, "")
	if expectedToken == "" {
		log.Printf("InstagramWebhookVerify: verify token не настроен, отклоняем запрос")
		c.String(http.StatusForbidden, "verify token not configured")
		return
	}

	if mode == "subscribe" && verifyToken == expectedToken {
		log.Printf("InstagramWebhookVerify: верификация успешна, отправляем challenge")
		c.String(http.StatusOK, challenge)
		return
	}

	log.Printf("InstagramWebhookVerify: неверный verify token (mode=%s)", mode)
	c.String(http.StatusForbidden, "verification failed")
}

// InstagramWebhook обрабатывает POST-запросы от Instagram
func InstagramWebhook(c *gin.Context) {
	log.Printf("InstagramWebhook: %s %s from %s", c.Request.Method, c.FullPath(), c.ClientIP())

	if c.Request.Method != http.MethodPost {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		return
	}

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("InstagramWebhook: ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read request body"})
		return
	}
	defer func() {
		// Восстанавливаем тело запроса для отладки, если понадобится
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}()

	if !verifyInstagramSignature(bodyBytes, c.GetHeader(instagramSignatureHeader)) {
		log.Printf("InstagramWebhook: проверка подписи не пройдена")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	log.Printf("InstagramWebhook: raw payload=%s", truncateForLog(string(bodyBytes), 1000))

	var payload instagramWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("InstagramWebhook: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}

	if strings.ToLower(payload.Object) != instagramSource {
		log.Printf("InstagramWebhook: неизвестный объект %s", payload.Object)
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	log.Printf("InstagramWebhook: payload.Object=%s, entries=%d", payload.Object, len(payload.Entry))

	processed := 0
	var processedDetails []gin.H
	for _, entry := range payload.Entry {
		log.Printf("InstagramWebhook: entry.ID=%s, changes=%d, messaging=%d", entry.ID, len(entry.Changes), len(entry.Messaging))

		// Обработка Direct Messages (формат messaging)
		for _, msg := range entry.Messaging {
			// ВРЕМЕННО: обрабатываем message_edit как обычное сообщение в dev режиме
			// TODO: Удалить после публикации приложения
			if msg.Message == nil && msg.MessageEdit != nil {
				numEdit := 0
				if msg.MessageEdit.NumEdit != "" {
					if val, err := msg.MessageEdit.NumEdit.Int64(); err == nil {
						numEdit = int(val)
					}
				}

				// Только num_edit=0 (новые сообщения), игнорируем реальные редактирования
				if numEdit == 0 {
					log.Printf("InstagramWebhook: [DEV MODE] обрабатываем message_edit как новое сообщение (num_edit=0)")

					// Пытаемся получить sender через Conversations API
					senderID, senderUsername, err := getSenderFromConversations(entry.ID)
					if err != nil {
						log.Printf("InstagramWebhook: [DEV MODE] ошибка получения sender через Conversations API: %v", err)

						// FALLBACK: используем generic sender ID
						if msg.MessageEdit != nil && msg.MessageEdit.MID != "" {
							senderID = "unknown_sender"
							senderUsername = "Instagram User"
							log.Printf("InstagramWebhook: [DEV MODE] используем fallback sender: id=%s, username=%s", senderID, senderUsername)
						} else {
							log.Printf("InstagramWebhook: [DEV MODE] не можем обработать сообщение без sender и MID")
							continue
						}
					} else {
						log.Printf("InstagramWebhook: [DEV MODE] найден отправитель через API: id=%s, username=%s", senderID, senderUsername)
					}

					// Заполняем sender и recipient
					msg.Sender = &instagramActor{
						ID:       senderID,
						Username: senderUsername,
					}

					msg.Recipient = &instagramActor{
						ID: entry.ID, // Business account ID
					}

					// Создаем message объект
					msg.Message = &instagramMessage{
						MID:  msg.MessageEdit.MID,
						Text: msg.MessageEdit.Text,
					}

					// Если текст пустой, пытаемся получить его через Graph API
					if msg.Message.Text == nil || fmt.Sprintf("%v", msg.Message.Text) == "" {
						if msg.MessageEdit.MID != "" {
							log.Printf("InstagramWebhook: [DEV MODE] текст пустой, пытаемся получить через Graph API")
							messageText, err := fetchMessageText(msg.MessageEdit.MID)
							if err != nil {
								log.Printf("InstagramWebhook: [DEV MODE] ошибка получения текста сообщения: %v", err)
							} else if messageText != "" {
								log.Printf("InstagramWebhook: [DEV MODE] текст получен через API: %s", truncateForLog(messageText, 50))
								msg.Message.Text = messageText
							}
						}
					}
				}
			}

			if msg.Message == nil {
				log.Printf("InstagramWebhook: пропускаем messaging без message (возможно реальное редактирование или удаление)")
				continue
			}

			envelope := buildInstagramEnvelopeFromMessaging(entry.ID, msg)
			if err := handleInstagramMessage(c.Request.Context(), envelope); err != nil {
				log.Printf("InstagramWebhook: ошибка обработки сообщения: %v", err)
			} else {
				processed++
				detail := createProcessedDetail(envelope)
				processedDetails = append(processedDetails, detail)
			}
		}

		// Обработка публичного контента (формат changes)
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				log.Printf("InstagramWebhook: пропускаем change field=%s", change.Field)
				continue
			}

			rawValue := truncateForLog(strings.TrimSpace(string(change.Value)), 800)
			if rawValue != "" {
				log.Printf("InstagramWebhook: входящее сообщение raw=%s", rawValue)
			}

			envelopes := extractInstagramEnvelopes(entry, change)
			if len(envelopes) == 0 {
				log.Printf("InstagramWebhook: не удалось извлечь сообщения (entry_id=%s)", entry.ID)
			}
			for _, envelope := range envelopes {
				if err := handleInstagramMessage(c.Request.Context(), envelope); err != nil {
					log.Printf("InstagramWebhook: ошибка обработки сообщения: %v", err)
				} else {
					processed++
					detail := createProcessedDetail(envelope)
					processedDetails = append(processedDetails, detail)
				}
			}
		}
	}

	log.Printf("InstagramWebhook: обработано %d сообщений", processed)

	response := gin.H{
		"status":    "received",
		"processed": processed,
	}

	if len(processedDetails) > 0 {
		response["messages"] = processedDetails
	}

	c.JSON(http.StatusOK, response)
}

func verifyInstagramSignature(payload []byte, signature string) bool {
	appSecret := database.GetSetting(instagramAppSecretSetting, "")
	if appSecret == "" || signature == "" {
		// Если секрет не настроен или подпись отсутствует, пропускаем проверку
		return true
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	_, _ = mac.Write(payload)
	expected := prefix + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

type instagramWebhookPayload struct {
	Object string           `json:"object"`
	Entry  []instagramEntry `json:"entry"`
}

type instagramEntry struct {
	ID        string              `json:"id"`
	Time      json.Number         `json:"time"`
	Changes   []instagramChange   `json:"changes"`
	Messaging []instagramMessaging `json:"messaging"` // Для Instagram Direct Messages
}

type instagramMessaging struct {
	Sender      *instagramActor        `json:"sender,omitempty"`
	Recipient   *instagramActor        `json:"recipient,omitempty"`
	Timestamp   json.Number            `json:"timestamp"`
	Message     *instagramMessage      `json:"message,omitempty"`
	MessageEdit *instagramMessageEdit  `json:"message_edit,omitempty"` // ВРЕМЕННО: для тестирования в dev режиме
}

// ВРЕМЕННО: структура для message_edit событий в режиме тестирования
// TODO: Удалить после публикации приложения, когда будут приходить обычные message события
type instagramMessageEdit struct {
	MID     string      `json:"mid"`
	NumEdit json.Number `json:"num_edit"` // 0 = новое сообщение, >0 = редактирование
	Text    string      `json:"text,omitempty"`
}

type instagramChange struct {
	Field string          `json:"field"`
	Value json.RawMessage `json:"value"`
}

type instagramValue struct {
	MessagingProduct string             `json:"messaging_product"`
	Sender           *instagramActor    `json:"sender,omitempty"`
	Recipient        *instagramActor    `json:"recipient,omitempty"`
	From             string             `json:"from,omitempty"`
	To               string             `json:"to,omitempty"`
	Timestamp        json.Number        `json:"timestamp"`
	Message          *instagramMessage  `json:"message,omitempty"`
	Messages         []instagramMessage `json:"messages,omitempty"`
	Contacts         []instagramContact `json:"contacts,omitempty"`
	ThreadID         string             `json:"thread_id,omitempty"`
}

type instagramActor struct {
	ID       string `json:"id"`
	Username string `json:"username,omitempty"`
	Name     string `json:"name,omitempty"`
}

type instagramContact struct {
	Profile instagramProfile `json:"profile"`
	ID      string           `json:"id"`
}

type instagramProfile struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

type instagramMessage struct {
	MID         string                `json:"mid,omitempty"`
	ID          string                `json:"id,omitempty"`
	From        string                `json:"from,omitempty"`
	To          string                `json:"to,omitempty"`
	Timestamp   json.Number           `json:"timestamp"`
	Text        interface{}           `json:"text,omitempty"`
	Type        string                `json:"type,omitempty"`
	Attachments []instagramAttachment `json:"attachments,omitempty"`
}

type instagramAttachment struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

type instagramEnvelope struct {
	SenderID       string
	SenderUsername string
	RecipientID    string
	Timestamp      time.Time
	ThreadID       string
	Message        instagramMessage
	Value          instagramValue
}

func extractInstagramEnvelopes(entry instagramEntry, change instagramChange) []instagramEnvelope {
	var value instagramValue
	if err := json.Unmarshal(change.Value, &value); err != nil {
		log.Printf("extractInstagramEnvelopes: ошибка парсинга value: %v", err)
		return nil
	}

	if value.MessagingProduct != "" && value.MessagingProduct != instagramMessagingProduct {
		log.Printf("extractInstagramEnvelopes: пропускаем messaging_product=%s", value.MessagingProduct)
		return nil
	}

	baseSender := firstNotEmpty(
		getActorID(value.Sender),
		value.From,
	)
	baseRecipient := firstNotEmpty(
		getActorID(value.Recipient),
		value.To,
		entry.ID,
	)
	baseUsername := firstNotEmpty(
		getActorUsername(value.Sender),
		extractContactUsername(value.Contacts),
	)
	defaultTimestamp := parseInstagramTimestamp(value.Timestamp)

	var envelopes []instagramEnvelope
	if value.Message != nil {
		env := buildInstagramEnvelope(baseSender, baseRecipient, baseUsername, defaultTimestamp, value.ThreadID, *value.Message, value)
		envelopes = append(envelopes, env)
	}
	for _, msg := range value.Messages {
		env := buildInstagramEnvelope(baseSender, baseRecipient, baseUsername, defaultTimestamp, value.ThreadID, msg, value)
		envelopes = append(envelopes, env)
	}

	return envelopes
}

func buildInstagramEnvelope(baseSender, baseRecipient, baseUsername string, defaultTimestamp time.Time, threadID string, msg instagramMessage, value instagramValue) instagramEnvelope {
	sender := firstNotEmpty(baseSender, msg.From)
	recipient := firstNotEmpty(baseRecipient, msg.To)
	msgTime := parseInstagramTimestamp(msg.Timestamp)
	if msgTime.IsZero() {
		msgTime = defaultTimestamp
	}
	if msgTime.IsZero() {
		msgTime = time.Now()
	}

	username := baseUsername
	if username == "" {
		username = sender
	}

	return instagramEnvelope{
		SenderID:       sender,
		SenderUsername: username,
		RecipientID:    recipient,
		Timestamp:      msgTime,
		ThreadID:       threadID,
		Message:        msg,
		Value:          value,
	}
}

func buildInstagramEnvelopeFromMessaging(entryID string, msg instagramMessaging) instagramEnvelope {
	senderID := getActorID(msg.Sender)
	recipientID := getActorID(msg.Recipient)
	if recipientID == "" {
		recipientID = entryID
	}

	username := getActorUsername(msg.Sender)
	if username == "" {
		username = senderID
	}

	msgTime := parseInstagramTimestamp(msg.Timestamp)
	if msgTime.IsZero() {
		msgTime = time.Now()
	}

	return instagramEnvelope{
		SenderID:       senderID,
		SenderUsername: username,
		RecipientID:    recipientID,
		Timestamp:      msgTime,
		ThreadID:       "",
		Message:        *msg.Message,
		Value:          instagramValue{},
	}
}

func createProcessedDetail(envelope instagramEnvelope) gin.H {
	messageID := firstNotEmpty(envelope.Message.MID, envelope.Message.ID)
	messageType := strings.TrimSpace(envelope.Message.Type)
	if messageType == "" && len(envelope.Message.Attachments) > 0 {
		messageType = envelope.Message.Attachments[0].Type
	}
	if messageType == "" {
		messageType = "text"
	}

	log.Printf("InstagramWebhook: сообщение от %s (%s) тип=%s", envelope.SenderID, envelope.SenderUsername, messageType)

	timestamp := envelope.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	detail := gin.H{
		"sender_id":       envelope.SenderID,
		"sender_username": envelope.SenderUsername,
		"recipient_id":    envelope.RecipientID,
		"message_id":      messageID,
		"message_type":    messageType,
		"timestamp":       timestamp.Format(time.RFC3339),
	}

	if envelope.ThreadID != "" {
		detail["thread_id"] = envelope.ThreadID
	}

	if preview := strings.TrimSpace(extractInstagramText(envelope.Message)); preview != "" {
		if len(preview) > 160 {
			preview = preview[:160] + "..."
		}
		detail["preview"] = preview
	}

	if len(envelope.Message.Attachments) > 0 {
		detail["attachments"] = len(envelope.Message.Attachments)
	}

	return detail
}

func getActorID(actor *instagramActor) string {
	if actor == nil {
		return ""
	}
	return actor.ID
}

func getActorUsername(actor *instagramActor) string {
	if actor == nil {
		return ""
	}
	if actor.Username != "" {
		return actor.Username
	}
	return actor.Name
}

func extractContactUsername(contacts []instagramContact) string {
	if len(contacts) == 0 {
		return ""
	}
	if contacts[0].Profile.Username != "" {
		return contacts[0].Profile.Username
	}
	return contacts[0].Profile.Name
}

func parseInstagramTimestamp(ts json.Number) time.Time {
	if ts == "" {
		return time.Time{}
	}

	str := ts.String()
	if str == "" {
		return time.Time{}
	}

	if val, err := strconv.ParseInt(str, 10, 64); err == nil {
		switch {
		case val > 1e15:
			// Наносекунды
			return time.Unix(0, val)
		case val > 1e12:
			// Миллисекунды
			return time.UnixMilli(val)
		default:
			return time.Unix(val, 0)
		}
	}

	if fl, err := strconv.ParseFloat(str, 64); err == nil {
		intVal := int64(fl)
		if fl > 1e12 {
			return time.UnixMilli(intVal)
		}
		return time.Unix(intVal, 0)
	}

	return time.Time{}
}

func firstNotEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func handleInstagramMessage(ctx context.Context, envelope instagramEnvelope) error {
	if envelope.SenderID == "" {
		return fmt.Errorf("sender id отсутствует")
	}

	// Пропускаем эхо-сообщения от собственного бота
	if envelope.RecipientID != "" && envelope.SenderID == envelope.RecipientID {
		log.Printf("handleInstagramMessage: пропускаем сообщение-эхо (sender=recipient=%s)", envelope.SenderID)
		return nil
	}

	messageID := firstNotEmpty(envelope.Message.MID, envelope.Message.ID)
	clientAPIKey := database.GetSetting(instagramClientKeySetting, defaultInstagramClientKey)
	botID := firstNotEmpty(envelope.RecipientID, database.GetSetting(instagramBusinessIDSetting, ""))
	if botID == "" {
		return fmt.Errorf("bot id не найден для сообщения %s", messageID)
	}

	if strings.TrimSpace(envelope.SenderID) == strings.TrimSpace(botID) {
		log.Printf("handleInstagramMessage: пропускаем исходящее сообщение (sender=botID=%s)", botID)
		return nil
	}

	userName := envelope.SenderUsername
	if userName == "" {
		userName = fmt.Sprintf("Instagram user %s", maskIdentifier(envelope.SenderID))
	}

	chat, err := database.GetOrCreateChatMetadata(
		envelope.SenderID,
		userName,
		"",
		instagramSource,
		envelope.SenderID,
		botID,
		clientAPIKey,
	)
	if err != nil {
		return fmt.Errorf("GetOrCreateChatMetadata: %w", err)
	}

	content := strings.TrimSpace(extractInstagramText(envelope.Message))
	if content == "" && len(envelope.Message.Attachments) == 0 {
		log.Printf("handleInstagramMessage: пустое сообщение, пропускаем (messageID=%s)", messageID)
		return nil
	}

	if content == "" {
		content = describeInstagramAttachment(envelope.Message.Attachments)
	}

	messageType := strings.TrimSpace(envelope.Message.Type)
	if messageType == "" && len(envelope.Message.Attachments) > 0 {
		messageType = envelope.Message.Attachments[0].Type
	}
	if messageType == "" {
		messageType = "text"
	}

	metadata := make(map[string]interface{})
	metadata["source"] = instagramSource
	metadata["rawType"] = envelope.Message.Type
	if messageID != "" {
		metadata["instagramMessageId"] = messageID
	}
	if envelope.ThreadID != "" {
		metadata["instagramThreadId"] = envelope.ThreadID
	}
	if len(envelope.Message.Attachments) > 0 {
		metadata["attachments"] = envelope.Message.Attachments
	}

	originalContent := content

	if Translator != nil && strings.TrimSpace(content) != "" {
		log.Printf("handleInstagramMessage: запуск перевода сообщения")
		result, err := Translator.TranslateUserMessage(ctx, content, chat.ID)
		if err != nil {
			log.Printf("handleInstagramMessage: ошибка перевода сообщения: %v", err)
		} else if result != nil {
			// Сохраняем оригинал в контенте, а метаданные объединяем
			content = originalContent
			for k, v := range result.Metadata {
				metadata[k] = v
			}
			log.Printf("handleInstagramMessage: перевод выполнен, detectedLang=%s", result.DetectedLanguage)
		}
	}

	userUUID := deterministicUUID(envelope.SenderID)
	msgUUID := uuid.New()
	if messageID != "" {
		msgUUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(messageID))
	}

	timestamp := envelope.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	userMsg, err := database.AddMessageWithID(
		msgUUID,
		chat.ID,
		content,
		"user",
		userUUID,
		timestamp,
		messageType,
		metadata,
		instagramSource,
	)
	if err != nil {
		return fmt.Errorf("AddMessageWithID: %w", err)
	}

	log.Printf("handleInstagramMessage: сообщение сохранено (id=%s, chat=%s)", userMsg.ID, chat.ID)

	// Быстро обновляем время чата
	updateChatTimestamp(chat.ID)

	var botMsg *models.Message
	if AutoResponder != nil && chat.AutoResponderEnabled {
		log.Printf("handleInstagramMessage: запуск автоответчика")

		lightChat, err := queries.GetChatLightweight(database.DB, chat.ID)
		if err != nil {
			log.Printf("handleInstagramMessage: ошибка загрузки чата для автоответчика: %v", err)
			lightChat = chat
		}

		botMsg, err = AutoResponder.ProcessMessage(ctx, lightChat, userMsg)
		if err != nil {
			log.Printf("handleInstagramMessage: ошибка автоответчика: %v", err)
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
			saved, err := database.AddMessage(
				chat.ID,
				botMsg.Content,
				botMsg.Sender,
				botMsg.SenderID,
				botMsg.Type,
				botMsg.Metadata,
			)
			if err != nil {
				log.Printf("handleInstagramMessage: ошибка сохранения автоответа: %v", err)
			} else {
				botMsg = saved
				go dispatchExternalMessage(chat.ID, botMsg)
				updateChatTimestamp(chat.ID)

				if needEscalation, ok := botMsg.Metadata["needEscalation"].(bool); ok && needEscalation {
					escalationNotification := createEscalationNotification(chat.ID, userMsg)
					totalSent := WebSocketHub.SendToAllAdmins(escalationNotification)
					log.Printf("handleInstagramMessage: уведомление об эскалации отправлено %d админам", totalSent)
				}
			}
		}
	}

	chatInfo := createChatInfo(chat)
	userNotification := createMessageNotification(chat.ID, userMsg, chatInfo)
	totalSent := WebSocketHub.SendToChatAndAdmins(chat.ID.String(), userNotification)
	log.Printf("handleInstagramMessage: уведомление о сообщении пользователя отправлено %d клиентам", totalSent)

	if botMsg != nil {
		botNotification := createMessageNotification(chat.ID, botMsg, chatInfo)
		totalSent = WebSocketHub.SendToChatAndAdmins(chat.ID.String(), botNotification)
		log.Printf("handleInstagramMessage: уведомление о сообщении бота отправлено %d клиентам", totalSent)
	}

	return nil
}

func extractInstagramText(msg instagramMessage) string {
	switch v := msg.Text.(type) {
	case string:
		return v
	case map[string]interface{}:
		if body, ok := v["body"].(string); ok {
			return body
		}
		if text, ok := v["text"].(string); ok {
			return text
		}
	}
	return ""
}

func describeInstagramAttachment(attachments []instagramAttachment) string {
	if len(attachments) == 0 {
		return ""
	}

	var types []string
	for _, att := range attachments {
		if att.Type != "" {
			types = append(types, att.Type)
		}
	}

	if len(types) == 0 {
		return "Instagram attachment"
	}
	return fmt.Sprintf("Instagram attachment: %s", strings.Join(types, ", "))
}

func deterministicUUID(value string) uuid.UUID {
	if parsed, err := uuid.Parse(value); err == nil {
		return parsed
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(value))
}

func maskIdentifier(id string) string {
	if len(id) <= 4 {
		return id
	}
	return id[len(id)-4:]
}

func sendInstagramOutgoingMessage(ctx context.Context, chat *models.Chat, message *models.Message) error {
	if chat == nil || message == nil {
		return fmt.Errorf("пустой чат или сообщение")
	}

	if !strings.EqualFold(message.Sender, "admin") {
		return nil
	}

	text := strings.TrimSpace(message.Content)
	if text == "" {
		return fmt.Errorf("пустое сообщение, отправка в Instagram пропущена")
	}

	userID := strings.TrimSpace(chat.User.SourceID)
	if userID == "" {
		return fmt.Errorf("instagram user id отсутствует для чата %s", chat.ID)
	}

	botID := strings.TrimSpace(chat.BotID)
	if botID == "" {
		botID = database.GetSetting(instagramBusinessIDSetting, "")
		if botID == "" {
			return fmt.Errorf("instagram business id не настроен")
		}
	}

	token := database.GetSetting(instagramAccessTokenSetting, "")
	if token == "" {
		return fmt.Errorf("instagram access token не настроен")
	}

	apiVersion := database.GetSetting(instagramAPIVersionSetting, defaultInstagramAPIVersion)
	apiURL := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages", apiVersion, botID)

	payload := map[string]any{
		"messaging_product": instagramMessagingProduct,
		"recipient": map[string]string{
			"id": userID,
		},
		"text": map[string]string{
			"body": text,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := instagramHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("instagram request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("instagram API error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	log.Printf("sendInstagramOutgoingMessage: сообщение отправлено (chat=%s, user=%s)", chat.ID, userID)
	return nil
}

func truncateForLog(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "...(truncated)"
}
