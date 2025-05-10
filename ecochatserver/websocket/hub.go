package websocket

import (
    "sync"
)

const (
    ClientTypeAdmin  = "admin"
    ClientTypeWidget = "widget"
)

// Hub отвечает за регистрацию клиентов и вещание сообщений.
type Hub struct {
    clients     map[*Client]bool
    adminsByID  map[string]*Client
    widgetsByID map[string]map[*Client]bool
    chatClients map[string]map[*Client]bool // для всех клиентов конкретного чата

    Broadcast  chan []byte   // ЭКСПОРТИРОВАНО
    Register   chan *Client  // ЭКСПОРТИРОВАНО
    Unregister chan *Client  // ЭКСПОРТИРОВАНО

    mu sync.RWMutex
}

// NewHub создаёт и инициализирует Hub.
func NewHub() *Hub {
    return &Hub{
        clients:     make(map[*Client]bool),
        adminsByID:  make(map[string]*Client),
        widgetsByID: make(map[string]map[*Client]bool),
        chatClients: make(map[string]map[*Client]bool),
        Broadcast:   make(chan []byte),
        Register:    make(chan *Client),
        Unregister:  make(chan *Client),
    }
}

// Run слушает события регистрации, отключения и широковещания.
func (h *Hub) Run() {
    for {
        select {
        case c := <-h.Register:
            h.mu.Lock()
            h.clients[c] = true
            
            // Регистрируем по типу клиента
            if c.ClientType == ClientTypeAdmin {
                h.adminsByID[c.ID.String()] = c
            } else if c.ClientType == ClientTypeWidget {
                // для виджетов группируем по chatID
                if _, ok := h.widgetsByID[c.ChatID.String()]; !ok {
                    h.widgetsByID[c.ChatID.String()] = make(map[*Client]bool)
                }
                h.widgetsByID[c.ChatID.String()][c] = true
            }
            
            // Добавляем в карту клиентов чата для отправки сообщений всем участникам
            chatID := c.ChatID.String()
            if chatID != "" {
                if _, ok := h.chatClients[chatID]; !ok {
                    h.chatClients[chatID] = make(map[*Client]bool)
                }
                h.chatClients[chatID][c] = true
            }
            
            h.mu.Unlock()

        case c := <-h.Unregister:
            h.mu.Lock()
            
            // Удаляем из основной мапы
            if _, ok := h.clients[c]; ok {
                delete(h.clients, c)
                close(c.send)
            }
            
            // Удаляем по типу клиента
            if c.ClientType == ClientTypeAdmin {
                delete(h.adminsByID, c.ID.String())
            } else if c.ClientType == ClientTypeWidget {
                chatID := c.ChatID.String()
                if widgets, ok := h.widgetsByID[chatID]; ok {
                    delete(widgets, c)
                    if len(widgets) == 0 {
                        delete(h.widgetsByID, chatID)
                    }
                }
            }
            
            // Удаляем из карты клиентов чата
            chatID := c.ChatID.String()
            if chatID != "" {
                if clients, ok := h.chatClients[chatID]; ok {
                    delete(clients, c)
                    if len(clients) == 0 {
                        delete(h.chatClients, chatID)
                    }
                }
            }
            
            h.mu.Unlock()

        case msg := <-h.Broadcast:
            h.mu.RLock()
            for client := range h.clients {
                select {
                case client.send <- msg:
                default:
                    // Клиент не отвечает, удаляем
                    close(client.send)
                    delete(h.clients, client)
                }
            }
            h.mu.RUnlock()
        }
    }
}

// BroadcastMessage шлёт сообщение всем подключённым клиентам.
func (h *Hub) BroadcastMessage(message []byte) {
    h.Broadcast <- message
}

// SendToAdmin пытается отправить сообщение конкретному админу.
func (h *Hub) SendToAdmin(adminID string, message []byte) bool {
    h.mu.RLock()
    defer h.mu.RUnlock()
    
    if c, ok := h.adminsByID[adminID]; ok {
        select {
        case c.send <- message:
            return true
        default:
            return false
        }
    }
    return false
}

// SendToChat вещает сообщение всем клиентам конкретного чата.
func (h *Hub) SendToChat(chatID string, message []byte) int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    
    sent := 0
    if clients, ok := h.chatClients[chatID]; ok {
        for c := range clients {
            select {
            case c.send <- message:
                sent++
            default:
                // Игнорируем недоступные клиенты
            }
        }
    }
    return sent
}

// SendConnectionStatus уведомляет о подключении/отключении (по вашему протоколу).
func (h *Hub) SendConnectionStatus(c *Client, online bool) {
    payload := struct {
        ClientType string `json:"clientType"`
        ID         string `json:"id"`
        ChatID     string `json:"chatId,omitempty"`
        Online     bool   `json:"online"`
    }{
        ClientType: c.ClientType,
        ID:         c.ID.String(),
        ChatID:     c.ChatID.String(),
        Online:     online,
    }
    msg, _ := NewMessage("connection_status", payload)
    h.BroadcastMessage(msg)
}