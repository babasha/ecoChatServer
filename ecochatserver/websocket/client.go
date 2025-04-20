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

// Типы клиентов
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

	// Данные о клиенте
	clientType string // "admin" или "widget"
	id         string // ID администратора или пользователя
	chatID     string // ID чата (для виджета)
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
				log.Printf("WebSocket ошибка: %v", err)
			}
			break
		}
		message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		
		// Здесь можно обрабатывать входящие сообщения от клиента
		log.Printf("Получено сообщение от клиента типа %s с ID %s: %s", c.clientType, c.id, string(message))
		
		// Пересылаем сообщение в хаб для обработки
		c.hub.broadcast <- message
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
		log.Println("Ошибка при установке WebSocket соединения:", err)
		return
	}
	
	// Параметры запроса
	query := r.URL.Query()
	token := query.Get("token")
	clientType := query.Get("type")
	chatID := query.Get("chat_id")
	
	// Определяем тип клиента и ID
	var id string
	
	// Если тип не указан явно, пробуем определить по наличию параметров
	if clientType == "" {
		if chatID != "" {
			clientType = ClientTypeWidget // Если указан chatID, это виджет
		} else {
			clientType = ClientTypeAdmin // По умолчанию считаем админом
		}
	}
	
	// Логика идентификации клиента
	if clientType == ClientTypeAdmin {
		// Для админов извлекаем ID из токена
		// В реальной системе здесь должна быть проверка JWT
		id = token
		log.Printf("Подключен администратор с ID: %s", id)
	} else {
		// Для виджетов используем token как userId
		id = token
		log.Printf("Подключен виджет пользователя с ID: %s, для чата: %s", id, chatID)
	}
	
	// Создаем клиента соответствующего типа
	client := &Client{
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		clientType: clientType,
		id:         id,
		chatID:     chatID,
	}
	
	// Регистрируем клиента
	client.hub.register <- client

	// Запускаем горутины для чтения и записи
	go client.writePump()
	go client.readPump()
	
	// Отправляем подтверждение подключения
	hub.SendConnectionStatus(client, true)
}