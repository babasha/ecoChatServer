package websocket

import (
	"encoding/json"
	"log"
)

// Hub обрабатывает WebSocket соединения
type Hub struct {
	// Зарегистрированные клиенты
	clients map[*Client]bool

	// Входящие сообщения от клиентов
	broadcast chan []byte

	// Регистрация клиента
	register chan *Client

	// Отмена регистрации клиента
	unregister chan *Client
}

// NewHub создает новый Hub
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

// Run запускает Hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			log.Printf("Клиент подключился. Всего клиентов: %d", len(h.clients))
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				log.Printf("Клиент отключился. Всего клиентов: %d", len(h.clients))
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// Broadcast отправляет сообщение всем подключенным клиентам
func (h *Hub) Broadcast(message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		log.Printf("Ошибка при маршализации сообщения: %v", err)
		return
	}
	h.broadcast <- data
}