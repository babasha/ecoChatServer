// moooving_handler.go — REST API для интеграции с moooving Rust-сервером.
// Все server-to-server endpoints защищены shared secret через заголовок
// X-Moooving-Secret (значение из env MOOOVING_SHARED_SECRET).
//
// Frontend-endpoints (для driver/client панелей в moooving приложении)
// защищаются JWT с Issuer=moooving-chat — те же токены, что используются
// для WebSocket подключения.
package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/middleware"
)

// mooovingSecretHeader — заголовок с shared secret для server-to-server вызовов.
const mooovingSecretHeader = "X-Moooving-Secret"

// MooovingSharedSecretMiddleware проверяет общий секрет между ecoChat и moooving Rust.
func MooovingSharedSecretMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("MOOOVING_SHARED_SECRET")
		if expected == "" {
			log.Println("MooovingSharedSecretMiddleware: MOOOVING_SHARED_SECRET не задан — отклоняем все запросы")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "moooving integration not configured"})
			return
		}

		got := c.GetHeader(mooovingSecretHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid moooving secret"})
			return
		}

		c.Next()
	}
}

// MooovingTokenMiddleware валидирует JWT с Issuer=moooving-chat (для frontend driver/client).
// Сохраняет в контекст: moooving_user_type, moooving_ext_user_id, moooving_order_id.
func MooovingTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.Query("token")
		} else {
			// strip "Bearer "
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}

		claims, err := middleware.ValidateToken(token)
		if err != nil || claims.Issuer != middleware.MooovingTokenIssuer {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid moooving token"})
			return
		}

		c.Set("moooving_user_type", claims.UserType)
		c.Set("moooving_ext_user_id", claims.ExtUserID)
		c.Set("moooving_order_id", claims.OrderID)
		c.Next()
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Server-to-server endpoints (вызываются из moooving Rust)
// ───────────────────────────────────────────────────────────────────────────

// MooovingOpenChatRequest — payload для POST /api/moooving/chat/open.
type MooovingOpenChatRequest struct {
	OrderID      int64  `json:"orderId" binding:"required"`
	ClientID     int64  `json:"clientId" binding:"required"`
	DriverID     int64  `json:"driverId,omitempty"` // 0 если водитель ещё не назначен
	ClientName   string `json:"clientName,omitempty"`
	DriverName   string `json:"driverName,omitempty"`
	ClientAPIKey string `json:"clientApiKey,omitempty"` // API key компании в ecoChat (если несколько брендов)
}

// MooovingOpenChatResponse — ответ POST /api/moooving/chat/open.
type MooovingOpenChatResponse struct {
	ChatID    string `json:"chatId"`
	OrderID   int64  `json:"orderId"`
	IsNewChat bool   `json:"isNewChat"`
}

// MooovingOpenChat создаёт или возвращает чат для заказа moooving.
// POST /api/moooving/chat/open
func MooovingOpenChat(c *gin.Context) {
	var req MooovingOpenChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	apiKey := req.ClientAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("MOOOVING_DEFAULT_CLIENT_API_KEY")
	}
	if apiKey == "" {
		apiKey = "moooving_default_client"
	}

	chat, err := database.GetOrCreateMooovingChat(database.MooovingChatRequest{
		OrderID:      req.OrderID,
		ClientID:     req.ClientID,
		DriverID:     req.DriverID,
		ClientName:   req.ClientName,
		DriverName:   req.DriverName,
		ClientAPIKey: apiKey,
	})
	if err != nil {
		log.Printf("MooovingOpenChat: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, MooovingOpenChatResponse{
		ChatID:    chat.ID.String(),
		OrderID:   req.OrderID,
		IsNewChat: chat.IsNewChat,
	})
}

// MooovingCloseChat архивирует чат заказа (вызывается при order.completed).
// POST /api/moooving/chat/close — body: {orderId} или {chatId}
func MooovingCloseChat(c *gin.Context) {
	var req struct {
		OrderID int64  `json:"orderId,omitempty"`
		ChatID  string `json:"chatId,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	var chatID uuid.UUID
	if req.ChatID != "" {
		parsed, err := uuid.Parse(req.ChatID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chatId"})
			return
		}
		chatID = parsed
	} else if req.OrderID > 0 {
		chat, err := database.GetMooovingChatByOrderID(req.OrderID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if chat == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "chat not found"})
			return
		}
		chatID = chat.ID
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "orderId or chatId required"})
		return
	}

	if err := database.CloseMooovingChat(chatID); err != nil {
		log.Printf("MooovingCloseChat: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Уведомляем всех участников и отключаем WS-клиентов чата
	if WebSocketHub != nil {
		closeMsg, _ := json.Marshal(map[string]interface{}{
			"type": "chat_closed",
			"payload": map[string]interface{}{
				"chatId": chatID.String(),
				"reason": "order_completed",
			},
		})
		// Сначала всем подключенным к чату (включая admin-наблюдателя)
		WebSocketHub.SendToChat(chatID.String(), closeMsg)
		// Затем отключаем moooving-клиентов
		WebSocketHub.CloseMooovingChat(chatID.String(), closeMsg)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "chatId": chatID.String()})
}

// MooovingUnreadRequest — server-to-server batch-запрос непрочитанных по заказам.
type MooovingUnreadRequest struct {
	OrderIDs  []int64 `json:"orderIds" binding:"required"`
	UserType  string  `json:"userType" binding:"required"` // "driver" | "client"
	ExtUserID int64   `json:"extUserId,omitempty"`         // не используется в подсчёте, но идентифицирует viewer'a в логах
}

// MooovingUnread возвращает map orderID → unread count для viewer'а.
// POST /api/moooving/chats/unread (server-to-server)
func MooovingUnread(c *gin.Context) {
	var req MooovingUnreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.UserType != "driver" && req.UserType != "client" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userType must be driver or client"})
		return
	}
	if len(req.OrderIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"unread": map[string]int{}})
		return
	}

	// viewerSender: что в БД лежит как messages.sender для этого viewer'a.
	// Считаем непрочитанные = sender противоположной стороны.
	viewerSender := "user"
	if req.UserType == "driver" {
		viewerSender = "driver"
	}

	rows, err := database.GetMooovingUnreadByOrders(req.OrderIDs, viewerSender)
	if err != nil {
		log.Printf("MooovingUnread: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Возвращаем как map для удобства фронта (orderId → count, отсутствующие = 0)
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[strconv.FormatInt(r.OrderID, 10)] = r.Count
	}
	c.JSON(http.StatusOK, gin.H{"unread": out})
}

// MooovingTokenRequest — payload для POST /api/moooving/chat/token.
type MooovingTokenRequest struct {
	UserType  string `json:"userType" binding:"required"` // "driver" | "client"
	ExtUserID int64  `json:"extUserId" binding:"required"`
	OrderID   int64  `json:"orderId" binding:"required"`
	TTLHours  int    `json:"ttlHours,omitempty"` // по умолчанию 24
}

// MooovingTokenResponse — ответ POST /api/moooving/chat/token.
type MooovingTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// MooovingIssueToken выпускает короткоживущий JWT для подключения moooving-юзера к чату.
// Вызывается из moooving Rust-сервера: фронт moooving просит токен у своего бекенда,
// тот зовёт этот endpoint с shared secret и проксирует токен фронту.
// POST /api/moooving/chat/token
func MooovingIssueToken(c *gin.Context) {
	var req MooovingTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.UserType != "driver" && req.UserType != "client" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userType must be driver or client"})
		return
	}

	ttl := time.Duration(req.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	token, err := middleware.GenerateMooovingChatToken(req.UserType, req.ExtUserID, req.OrderID, ttl)
	if err != nil {
		log.Printf("MooovingIssueToken: ошибка генерации: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, MooovingTokenResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(ttl).Format(time.RFC3339),
	})
}

// ───────────────────────────────────────────────────────────────────────────
// Frontend endpoints (driver/client из moooving приложения)
// ───────────────────────────────────────────────────────────────────────────

// ── Push notifications (server-to-server для moooving) ──────────────────────

// MooovingPushSubscribeRequest — ставит подписку для moooving-юзера.
type MooovingPushSubscribeRequest struct {
	UserType  string `json:"userType" binding:"required"` // driver | client
	ExtUserID int64  `json:"extUserId" binding:"required"`

	Endpoint string `json:"endpoint" binding:"required"`
	Keys     struct {
		P256dh string `json:"p256dh" binding:"required"`
		Auth   string `json:"auth" binding:"required"`
	} `json:"keys" binding:"required"`
}

// MooovingPushSubscribe сохраняет подписку moooving driver/client (server-to-server).
// POST /api/moooving/push/subscribe
func MooovingPushSubscribe(c *gin.Context) {
	var req MooovingPushSubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	source := mooovingSourceFor(req.UserType)
	if source == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userType must be driver or client"})
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"endpoint": req.Endpoint,
		"keys":     map[string]string{"p256dh": req.Keys.P256dh, "auth": req.Keys.Auth},
	})

	if err := database.SaveMooovingPushSubscription(source, req.ExtUserID, req.Endpoint, req.Keys.P256dh, req.Keys.Auth, payload); err != nil {
		log.Printf("MooovingPushSubscribe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MooovingPushUnsubscribeRequest — удаляет подписку.
type MooovingPushUnsubscribeRequest struct {
	UserType  string `json:"userType" binding:"required"`
	ExtUserID int64  `json:"extUserId" binding:"required"`
	Endpoint  string `json:"endpoint" binding:"required"`
}

// MooovingPushUnsubscribe удаляет подписку (server-to-server).
// POST /api/moooving/push/unsubscribe
func MooovingPushUnsubscribe(c *gin.Context) {
	var req MooovingPushUnsubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	source := mooovingSourceFor(req.UserType)
	if source == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userType must be driver or client"})
		return
	}
	if err := database.RemoveMooovingPushSubscription(source, req.ExtUserID, req.Endpoint); err != nil {
		log.Printf("MooovingPushUnsubscribe: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MooovingVAPIDKey возвращает публичный VAPID ключ (server-to-server: фронт moooving
// получает его через свой Rust-бекенд, чтобы зашифровать subscription).
// GET /api/moooving/push/vapid-key
func MooovingVAPIDKey(c *gin.Context) {
	pub := VAPIDPublicKey()
	if pub == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "VAPID not configured"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"publicKey": pub})
}

// mooovingSourceFor возвращает database.MooovingDriverSource | MooovingClientSource | "".
func mooovingSourceFor(userType string) string {
	switch userType {
	case "driver":
		return database.MooovingDriverSource
	case "client":
		return database.MooovingClientSource
	}
	return ""
}

// MooovingMyChats возвращает список активных чатов текущего юзера (driver или client).
// GET /api/moooving/chats — авторизация через MooovingTokenMiddleware
func MooovingMyChats(c *gin.Context) {
	userType, _ := c.Get("moooving_user_type")
	extID, _ := c.Get("moooving_ext_user_id")

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 100 {
		limit = 50
	}

	uType, _ := userType.(string)
	uID, _ := extID.(int64)

	if uID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing identity"})
		return
	}

	var (
		chats []database.MooovingChatSummary
		err   error
	)
	switch uType {
	case "driver":
		chats, err = database.GetMooovingChatsForDriver(uID, limit)
	case "client":
		chats, err = database.GetMooovingChatsForClient(uID, limit)
	default:
		err = errors.New("unknown user type")
	}
	if err != nil {
		log.Printf("MooovingMyChats: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"chats": chats})
}

// MooovingChatMessages возвращает историю чата (доступно только участнику).
// GET /api/moooving/chats/:id/messages — авторизация через MooovingTokenMiddleware
func MooovingChatMessages(c *gin.Context) {
	chatID, err := parseChatID(c)
	if err != nil {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	before := c.Query("before")

	chat, total, err := database.GetChatByID(chatID, limit, before)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "чат не найден"})
		return
	}

	// Проверяем, что текущий юзер действительно участник этого чата
	userType, _ := c.Get("moooving_user_type")
	extID, _ := c.Get("moooving_ext_user_id")
	uType, _ := userType.(string)
	uID, _ := extID.(int64)

	allowed := false
	switch uType {
	case "driver":
		allowed = chat.DriverIDExt != nil && *chat.DriverIDExt == uID
	case "client":
		allowed = chat.ClientIDExt != nil && *chat.ClientIDExt == uID
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "доступ к чату запрещён"})
		return
	}

	// Lazy-перевод истории на язык viewer'а (если уже знаем его язык из metadata)
	if Translator != nil {
		var viewerLang string
		switch uType {
		case "driver":
			viewerLang, _ = database.GetDetectedLanguageBySender(chatID, "driver")
		case "client":
			viewerLang, _ = database.GetClientLanguageFromChat(chatID)
		}
		if viewerLang != "" {
			if err := Translator.TranslateMessagesForWidget(c.Request.Context(), chat.Messages, viewerLang); err != nil {
				log.Printf("MooovingChatMessages: ошибка перевода истории: %v", err)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"chat":    chat,
		"total":   total,
		"hasMore": len(chat.Messages) >= limit,
	})
}
