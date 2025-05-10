package websocket

import (
    "sync"
    "log"
    "time"
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
    chatClients map[string]map[*Client]bool

    Broadcast  chan []byte
    Register   chan *Client
    Unregister chan *Client

    mu sync.RWMutex
    
    // Статистика для мониторинга
    stats HubStats
}

type HubStats struct {
    TotalConnections    int64
    ActiveConnections   int64
    TotalMessages       int64
    DisconnectedClients int64
    mu                  sync.RWMutex
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
    // Запускаем горутину для периодического логирования статистики
    go h.logStats()
    
    for {
        select {
        case c := <-h.Register:
            h.registerClient(c)

        case c := <-h.Unregister:
            h.unregisterClient(c)

        case msg := <-h.Broadcast:
            h.broadcastMessage(msg)
        }
    }
}

// registerClient регистрирует нового клиента
func (h *Hub) registerClient(c *Client) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
    h.clients[c] = true
    
    // Регистрируем по типу клиента
    if c.ClientType == ClientTypeAdmin {
        h.adminsByID[c.ID.String()] = c
    } else if c.ClientType == ClientTypeWidget {
        if _, ok := h.widgetsByID[c.ChatID.String()]; !ok {
            h.widgetsByID[c.ChatID.String()] = make(map[*Client]bool)
        }
        h.widgetsByID[c.ChatID.String()][c] = true
    }
    
    // Добавляем в карту клиентов чата
    chatID := c.ChatID.String()
    if chatID != "" {
        if _, ok := h.chatClients[chatID]; !ok {
            h.chatClients[chatID] = make(map[*Client]bool)
        }
        h.chatClients[chatID][c] = true
    }
    
    // Обновляем статистику
    h.stats.mu.Lock()
    h.stats.TotalConnections++
    h.stats.ActiveConnections++
    h.stats.mu.Unlock()
    
    log.Printf("Клиент зарегистрирован: type=%s, id=%s, chatID=%s", 
        c.ClientType, c.ID, c.ChatID)
}

// unregisterClient отменяет регистрацию клиента
func (h *Hub) unregisterClient(c *Client) {
    h.mu.Lock()
    defer h.mu.Unlock()
    
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
    
    // Обновляем статистику
    h.stats.mu.Lock()
    h.stats.ActiveConnections--
    h.stats.DisconnectedClients++
    h.stats.mu.Unlock()
    
    log.Printf("Клиент отключен: type=%s, id=%s", c.ClientType, c.ID)
}

// broadcastMessage отправляет сообщение всем клиентам (исправлена race condition)
func (h *Hub) broadcastMessage(msg []byte) {
    h.mu.Lock()
    disconnected := make([]*Client, 0)
    
    for client := range h.clients {
        select {
        case client.send <- msg:
            // Сообщение успешно отправлено
        default:
            // Клиент не готов принять сообщение
            disconnected = append(disconnected, client)
        }
    }
    h.mu.Unlock()
    
    // Отключаем клиентов, которые не смогли получить сообщение
    for _, client := range disconnected {
        h.cleanupClient(client)
    }
    
    // Обновляем статистику
    h.stats.mu.Lock()
    h.stats.TotalMessages++
    h.stats.mu.Unlock()
}

// cleanupClient асинхронно очищает клиента
func (h *Hub) cleanupClient(client *Client) {
    go func() {
        // Даем клиенту короткое время для восстановления
        time.Sleep(100 * time.Millisecond)
        h.Unregister <- client
    }()
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
            go h.cleanupClient(c)
            return false
        }
    }
    return false
}

// SendToChat вещает сообщение всем клиентам конкретного чата.
func (h *Hub) SendToChat(chatID string, message []byte) int {
    h.mu.RLock()
    clients := make([]*Client, 0)
    if chatClients, ok := h.chatClients[chatID]; ok {
        for c := range chatClients {
            clients = append(clients, c)
        }
    }
    h.mu.RUnlock()
    
    sent := 0
    for _, c := range clients {
        select {
        case c.send <- message:
            sent++
        default:
            go h.cleanupClient(c)
        }
    }
    
    log.Printf("Отправлено %d сообщений в чат %s", sent, chatID)
    return sent
}

// SendConnectionStatus уведомляет о подключении/отключении.
func (h *Hub) SendConnectionStatus(c *Client, online bool) {
    payload := struct {
        ClientType string `json:"clientType"`
        ID         string `json:"id"`
        ChatID     string `json:"chatId,omitempty"`
        Online     bool   `json:"online"`
        Timestamp  string `json:"timestamp"`
    }{
        ClientType: c.ClientType,
        ID:         c.ID.String(),
        ChatID:     c.ChatID.String(),
        Online:     online,
        Timestamp:  time.Now().Format(time.RFC3339),
    }
    msg, _ := NewMessage("connection_status", payload)
    h.BroadcastMessage(msg)
}

// GetStats возвращает статистику хаба
func (h *Hub) GetStats() HubStats {
    h.stats.mu.RLock()
    defer h.stats.mu.RUnlock()
    
    return HubStats{
        TotalConnections:    h.stats.TotalConnections,
        ActiveConnections:   h.stats.ActiveConnections,
        TotalMessages:       h.stats.TotalMessages,
        DisconnectedClients: h.stats.DisconnectedClients,
    }
}

// logStats периодически выводит статистику в лог
func (h *Hub) logStats() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()
    
    for range ticker.C {
        stats := h.GetStats()
        log.Printf("Hub статистика: active=%d, total=%d, messages=%d, disconnected=%d",
            stats.ActiveConnections,
            stats.TotalConnections,
            stats.TotalMessages,
            stats.DisconnectedClients,
        )
    }
}

// GetActiveClients возвращает текущее количество активных клиентов
func (h *Hub) GetActiveClients() map[string]int {
    h.mu.RLock()
    defer h.mu.RUnlock()
    
    return map[string]int{
        "total":  len(h.clients),
        "admin":  len(h.adminsByID),
        "widget": len(h.widgetsByID),
    }
}