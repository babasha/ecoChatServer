package handlers

import (
    "log"
    "net/http"

    "github.com/gin-gonic/gin"
)

// GetWidgetChatMessages теперь возвращает только информацию о подключении к WebSocket
// Все данные чата виджет должен получать через WebSocket
func GetWidgetChatMessages(c *gin.Context) {
    chatIDStr := c.Param("id")
    userIDStr := c.GetHeader("X-Widget-User-ID")
    apiKey := c.GetHeader("X-API-Key")

    if chatIDStr == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "ID чата не указан"})
        return
    }

    if userIDStr == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "ID пользователя не указан"})
        return
    }

    if apiKey == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "API ключ не указан"})
        return
    }

    // Проверяем API ключ (можно оставить базовую проверку)
    // В дальнейшем виджет должен использовать только WebSocket
    
    log.Printf("GetWidgetChatMessages: перенаправление на WebSocket для чата %s", chatIDStr)
    
    // Возвращаем информацию для подключения к WebSocket
    response := gin.H{
        "websocket": gin.H{
            "url":     "/ws",
            "chatId":  chatIDStr,
            "userId":  userIDStr,
            "type":    "widget",
        },
        "message": "Используйте WebSocket для получения сообщений",
        "deprecated": "Этот REST endpoint устарел, используйте WebSocket подключение",
    }

    c.JSON(http.StatusOK, response)
}