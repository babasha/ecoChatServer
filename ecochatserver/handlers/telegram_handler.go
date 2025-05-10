package handlers

import (
    "fmt"
    "log"
    "net/http"
    "os"
    "strconv"
    "strings"
    "sync"
    "time"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/database/queries"
    "github.com/egor/ecochatserver/llm"
    "github.com/egor/ecochatserver/models"
    "github.com/egor/ecochatserver/websocket"
)

// AutoResponder — единственный экземпляр автоответчика
var AutoResponder *llm.AutoResponder

// Простое хранилище для дедупликации в памяти
var (
    recentMessages sync.Map // key: messageHash, value: time.Time
    messageCleanup sync.Once
)

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

// Функции для дедупликации
func isRecentMessage(hash string) bool {
    if val, exists := recentMessages.Load(hash); exists {
        if timestamp, ok := val.(time.Time); ok {
            return time.Since(timestamp) < 5*time.Second
        }
    }
    return false
}

func registerMessage(hash string) {
    recentMessages.Store(hash, time.Now())
    
    // Запускаем очистку только один раз
    messageCleanup.Do(func() {
        go cleanupRecentMessages()
    })
}

func cleanupRecentMessages() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for range ticker.C {
        now := time.Now()
        recentMessages.Range(func(key, value interface{}) bool {
            if timestamp, ok := value.(time.Time); ok {
                if now.Sub(timestamp) > 10*time.Second {
                    recentMessages.Delete(key)
                }
            }
            return true
        })
    }
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

    // ПРОСТОЕ РЕШЕНИЕ: Создаем уникальный ID для сообщения
    messageHash := fmt.Sprintf("%s_%s_%d", 
        in.UserID, 
        in.Content, 
        time.Now().Unix()/10) // группируем по 10-секундным интервалам
    
    // Проверяем, было ли такое сообщение недавно
    if isRecentMessage(messageHash) {
        log.Printf("TelegramWebhook: дублирующее сообщение пропущено")
        c.JSON(http.StatusOK, gin.H{
            "status": "duplicate_ignored",
            "message": "Сообщение уже обработано",
        })
        return
    }
    
    // Регистрируем сообщение как обработанное
    registerMessage(messageHash)

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

    // Быстро обновляем время чата
    if err := queries.UpdateChatTimestamp(database.DB, chat.ID); err != nil {
        log.Printf("TelegramWebhook: ошибка обновления времени: %v", err)
    }

    // Генерируем автоответ, если включено
    var botMsg *models.Message
    if AutoResponder != nil {
        log.Printf("TelegramWebhook: генерируем автоответ")
        
        // Загружаем минимальную информацию о чате для автоответчика
        lightChat, err := queries.GetChatLightweight(database.DB, chat.ID)
        if err != nil {
            log.Printf("TelegramWebhook: ошибка загрузки чата: %v", err)
            lightChat = chat // Используем уже загруженный чат
        }
        
        botMsg, err = AutoResponder.ProcessMessage(
            c.Request.Context(),
            lightChat,
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
                botMsg = saved
                log.Printf("TelegramWebhook: автоответ сохранен: ID=%s", botMsg.ID)

                // Обновляем время чата
                if err := queries.UpdateChatTimestamp(database.DB, chat.ID); err != nil {
                    log.Printf("TelegramWebhook: ошибка обновления времени: %v", err)
                }
            }
        } else {
            log.Printf("TelegramWebhook: автоответ не сгенерирован (botMsg == nil)")
        }
    } else {
        log.Printf("TelegramWebhook: автоответчик не активен")
    }

    // ВАЖНО: Отправляем только ОДНО комплексное WebSocket сообщение
    if userMsg != nil {
        notification := createChatNotification(chat.ID, userMsg, botMsg)
        WebSocketHub.SendToChat(chat.ID.String(), notification)
        log.Printf("TelegramWebhook: комплексное WebSocket уведомление отправлено")
    }

    // Ответ клиенту
    response := gin.H{
        "status":          "message processed",
        "message_id":      userMsg.ID.String(),
        "chat_id":         chat.ID.String(),
        "timestamp":       time.Now().Format(time.RFC3339),
    }
    
    if botMsg != nil {
        response["bot_response"] = botMsg.Content
        response["bot_message_id"] = botMsg.ID.String()
    }
    
    log.Printf("TelegramWebhook: отправляем ответ: %+v", response)
    c.JSON(http.StatusOK, response)
}

// createChatNotification создает комплексное уведомление для WebSocket
func createChatNotification(chatID uuid.UUID, userMsg, botMsg *models.Message) []byte {
    payload := map[string]interface{}{
        "type":      "chat_update",
        "chatId":    chatID.String(),
        "userMessage": map[string]interface{}{
            "id":        userMsg.ID.String(),
            "content":   userMsg.Content,
            "sender":    userMsg.Sender,
            "timestamp": userMsg.Timestamp.Format(time.RFC3339),
            "type":      userMsg.Type,
        },
        "timestamp": time.Now().Format(time.RFC3339),
    }
    
    if botMsg != nil {
        payload["botMessage"] = map[string]interface{}{
            "id":        botMsg.ID.String(),
            "content":   botMsg.Content,
            "sender":    botMsg.Sender,
            "timestamp": botMsg.Timestamp.Format(time.RFC3339),
            "type":      botMsg.Type,
            "metadata":  botMsg.Metadata,
        }
    }
    
    msg, _ := websocket.NewMessage("chat_update", payload)
    return msg
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