// morada_support_handler.go — чат «посетитель ↔ поддержка сайта» morada/tudonuma.
//
// Все эндпоинты — server-to-server под тем же shared secret, что и остальная
// morada-интеграция: Rust-бекенд сам проверяет, кто пользователь (посетитель
// или админ), и только потом зовёт сюда. Реалтайм и история — через уже
// существующие WS (type=tudonuma_visitor | tudonuma_support) и
// GET /chats/:id/messages с morada-токеном.
package handlers

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// MoradaSupportOpen создаёт или возвращает чат поддержки посетителя.
// POST /api/tudonuma/support/open — body: {visitorId, visitorName?}
func MoradaSupportOpen(c *gin.Context) {
	var req struct {
		VisitorID   int64  `json:"visitorId" binding:"required"`
		VisitorName string `json:"visitorName,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	apiKey := os.Getenv("MORADA_DEFAULT_CLIENT_API_KEY")
	if apiKey == "" {
		apiKey = "morada_default_client"
	}
	chatID, created, err := database.GetOrCreateMoradaSupportChat(req.VisitorID, req.VisitorName, apiKey)
	if err != nil {
		log.Printf("MoradaSupportOpen: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"chatId": chatID.String(), "isNewChat": created})
}

// MoradaSupportList — инбокс поддержки.
// GET /api/tudonuma/support/chats?status=open|resolved|all&limit=
func MoradaSupportList(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	chats, err := database.ListMoradaSupportChats(c.DefaultQuery("status", "open"), limit)
	if err != nil {
		log.Printf("MoradaSupportList: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	counts, err := database.CountMoradaSupportChats()
	if err != nil {
		log.Printf("MoradaSupportList: counts: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{"chats": chats, "counts": counts})
}

// MoradaSupportCounts — только счётчики (бейдж в админке).
// GET /api/tudonuma/support/counts
func MoradaSupportCounts(c *gin.Context) {
	counts, err := database.CountMoradaSupportChats()
	if err != nil {
		log.Printf("MoradaSupportCounts: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, counts)
}

// MoradaSupportSetStatus — отметить чат решённым / снова открыть.
// POST /api/tudonuma/support/status — body: {chatId, resolved}
func MoradaSupportSetStatus(c *gin.Context) {
	var req struct {
		ChatID   string `json:"chatId" binding:"required"`
		Resolved bool   `json:"resolved"`
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
	if err := database.SetMoradaSupportStatus(chatID, req.Resolved); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	status := "open"
	if req.Resolved {
		status = "resolved"
	}
	if WebSocketHub != nil {
		if msg, err := websocketpkg.NewMessage("support_status", map[string]interface{}{
			"chatId": chatID.String(), "status": status,
		}); err == nil {
			WebSocketHub.SendToChat(chatID.String(), msg)
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "chatId": chatID.String(), "status": status})
}
