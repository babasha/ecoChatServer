package websocket

import (
	"bytes"
	"encoding/json"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second    // время на запись одного сообщения
	pongWait       = 60 * time.Second    // максимальное время ожидания PONG
	pingPeriod     = (pongWait * 9) / 10 // как часто слать PING
	maxMessageSize = 2048                // максимальный размер входящего сообщения (увеличен для длинных вопросов клиентов)
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// Client представляет одно WebSocket-соединение.
type Client struct {
	hub          *Hub
	Conn         *websocket.Conn // ЭКСПОРТИРОВАНО для DisconnectSession
	Send         chan interface{} // ЭКСПОРТИРОВАНО: исходящие сообщения (изменено на interface{} для гибкости)
	send         chan []byte      // deprecated, для обратной совместимости
	ClientType   string           // ЭКСПОРТИРОВАНО: "admin" или "widget"
	ID           string           // ЭКСПОРТИРОВАНО: уникальный ID сессии (строка для удобства)
	UserID       uuid.UUID        // adminID или widget-userID (UUID)
	UserIDString string           // ЭКСПОРТИРОВАНО: исходный строковый userID для виджета
	ChatID       uuid.UUID        // ЭКСПОРТИРОВАНО: для виджета — chatID
	Context      *gin.Context     // Gin context для доступа к данным запроса/аутентификации

	// Метаданные сессии для мониторинга
	IP            string    // IP адрес клиента
	UserAgent     string    // User Agent браузера
	ConnectedAt   time.Time // Время подключения
	LastActivity  time.Time // Время последней активности
	MessageCount  int       // Количество отправленных сообщений
}

// NewClient создает нового WebSocket клиента
func NewClient(hub *Hub, conn *websocket.Conn, clientType string, id uuid.UUID, chatID uuid.UUID) *Client {
	now := time.Now()
	return &Client{
		hub:           hub,
		Conn:          conn,
		send:          make(chan []byte, 256),
		Send:          make(chan interface{}, 256),
		ClientType:    clientType,
		ID:            uuid.New().String(), // Генерируем уникальный ID сессии
		UserID:        id,
		ChatID:        chatID,
		ConnectedAt:   now,
		LastActivity:  now,
		MessageCount:  0,
		IP:            "",
		UserAgent:     "",
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
		c.Conn.Close()
		log.Printf("WebSocket closed: %s (%s)", c.ClientType, c.ID)
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		c.LastActivity = time.Now() // Обновляем время последней активности
		return nil
	})

	for {
		_, raw, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket unexpected close (%s): %v", c.ID, err)
			}
			break
		}

		// Обновляем метаданные
		c.LastActivity = time.Now()
		c.MessageCount++

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
		c.Conn.Close()
		log.Printf("WritePump closed: %s (%s)", c.ClientType, c.ID)
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// канал закрыт Hub'ом
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Конвертируем message в JSON если это map или struct
			var msgBytes []byte
			switch v := message.(type) {
			case []byte:
				msgBytes = v
			case string:
				msgBytes = []byte(v)
			default:
				var err error
				msgBytes, err = json.Marshal(message)
				if err != nil {
					log.Printf("WritePump: ошибка маршалинга сообщения: %v", err)
					continue
				}
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(msgBytes)

			// сбрасываем накопленные сообщения
			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				msg := <-c.Send
				switch v := msg.(type) {
				case []byte:
					w.Write(v)
				case string:
					w.Write([]byte(v))
				default:
					if b, err := json.Marshal(msg); err == nil {
						w.Write(b)
					}
				}
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
