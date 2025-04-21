package websocket

import (
    "bytes"
    "log"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
)

const (
    writeWait      = 10 * time.Second
    pongWait       = 60 * time.Second
    pingPeriod     = (pongWait * 9) / 10
    maxMessageSize = 512
)

const (
    ClientTypeAdmin  = "admin"
    ClientTypeWidget = "widget"
)

var (
    newline = []byte{'\n'}
    space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true },
}

type Client struct {
    hub        *Hub
    conn       *websocket.Conn
    send       chan []byte
    clientType string
    id         string
    chatID     string
}

func (c *Client) readPump() {
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
        _, message, err := c.conn.ReadMessage()
        if err != nil {
            if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
                log.Printf("WebSocket error: %v", err)
            }
            break
        }
        message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
        log.Printf("Received from %s (%s): %s", c.clientType, c.id, string(message))
        c.hub.broadcast <- message
    }
}

func (c *Client) writePump() {
    ticker := time.NewTicker(pingPeriod)
    defer func() {
        ticker.Stop()
        // Снимаем регистрацию клиента при выходе из writePump
        c.hub.unregister <- c
        c.conn.Close()
    }()
    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(writeWait))
            if !ok {
                c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            w, err := c.conn.NextWriter(websocket.TextMessage)
            if err != nil {
                return
            }
            w.Write(message)
            // Отправляем накопленные сообщения
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

func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        log.Println("WebSocket upgrade error:", err)
        return
    }
    q := r.URL.Query()
    token := q.Get("token")
    clientType := q.Get("type")
    chatID := q.Get("chat_id")
    if clientType == "" {
        if chatID != "" {
            clientType = ClientTypeWidget
        } else {
            clientType = ClientTypeAdmin
        }
    }
    id := token
    if clientType == ClientTypeAdmin {
        log.Printf("Admin connected: %s", id)
    } else {
        log.Printf("Widget connected: user %s chat %s", id, chatID)
    }
    client := &Client{
        hub:        hub,
        conn:       conn,
        send:       make(chan []byte, 256),
        clientType: clientType,
        id:         id,
        chatID:     chatID,
    }
    hub.register <- client
    go client.writePump()
    go client.readPump()
    hub.SendConnectionStatus(client, true)
}