package websocket

import (
    "bytes"
    "encoding/json"
    "log"
    "time"

    "github.com/gin-gonic/gin"
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
)

// Client представляет одно WebSocket-соединение.
type Client struct {
    hub        *Hub
    conn       *websocket.Conn
    send       chan []byte         // исходящие сообщения
    ClientType string              // ЭКСПОРТИРОВАНО: "admin" или "widget"
    ID         uuid.UUID           // ЭКСПОРТИРОВАНО: adminID или widget-userID
    ChatID     uuid.UUID           // ЭКСПОРТИРОВАНО: для виджета — chatID
    Context    *gin.Context        // Gin context для доступа к данным запроса/аутентификации
}

// NewClient создает нового WebSocket клиента
func NewClient(hub *Hub, conn *websocket.Conn, clientType string, id uuid.UUID, chatID uuid.UUID) *Client {
    return &Client{
        hub:        hub,
        conn:       conn,
        send:       make(chan []byte, 256),
        ClientType: clientType,
        ID:         id,
        ChatID:     chatID,
    }
}

// SendJSON отправляет JSON-объект клиенту
func (c *Client) SendJSON(data interface{}) error {
    json, err := json.Marshal(data)
    if err != nil {
        return err
    }
    
    c.send <- json
    return nil
}

// SendError отправляет сообщение об ошибке
func (c *Client) SendError(code, message string) {
    errorMsg, _ := NewErrorMessage(code, message)
    c.send <- errorMsg
}

// ReadPump читает сообщения из WebSocket, парсит их и вызывает handler.
func (c *Client) ReadPump(messageHandler func(client *Client, message []byte)) {
    defer func() {
        c.hub.Unregister <- c
        c.conn.Close()
        log.Printf("WebSocket closed: %s (%s)", c.ClientType, c.ID)
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
                log.Printf("WebSocket unexpected close (%s): %v", c.ID, err)
            }
            break
        }

        // Очищаем переносы строк
        raw = bytes.TrimSpace(bytes.Replace(raw, newline, space, -1))
        log.Printf("WS recv from %s %s: %s", c.ClientType, c.ID, string(raw))

        // Вызываем обработчик сообщения
        if messageHandler != nil {
            messageHandler(c, raw)
        }
    }
}

// WritePump пишет из канала send в WebSocket и держит соединение живым ping/pong'ом.
func (c *Client) WritePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        c.hub.Unregister <- c
        c.conn.Close()
        log.Printf("WritePump closed: %s (%s)", c.ClientType, c.ID)
    }()

    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                // канал закрыт Hub'ом
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            
            w, err := c.conn.NextWriter(websocket.TextMessage)
            if err != nil {
                return
            }
            w.Write(message)

            // сбрасываем накопленные сообщения
            n := len(c.send)
            for i := 0; i < n; i++ {
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