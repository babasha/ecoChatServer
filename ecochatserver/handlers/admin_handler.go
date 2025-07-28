package handlers

import (
	"net/http"
	"strconv"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetChats возвращает список чатов для админки
func GetChats(c *gin.Context) {
	// Получаем параметры пагинации
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}

	// Получаем ID клиента и админа из контекста
	clientID := uuid.Nil // Для админки получаем все чаты
	adminID := uuid.Nil  // TODO: получить из токена

	chats, total, err := database.GetChats(clientID, adminID, page-1, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Ошибка получения чатов",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chats": chats,
		"pagination": gin.H{
			"page":     page,
			"size":     size,
			"total":    total,
			"pages":    (total + size - 1) / size,
		},
	})
}

// GetChatByID возвращает конкретный чат с сообщениями
func GetChatByID(c *gin.Context) {
	chatIDStr := c.Param("id")
	chatID, err := uuid.Parse(chatIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Неверный ID чата",
		})
		return
	}

	// Получаем параметры пагинации для сообщений
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 50
	}

	chat, total, err := database.GetChatByID(chatID, page-1, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Ошибка получения чата",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chat": chat,
		"pagination": gin.H{
			"page":     page,
			"size":     size,
			"total":    total,
			"pages":    (total + size - 1) / size,
		},
	})
}