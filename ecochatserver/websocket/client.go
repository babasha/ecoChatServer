package websocket

import (
	"bytes"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Время ожидания для записи сообщения клиенту
	writeWait = 10 * time.Second

	// Время ожидания для чтения следующего сообщения от клиента
	pongWait = 60 * time.Second

	// Отправляет пинги клиенту с этим периодом.
	// Должно быть меньше pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Максимальный размер сообщения
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Разрешаем соединения с любых источников (в продакшене нужно ограничить)
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Client представляет WebSocket соединение
type Client struct {
	hub *Hub

	// WebSocket соединение
	conn *websocket.Conn

	// Буферизованный канал для исходящих сообщений
	send chan []byte

	// Данные о клиенте (админе)
	adminID string
}

// readPump читает сообщения от WebSocket соединения и отправляет их в Hub
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("error: %v", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		
		// Здесь можно обрабатывать входящие сообщения от клиента
		
		//c.hub.broadcast <- message
	}
}

// writePump отправляет сообщения клиенту WebSocket
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub закрыл канал
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Отправляем все накопившиеся сообщения
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

// ServeWs обрабатывает WebSocket запросы от клиента
func ServeWs(hub *Hub, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	
	// Извлекаем adminID из токена (в реальном приложении)
	// adminID := extractAdminIDFromToken(r)
	adminID := r.URL.Query().Get("token") // Упрощенный вариант
	
	client := &Client{hub: hub, conn: conn, send: make(chan []byte, 256), adminID: adminID}
	client.hub.register <- client

	// Запускаем горутины для чтения и записи
	go client.writePump()
	go client.readPump()
}