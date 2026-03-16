package handlers

import (
	"bytes"
	"context"
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
	"github.com/egor/ecochatserver/models"
)

const (
	whatsappSource              = "whatsapp"
	whatsappVerifyTokenSetting  = "WHATSAPP_VERIFY_TOKEN"
	whatsappAccessTokenSetting  = "WHATSAPP_ACCESS_TOKEN"
	whatsappPhoneNumberIDSetting = "WHATSAPP_PHONE_NUMBER_ID"
	whatsappClientKeySetting    = "WHATSAPP_CLIENT_API_KEY"
	defaultWhatsAppVerifyToken  = "eddelchat_whatsapp_verify_2026"
	defaultWhatsAppClientKey    = "whatsapp_default_client"
	whatsappAPIVersion          = "v18.0"
)

var whatsappHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
}

// WhatsApp webhook payload structures

type whatsappWebhookPayload struct {
	Object string          `json:"object"`
	Entry  []whatsappEntry `json:"entry"`
}

type whatsappEntry struct {
	ID      string           `json:"id"`
	Changes []whatsappChange `json:"changes"`
}

type whatsappChange struct {
	Value whatsappValue `json:"value"`
	Field string        `json:"field"`
}

type whatsappValue struct {
	MessagingProduct string             `json:"messaging_product"`
	Metadata         whatsappMetadata   `json:"metadata"`
	Contacts         []whatsappContact  `json:"contacts"`
	Messages         []whatsappMessage  `json:"messages"`
	Statuses         []whatsappStatus   `json:"statuses"`
}

type whatsappMetadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type whatsappContact struct {
	Profile whatsappProfile `json:"profile"`
	WaID    string          `json:"wa_id"`
}

type whatsappProfile struct {
	Name string `json:"name"`
}

type whatsappMessage struct {
	From      string         `json:"from"`
	ID        string         `json:"id"`
	Timestamp string         `json:"timestamp"`
	Type      string         `json:"type"`
	Text      *whatsappText  `json:"text,omitempty"`
	Image     *whatsappMedia `json:"image,omitempty"`
	Audio     *whatsappMedia `json:"audio,omitempty"`
	Video     *whatsappMedia `json:"video,omitempty"`
	Document  *whatsappMedia `json:"document,omitempty"`
	Sticker   *whatsappMedia `json:"sticker,omitempty"`
}

type whatsappText struct {
	Body string `json:"body"`
}

type whatsappMedia struct {
	Caption  string `json:"caption,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	ID       string `json:"id,omitempty"`
}

type whatsappStatus struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Timestamp   string `json:"timestamp"`
	RecipientID string `json:"recipient_id"`
}

// WhatsAppWebhookVerify обрабатывает GET-запрос для подтверждения вебхука WhatsApp
func WhatsAppWebhookVerify(c *gin.Context) {
	mode := c.Query("hub.mode")
	challenge := c.Query("hub.challenge")
	verifyToken := c.Query("hub.verify_token")

	// Сначала проверяем в БД настройках, если нет — используем дефолтный
	expectedToken := database.GetSetting(whatsappVerifyTokenSetting, defaultWhatsAppVerifyToken)

	log.Printf("WhatsAppWebhookVerify: mode=%s, verifyToken=%s", mode, verifyToken)

	if mode == "subscribe" && verifyToken == expectedToken {
		log.Printf("WhatsAppWebhookVerify: верификация успешна, отправляем challenge=%s", challenge)
		c.String(http.StatusOK, challenge)
		return
	}

	log.Printf("WhatsAppWebhookVerify: неверный verify token (mode=%s, received=%s)", mode, verifyToken)
	c.String(http.StatusForbidden, "verification failed")
}

// WhatsAppWebhook обрабатывает POST-запросы от WhatsApp
func WhatsAppWebhook(c *gin.Context) {
	log.Printf("WhatsAppWebhook: %s %s from %s", c.Request.Method, c.FullPath(), c.ClientIP())

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("WhatsAppWebhook: ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusOK, gin.H{"status": "ok"}) // Meta требует 200 в любом случае
		return
	}

	log.Printf("WhatsAppWebhook: raw payload=%s", truncateForLog(string(bodyBytes), 1000))

	var payload whatsappWebhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		log.Printf("WhatsAppWebhook: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	if strings.ToLower(payload.Object) != "whatsapp_business_account" {
		log.Printf("WhatsAppWebhook: неизвестный объект %s", payload.Object)
		c.JSON(http.StatusOK, gin.H{"status": "ignored"})
		return
	}

	processed := 0
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				log.Printf("WhatsAppWebhook: пропускаем field=%s", change.Field)
				continue
			}

			value := change.Value

			// Статусы доставки — просто логируем, не обрабатываем
			for _, status := range value.Statuses {
				log.Printf("WhatsAppWebhook: статус сообщения %s: %s (recipient=%s)", status.ID, status.Status, status.RecipientID)
			}

			// Строим map: wa_id → имя контакта
			contactNames := make(map[string]string)
			for _, contact := range value.Contacts {
				contactNames[contact.WaID] = contact.Profile.Name
			}

			for _, msg := range value.Messages {
				if err := handleWhatsAppMessage(c.Request.Context(), entry.ID, value.Metadata, msg, contactNames); err != nil {
					log.Printf("WhatsAppWebhook: ошибка обработки сообщения %s: %v", msg.ID, err)
				} else {
					processed++
				}
			}
		}
	}

	log.Printf("WhatsAppWebhook: обработано %d сообщений", processed)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func handleWhatsAppMessage(ctx context.Context, entryID string, metadata whatsappMetadata, msg whatsappMessage, contactNames map[string]string) error {
	senderPhone := strings.TrimSpace(msg.From)
	if senderPhone == "" {
		return fmt.Errorf("sender phone отсутствует")
	}

	phoneNumberID := firstNotEmpty(metadata.PhoneNumberID, database.GetSetting(whatsappPhoneNumberIDSetting, ""), entryID)

	// Пропускаем сообщения от собственного номера (echo)
	if senderPhone == metadata.DisplayPhoneNumber {
		log.Printf("handleWhatsAppMessage: пропускаем echo-сообщение от %s", senderPhone)
		return nil
	}

	userName := contactNames[senderPhone]
	if userName == "" {
		userName = "WhatsApp " + maskIdentifier(senderPhone)
	}

	clientAPIKey := database.GetSetting(whatsappClientKeySetting, defaultWhatsAppClientKey)

	chat, err := database.GetOrCreateChatMetadata(
		senderPhone,
		userName,
		"",
		whatsappSource,
		senderPhone,
		phoneNumberID,
		clientAPIKey,
		nil,
	)
	if err != nil {
		return fmt.Errorf("GetOrCreateChatMetadata: %w", err)
	}

	content, msgType := extractWhatsAppContent(msg)
	if content == "" {
		log.Printf("handleWhatsAppMessage: пустое/неподдерживаемое сообщение (type=%s), пропускаем", msg.Type)
		return nil
	}

	metadata2 := map[string]interface{}{
		"source":            whatsappSource,
		"whatsappMessageId": msg.ID,
		"phoneNumberId":     phoneNumberID,
		"rawType":           msg.Type,
	}

	timestamp := parseWhatsAppTimestamp(msg.Timestamp)

	msgUUID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(msg.ID))
	userUUID := deterministicUUID(senderPhone)

	userMsg, err := database.AddMessageWithID(
		msgUUID,
		chat.ID,
		content,
		"user",
		userUUID,
		timestamp,
		msgType,
		metadata2,
		whatsappSource,
	)
	if err != nil {
		return fmt.Errorf("AddMessageWithID: %w", err)
	}

	log.Printf("handleWhatsAppMessage: сообщение сохранено (id=%s, chat=%s, from=%s)", userMsg.ID, chat.ID, senderPhone)

	// Notify admins about user message IMMEDIATELY (before autoresponder debounce)
	notifyNewMessages(chat, userMsg, nil)

	botMsg := runAutoResponder(ctx, chat, userMsg, true)
	if botMsg != nil {
		notifyNewMessages(chat, nil, botMsg)
	}

	return nil
}

func extractWhatsAppContent(msg whatsappMessage) (content, msgType string) {
	switch msg.Type {
	case "text":
		if msg.Text != nil {
			return msg.Text.Body, "text"
		}
	case "image":
		if msg.Image != nil && msg.Image.Caption != "" {
			return msg.Image.Caption, "image"
		}
		return "[Изображение]", "image"
	case "audio":
		return "[Голосовое сообщение]", "audio"
	case "video":
		if msg.Video != nil && msg.Video.Caption != "" {
			return msg.Video.Caption, "video"
		}
		return "[Видео]", "video"
	case "document":
		if msg.Document != nil && msg.Document.Caption != "" {
			return msg.Document.Caption, "document"
		}
		return "[Документ]", "document"
	case "sticker":
		return "[Стикер]", "sticker"
	default:
		log.Printf("extractWhatsAppContent: неизвестный тип сообщения: %s", msg.Type)
	}
	return "", ""
}

func parseWhatsAppTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Now()
	}
	if val, err := strconv.ParseInt(ts, 10, 64); err == nil {
		return time.Unix(val, 0)
	}
	return time.Now()
}

// sendWhatsAppOutgoingMessage отправляет исходящее сообщение в WhatsApp через Cloud API
func sendWhatsAppOutgoingMessage(ctx context.Context, chat *models.Chat, message *models.Message) error {
	if chat == nil || message == nil {
		return fmt.Errorf("пустой чат или сообщение")
	}

	if !strings.EqualFold(message.Sender, "admin") {
		return nil
	}

	text := strings.TrimSpace(message.Content)
	if text == "" {
		return fmt.Errorf("пустое сообщение, отправка в WhatsApp пропущена")
	}

	toPhone := strings.TrimSpace(chat.User.SourceID)
	if toPhone == "" {
		return fmt.Errorf("whatsapp phone number отсутствует для чата %s", chat.ID)
	}

	phoneNumberID := strings.TrimSpace(chat.BotID)
	if phoneNumberID == "" {
		phoneNumberID = database.GetSetting(whatsappPhoneNumberIDSetting, "")
		if phoneNumberID == "" {
			return fmt.Errorf("whatsapp phone_number_id не настроен")
		}
	}

	accessToken := database.GetSetting(whatsappAccessTokenSetting, "")
	if accessToken == "" {
		log.Printf("sendWhatsAppOutgoingMessage: токен не настроен, пропускаем отправку (chat=%s)", chat.ID)
		return nil
	}

	apiURL := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages", whatsappAPIVersion, phoneNumberID)

	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                toPhone,
		"type":              "text",
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
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := whatsappHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("whatsapp request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("whatsapp API error: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	log.Printf("sendWhatsAppOutgoingMessage: сообщение отправлено (chat=%s, to=%s)", chat.ID, toPhone)
	return nil
}
