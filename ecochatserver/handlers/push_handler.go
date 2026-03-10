package handlers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

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

type vapidConfig struct {
	privateKey *ecdsa.PrivateKey
	publicKey  string
	subject    string
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

	sent := 0
	for _, sub := range subs {
		status, sendErr := sendWebPushNotification(cfg, &sub)
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

		privBytes, err := decodeBase64URL(privRaw)
		if err != nil {
			vapidErr = fmt.Errorf("не удалось декодировать VAPID_PRIVATE_KEY: %w", err)
			return
		}
		if len(privBytes) != 32 {
			vapidErr = fmt.Errorf("ожидался приватный ключ длиной 32 байта, получили %d", len(privBytes))
			return
		}

		priv := new(ecdsa.PrivateKey)
		priv.PublicKey.Curve = elliptic.P256()
		priv.D = new(big.Int).SetBytes(privBytes)
		priv.PublicKey.X, priv.PublicKey.Y = priv.PublicKey.Curve.ScalarBaseMult(privBytes)

		pubBytes, err := decodeBase64URL(pubRaw)
		if err != nil {
			vapidErr = fmt.Errorf("не удалось декодировать VAPID_PUBLIC_KEY: %w", err)
			return
		}
		if len(pubBytes) != 65 {
			vapidErr = fmt.Errorf("ожидался публичный ключ длиной 65 байт, получили %d", len(pubBytes))
			return
		}

		if !priv.PublicKey.IsOnCurve(priv.PublicKey.X, priv.PublicKey.Y) {
			vapidErr = errors.New("полученный VAPID публичный ключ не соответствует кривой P-256")
			return
		}

		vapidCfg = &vapidConfig{
			privateKey: priv,
			publicKey:  pubRaw,
			subject:    ensureMailto(subject),
		}
	})

	return vapidCfg, vapidErr
}

func sendWebPushNotification(cfg *vapidConfig, sub *models.PushSubscription) (int, error) {
	aud, err := audienceFromEndpoint(sub.Endpoint)
	if err != nil {
		return 0, err
	}

	token, err := buildVAPIDToken(cfg.privateKey, cfg.subject, aud)
	if err != nil {
		return 0, err
	}

	// Пока отправляем пустой payload, чтобы избежать обязательного шифрования.
	httpReq, err := http.NewRequest(http.MethodPost, sub.Endpoint, nil)
	if err != nil {
		return 0, err
	}

	httpReq.Header.Set("TTL", "90")
	httpReq.Header.Set("Urgency", "normal")
	httpReq.Header.Set("Authorization", "WebPush "+token)
	httpReq.Header.Set("Crypto-Key", "p256ecdsa="+cfg.publicKey)
	httpReq.Header.Set("Content-Length", "0")
	httpReq.Header.Set("Content-Type", "application/octet-stream")

	resp, err := pushHTTPClient.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

func buildVAPIDToken(priv *ecdsa.PrivateKey, subject, aud string) (string, error) {
	headerJSON := `{"alg":"ES256","typ":"JWT"}`
	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))

	exp := time.Now().Add(12 * time.Hour).Unix()
	payloadStruct := map[string]interface{}{
		"aud": aud,
		"exp": exp,
		"sub": subject,
	}
	payloadBytes, err := json.Marshal(payloadStruct)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)

	signingInput := header + "." + payload
	hash := sha256.Sum256([]byte(signingInput))

	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		return "", err
	}

	sigBytes := make([]byte, 64)
	copyWithPadding(sigBytes[:32], r.Bytes())
	copyWithPadding(sigBytes[32:], s.Bytes())

	signature := base64.RawURLEncoding.EncodeToString(sigBytes)
	return signingInput + "." + signature, nil
}

func audienceFromEndpoint(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("не удалось разобрать endpoint: %w", err)
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host), nil
}

func decodeBase64URL(input string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(input)
}

func copyWithPadding(dst, src []byte) {
	if len(src) > len(dst) {
		copy(dst, src[len(src)-len(dst):])
		return
	}
	copy(dst[len(dst)-len(src):], src)
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
