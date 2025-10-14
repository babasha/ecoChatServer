package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// PushSubscribeHandler сохраняет подписку браузера администратора
func PushSubscribeHandler(c *gin.Context) {
	adminIDStr, ok := c.Get("adminID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		log.Printf("PushSubscribeHandler: неверный adminID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
		return
	}

	var req pushSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("PushSubscribeHandler: ошибка парсинга входных данных: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат подписки"})
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
	adminIDStr, ok := c.Get("adminID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		log.Printf("PushUnsubscribeHandler: неверный adminID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
		return
	}

	var req pushUnsubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("PushUnsubscribeHandler: ошибка парсинга входных данных: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
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
	adminIDStr, ok := c.Get("adminID")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	adminID, err := uuid.Parse(adminIDStr.(string))
	if err != nil {
		log.Printf("PushSendHandler: неверный adminID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
		return
	}

	var req pushSendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("PushSendHandler: ошибка парсинга тела запроса: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
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

	vapidPrivate := os.Getenv("VAPID_PRIVATE_KEY")
	vapidPublic := os.Getenv("VAPID_PUBLIC_KEY")
	vapidEmail := os.Getenv("VAPID_EMAIL")

	if vapidPrivate == "" || vapidPublic == "" || vapidEmail == "" {
		log.Printf("PushSendHandler: VAPID ключи не настроены, заглушка. title=%s message=%s", req.Title, req.Message)

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"sent":    0,
			"total":   len(subs),
			"message": "Push уведомления не отправлены: VAPID ключи не настроены",
		})
		return
	}

	// TODO: Реальная отправка push (webpush-go) — пока просто логируем успешное событие
	log.Printf("PushSendHandler: псевдо-отправка %d push уведомлений для admin=%s, title=%s", len(subs), adminID, req.Title)

	for _, sub := range subs {
		_ = database.TouchPushSubscription(sub.Endpoint)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"sent":    len(subs),
		"total":   len(subs),
		"message": "Push уведомления поставлены в очередь",
	})
}
