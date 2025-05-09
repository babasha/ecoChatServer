package websocket

import (
    "bytes"
    "encoding/json"
    "log"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
    "github.com/google/uuid"
)

const (
    writeWait      = 10 * time.Second      // время на запись одного сообщения
    pongWait       = 60 * time.Second      // максимальное время ожидания PONG
    pingPeriod     = (pongWait * 9) / 10   // как часто слать PING
    maxMessageSize = 512                   // максимальный размер входящего сообщения
)

var (
    newline  = []byte{'\n'}
    space    = []byte{' '}
    upgrader = websocket.Upgrader{
        ReadBufferSize:  1024,
        WriteBufferSize: 1024,
        CheckOrigin:     func(r *http.Request) bool { return true }, // ограничьте по origin в продакшне
    }
)

// Client представляет одно WebSocket-соединение.
type Client struct {
    hub        *Hub
    conn       *websocket.Conn
    send       chan []byte       // исходящие сообщения
    clientType string            // "admin" или "widget"
    id         uuid.UUID         // adminID или widget-userID
    chatID     uuid.UUID         // для виджета — chatID
}

// ReadPump читает сообщения из WebSocket, парсит их в WSMessage и вызывает handler.
func (c *Client) ReadPump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()

    c.conn.SetReadLimit(maxMessageSize)
    c.conn.SetReadDeadline(time.Now().Add(pongWait))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(pongWait))
        return nil
    })

    for {
        _, raw, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                log.Printf("WebSocket unexpected close (%s): %v", c.id, err)
            }
            break
        }

        // Очищаем переносы строк
        raw = bytes.TrimSpace(bytes.Replace(raw, newline, space, -1))
        log.Printf("WS recv from %s %s: %s", c.clientType, c.id, raw)

        var msg WebSocketMessage
        if err := json.Unmarshal(raw, &msg); err != nil {
            c.SendError("invalid_json", "Невалидный формат JSON")
            continue
        }

        // Делегируем логику вашему handler’у
        handleWSMessage(c, msg)
    }
}

// WritePump пишет из канала send в WebSocket и держит соединение живым ping/pong’ом.
func (c *Client) WritePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        c.hub.unregister <- c
        c.conn.Close()
    }()

    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                // канал закрыт Hub’ом
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            w, err := c.conn.NextWriter(websocket.TextMessage)
            if err != nil {
                return
            }
            w.Write(message)

            // сбрасываем накопленные
            for i := len(c.send); i > 0; i-- {
                w.Write(newline)
                w.Write(<-c.send)
            }
            if err := w.Close(); err != nil {
                return
            }

        case <-ticker.C:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}

// ServeWs апгрейдит HTTP→WebSocket, создаёт Client, регистрирует его в Hub и стартует Read/WritePump.
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Printf("WebSocket upgrade error: %v", err)
        return
    }

    // Разбираем параметры
    token := r.URL.Query().Get("token")
    clientType := r.URL.Query().Get("type")
    chatIDStr := r.URL.Query().Get("chat_id")

    if clientType == "" {
        clientType = ClientTypeAdmin
        if chatIDStr != "" {
            clientType = ClientTypeWidget
        }
    }

    // Парсим UUID из token и chat_id
    id, _ := uuid.Parse(token)
    chatID, _ := uuid.Parse(chatIDStr)

    client := &Client{
        hub:        hub,
        conn:       conn,
        send:       make(chan []byte, 256),
        clientType: clientType,
        id:         id,
        chatID:     chatID,
    }
    hub.register <- client

    go client.WritePump()
    go client.ReadPump()

    // Уведомить всех о подключении
    hub.SendConnectionStatus(client, true)
}