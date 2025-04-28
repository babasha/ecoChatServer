package handlers

import (
    "log"
    "net/http"
    "strconv"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"

    "github.com/egor/ecochatserver/database"
)

// GetWidgetChatMessages возвращает историю сообщений чата для виджета
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

    // Проверяем, валидный ли API ключ
    _, err := database.EnsureClientWithAPIKey(apiKey, "")
    if err != nil {
        log.Printf("GetWidgetChatMessages: ошибка проверки API ключа: %v", err)
        c.JSON(http.StatusUnauthorized, gin.H{"error": "Неверный API ключ"})
        return
    }

    // Преобразуем строковый ID чата в UUID
    chatID, err := uuid.Parse(chatIDStr)
    if err != nil {
        log.Printf("GetWidgetChatMessages: ошибка преобразования chatID в UUID: %v", err)
        c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат ID чата"})
        return
    }

    // Проверяем, принадлежит ли чат этому пользователю и клиенту
    var userUUID uuid.UUID
    if parsedUUID, err := uuid.Parse(userIDStr); err == nil {
        userUUID = parsedUUID
    } else {
        // Создаем детерминированный UUID на основе userID
        userUUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(userIDStr))
    }

    // Получаем параметры пагинации для сообщений (опционально)
    page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
    pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", strconv.Itoa(database.DefaultPageSize)))

    log.Printf("Запрос на получение сообщений чата %s от пользователя: %s (страница: %d, размер: %d)",
        chatIDStr, userIDStr, page, pageSize)

    // Получаем чат из базы данных
    chat, totalMessages, err := database.GetChatByID(chatID, page, pageSize)
    if err != nil {
        log.Printf("GetWidgetChatMessages: ошибка получения чата %s: %v", chatIDStr, err)
        c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден"})
        return
    }

    // Проверяем, принадлежит ли чат запрашивающему пользователю
    // В реальном приложении вы можете добавить более сложную проверку
    if chat.User.ID != userUUID && chat.User.SourceID != userIDStr {
        // Для безопасности не сообщаем, что чат существует, но не принадлежит пользователю
        log.Printf("GetWidgetChatMessages: доступ запрещен, пользователь %s пытается получить чат %s",
            userIDStr, chatIDStr)
        c.JSON(http.StatusNotFound, gin.H{"error": "Чат не найден"})
        return
    }

    // Рассчитываем общее количество страниц
    totalPages := (totalMessages + pageSize - 1) / pageSize
    if totalPages < 1 {
        totalPages = 1
    }

    // Формируем ответ с пагинацией и только нужными полями для виджета
    response := struct {
        Messages    []map[string]interface{} `json:"messages"`
        Page        int                      `json:"page"`
        PageSize    int                      `json:"pageSize"`
        TotalItems  int                      `json:"totalMessages"`
        TotalPages  int                      `json:"totalPages"`
        ChatID      string                   `json:"chatId"`
        UserID      string                   `json:"userId"`
    }{
        Page:       page,
        PageSize:   pageSize,
        TotalItems: totalMessages,
        TotalPages: totalPages,
        ChatID:     chat.ID.String(),
        UserID:     userIDStr,
    }

    // Преобразуем сообщения в более простой формат для виджета
    simplifiedMessages := make([]map[string]interface{}, 0, len(chat.Messages))
    for _, msg := range chat.Messages {
        simplifiedMessages = append(simplifiedMessages, map[string]interface{}{
            "id":        msg.ID.String(),
            "content":   msg.Content,
            "sender":    msg.Sender,
            "timestamp": msg.Timestamp,
            "type":      msg.Type,
        })
    }
    response.Messages = simplifiedMessages

    log.Printf("Успешно получены сообщения чата %s: всего %d (страница %d из %d)",
        chatIDStr, len(simplifiedMessages), page, totalPages)
    c.JSON(http.StatusOK, response)
}