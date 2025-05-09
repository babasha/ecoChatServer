// handlers/ws.go
package handlers

import (
    "encoding/json"
    "log"
    "net/http"

    "github.com/gin-gonic/gin"
    "github.com/google/uuid"
    "github.com/gorilla/websocket"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/models"
    "github.com/egor/ecochatserver/websocket" // ваш Hub и Client
)

// upgrader апгрейдит HTTP→WebSocket. В продакшене следует ограничивать CheckOrigin.
var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// WSMessage общий контейнер входящих/исходящих пакетов
type WSMessage struct {
    Type    string          `json:"type"`    // e.g. "getChats", "sendMessage"
    Payload json.RawMessage `json:"payload"` // сырые данные, разберите ниже
}

// payload для разных запросов
type GetChatsPayload struct {
    Page     int `json:"page"`
    PageSize int `json:"pageSize"`
}

type GetChatByIDPayload struct {
    ChatID   string `json:"chatID"`
    Page     int    `json:"page"`
    PageSize int    `json:"pageSize"`
}

type SendMessagePayload struct {
    ChatID  string `json:"chatID"`
    Content string `json:"content"`
    Type    string `json:"type"` // "text", "image" и т.п.
}

// ServeWs прокачивает соединение, регистрирует клиента и запускает Read/Write циклы.
func ServeWs(c *gin.Context) {
    // 1) Апгрейдим соединение
    conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
    if err != nil {
        log.Printf("ServeWs: upgrade error: %v", err)
        c.AbortWithStatus(http.StatusInternalServerError)
        return
    }

    // 2) Создаём клиента, сохраняем в контексте Gin adminID/clientID
    client := websocket.NewClient(conn)
    client.Context = c // чтобы потом доставать adminID, clientID

    // 3) Регистрируем клиента в Hub
    websocket.WebSocketHub.Register(client)

    // 4) Запускаем горутины для чтения и записи
    go client.WritePump()
    go client.ReadPump(handleWSMessage)
}

// handleWSMessage — основная логика разбора и обработки входящих WS-пакетов.
func handleWSMessage(client *websocket.Client, raw []byte) {
    var msg WSMessage
    if err := json.Unmarshal(raw, &msg); err != nil {
        client.SendError("Invalid JSON format")
        return
    }

    // Получаем admin/client ID из контекста
    ginCtx := client.Context.(*gin.Context)
    adminIDStr := ginCtx.GetString("adminID")
    clientIDStr := ginCtx.GetString("clientID")

    switch msg.Type {
    case "getChats":
        var p GetChatsPayload
        if err := json.Unmarshal(msg.Payload, &p); err != nil {
            client.SendError("Invalid getChats payload")
            return
        }
        // Дефолты и валидация
        if p.Page < 1 {
            p.Page = 1
        }
        if p.PageSize < 1 {
            p.PageSize = database.DefaultPageSize
        }

        // Парсим UUID
        adminID, err1 := uuid.Parse(adminIDStr)
        clientID, err2 := uuid.Parse(clientIDStr)
        if err1 != nil || err2 != nil {
            client.SendError("Invalid UUID in context")
            return
        }

        // Получаем из БД
        chats, total, err := database.GetChats(clientID, adminID, p.Page, p.PageSize)
        if err != nil {
            client.SendError("Ошибка получения чатов: " + err.Error())
            return
        }
        totalPages := (total + p.PageSize - 1) / p.PageSize
        if totalPages < 1 {
            totalPages = 1
        }

        // Ответ
        resp := map[string]interface{}{
            "type": "chatsList",
            "payload": map[string]interface{}{
                "items":      chats,
                "page":       p.Page,
                "pageSize":   p.PageSize,
                "totalItems": total,
                "totalPages": totalPages,
            },
        }
        client.SendJSON(resp)

    case "getChatByID":
        var p GetChatByIDPayload
        if err := json.Unmarshal(msg.Payload, &p); err != nil {
            client.SendError("Invalid getChatByID payload")
            return
        }
        if p.Page < 1 {
            p.Page = 1
        }
        if p.PageSize < 1 {
            p.PageSize = database.DefaultPageSize
        }

        // Парсим ID чата
        chatUUID, err := uuid.Parse(p.ChatID)
        if err != nil {
            client.SendError("Некорректный формат chatID")
            return
        }
        chat, total, err := database.GetChatByID(chatUUID, p.Page, p.PageSize)
        if err != nil {
            client.SendError("Чат не найден: " + err.Error())
            return
        }
        // Отмечаем прочитанные
        _ = database.MarkMessagesAsRead(chatUUID)

        totalPages := (total + p.PageSize - 1) / p.PageSize
        if totalPages < 1 {
            totalPages = 1
        }
        resp := map[string]interface{}{
            "type": "chatDetails",
            "payload": map[string]interface{}{
                "chat":       chat,
                "page":       p.Page,
                "pageSize":   p.PageSize,
                "totalItems": total,
                "totalPages": totalPages,
            },
        }
        client.SendJSON(resp)

    case "sendMessage":
        var p SendMessagePayload
        if err := json.Unmarshal(msg.Payload, &p); err != nil {
            client.SendError("Invalid sendMessage payload")
            return
        }
        // Парсим UUID
        chatUUID, err := uuid.Parse(p.ChatID)
        if err != nil {
            client.SendError("Некорректный формат chatID")
            return
        }
        adminID, err := uuid.Parse(adminIDStr)
        if err != nil {
            client.SendError("Invalid adminID in context")
            return
        }

        // Добавляем в БД
        message, err := database.AddMessage(chatUUID, p.Content, "admin", adminID, p.Type, nil)
        if err != nil {
            client.SendError("Ошибка при отправке: " + err.Error())
            return
        }

        // Подготавливаем вещаемое сообщение
        broadcastData, err := websocket.NewChatMessage(nil, message)
        if err != nil {
            log.Printf("sendMessage: build WS message error: %v", err)
            return
        }
        websocket.WebSocketHub.Broadcast(broadcastData)

    default:
        client.SendError("Unknown message type: " + msg.Type)
    }
}