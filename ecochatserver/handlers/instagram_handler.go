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
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
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
	instagramAccessTokenSetting  = "INSTAGRAM_ACCESS_TOKEN"
	instagramWebhookEntryID      = "INSTAGRAM_WEBHOOK_ENTRY_ID"
	instagramAPIVersionSetting   = "INSTAGRAM_API_VERSION"
	defaultInstagramAPIVersion   = "v18.0"
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
	accessToken := database.GetDynamicSetting(instagramAccessTokenSetting, "")
	if accessToken == "" {
		return nil, fmt.Errorf("instagram access token not configured")
	}

	apiVersion := database.GetSetting(instagramAPIVersionSetting, defaultInstagramAPIVersion)

	// Пробуем разные endpoints, т.к. в dev mode может быть ограничение
	endpoints := []string{
		fmt.Sprintf("https://graph.instagram.com/%s/%s?fields=id,username,name&access_token=%s", apiVersion, businessAccountID, accessToken),
		fmt.Sprintf("https://graph.instagram.com/%s/me?access_token=%s", apiVersion, accessToken),
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
	url := fmt.Sprintf("https://graph.instagram.com/%s/%s/conversations?platform=instagram&fields=id,participants{id,username,name}&limit=10&access_token=%s",
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
	accessToken := database.GetDynamicSetting(instagramAccessTokenSetting, "")
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

func fetchInstagramUserProfile(senderID string) (name, profilePic, username string) {
	accessToken := database.GetDynamicSetting(instagramAccessTokenSetting, "")
	if accessToken == "" {
		log.Printf("fetchInstagramUserProfile: access token не настроен")
		return "", "", ""
	}

	apiVersion := database.GetSetting(instagramAPIVersionSetting, defaultInstagramAPIVersion)

	profileURL := fmt.Sprintf(
		"https://graph.instagram.com/%s/%s?fields=name,profile_pic&access_token=%s",
		apiVersion, senderID, accessToken,
	)

	log.Printf("fetchInstagramUserProfile: запрос профиля для %s", maskIdentifier(senderID))

	resp, err := instagramHTTPClient.Get(profileURL)
	if err != nil {
		log.Printf("fetchInstagramUserProfile: ошибка HTTP: %v", err)
		return "", "", ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("fetchInstagramUserProfile: ошибка чтения: %v", err)
		return "", "", ""
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("fetchInstagramUserProfile: статус %d, body=%s",
			resp.StatusCode, truncateForLog(string(body), 200))
		return "", "", ""
	}

	var profile instagramUserProfileResponse
	if err := json.Unmarshal(body, &profile); err != nil {
		log.Printf("fetchInstagramUserProfile: ошибка парсинга: %v", err)
		return "", "", ""
	}

	name = strings.TrimSpace(profile.Name)
	profilePic = strings.TrimSpace(profile.ProfilePic)

	log.Printf("fetchInstagramUserProfile: name=%q, has_pic=%v", name, profilePic != "")

	businessAccountID := database.GetDynamicSetting(instagramBusinessIDSetting, "")
	if businessAccountID != "" {
		convs, err := fetchInstagramConversations(businessAccountID)
		if err == nil {
			for _, conv := range convs {
				for _, p := range conv.Participants.Data {
					if p.ID == senderID && p.Username != "" {
						username = p.Username
						break
					}
				}
				if username != "" {
					break
				}
			}
		}
	}

	return name, profilePic, username
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

	// Сразу отвечаем Instagram 200 OK — обработку делаем в фоне
	// Instagram ожидает ответ за 5 секунд, иначе ретраит
	c.JSON(http.StatusOK, gin.H{"status": "received"})

	// Собираем все envelopes для обработки в фоне
	webhookEntryID := database.GetDynamicSetting(instagramWebhookEntryID, "")

	for _, entry := range payload.Entry {
		log.Printf("InstagramWebhook: entry.ID=%s, changes=%d, messaging=%d, filter=%s",
			entry.ID, len(entry.Changes), len(entry.Messaging), webhookEntryID)

		// Фильтруем вебхуки от чужих аккаунтов (только если INSTAGRAM_WEBHOOK_ENTRY_ID задан вручную)
		// Подпись уже проверена выше — это основной механизм безопасности
		if webhookEntryID != "" && entry.ID != webhookEntryID {
			log.Printf("InstagramWebhook: SKIP entry %s (фильтр=%s)", entry.ID, webhookEntryID)
			continue
		}

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
			go func(env instagramEnvelope) {
				if _, err := handleInstagramMessage(context.Background(), env); err != nil {
					log.Printf("InstagramWebhook: ошибка обработки сообщения: %v", err)
				}
			}(envelope)
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
				go func(env instagramEnvelope) {
					if _, err := handleInstagramMessage(context.Background(), env); err != nil {
						log.Printf("InstagramWebhook: ошибка обработки сообщения: %v", err)
					}
				}(envelope)
			}
		}
	}

	log.Printf("InstagramWebhook: сообщения отправлены на обработку в фоне")
}

func verifyInstagramSignature(payload []byte, signature string) bool {
	// Для Instagram Business Login подпись вычисляется с Instagram App Secret
	appSecret := os.Getenv("INSTAGRAM_APP_SECRET")
	if appSecret == "" {
		// Фолбэк на FACEBOOK_APP_SECRET и БД
		appSecret = os.Getenv("FACEBOOK_APP_SECRET")
	}
	if appSecret == "" {
		appSecret = database.GetSetting(instagramAppSecretSetting, "")
	}
	if appSecret == "" || signature == "" {
		// Если секрет не настроен или подпись отсутствует, пропускаем проверку
		log.Printf("verifyInstagramSignature: SKIP (appSecret=%v, signature=%v)", appSecret != "", signature != "")
		return true
	}

	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		log.Printf("verifyInstagramSignature: signature doesn't have sha256= prefix: %s", signature)
		return false
	}

	mac := hmac.New(sha256.New, []byte(appSecret))
	_, _ = mac.Write(payload)
	expected := prefix + hex.EncodeToString(mac.Sum(nil))

	log.Printf("verifyInstagramSignature: payload_len=%d", len(payload))
	log.Printf("verifyInstagramSignature: payload_sample=%s", truncateForLog(string(payload), 200))
	log.Printf("verifyInstagramSignature: appSecret=%s", truncateForLog(appSecret, 20))
	log.Printf("verifyInstagramSignature: received=%s", signature)
	log.Printf("verifyInstagramSignature: expected=%s", expected)
	log.Printf("verifyInstagramSignature: match=%v", hmac.Equal([]byte(expected), []byte(signature)))

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

type instagramUserProfileResponse struct {
	Name       string `json:"name"`
	ProfilePic string `json:"profile_pic"`
	ID         string `json:"id"`
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
	IsEcho      bool                  `json:"is_echo,omitempty"`
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

func createProcessedDetail(envelope instagramEnvelope, chatID string) gin.H {
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
		"chat_id":         chatID,
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

func handleInstagramMessage(ctx context.Context, envelope instagramEnvelope) (string, error) {
	messageID := firstNotEmpty(envelope.Message.MID, envelope.Message.ID)

	log.Printf("handleInstagramMessage: sender=%s, recipient=%s, is_echo=%v, text=%s",
		envelope.SenderID, envelope.RecipientID, envelope.Message.IsEcho,
		truncateForLog(extractInstagramText(envelope.Message), 50))

	if envelope.SenderID == "" {
		return "", fmt.Errorf("sender id отсутствует")
	}

	// Пропускаем эхо-сообщения (is_echo=true — уведомление об отправленном сообщении от бизнес-аккаунта)
	if envelope.Message.IsEcho {
		log.Printf("handleInstagramMessage: SKIP is_echo (sender=%s)", envelope.SenderID)
		return "", nil
	}

	// Пропускаем сообщения где sender=recipient
	if envelope.RecipientID != "" && envelope.SenderID == envelope.RecipientID {
		log.Printf("handleInstagramMessage: SKIP sender=recipient=%s", envelope.SenderID)
		return "", nil
	}

	clientAPIKey := database.GetSetting(instagramClientKeySetting, defaultInstagramClientKey)
	botID := firstNotEmpty(envelope.RecipientID, database.GetDynamicSetting(instagramBusinessIDSetting, ""))
	if botID == "" {
		return "", fmt.Errorf("bot id не найден для сообщения %s", messageID)
	}

	if strings.TrimSpace(envelope.SenderID) == strings.TrimSpace(botID) {
		log.Printf("handleInstagramMessage: пропускаем исходящее сообщение (sender=botID=%s)", botID)
		return "", nil
	}

	userName := envelope.SenderUsername
	var avatarURL, profileURL string

	if userName == "" || userName == envelope.SenderID {
		profileName, profilePic, profileUsername := fetchInstagramUserProfile(envelope.SenderID)
		avatarURL = profilePic
		if profileUsername != "" {
			userName = profileUsername
			profileURL = "https://instagram.com/" + profileUsername
		} else if profileName != "" {
			userName = profileName
		} else {
			userName = fmt.Sprintf("Instagram user %s", maskIdentifier(envelope.SenderID))
		}
		log.Printf("handleInstagramMessage: профиль из API: name=%q, username=%q, avatar=%v, profileURL=%q",
			profileName, profileUsername, avatarURL != "", profileURL)
	}

	chat, err := database.GetOrCreateChatMetadata(
		envelope.SenderID,
		userName,
		"",
		instagramSource,
		envelope.SenderID,
		botID,
		clientAPIKey,
		nil, // widgetBusinessID - not applicable for Instagram
	)
	if err != nil {
		return "", fmt.Errorf("GetOrCreateChatMetadata: %w", err)
	}

	// Обновляем avatar и profile_url пользователя
	if avatarURL != "" || profileURL != "" {
		go database.UpdateUserProfile(chat.User.ID, avatarURL, profileURL)
	}

	content := strings.TrimSpace(extractInstagramText(envelope.Message))
	if content == "" && len(envelope.Message.Attachments) == 0 {
		log.Printf("handleInstagramMessage: пустое сообщение, пропускаем (messageID=%s)", messageID)
		return chat.ID.String(), nil
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
		return chat.ID.String(), fmt.Errorf("AddMessageWithID: %w", err)
	}

	log.Printf("handleInstagramMessage: сообщение сохранено (id=%s, chat=%s)", userMsg.ID, chat.ID)

	botMsg := runAutoResponder(ctx, chat, userMsg, true)
	notifyNewMessages(chat, userMsg, botMsg)

	return chat.ID.String(), nil
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
		botID = database.GetDynamicSetting(instagramBusinessIDSetting, "")
		if botID == "" {
			return fmt.Errorf("instagram business id не настроен")
		}
	}

	// DEV MODE: если включен режим разработки, не отправляем в реальный Instagram
	if os.Getenv("INSTAGRAM_DEV_MODE") == "true" {
		log.Printf("sendInstagramOutgoingMessage: [DEV MODE] режим разработки включен, пропускаем отправку в Instagram API")
		log.Printf("sendInstagramOutgoingMessage: [DEV MODE] сообщение для отправки: userID=%s, text=%s", userID, text)
		return nil
	}

	token := database.GetDynamicSetting(instagramAccessTokenSetting, "")
	if token == "" {
		return fmt.Errorf("instagram access token не настроен (userID=%s, text=%s)", userID, truncateForLog(text, 50))
	}

	// Instagram Business Login API: отправка через graph.instagram.com
	apiURL := "https://graph.instagram.com/v25.0/me/messages"

	payload := map[string]any{
		"recipient": map[string]string{
			"id": userID,
		},
		"message": map[string]string{
			"text": text,
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

// FindInstagramChat ищет существующий Instagram чат по sender_id
// Используется ТОЛЬКО для Instagram demo страницы, не влияет на widget систему
func FindInstagramChat(c *gin.Context) {
	var req struct {
		SenderID string `json:"sender_id"`
		Source   string `json:"source"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Проверяем что source = instagram
	if req.Source != instagramSource {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only instagram source supported"})
		return
	}

	if req.SenderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sender_id required"})
		return
	}

	log.Printf("FindInstagramChat: поиск чата для sender_id=%s, source=%s", req.SenderID, req.Source)

	// Используем существующую функцию из database/queries
	chat, err := database.FindChatByUserSourceID(req.SenderID, instagramSource)
	if err != nil {
		log.Printf("FindInstagramChat: чат не найден: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
		return
	}

	log.Printf("FindInstagramChat: найден чат %s для sender_id=%s", chat.ID, req.SenderID)

	c.JSON(http.StatusOK, gin.H{
		"chat_id": chat.ID.String(),
		"status":  "found",
	})
}
