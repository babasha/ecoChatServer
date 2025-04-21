package handlers

import (
    "log"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/llm"
    "github.com/egor/ecochatserver/models"
    "github.com/egor/ecochatserver/websocket"
)

// AutoResponder — единственный экземпляр автоответчика
var AutoResponder *llm.AutoResponder

// InitAutoResponder инициализирует автоответчик (LLM-клиент + конфиг)
func InitAutoResponder() {
    raw := os.Getenv("ENABLE_AUTO_RESPONDER")
    if raw == "" {
        raw = "true"
    }
    enabled, err := strconv.ParseBool(raw)
    if err != nil {
        log.Printf("InitAutoResponder: неверное значение ENABLE_AUTO_RESPONDER=%q: %v — включаем по умолчанию", raw, err)
        enabled = true
    }
    if !enabled {
        log.Println("Автоответчик отключен в настройках")
        return
    }

    client := llm.NewLLMClient()
    cfg := llm.GetDefaultConfig()
    AutoResponder = llm.NewAutoResponder(client, cfg)
    log.Println("Автоответчик успешно инициализирован")
}

// TelegramWebhook обрабатывает вебхук Telegram и виджета
func TelegramWebhook(c *gin.Context) {
    log.Printf("TelegramWebhook: %s %s from %s", c.Request.Method, c.FullPath(), c.ClientIP())

    // OPTIONS для CORS
    if c.Request.Method == http.MethodOptions {
        handleCORS(c)
        c.Status(http.StatusOK)
        return
    }
    handleCORS(c)

    // Проверяем Content-Type
    if !strings.Contains(c.GetHeader("Content-Type"), "application/json") {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type должен быть application/json"})
        return
    }

    // Парсим входящее сообщение
    var in models.IncomingMessage
    if err := c.ShouldBindJSON(&in); err != nil {
        log.Printf("TelegramWebhook: ошибка парсинга JSON: %v", err)
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    if in.UserID == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "UserID обязателен"})
        return
    }
    if in.ClientID == "" {
        in.ClientID = "test_client_id"
    }

    // Создаём или получаем чат
    chat, _, err := database.CreateOrGetChat(
        in.UserID, in.UserName, in.UserEmail,
        in.Source, in.UserID, in.BotID, in.ClientID,
    )
    if err != nil {
        log.Printf("TelegramWebhook: CreateOrGetChat: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    // Добавляем сообщение пользователя
    msgType := "text"
    if in.MessageType != "" {
        msgType = in.MessageType
    }
    userMsg, err := database.AddMessage(
        chat.ID, in.Content, "user", in.UserID, msgType, in.Metadata,
    )
    if err != nil {
        log.Printf("TelegramWebhook: AddMessage: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    // Отправляем через WebSocket обновление
    updatedChat, _, _ := database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
    if updatedChat != nil {
        if data, err := websocket.NewChatMessage(updatedChat, userMsg); err == nil {
            WebSocketHub.Broadcast(data)
        }
    }

    // Генерируем автоответ, если включено
    var botText, botID string
    if AutoResponder != nil && updatedChat != nil {
        botMsg, err := AutoResponder.ProcessMessage(c.Request.Context(), updatedChat, userMsg)
        if err != nil {
            log.Printf("TelegramWebhook: AutoResponder.ProcessMessage: %v", err)
        } else if botMsg != nil {
            saved, err := database.AddMessage(
                chat.ID, botMsg.Content, botMsg.Sender, botMsg.SenderID, botMsg.Type, botMsg.Metadata,
            )
            if err != nil {
                log.Printf("TelegramWebhook: сохранение автоответа: %v", err)
            } else {
                botText = saved.Content
                botID = saved.ID

                // Обновлённый чат и уведомления по WebSocket
                updatedChat, _, _ = database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
                if updatedChat != nil {
                    if data, err := websocket.NewChatMessage(updatedChat, saved); err == nil {
                        WebSocketHub.Broadcast(data)
                    }
                    if widget, err := websocket.NewWidgetMessage(saved); err == nil {
                        WebSocketHub.SendToChat(chat.ID, widget)
                    }
                }
            }
        }
    }

    // Ответ клиенту
    c.JSON(http.StatusOK, gin.H{
        "status":          "message processed",
        "message_id":      userMsg.ID,
        "chat_id":         chat.ID,
        "timestamp":       time.Now().Format(time.RFC3339),
        "bot_response":    botText,
        "bot_message_id":  botID,
    })
}

// handleCORS выставляет стандартные CORS заголовки
func handleCORS(c *gin.Context) {
    origin := c.GetHeader("Origin")
    if origin == "" {
        origin = "*"
    }
    c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
    c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
    c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
    c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
    c.Writer.Header().Set("Access-Control-Max-Age", "86400")
}