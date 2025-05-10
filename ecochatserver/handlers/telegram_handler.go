package handlers

import (
    "log"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/llm"
    "github.com/egor/ecochatserver/models"
    "github.com/egor/ecochatserver/websocket"
)

// AutoResponder — единственный экземпляр автоответчика
var AutoResponder *llm.AutoResponder

// InitAutoResponder инициализирует автоответчик (LLMклиент + конфиг)
func InitAutoResponder() {
    raw := os.Getenv("ENABLE_AUTO_RESPONDER")
    if raw == "" {
        raw = "true"
    }
    enabled, err := strconv.ParseBool(raw)
    if err != nil {
        log.Printf(
            "InitAutoResponder: неверное значение ENABLE_AUTO_RESPONDER=%q: %v — включаем по умолчанию",
            raw, err,
        )
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
        log.Printf("TelegramWebhook: неверный Content-Type: %s", c.GetHeader("Content-Type"))
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
    
    log.Printf("TelegramWebhook: получено сообщение: %+v", in)

    if in.UserID == "" {
        log.Printf("TelegramWebhook: отсутствует UserID")
        c.JSON(http.StatusBadRequest, gin.H{"error": "UserID обязателен"})
        return
    }
    if in.ClientID == "" {
        in.ClientID = "test_client_id"
        log.Printf("TelegramWebhook: ClientID не указан, используем: %s", in.ClientID)
    } else {
        log.Printf("TelegramWebhook: используем ClientID: %s", in.ClientID)
    }

    // Создаём или получаем чат
    log.Printf("TelegramWebhook: создаем/получаем чат для user=%s, source=%s, botID=%s, clientID=%s", 
        in.UserID, in.Source, in.BotID, in.ClientID)
    
    chat, err := database.GetOrCreateChat(
        in.UserID, in.UserName, in.UserEmail,
        in.Source, in.UserID, in.BotID, in.ClientID,
    )
    if err != nil {
        log.Printf("TelegramWebhook: GetOrCreateChat error: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    
    log.Printf("TelegramWebhook: получен чат: ID=%s, ClientID=%s, UserID=%s", 
        chat.ID, chat.ClientID, chat.User.ID)
    
    // Создаем детерминированный UUID для отправителя
    var userUUID uuid.UUID
    if parsedUUID, err := uuid.Parse(in.UserID); err == nil {
        userUUID = parsedUUID
        log.Printf("TelegramWebhook: UserID %s уже является валидным UUID", in.UserID)
    } else {
        userUUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(in.UserID))
        log.Printf("TelegramWebhook: создан детерминированный UUID для userID %s: %s", in.UserID, userUUID.String())
    }

    // Добавляем сообщение пользователя
    msgType := "text"
    if in.MessageType != "" {
        msgType = in.MessageType
    }
    
    log.Printf("TelegramWebhook: добавляем сообщение в чат %s от пользователя %s", chat.ID, userUUID)
    
    userMsg, err := database.AddMessage(
        chat.ID,
        in.Content,
        "user",
        userUUID,
        msgType,
        in.Metadata,
    )
    if err != nil {
        log.Printf("TelegramWebhook: AddMessage error: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    
    log.Printf("TelegramWebhook: сообщение добавлено: ID=%s", userMsg.ID)

    // Отправляем через WebSocket обновление списка сообщений
    log.Printf("TelegramWebhook: получаем обновленный чат для WebSocket")
    updatedChat, _, err := database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
    if err != nil {
        log.Printf("TelegramWebhook: ошибка при получении обновленного чата: %v", err)
    } else if updatedChat != nil {
        log.Printf("TelegramWebhook: отправляем WebSocket уведомление для чата %s", updatedChat.ID)
        // Исправленный вызов
        if data, err := websocket.NewChatMessage(updatedChat, userMsg); err == nil {
            WebSocketHub.BroadcastMessage(data) // Исправлено: используем BroadcastMessage
            log.Printf("TelegramWebhook: WebSocket уведомление отправлено")
        } else {
            log.Printf("TelegramWebhook: ошибка создания WebSocket сообщения: %v", err)
        }
    }

    // Генерируем автоответ, если включено
    var botText, botID string
    if AutoResponder != nil && updatedChat != nil {
        log.Printf("TelegramWebhook: генерируем автоответ")
        botMsg, err := AutoResponder.ProcessMessage(
            c.Request.Context(),
            updatedChat,
            userMsg,
        )
        if err != nil {
            log.Printf("TelegramWebhook: AutoResponder.ProcessMessage error: %v", err)
        } else if botMsg != nil {
            log.Printf("TelegramWebhook: автоответ сгенерирован, сохраняем в БД")
            botUUID := botMsg.SenderID
            
            saved, err := database.AddMessage(
                chat.ID,
                botMsg.Content,
                botMsg.Sender,
                botUUID,
                botMsg.Type,
                botMsg.Metadata,
            )
            if err != nil {
                log.Printf("TelegramWebhook: ошибка сохранения автоответа: %v", err)
            } else {
                botText = saved.Content
                botID = saved.ID.String()
                log.Printf("TelegramWebhook: автоответ сохранен: ID=%s", botID)

                // Обновлённый чат и уведомления по WebSocket
                updatedChat, _, _ = database.GetChatByID(chat.ID, 1, database.DefaultPageSize)
                if updatedChat != nil {
                    // Исправленный вызов
                    if data, err := websocket.NewChatMessage(updatedChat, saved); err == nil {
                        WebSocketHub.BroadcastMessage(data) // Исправлено: используем BroadcastMessage
                        log.Printf("TelegramWebhook: WebSocket уведомление об автоответе отправлено")
                    }
                    if widget, err := websocket.NewWidgetMessage(saved); err == nil {
                        WebSocketHub.SendToChat(chat.ID.String(), widget)
                        log.Printf("TelegramWebhook: WebSocket сообщение виджету отправлено")
                    }
                }
            }
        } else {
            log.Printf("TelegramWebhook: автоответ не сгенерирован (botMsg == nil)")
        }
    } else {
        log.Printf("TelegramWebhook: автоответчик не активен или чат не найден")
    }

    // Ответ клиенту
    response := gin.H{
        "status":          "message processed",
        "message_id":      userMsg.ID.String(),
        "chat_id":         chat.ID.String(),
        "timestamp":       time.Now().Format(time.RFC3339),
        "bot_response":    botText,
        "bot_message_id":  botID,
    }
    log.Printf("TelegramWebhook: отправляем ответ: %+v", response)
    c.JSON(http.StatusOK, response)
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