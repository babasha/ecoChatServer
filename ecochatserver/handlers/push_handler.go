package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
	"github.com/gin-gonic/gin"
)

type pushSubscriptionKeys struct {
	P256dh string `json:"p256dh" binding:"required"`
	Auth   string `json:"auth" binding:"required"`
}

type pushSubscriptionRequest struct {
	Endpoint       string               `json:"endpoint" binding:"required"`
	ExpirationTime *string              `json:"expirationTime,omitempty"`
	Keys           pushSubscriptionKeys `json:"keys" binding:"required"`
}

type pushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
}

type pushSendRequest struct {
	Title   string                 `json:"title" binding:"required"`
	Message string                 `json:"message" binding:"required"`
	Data    map[string]interface{} `json:"data"`
}

// vapidConfig содержит параметры VAPID для библиотеки webpush-go.
// privateKey/publicKey хранятся в base64url-формате (как и принимает webpush-go).
type vapidConfig struct {
	privateKey string
	publicKey  string
	subject    string
}

// PushPayload — структура которая шифруется и шлётся в push event.
// SW в `public/sw.js` (moooving) и в админке распарсит её через event.data.json().
type PushPayload struct {
	Title string                 `json:"title"`
	Body  string                 `json:"body,omitempty"`
	Tag   string                 `json:"tag,omitempty"`
	Data  map[string]interface{} `json:"data,omitempty"`
}

var (
	vapidOnce sync.Once
	vapidCfg  *vapidConfig
	vapidErr  error

	pushHTTPClient = &http.Client{
		Timeout: 10 * time.Second,
	}
)

// PushSubscribeHandler сохраняет подписку браузера администратора
func PushSubscribeHandler(c *gin.Context) {
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	var req pushSubscriptionRequest
	if !bindJSON(c, &req) {
		return
	}

	payload, err := json.Marshal(req)
	if err != nil {
		log.Printf("PushSubscribeHandler: ошибка сериализации подписки: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Внутренняя ошибка"})
		return
	}

	if err := database.SavePushSubscription(adminID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth, payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось сохранить подписку"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Подписка сохранена",
	})
}

// PushUnsubscribeHandler удаляет подписку на push-уведомления
func PushUnsubscribeHandler(c *gin.Context) {
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	var req pushUnsubscribeRequest
	if !bindJSON(c, &req) {
		return
	}

	if err := database.RemovePushSubscription(adminID, req.Endpoint); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось удалить подписку"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Подписка удалена",
	})
}

// PushSendHandler обрабатывает запрос на отправку push-уведомления
// Реальная доставка push зависит от наличия VAPID ключей. Если они не настроены, просто логируем событие.
func PushSendHandler(c *gin.Context) {
	adminID, err := getAdminID(c)
	if err != nil {
		return
	}

	var req pushSendRequest
	if !bindJSON(c, &req) {
		return
	}

	subs, err := database.ListPushSubscriptions(adminID)
	if err != nil {
		log.Printf("PushSendHandler: ошибка загрузки подписок: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Не удалось загрузить подписки"})
		return
	}

	if len(subs) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"sent":    0,
			"total":   0,
			"message": "Нет активных подписок для отправки push-уведомлений",
		})
		return
	}

	cfg, err := getVAPIDConfig()
	if err != nil {
		log.Printf("PushSendHandler: VAPID конфигурация недоступна: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Push уведомления не настроены (VAPID)"})
		return
	}

	log.Printf("PushSendHandler: отправка push для admin=%s, title=%s, подписок=%d", adminID, req.Title, len(subs))

	payload := MarshalPushPayload(&PushPayload{
		Title: req.Title,
		Body:  req.Message,
		Data:  req.Data,
	})

	sent := 0
	for _, sub := range subs {
		status, sendErr := sendWebPushNotification(cfg, &sub, payload)
		if sendErr != nil {
			log.Printf("PushSendHandler: ошибка отправки на %s: %v", sub.Endpoint, sendErr)
			continue
		}

		switch status {
		case http.StatusGone, http.StatusNotFound:
			log.Printf("PushSendHandler: удаляем недействительную подписку %s (status=%d)", sub.Endpoint, status)
			_ = database.RemovePushSubscriptionByEndpoint(sub.Endpoint)
		default:
			if status >= 200 && status < 300 {
				sent++
				_ = database.TouchPushSubscription(sub.Endpoint)
			} else {
				log.Printf("PushSendHandler: неожиданный статус отправки %d для %s", status, sub.Endpoint)
			}
		}
	}

	failed := len(subs) - sent

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"sent":    sent,
		"failed":  failed,
		"total":   len(subs),
		"message": fmt.Sprintf("Push уведомления отправлены (%d успешно, %d ошибок)", sent, failed),
	})
}

func getVAPIDConfig() (*vapidConfig, error) {
	vapidOnce.Do(func() {
		privRaw := strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY"))
		pubRaw := strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY"))
		if pubRaw == "" {
			pubRaw = strings.TrimSpace(os.Getenv("NEXT_PUBLIC_VAPID_PUBLIC_KEY"))
		}
		subject := strings.TrimSpace(os.Getenv("VAPID_EMAIL"))
		if subject == "" {
			subject = strings.TrimSpace(os.Getenv("VAPID_SUBJECT"))
		}

		if privRaw == "" || pubRaw == "" || subject == "" {
			vapidErr = errors.New("VAPID ключи или subject не заданы в переменных окружения")
			return
		}

		// webpush-go ожидает публичный/приватный ключ в base64url — то же что
		// генерирует `npx web-push generate-vapid-keys`. Дополнительной валидации
		// не делаем: библиотека сама вернёт ошибку при первом SendNotification.
		vapidCfg = &vapidConfig{
			privateKey: privRaw,
			publicKey:  pubRaw,
			subject:    ensureMailto(subject),
		}
	})

	return vapidCfg, vapidErr
}

// SendMooovingPushToSubscriptions отправляет push на список подписок (no-op без VAPID).
// Используется при доставке нового сообщения в moooving-чате — driver/client получает
// push если не подключён по WebSocket. Payload зашифровывается через webpush-go,
// браузерный SW покажет {title, body, data}. Удаляет недействительные подписки.
func SendMooovingPushToSubscriptions(subs []models.PushSubscription, payload *PushPayload) int {
	if len(subs) == 0 {
		return 0
	}
	cfg, err := getVAPIDConfig()
	if err != nil {
		log.Printf("SendMooovingPushToSubscriptions: VAPID не настроен: %v", err)
		return 0
	}
	body := MarshalPushPayload(payload)
	sent := 0
	for i := range subs {
		sub := &subs[i]
		status, sendErr := sendWebPushNotification(cfg, sub, body)
		if sendErr != nil {
			log.Printf("SendMooovingPushToSubscriptions: ошибка %s: %v", sub.Endpoint, sendErr)
			continue
		}
		switch {
		case status == http.StatusGone || status == http.StatusNotFound:
			_ = database.RemovePushSubscriptionByEndpoint(sub.Endpoint)
		case status >= 200 && status < 300:
			sent++
			_ = database.TouchPushSubscription(sub.Endpoint)
		default:
			log.Printf("SendMooovingPushToSubscriptions: статус %d для %s", status, sub.Endpoint)
		}
	}
	return sent
}

// VAPIDPublicKey возвращает текущий VAPID public key (или пустую строку если не настроен).
// Фронту нужен этот ключ для pushManager.subscribe(applicationServerKey=...).
func VAPIDPublicKey() string {
	cfg, err := getVAPIDConfig()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.publicKey
}

// sendWebPushNotification шифрует payload через webpush-go (RFC 8291, aes128gcm)
// и доставляет его браузерному push-сервису. Возвращает HTTP-статус,
// чтобы вызывающий код мог удалить мёртвые подписки (410/404).
//
// payload может быть nil — тогда уйдёт пустой push (SW покажет generic-нотификацию).
// При ненулевом payload SW парсит JSON через event.data.json().
func sendWebPushNotification(cfg *vapidConfig, sub *models.PushSubscription, payload []byte) (int, error) {
	wpSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}

	resp, err := webpush.SendNotification(payload, wpSub, &webpush.Options{
		Subscriber:      cfg.subject,
		VAPIDPublicKey:  cfg.publicKey,
		VAPIDPrivateKey: cfg.privateKey,
		TTL:             90,
		Urgency:         webpush.UrgencyNormal,
		HTTPClient:      pushHTTPClient,
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

// MarshalPushPayload сериализует payload в JSON. nil-вход → nil-выход (пустой push).
func MarshalPushPayload(p *PushPayload) []byte {
	if p == nil {
		return nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		log.Printf("MarshalPushPayload: %v", err)
		return nil
	}
	return raw
}

func ensureMailto(subject string) string {
	if strings.HasPrefix(subject, "mailto:") {
		return subject
	}
	if strings.Contains(subject, "@") {
		return "mailto:" + subject
	}
	return subject
}
