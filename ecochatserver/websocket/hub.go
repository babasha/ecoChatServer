package websocket

import (
	"encoding/json"
	"log"
)

// Hub обрабатывает WebSocket соединения
type Hub struct {
	// Зарегистрированные клиенты
	clients map[*Client]bool

	// Карта клиентов по ID для быстрого поиска
	adminsByID  map[string]*Client
	widgetsByID map[string]map[*Client]bool // Карта chatID -> клиенты

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
		broadcast:   make(chan []byte),
		register:    make(chan *Client),
		unregister:  make(chan *Client),
		clients:     make(map[*Client]bool),
		adminsByID:  make(map[string]*Client),
		widgetsByID: make(map[string]map[*Client]bool),
	}
}

// Run запускает Hub
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			
			// Регистрируем клиента в соответствующей карте
			if client.clientType == ClientTypeAdmin {
				h.adminsByID[client.id] = client
				log.Printf("Зарегистрирован администратор с ID: %s. Всего администраторов: %d", 
					client.id, len(h.adminsByID))
			} else if client.clientType == ClientTypeWidget {
				// Для виджетов используем карту по chatID
				chatID := client.chatID
				if chatID == "" {
					chatID = client.id // Если chatID не указан, используем ID пользователя
				}
				
				if _, ok := h.widgetsByID[chatID]; !ok {
					h.widgetsByID[chatID] = make(map[*Client]bool)
				}
				h.widgetsByID[chatID][client] = true
				log.Printf("Зарегистрирован виджет пользователя %s для чата %s. Всего виджетов: %d", 
					client.id, chatID, countWidgets(h.widgetsByID))
			}
			
			log.Printf("Клиент подключился. Всего клиентов: %d", len(h.clients))

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				
				// Удаляем клиента из соответствующей карты
				if client.clientType == ClientTypeAdmin {
					delete(h.adminsByID, client.id)
					log.Printf("Администратор с ID %s отключился", client.id)
				} else if client.clientType == ClientTypeWidget {
					chatID := client.chatID
					if chatID == "" {
						chatID = client.id
					}
					
					if widgets, ok := h.widgetsByID[chatID]; ok {
						delete(widgets, client)
						
						// Если для чата больше нет виджетов, удаляем запись
						if len(widgets) == 0 {
							delete(h.widgetsByID, chatID)
						}
					}
					log.Printf("Виджет пользователя %s для чата %s отключился", client.id, chatID)
				}
				
				log.Printf("Клиент отключился. Всего клиентов: %d", len(h.clients))
			}

		case message := <-h.broadcast:
			// В будущем здесь можно реализовать более сложную логику маршрутизации
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
func (h *Hub) Broadcast(message []byte) {
	h.broadcast <- message
}

// SendToAdmin отправляет сообщение конкретному администратору
func (h *Hub) SendToAdmin(adminID string, message []byte) bool {
	admin, exists := h.adminsByID[adminID]
	if !exists {
		return false
	}
	
	select {
	case admin.send <- message:
		return true
	default:
		close(admin.send)
		delete(h.clients, admin)
		delete(h.adminsByID, adminID)
		return false
	}
}

// SendToChat отправляет сообщение всем клиентам (виджетам) связанным с указанным чатом
func (h *Hub) SendToChat(chatID string, message []byte) int {
	widgets, exists := h.widgetsByID[chatID]
	if !exists || len(widgets) == 0 {
		return 0
	}
	
	sentCount := 0
	for widget := range widgets {
		select {
		case widget.send <- message:
			sentCount++
		default:
			close(widget.send)
			delete(h.clients, widget)
			delete(widgets, widget)
		}
	}
	
	// Если для чата больше нет виджетов, удаляем запись
	if len(widgets) == 0 {
		delete(h.widgetsByID, chatID)
	}
	
	return sentCount
}

// SendConnectionStatus отправляет статус подключения клиенту
func (h *Hub) SendConnectionStatus(client *Client, connected bool) {
	status := "connected"
	if !connected {
		status = "disconnected"
	}
	
	data, err := json.Marshal(struct {
		Type    string `json:"type"`
		Payload struct {
			Status string `json:"status"`
		} `json:"payload"`
	}{
		Type: "connection",
		Payload: struct {
			Status string `json:"status"`
		}{
			Status: status,
		},
	})
	
	if err != nil {
		log.Printf("Ошибка при создании сообщения о статусе подключения: %v", err)
		return
	}
	
	select {
	case client.send <- data:
	default:
		log.Printf("Не удалось отправить статус подключения клиенту")
	}
}

// BroadcastToChatAndAdmin отправляет сообщение всем виджетам чата и назначенному администратору
func (h *Hub) BroadcastToChatAndAdmin(chatID string, assignedAdminID string, message []byte) {
	// Отправляем сообщение виджетам чата
	sentToWidgets := h.SendToChat(chatID, message)
	if sentToWidgets > 0 {
		log.Printf("Сообщение отправлено %d виджетам для чата %s", sentToWidgets, chatID)
	}
	
	// Отправляем сообщение администратору
	if assignedAdminID != "" && h.SendToAdmin(assignedAdminID, message) {
		log.Printf("Сообщение отправлено администратору %s", assignedAdminID)
	}
}

// Вспомогательная функция для подсчета общего количества виджетов
func countWidgets(widgetsByID map[string]map[*Client]bool) int {
	count := 0
	for _, widgets := range widgetsByID {
		count += len(widgets)
	}
	return count
}