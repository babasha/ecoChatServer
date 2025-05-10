package handlers

import (
    "context"
    "encoding/json"
    "log"
    "net/http"
    "os"
    "strings"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/gorilla/websocket"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/middleware"
    "github.com/egor/ecochatserver/models"
    websocketpkg "github.com/egor/ecochatserver/websocket"
)

// wsUpgrader апгрейдит HTTP→WebSocket с улучшенной проверкой Origin
var wsUpgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     checkOrigin,
}

// checkOrigin проверяет, разрешен ли Origin для подключения
func checkOrigin(r *http.Request) bool {
    origin := r.Header.Get("Origin")
    if origin == "" {
        // Разрешаем локальные подключения без Origin
        host := r.Host
        if strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") {
            return true
        }
        return false
    }

    // Получаем разрешенные origins из переменных окружения
    allowedOrigins := []string{}
    
    // Основной URL фронтенда
    if frontendURL := os.Getenv("FRONTEND_URL"); frontendURL != "" {
        allowedOrigins = append(allowedOrigins, frontendURL)
    }
    
    // Дополнительные разрешенные origins
    if additional := os.Getenv("ADDITIONAL_ALLOWED_ORIGINS"); additional != "" {
        for _, url := range strings.Split(additional, ",") {
            url = strings.TrimSpace(url)
            if url != "" {
                allowedOrigins = append(allowedOrigins, url)
            }
        }
    }
    
    // Проверяем, есть ли origin в списке разрешенных
    for _, allowed := range allowedOrigins {
        if allowed == origin {
            return true
        }
    }
    
    // Для разработки можно разрешить все origins
    if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
        log.Printf("ВНИМАНИЕ: Разрешен origin %s (ALLOW_ALL_ORIGINS=true)", origin)
        return true
    }
    
    log.Printf("Отклонен origin: %s", origin)
    return false
}

// ServeWs обрабатывает WebSocket соединение
func ServeWs(c *gin.Context) {
    log.Printf("ServeWs: новое соединение от %s, origin: %s", 
        c.ClientIP(), c.Request.Header.Get("Origin"))

    // Получаем параметры и токен
    token := c.Query("token")
    clientType := c.DefaultQuery("type", "admin")
    chatIDStr := c.Query("chat_id")

    // Для виджета обязательно нужен chat_id
    if clientType == "widget" && chatIDStr == "" {
        log.Printf("ServeWs: ошибка для виджета - отсутствует chat_id")
        c.JSON(http.StatusBadRequest, gin.H{"error": "Для виджета обязателен параметр chat_id"})
        return
    }

    // Проверяем токен для админа
    var adminID, clientID, chatID uuid.UUID
    var err error

    if clientType == "admin" && token != "" {
        // Валидируем JWT токен
        claims, err := middleware.ValidateToken(token)
        if err != nil {
            log.Printf("ServeWs: ошибка валидации токена: %v", err)
            c.JSON(http.StatusUnauthorized, gin.H{"error": "Неверный токен"})
            return
        }
        
        adminID, err = uuid.Parse(claims.AdminID)
        if err != nil {
            log.Printf("ServeWs: ошибка парсинга adminID: %v", err)
            c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
            return
        }
        
        clientID, err = uuid.Parse(claims.ClientID)
        if err != nil {
            log.Printf("ServeWs: ошибка парсинга clientID: %v", err)
            c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный clientID"})
            return
        }
        
        // Сохраняем данные в контексте для использования в обработчиках
        c.Set("adminID", claims.AdminID)
        c.Set("clientID", claims.ClientID)
        c.Set("role", claims.Role)
        
        log.Printf("ServeWs: аутентифицирован admin %s (client: %s)", adminID, clientID)
    } else if clientType == "widget" {
        // Для виджета проверяем существование чата
        chatID, err = uuid.Parse(chatIDStr)
        if err != nil {
            log.Printf("ServeWs: ошибка парсинга chatID: %v", err)
            c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат chatID"})
            return
        }
        
        // Получаем userID из заголовка для виджета, если есть
        userIDStr := c.GetHeader("X-Widget-User-ID")
        if userIDStr != "" {
            adminID, _ = uuid.Parse(userIDStr)
        }
        
        log.Printf("ServeWs: подключение виджета, chatID: %s, userID: %s", chatID, adminID)
    } else {
        log.Printf("ServeWs: неверный тип клиента или отсутствует токен")
        c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный тип клиента или отсутствует токен"})
        return
    }

    // Апгрейдим соединение
    conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        log.Printf("ServeWs: ошибка апгрейда соединения: %v", err)
        return
    }

    // Создаем нового клиента
    client := websocketpkg.NewClient(WebSocketHub, conn, clientType, adminID, chatID)
    client.Context = c

    // Регистрируем клиента в хабе
    WebSocketHub.Register <- client

    // Запускаем горутины обработки
    go client.WritePump()
    go client.ReadPump(processWebSocketMessage)

    // Отправляем статус подключения
    WebSocketHub.SendConnectionStatus(client, true)
    
    log.Printf("ServeWs: клиент %s успешно подключен", client.ID)
}

// processWebSocketMessage обрабатывает входящие WebSocket сообщения
func processWebSocketMessage(client *websocketpkg.Client, raw []byte) {
    var msg websocketpkg.WebSocketMessage
    if err := json.Unmarshal(raw, &msg); err != nil {
        client.SendError("invalid_json", "Некорректный формат JSON")
        return
    }

    // Получаем данные из контекста Gin
    ginCtx := client.Context

    switch msg.Type {
    case "getChats":
        processGetChats(client, msg.Payload, ginCtx)
    case "getChatByID":
        processGetChatByID(client, msg.Payload, ginCtx)
    case "sendMessage":
        processSendMessage(client, msg.Payload, ginCtx)
    case "markAsRead":
        processMarkAsRead(client, msg.Payload, ginCtx)
    case "typing":
        processTypingStatus(client, msg.Payload, ginCtx)
    case "getWidgetMessages":
        processGetWidgetMessages(client, msg.Payload, ginCtx)
    default:
        client.SendError("unknown_type", "Неизвестный тип сообщения: "+msg.Type)
    }
}

// processSendMessage обрабатывает отправку сообщений с автоответчиком
func processSendMessage(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        ChatID  string                 `json:"chatID"`
        Content string                 `json:"content"`
        Type    string                 `json:"type"`
        Metadata map[string]interface{} `json:"metadata,omitempty"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для sendMessage")
        return
    }

    // Проверяем обязательные поля
    if p.ChatID == "" || p.Content == "" {
        client.SendError("missing_fields", "Необходимы поля chatID и content")
        return
    }
    
    // Устанавливаем тип сообщения по умолчанию
    if p.Type == "" {
        p.Type = "text"
    }

    // Парсим chatID
    chatID, err := uuid.Parse(p.ChatID)
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный формат chatID")
        return
    }

    // Определяем отправителя в зависимости от типа клиента
    var senderID uuid.UUID
    var sender string
    
    if client.ClientType == "admin" {
        // Для админа берем ID из контекста аутентификации
        adminIDStr, exists := ginCtx.Get("adminID")
        if !exists {
            client.SendError("auth_error", "Не удалось получить ID администратора")
            return
        }
        adminID, err := uuid.Parse(adminIDStr.(string))
        if err != nil {
            client.SendError("invalid_uuid", "Некорректный adminID")
            return
        }
        senderID = adminID
        sender = "admin"
    } else {
        // Для виджета используем ID пользователя
        senderID = client.ID
        sender = "user"
    }

    // Добавляем сообщение в базу
    log.Printf("processSendMessage: добавление сообщения в чат %s от %s (%s): %s", 
        chatID, sender, senderID, p.Content)
        
    message, err := database.AddMessage(
        chatID, 
        p.Content, 
        sender, 
        senderID, 
        p.Type, 
        p.Metadata,
    )
    if err != nil {
        log.Printf("processSendMessage: ошибка добавления сообщения: %v", err)
        client.SendError("db_error", "Ошибка при отправке сообщения: "+err.Error())
        return
    }

    // Получаем обновленный чат для отправки в WebSocket
    chat, _, err := database.GetChatByID(chatID, 1, 1)
    if err != nil {
        log.Printf("processSendMessage: ошибка получения чата: %v", err)
    }

    // Подготавливаем сообщение для рассылки всем клиентам
    broadcastData, err := websocketpkg.NewChatMessage(chat, message)
    if err != nil {
        log.Printf("processSendMessage: ошибка формирования WS сообщения: %v", err)
    }
    
    // Отправляем всем подключенным клиентам
    WebSocketHub.BroadcastMessage(broadcastData)
    
    // Специальное сообщение для виджета этого чата
    if sender == "admin" {
        if widgetMsg, err := websocketpkg.NewWidgetMessage(message); err == nil {
            WebSocketHub.SendToChat(chatID.String(), widgetMsg)
        }
    }
    
    // ОБРАБОТКА АВТООТВЕТЧИКА
    if sender == "user" && AutoResponder != nil && chat != nil {
        go processAutoResponse(ginCtx.Request.Context(), chat, message)
    }
    
    log.Printf("processSendMessage: сообщение успешно отправлено (ID=%s)", message.ID)
    
    // Отправляем подтверждение отправителю
    response := map[string]interface{}{
        "type": "messageSent",
        "payload": map[string]interface{}{
            "messageID": message.ID.String(),
            "timestamp": message.Timestamp,
            "status":    "delivered",
        },
    }
    
    if err := client.SendJSON(response); err != nil {
        log.Printf("processSendMessage: ошибка отправки подтверждения: %v", err)
    }
}

// processAutoResponse обрабатывает автоответчик асинхронно
func processAutoResponse(ctx context.Context, chat *models.Chat, userMsg *models.Message) {
    log.Printf("processAutoResponse: генерируем автоответ для чата %s", chat.ID)
    
    botMsg, err := AutoResponder.ProcessMessage(ctx, chat, userMsg)
    if err != nil {
        log.Printf("processAutoResponse: ошибка генерации автоответа: %v", err)
        return
    }
    
    if botMsg == nil {
        log.Printf("processAutoResponse: автоответ не сгенерирован")
        return
    }
    
    log.Printf("processAutoResponse: автоответ сгенерирован, сохраняем в БД")
    
    // Сохраняем автоответ в базу данных
    saved, err := database.AddMessage(
        chat.ID,
        botMsg.Content,
        botMsg.Sender,
        botMsg.SenderID,
        botMsg.Type,
        botMsg.Metadata,
    )
    if err != nil {
        log.Printf("processAutoResponse: ошибка сохранения автоответа: %v", err)
        return
    }
    
    // Получаем обновленный чат
    updatedChat, _, err := database.GetChatByID(chat.ID, 1, 1)
    if err != nil {
        log.Printf("processAutoResponse: ошибка получения обновленного чата: %v", err)
        updatedChat = chat // Используем исходный чат
    }
    
    // Отправляем автоответ всем клиентам
    if broadcastData, err := websocketpkg.NewChatMessage(updatedChat, saved); err == nil {
        WebSocketHub.BroadcastMessage(broadcastData)
        log.Printf("processAutoResponse: автоответ отправлен всем клиентам")
    }
    
    // Отправляем виджету
    if widgetMsg, err := websocketpkg.NewWidgetMessage(saved); err == nil {
        WebSocketHub.SendToChat(chat.ID.String(), widgetMsg)
        log.Printf("processAutoResponse: автоответ отправлен виджету")
    }
    
    // Проверяем необходимость эскалации
    if needEscalation, ok := saved.Metadata["needEscalation"].(bool); ok && needEscalation {
        escalateChat(chat.ID, saved.Metadata)
    }
}

// escalateChat эскалирует чат к живому оператору
func escalateChat(chatID uuid.UUID, metadata map[string]interface{}) {
    log.Printf("escalateChat: эскалация чата %s", chatID)
    
    // Здесь можно добавить логику эскалации:
    // 1. Назначить чат конкретному оператору
    // 2. Уведомить оператора о необходимости вмешательства
    // 3. Изменить статус чата
    
    // Создаем уведомление для операторов
    escalationMsg, err := websocketpkg.NewMessage("chat_escalation", map[string]interface{}{
        "chatID":   chatID.String(),
        "reason":   metadata,
        "priority": "high",
    })
    
    if err == nil {
        WebSocketHub.BroadcastMessage(escalationMsg)
    }
}

// processGetWidgetMessages - новый метод для получения сообщений виджета через WebSocket
func processGetWidgetMessages(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        ChatID   string `json:"chatID"`
        Page     int    `json:"page"`
        PageSize int    `json:"pageSize"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для getWidgetMessages")
        return
    }

    // Устанавливаем дефолтные значения
    if p.Page < 1 {
        p.Page = 1
    }
    if p.PageSize < 1 || p.PageSize > database.MaxPageSize {
        p.PageSize = database.DefaultPageSize
    }

    // Парсим ID чата
    chatID, err := uuid.Parse(p.ChatID)
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный формат chatID")
        return
    }

    // Проверяем, принадлежит ли чат этому пользователю
    if client.ClientType == "widget" && client.ChatID != chatID {
        client.SendError("access_denied", "Доступ к чату запрещен")
        return
    }

    // Получаем сообщения
    chat, total, err := database.GetChatByID(chatID, p.Page, p.PageSize)
    if err != nil {
        log.Printf("processGetWidgetMessages: ошибка получения сообщений: %v", err)
        client.SendError("db_error", "Ошибка получения сообщений: "+err.Error())
        return
    }

    // Рассчитываем общее количество страниц
    totalPages := (total + p.PageSize - 1) / p.PageSize
    if totalPages < 1 {
        totalPages = 1
    }

    // Преобразуем сообщения в формат для виджета
    simplifiedMessages := make([]map[string]interface{}, 0, len(chat.Messages))
    for _, msg := range chat.Messages {
        simplifiedMessages = append(simplifiedMessages, map[string]interface{}{
            "id":        msg.ID.String(),
            "content":   msg.Content,
            "sender":    msg.Sender,
            "timestamp": msg.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
            "type":      msg.Type,
        })
    }

    // Формируем ответ
    response := map[string]interface{}{
        "type": "widgetMessages",
        "payload": map[string]interface{}{
            "messages":    simplifiedMessages,
            "page":        p.Page,
            "pageSize":    p.PageSize,
            "totalItems":  total,
            "totalPages":  totalPages,
            "chatId":      chat.ID.String(),
            "userId":      chat.User.ID.String(),
        },
    }
    
    log.Printf("processGetWidgetMessages: найдено %d сообщений", len(simplifiedMessages))
    
    // Отправляем ответ
    if err := client.SendJSON(response); err != nil {
        log.Printf("processGetWidgetMessages: ошибка отправки ответа: %v", err)
    }
}

// Остальные обработчики остаются без изменений
func processGetChats(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        Page     int `json:"page"`
        PageSize int `json:"pageSize"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для getChats")
        return
    }

    // Устанавливаем дефолтные значения
    if p.Page < 1 {
        p.Page = 1
    }
    if p.PageSize < 1 || p.PageSize > database.MaxPageSize {
        p.PageSize = database.DefaultPageSize
    }

    // Получаем ID из контекста
    adminIDStr, _ := ginCtx.Get("adminID")
    clientIDStr, _ := ginCtx.Get("clientID")
    
    adminID, err := uuid.Parse(adminIDStr.(string))
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный adminID")
        return
    }
    
    clientID, err := uuid.Parse(clientIDStr.(string))
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный clientID")
        return
    }

    // Получаем чаты
    log.Printf("processGetChats: запрос чатов для admin=%s, client=%s, page=%d, size=%d", 
        adminID, clientID, p.Page, p.PageSize)
        
    chats, total, err := database.GetChats(clientID, adminID, p.Page, p.PageSize)
    if err != nil {
        log.Printf("processGetChats: ошибка получения чатов: %v", err)
        client.SendError("db_error", "Ошибка получения чатов: "+err.Error())
        return
    }

    // Рассчитываем общее количество страниц
    totalPages := (total + p.PageSize - 1) / p.PageSize
    if totalPages < 1 {
        totalPages = 1
    }

    // Формируем ответ
    response := map[string]interface{}{
        "type": "chatsList",
        "payload": models.ChatPaginationResponse{
            Chats:      chats,
            Page:       p.Page,
            PageSize:   p.PageSize,
            TotalItems: total,
            TotalPages: totalPages,
        },
    }
    
    log.Printf("processGetChats: найдено %d чатов из %d всего", len(chats), total)
    
    // Отправляем ответ
    if err := client.SendJSON(response); err != nil {
        log.Printf("processGetChats: ошибка отправки ответа: %v", err)
    }
}

func processGetChatByID(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        ChatID   string `json:"chatID"`
        Page     int    `json:"page"`
        PageSize int    `json:"pageSize"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для getChatByID")
        return
    }

    // Устанавливаем дефолтные значения
    if p.Page < 1 {
        p.Page = 1
    }
    if p.PageSize < 1 || p.PageSize > database.MaxPageSize {
        p.PageSize = database.DefaultPageSize
    }

    // Парсим ID чата
    chatID, err := uuid.Parse(p.ChatID)
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный формат chatID")
        return
    }

    // Получаем чат и его сообщения
    log.Printf("processGetChatByID: запрос чата ID=%s, page=%d, size=%d", 
        chatID, p.Page, p.PageSize)
        
    chat, total, err := database.GetChatByID(chatID, p.Page, p.PageSize)
    if err != nil {
        log.Printf("processGetChatByID: ошибка получения чата: %v", err)
        client.SendError("db_error", "Ошибка получения чата: "+err.Error())
        return
    }

    // Отмечаем сообщения как прочитанные
    if client.ClientType == "admin" {
        if err := database.MarkMessagesAsRead(chatID); err != nil {
            log.Printf("processGetChatByID: ошибка маркировки сообщений: %v", err)
        }
    }

    // Рассчитываем общее количество страниц
    totalPages := (total + p.PageSize - 1) / p.PageSize
    if totalPages < 1 {
        totalPages = 1
    }

    // Формируем ответ
    response := map[string]interface{}{
        "type": "chatDetails",
        "payload": map[string]interface{}{
            "chat":       chat,
            "page":       p.Page,
            "pageSize":   p.PageSize,
            "totalItems": total,
            "totalPages": totalPages,
        },
    }
    
    log.Printf("processGetChatByID: найден чат с %d сообщениями", len(chat.Messages))
    
    // Отправляем ответ
    if err := client.SendJSON(response); err != nil {
        log.Printf("processGetChatByID: ошибка отправки ответа: %v", err)
    }
}

func processMarkAsRead(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        ChatID string `json:"chatID"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для markAsRead")
        return
    }

    chatID, err := uuid.Parse(p.ChatID)
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный формат chatID")
        return
    }

    log.Printf("processMarkAsRead: отметка сообщений как прочитанных в чате %s", chatID)
    
    if err := database.MarkMessagesAsRead(chatID); err != nil {
        log.Printf("processMarkAsRead: ошибка: %v", err)
        client.SendError("db_error", "Ошибка при обновлении статуса сообщений: "+err.Error())
        return
    }

    // Отправляем обновление всем клиентам чата о прочтении сообщений
    statusMsg, _ := websocketpkg.NewMessage("messagesRead", map[string]interface{}{
        "chatID": chatID.String(),
        "readBy": client.ID.String(),
    })
    
    // Отправляем статус другим клиентам этого чата
    WebSocketHub.SendToChat(chatID.String(), statusMsg)
    
    log.Printf("processMarkAsRead: успешно обновлен статус сообщений в чате %s", chatID)
    
    // Отправляем подтверждение отправителю запроса
    response := map[string]interface{}{
        "type": "markAsReadConfirmed",
        "payload": map[string]interface{}{
            "chatID": chatID.String(),
            "status": "success",
        },
    }
    
    if err := client.SendJSON(response); err != nil {
        log.Printf("processMarkAsRead: ошибка отправки подтверждения: %v", err)
    }
}

func processTypingStatus(client *websocketpkg.Client, payload json.RawMessage, ginCtx *gin.Context) {
    var p struct {
        ChatID   string `json:"chatID"`
        IsTyping bool   `json:"isTyping"`
    }
    if err := json.Unmarshal(payload, &p); err != nil {
        client.SendError("invalid_payload", "Некорректный формат данных для typing")
        return
    }

    chatID, err := uuid.Parse(p.ChatID)
    if err != nil {
        client.SendError("invalid_uuid", "Некорректный формат chatID")
        return
    }

    // Определяем тип отправителя
    sender := "admin"
    if client.ClientType == "widget" {
        sender = "user"
    }

    // Создаем и отправляем сообщение о наборе текста
    typingMsg, err := websocketpkg.NewTypingMessage(chatID, p.IsTyping, sender)
    if err != nil {
        log.Printf("processTypingStatus: ошибка формирования сообщения: %v", err)
        return
    }
    
    // Отправляем только клиентам этого чата
    WebSocketHub.SendToChat(chatID.String(), typingMsg)
    
    log.Printf("processTypingStatus: отправлен статус typing=%v для чата %s от %s", 
        p.IsTyping, chatID, sender)
}