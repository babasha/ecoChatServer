package handlers

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/gorilla/websocket"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/middleware"
    "github.com/egor/ecochatserver/models"
    websocketpkg "github.com/egor/ecochatserver/websocket"
)

// wsUpgrader апгрейдит HTTP→WebSocket
var wsUpgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true }, // ограничьте по origin в продакшне
}

// ServeWs обрабатывает WebSocket соединение
func ServeWs(c *gin.Context) {
    log.Printf("ServeWs: новое соединение от %s", c.ClientIP())

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
    client.Context = c // Сохраняем Gin контекст для доступа к данным

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
    default:
        client.SendError("unknown_type", "Неизвестный тип сообщения: "+msg.Type)
    }
}

// Обработчики различных типов WebSocket сообщений

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
            // Продолжаем работу, несмотря на ошибку
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
    chat, _, err := database.GetChatByID(chatID, 1, 1) // Только для получения мета-информации чата
    if err != nil {
        log.Printf("processSendMessage: ошибка получения чата: %v", err)
        // Продолжаем даже при ошибке
    }

    // Подготавливаем сообщение для рассылки всем клиентам
    broadcastData, err := websocketpkg.NewChatMessage(chat, message)
    if err != nil {
        log.Printf("processSendMessage: ошибка формирования WS сообщения: %v", err)
        // Продолжаем даже при ошибке
    }
    
    // Отправляем всем подключенным клиентам
    WebSocketHub.BroadcastMessage(broadcastData)
    
    // Специальное сообщение для виджета этого чата
    if sender == "admin" {
        if widgetMsg, err := websocketpkg.NewWidgetMessage(message); err == nil {
            WebSocketHub.SendToChat(chatID.String(), widgetMsg)
        }
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