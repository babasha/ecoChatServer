// morada_handler.go — REST API для интеграции с morada (недвижимость) Rust-бекендом.
// Чат «посетитель сайта ↔ владелец/агентство об объекте». Структурно зеркало
// moooving_handler.go:
//   - server-to-server endpoints защищены shared secret (X-Morada-Secret);
//   - frontend endpoints (visitor/agent панели) защищены JWT с Issuer=morada-chat —
//     теми же токенами, что используются для WebSocket-подключения.
package handlers

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/middleware"
	"github.com/egor/ecochatserver/models"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// moradaSecretHeader — заголовок с shared secret для server-to-server вызовов.
const moradaSecretHeader = "X-Morada-Secret"

// MoradaSharedSecretMiddleware проверяет общий секрет между ecoChat и morada Rust.
func MoradaSharedSecretMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		expected := os.Getenv("MORADA_SHARED_SECRET")
		if expected == "" {
			log.Println("MoradaSharedSecretMiddleware: MORADA_SHARED_SECRET не задан — отклоняем все запросы")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "morada integration not configured"})
			return
		}
		got := c.GetHeader(moradaSecretHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid morada secret"})
			return
		}
		c.Next()
	}
}

// MoradaTokenMiddleware валидирует JWT с Issuer=morada-chat (для frontend visitor/agent).
// Сохраняет в контекст: morada_user_type, morada_ext_id, morada_chat_id.
func MoradaTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.Query("token")
		} else if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		claims, err := middleware.ValidateToken(token)
		if err != nil || claims.Issuer != middleware.MoradaTokenIssuer {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid morada token"})
			return
		}
		c.Set("morada_user_type", claims.MoradaUserType)
		c.Set("morada_ext_id", claims.MoradaExtID)
		c.Set("morada_chat_id", claims.MoradaChatID)
		c.Next()
	}
}

// ───────────────────────────────────────────────────────────────────────────
// Server-to-server endpoints (вызываются из morada Rust)
// ───────────────────────────────────────────────────────────────────────────

// MoradaOpenChatRequest — payload для POST /api/morada/chat/open.
type MoradaOpenChatRequest struct {
	ListingID    int64  `json:"listingId" binding:"required"`
	VisitorID    int64  `json:"visitorId" binding:"required"`
	AgentID      int64  `json:"agentId,omitempty"` // 0 если владелец/агент ещё не определён
	VisitorName  string `json:"visitorName,omitempty"`
	AgentName    string `json:"agentName,omitempty"`
	ClientAPIKey string `json:"clientApiKey,omitempty"` // API key morada в ecoChat
}

// MoradaOpenChatResponse — ответ POST /api/morada/chat/open.
type MoradaOpenChatResponse struct {
	ChatID    string `json:"chatId"`
	ListingID int64  `json:"listingId"`
	IsNewChat bool   `json:"isNewChat"`
}

// MoradaOpenChat создаёт или возвращает чат «посетитель↔агент» по объекту.
// POST /api/morada/chat/open
func MoradaOpenChat(c *gin.Context) {
	var req MoradaOpenChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}

	apiKey := req.ClientAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("MORADA_DEFAULT_CLIENT_API_KEY")
	}
	if apiKey == "" {
		apiKey = "morada_default_client"
	}

	chat, err := database.GetOrCreateMoradaChat(database.MoradaChatRequest{
		ListingID:    req.ListingID,
		VisitorID:    req.VisitorID,
		AgentID:      req.AgentID,
		VisitorName:  req.VisitorName,
		AgentName:    req.AgentName,
		ClientAPIKey: apiKey,
	})
	if err != nil {
		log.Printf("MoradaOpenChat: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, MoradaOpenChatResponse{
		ChatID:    chat.ID.String(),
		ListingID: req.ListingID,
		IsNewChat: chat.IsNewChat,
	})
}

// MoradaCloseChat архивирует чат (объект снят с публикации / сделка закрыта).
// POST /api/morada/chat/close — body: {chatId}
func MoradaCloseChat(c *gin.Context) {
	var req struct {
		ChatID string `json:"chatId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	chatID, err := uuid.Parse(req.ChatID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chatId"})
		return
	}

	if err := database.CloseMoradaChat(chatID); err != nil {
		log.Printf("MoradaCloseChat: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if WebSocketHub != nil {
		closeMsg, _ := websocketpkg.NewMessage("chat_closed", map[string]interface{}{
			"chatId": chatID.String(),
			"reason": "closed",
		})
		WebSocketHub.SendToChat(chatID.String(), closeMsg)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "chatId": chatID.String()})
}

// MoradaTokenRequest — payload для POST /api/morada/chat/token.
type MoradaTokenRequest struct {
	UserType  string `json:"userType" binding:"required"` // "visitor" | "agent"
	ExtUserID int64  `json:"extUserId" binding:"required"`
	ChatID    string `json:"chatId" binding:"required"`
	TTLHours  int    `json:"ttlHours,omitempty"` // по умолчанию 24
}

// MoradaTokenResponse — ответ POST /api/morada/chat/token.
type MoradaTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// MoradaIssueToken выпускает короткоживущий JWT для подключения morada-юзера к чату.
// Вызывается из morada Rust-бекенда: фронт morada просит токен у своего бекенда,
// тот зовёт этот endpoint с shared secret и проксирует токен фронту.
// POST /api/morada/chat/token
func MoradaIssueToken(c *gin.Context) {
	var req MoradaTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	if req.UserType != "visitor" && req.UserType != "agent" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "userType must be visitor or agent"})
		return
	}
	if _, err := uuid.Parse(req.ChatID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chatId"})
		return
	}

	ttl := time.Duration(req.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}

	token, err := middleware.GenerateMoradaChatToken(req.UserType, req.ExtUserID, req.ChatID, ttl)
	if err != nil {
		log.Printf("MoradaIssueToken: ошибка генерации: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, MoradaTokenResponse{
		Token:     token,
		ExpiresAt: time.Now().Add(ttl).Format(time.RFC3339),
	})
}

// ───────────────────────────────────────────────────────────────────────────
// Frontend endpoints (visitor/agent из morada приложения)
// ───────────────────────────────────────────────────────────────────────────

// MoradaMyChats возвращает список активных чатов текущего юзера (visitor|agent).
// GET /api/morada/chats — авторизация через MoradaTokenMiddleware
func MoradaMyChats(c *gin.Context) {
	userType, _ := c.Get("morada_user_type")
	extID, _ := c.Get("morada_ext_id")
	uType, _ := userType.(string)
	uID, _ := extID.(int64)

	if uID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing identity"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit < 1 || limit > 100 {
		limit = 50
	}

	var (
		chats []database.MoradaChatSummary
		err   error
	)
	switch uType {
	case "agent":
		chats, err = database.GetMoradaChatsForAgent(uID, limit)
	case "visitor":
		chats, err = database.GetMoradaChatsForVisitor(uID, limit)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown user type"})
		return
	}
	if err != nil {
		log.Printf("MoradaMyChats: ошибка: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"chats": chats})
}

// MoradaChatMessages возвращает историю чата (доступно только участнику).
// GET /api/morada/chats/:id/messages — авторизация через MoradaTokenMiddleware
func MoradaChatMessages(c *gin.Context) {
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
	if chat.Source != database.MoradaChatSource {
		c.JSON(http.StatusForbidden, gin.H{"error": "доступ к чату запрещён"})
		return
	}

	// Проверяем, что текущий юзер действительно участник этого чата.
	userType, _ := c.Get("morada_user_type")
	extID, _ := c.Get("morada_ext_id")
	uType, _ := userType.(string)
	uID, _ := extID.(int64)

	allowed := false
	switch uType {
	case "agent":
		allowed = chat.DriverIDExt != nil && *chat.DriverIDExt == uID
	case "visitor":
		allowed = chat.ClientIDExt != nil && *chat.ClientIDExt == uID
	}
	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "доступ к чату запрещён"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chat":    chat,
		"total":   total,
		"hasMore": len(chat.Messages) >= limit,
	})
}

// deliverMoradaMessage рассылает новое сообщение обеим сторонам morada-чата.
// Обе стороны (посетитель и агент) зарегистрированы в hub.chatClients[chatID],
// поэтому достаточно широковещалки по чату — moooving-специфика не задействуется.
func deliverMoradaMessage(chatID uuid.UUID, message *models.Message) {
	if WebSocketHub == nil {
		return
	}
	payload := map[string]interface{}{
		"chatId":  chatID.String(),
		"message": createMessagePayload(message, chatID),
	}
	notification, err := websocketpkg.NewMessage("new_message", payload)
	if err != nil {
		log.Printf("deliverMoradaMessage: ошибка сборки уведомления: %v", err)
		return
	}
	WebSocketHub.SendToChat(chatID.String(), notification)
}
