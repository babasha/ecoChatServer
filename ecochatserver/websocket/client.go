package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// errSendBufferFull возвращается, когда исходящий канал клиента переполнен или закрыт.
var errSendBufferFull = errors.New("websocket: send buffer full or closed")

const (
	writeWait  = 10 * time.Second // время на запись одного сообщения
	pongWait   = 60 * time.Second // максимальное время ожидания PONG от клиента
	pingPeriod = 30 * time.Second // как часто слать PING (синхронизировано с клиентом)
	// maxMessageSize — лимит размера входящего WS-фрейма (gorilla SetReadLimit).
	// Должен с запасом превышать лимиты длины content (admin 2000, driver 1500,
	// widget 1000 байт) ПЛЮС JSON-обёртку и metadata: при 2048 даже валидное
	// сообщение у лимита рвало соединение (1009) до дружелюбной ошибки приложения.
	maxMessageSize = 16384
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// Client представляет одно WebSocket-соединение.
type Client struct {
	hub          *Hub
	Conn         *websocket.Conn  // ЭКСПОРТИРОВАНО для DisconnectSession
	Send         chan interface{} // ЭКСПОРТИРОВАНО: исходящие сообщения (изменено на interface{} для гибкости)
	ClientType   string           // ЭКСПОРТИРОВАНО: "admin" или "widget"
	ID           string           // ЭКСПОРТИРОВАНО: уникальный ID сессии (строка для удобства)
	UserID       uuid.UUID        // adminID или widget-userID (UUID)
	UserIDString string           // ЭКСПОРТИРОВАНО: исходный строковый userID для виджета
	Context      *gin.Context     // Gin context для доступа к данным запроса/аутентификации

	// chatID виджета может меняться после создания чата (см. Hub.UpdateWidgetChatID).
	// Запись идёт под hub.mu, но читается из горутины ReadPump (enforceChatScope),
	// поэтому доступ защищён собственным RWMutex через GetChatID/SetChatID.
	chatID   uuid.UUID
	chatIDMu sync.RWMutex

	// Метаданные сессии для мониторинга
	IP          string    // IP адрес клиента (write-once до публикации в Hub)
	UserAgent   string    // User Agent браузера (write-once до публикации в Hub)
	ConnectedAt time.Time // Время подключения (write-once)
	// lastActivityNano/messageCount пишутся из ReadPump и читаются из обработчика
	// сессий — атомарные, чтобы убрать data race на горячем пути без блокировок.
	lastActivityNano atomic.Int64 // время последней активности (UnixNano)
	messageCount     atomic.Int64 // количество полученных сообщений

	// Защита от повторного отключения
	disconnectOnce sync.Once // Гарантирует однократное отключение

	// Защита канала Send от паники send-on-closed-channel: отправители берут
	// RLock и проверяют sendClosed, закрытие идёт под Lock ровно один раз.
	sendMu     sync.RWMutex
	sendClosed bool
}

// TrySend неблокирующе и БЕЗОПАСНО кладёт сообщение в канал клиента.
// Возвращает false, если канал переполнен или уже закрыт (без паники).
func (c *Client) TrySend(message interface{}) bool {
	c.sendMu.RLock()
	defer c.sendMu.RUnlock()
	if c.sendClosed {
		return false
	}
	select {
	case c.Send <- message:
		return true
	default:
		return false
	}
}

// closeSend закрывает исходящий канал ровно один раз под защитой мьютекса.
// Пока держится Lock, ни один TrySend (RLock) не может писать в канал.
func (c *Client) closeSend() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if !c.sendClosed {
		c.sendClosed = true
		close(c.Send)
	}
}

// NewClient создает нового WebSocket клиента
func NewClient(hub *Hub, conn *websocket.Conn, clientType string, id uuid.UUID, chatID uuid.UUID) *Client {
	now := time.Now()
	c := &Client{
		hub:         hub,
		Conn:        conn,
		Send:        make(chan interface{}, 256),
		ClientType:  clientType,
		ID:          uuid.New().String(), // Генерируем уникальный ID сессии
		UserID:      id,
		chatID:      chatID,
		ConnectedAt: now,
		IP:          "",
		UserAgent:   "",
	}
	c.lastActivityNano.Store(now.UnixNano())
	return c
}

// GetChatID возвращает текущий chatID клиента (потокобезопасно).
func (c *Client) GetChatID() uuid.UUID {
	c.chatIDMu.RLock()
	defer c.chatIDMu.RUnlock()
	return c.chatID
}

// SetChatID атомарно обновляет chatID. Вызывается под hub.mu при привязке
// виджета к созданному чату.
func (c *Client) SetChatID(id uuid.UUID) {
	c.chatIDMu.Lock()
	c.chatID = id
	c.chatIDMu.Unlock()
}

// touch отмечает активность клиента: обновляет время и увеличивает счётчик.
func (c *Client) touch() {
	c.lastActivityNano.Store(time.Now().UnixNano())
	c.messageCount.Add(1)
}

// LastActivity возвращает время последней активности (потокобезопасно).
func (c *Client) LastActivity() time.Time {
	return time.Unix(0, c.lastActivityNano.Load())
}

// MessageCount возвращает количество полученных сообщений (потокобезопасно).
func (c *Client) MessageCount() int {
	return int(c.messageCount.Load())
}

// SendJSON отправляет JSON-объект клиенту
func (c *Client) SendJSON(data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if c.TrySend(payload) {
		return nil
	}
	return errSendBufferFull
}

// SendError отправляет сообщение об ошибке
func (c *Client) SendError(code, message string) {
	errorMsg, _ := NewErrorMessage(code, message)
	if !c.TrySend(errorMsg) {
		log.Printf("SendError: не удалось отправить ошибку клиенту %s (канал недоступен)", c.ID)
	}
}

// Disconnect безопасно отключает клиента (вызывается только один раз благодаря sync.Once)
func (c *Client) Disconnect() {
	c.disconnectOnce.Do(func() {
		c.hub.Unregister <- c
		c.Conn.Close()
		log.Printf("Client disconnected: %s (%s)", c.ClientType, c.ID)
	})
}

// ReadPump читает сообщения из WebSocket, парсит их и вызывает handler.
func (c *Client) ReadPump(messageHandler func(client *Client, message []byte)) {
	defer c.Disconnect()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		c.lastActivityNano.Store(time.Now().UnixNano()) // Обновляем время последней активности
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

		// Обновляем метаданные активности (атомарно)
		c.touch()

		// Очищаем переносы строк
		raw = bytes.TrimSpace(bytes.Replace(raw, newline, space, -1))
		// DEBUG: логируем только в режиме отладки
		// log.Printf("WS recv from %s %s: %s", c.ClientType, c.ID, string(raw))

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
		c.Disconnect()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.writeMessage(message); err != nil {
				return
			}

			// отправляем дополнительные сообщения, если они накопились в очереди
			for i := 0; i < len(c.Send); i++ {
				next := <-c.Send
				if err := c.writeMessage(next); err != nil {
					return
				}
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) writeMessage(message interface{}) error {
	c.Conn.SetWriteDeadline(time.Now().Add(writeWait))

	var msgBytes []byte
	switch v := message.(type) {
	case []byte:
		msgBytes = v
	case string:
		msgBytes = []byte(v)
	default:
		var err error
		msgBytes, err = json.Marshal(v)
		if err != nil {
			log.Printf("WritePump: ошибка маршалинга сообщения: %v", err)
			return nil // не разрываем соединение, просто пропускаем сообщение
		}
	}

	// DEBUG: логируем только в режиме отладки
	// log.Printf("WS send to %s %s: %s", c.ClientType, c.ID, string(msgBytes))

	if err := c.Conn.WriteMessage(websocket.TextMessage, msgBytes); err != nil {
		return err
	}

	return nil
}
