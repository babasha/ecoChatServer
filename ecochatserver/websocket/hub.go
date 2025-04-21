package websocket

import (
    "encoding/json"
    "log"
)

type Hub struct {
    clients     map[*Client]bool
    adminsByID  map[string]*Client
    widgetsByID map[string]map[*Client]bool
    broadcast   chan []byte
    register    chan *Client
    unregister  chan *Client
}

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

func (h *Hub) Run() {
    for {
        select {
        case client := <-h.register:
            h.clients[client] = true
            if client.clientType == ClientTypeAdmin {
                h.adminsByID[client.id] = client
                log.Printf("Admin registered: %s (total %d)", client.id, len(h.adminsByID))
            } else {
                chatID := client.chatID
                if chatID == "" {
                    chatID = client.id
                }
                if _, ok := h.widgetsByID[chatID]; !ok {
                    h.widgetsByID[chatID] = make(map[*Client]bool)
                }
                h.widgetsByID[chatID][client] = true
                log.Printf("Widget registered: %s for chat %s (widgets %d)", client.id, chatID, countWidgets(h.widgetsByID))
            }
            log.Printf("Client connected: total %d", len(h.clients))

        case client := <-h.unregister:
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
                if client.clientType == ClientTypeAdmin {
                    delete(h.adminsByID, client.id)
                    log.Printf("Admin disconnected: %s", client.id)
                } else {
                    chatID := client.chatID
                    if chatID == "" {
                        chatID = client.id
                    }
                    if widgets, ok := h.widgetsByID[chatID]; ok {
                        delete(widgets, client)
                        if len(widgets) == 0 {
                            delete(h.widgetsByID, chatID)
                        }
                    }
                    log.Printf("Widget disconnected: %s for chat %s", client.id, chatID)
                }
                log.Printf("Client disconnected: total %d", len(h.clients))
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

func (h *Hub) Broadcast(message []byte) {
    h.broadcast <- message
}

func (h *Hub) SendToAdmin(adminID string, message []byte) bool {
    admin, ok := h.adminsByID[adminID]
    if !ok {
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

func (h *Hub) SendToChat(chatID string, message []byte) int {
    widgets, ok := h.widgetsByID[chatID]
    if !ok {
        return 0
    }
    sent := 0
    for w := range widgets {
        select {
        case w.send <- message:
            sent++
        default:
            close(w.send)
            delete(h.clients, w)
            delete(widgets, w)
        }
    }
    if len(widgets) == 0 {
        delete(h.widgetsByID, chatID)
    }
    return sent
}

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
        }{Status: status},
    })
    if err != nil {
        log.Printf("Error creating connection status: %v", err)
        return
    }
    select {
    case client.send <- data:
    default:
        log.Printf("Failed to send connection status")
    }
}

func (h *Hub) BroadcastToChatAndAdmin(chatID, adminID string, message []byte) {
    sent := h.SendToChat(chatID, message)
    if sent > 0 {
        log.Printf("Message sent to %d widgets for chat %s", sent, chatID)
    }
    if adminID != "" && h.SendToAdmin(adminID, message) {
        log.Printf("Message sent to admin %s", adminID)
    }
}

func countWidgets(m map[string]map[*Client]bool) int {
    total := 0
    for _, widgets := range m {
        total += len(widgets)
    }
    return total
}