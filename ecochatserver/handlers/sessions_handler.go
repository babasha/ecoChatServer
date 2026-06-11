package handlers

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SessionInfo представляет информацию об активной сессии
type SessionInfo struct {
	ID            string `json:"id"`
	ClientType    string `json:"clientType"` // "admin" | "widget" | "unknown"
	IP            string `json:"ip"`
	UserAgent     string `json:"userAgent"`
	ConnectedAt   string `json:"connectedAt"`
	Duration      int    `json:"duration"` // в секундах
	LastActivity  string `json:"lastActivity"`
	MessagesCount int    `json:"messagesCount"`
	Status        string `json:"status"` // "active" | "idle"
}

// GetActiveSessions возвращает список активных WebSocket сессий
func GetActiveSessions(c *gin.Context) {
	if WebSocketHub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":    "WebSocket hub not initialized",
			"sessions": []SessionInfo{},
		})
		return
	}

	// Получаем все активные клиенты из хаба
	sessions := make([]SessionInfo, 0)

	// Используем метод GetAllClients из Hub для получения информации о клиентах
	clients := WebSocketHub.GetAllClients()

	for _, client := range clients {
		// Определяем тип клиента
		clientType := "unknown"
		if client.ClientType == "admin" {
			clientType = "admin"
		} else if client.ClientType == "widget" {
			clientType = "widget"
		}

		// Определяем статус (активный или простаивающий)
		status := "active"
		idleThreshold := 2 * time.Minute
		lastActivity := client.LastActivity()
		if time.Since(lastActivity) > idleThreshold {
			status = "idle"
		}

		// Вычисляем длительность подключения
		duration := int(time.Since(client.ConnectedAt).Seconds())

		session := SessionInfo{
			ID:            client.ID,
			ClientType:    clientType,
			IP:            client.IP,
			UserAgent:     client.UserAgent,
			ConnectedAt:   client.ConnectedAt.Format(time.RFC3339),
			Duration:      duration,
			LastActivity:  lastActivity.Format(time.RFC3339),
			MessagesCount: client.MessageCount(),
			Status:        status,
		}

		sessions = append(sessions, session)
	}

	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// DisconnectSession отключает клиента по ID
func DisconnectSession(c *gin.Context) {
	sessionID := c.Param("id")

	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Session ID is required"})
		return
	}

	if WebSocketHub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WebSocket hub not initialized"})
		return
	}

	// Ищем клиента и отключаем его
	client := WebSocketHub.GetClientByID(sessionID)

	if client == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Session not found"})
		return
	}

	// Закрываем соединение
	log.Printf("DisconnectSession: принудительное отключение клиента %s по запросу админа", sessionID)

	// Отправляем клиенту сообщение о принудительном отключении
	disconnectMessage := map[string]interface{}{
		"type":    "force_disconnect",
		"message": "You have been disconnected by an administrator",
	}

	if client.TrySend(disconnectMessage) {
		log.Printf("DisconnectSession: отправлено сообщение о принудительном отключении клиенту %s", sessionID)
	} else {
		log.Printf("DisconnectSession: не удалось отправить сообщение клиенту %s, канал недоступен", sessionID)
	}

	// Закрываем соединение после небольшой задержки, чтобы сообщение успело дойти
	time.AfterFunc(500*time.Millisecond, func() {
		client.Disconnect()
		log.Printf("DisconnectSession: клиент %s отключен", sessionID)
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Session disconnected",
	})
}
