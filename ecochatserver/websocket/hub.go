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

    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client

    mu sync.RWMutex
}

// NewHub создаёт и инициализирует Hub.
func NewHub() *Hub {
    return &Hub{
        clients:     make(map[*Client]bool),
        adminsByID:  make(map[string]*Client),
        widgetsByID: make(map[string]map[*Client]bool),
        broadcast:   make(chan []byte),
        register:    make(chan *Client),
        unregister:  make(chan *Client),
    }
}

// Run слушает события регистрации, отключения и широковещания.
func (h *Hub) Run() {
    for {
        select {
        case c := <-h.register:
            h.mu.Lock()
            h.clients[c] = true
            if c.clientType == ClientTypeAdmin {
                h.adminsByID[c.id.String()] = c
            } else {
                // для виджетов группируем по chatID
                if _, ok := h.widgetsByID[c.chatID.String()]; !ok {
                    h.widgetsByID[c.chatID.String()] = make(map[*Client]bool)
                }
                h.widgetsByID[c.chatID.String()][c] = true
            }
            h.mu.Unlock()

        case c := <-h.unregister:
            h.mu.Lock()
            delete(h.clients, c)
            if c.clientType == ClientTypeAdmin {
                delete(h.adminsByID, c.id.String())
            } else {
                if widgets, ok := h.widgetsByID[c.chatID.String()]; ok {
                    delete(widgets, c)
                    if len(widgets) == 0 {
                        delete(h.widgetsByID, c.chatID.String())
                    }
                }
            }
            close(c.send)
            h.mu.Unlock()

        case msg := <-h.broadcast:
            h.mu.RLock()
            for client := range h.clients {
                select {
                case client.send <- msg:
                default:
                    // «мёртвый» клиент
                    close(client.send)
                    delete(h.clients, client)
                }
            }
            h.mu.RUnlock()
        }
    }
}

// Broadcast шлёт сообщение всем подключённым.
func (h *Hub) Broadcast(message []byte) {
    h.broadcast <- message
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
            close(c.send)
            delete(h.clients, c)
        }
    }
    return false
}

// SendToWidgets вещает сообщение всем виджетам одного чата.
func (h *Hub) SendToWidgets(chatID string, message []byte) int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    sent := 0
    if pool, ok := h.widgetsByID[chatID]; ok {
        for c := range pool {
            select {
            case c.send <- message:
                sent++
            default:
                close(c.send)
                delete(h.clients, c)
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
        ClientType: c.clientType,
        ID:         c.id.String(),
        ChatID:     c.chatID.String(),
        Online:     online,
    }
    msg, _ := NewMessage("connection_status", payload)
    h.Broadcast(msg)
}