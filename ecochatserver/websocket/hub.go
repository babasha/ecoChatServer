package websocket

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ClientTypeAdmin  = "admin"
	ClientTypeWidget = "widget"
)

// Hub отвечает за регистрацию  клиентов и  вещание сообщений.
type Hub struct {
	clients     map[*Client]bool
	adminsByID  map[string]*Client
	widgetsByID map[string]map[*Client]bool
	chatClients map[string]map[*Client]bool
	widgetsByUserID map[string]*Client  // Виджеты по userID для обновления chat_id

	Broadcast  chan []byte
	Register   chan *Client
	Unregister chan *Client

	mu sync.RWMutex

	// Статистика для мониторинга
	stats hubStatsInternal

	// Дедупликация сообщений
	sentMessages sync.Map // key: messageHash, value: time.Time
}

// HubStats содержит статистику работы хаба (для чтения, без мьютекса)
type HubStats struct {
	TotalConnections    int64 `json:"totalConnections"`
	ActiveConnections   int64 `json:"activeConnections"`
	TotalMessages       int64 `json:"totalMessages"`
	DisconnectedClients int64 `json:"disconnectedClients"`
}

// hubStatsInternal содержит статистику с мьютексом (для записи)
type hubStatsInternal struct {
	TotalConnections    int64
	ActiveConnections   int64
	TotalMessages       int64
	DisconnectedClients int64
	mu                  sync.RWMutex
}

// NewHub создаёт и инициализирует Hub.
func NewHub() *Hub {
	hub := &Hub{
		clients:         make(map[*Client]bool),
		adminsByID:      make(map[string]*Client),
		widgetsByID:     make(map[string]map[*Client]bool),
		chatClients:     make(map[string]map[*Client]bool),
		widgetsByUserID: make(map[string]*Client),
		Broadcast:       make(chan []byte),
		Register:        make(chan *Client),
		Unregister:      make(chan *Client),
	}

	// Запускаем очистку старых сообщений
	go hub.cleanupOldMessages()

	return hub
}

// cleanupOldMessages периодически очищает старые записи дедупликации
func (h *Hub) cleanupOldMessages() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		h.sentMessages.Range(func(key, value interface{}) bool {
			if timestamp, ok := value.(time.Time); ok {
				if now.Sub(timestamp) > 5*time.Minute {
					h.sentMessages.Delete(key)
				}
			}
			return true
		})
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
		h.adminsByID[c.ID] = c
		log.Printf("Админ %s зарегистрирован в adminsByID, всего админов: %d", c.ID, len(h.adminsByID))
	} else if c.ClientType == ClientTypeWidget {
		if _, ok := h.widgetsByID[c.ChatID.String()]; !ok {
			h.widgetsByID[c.ChatID.String()] = make(map[*Client]bool)
		}
		h.widgetsByID[c.ChatID.String()][c] = true

		// Всегда сохраняем виджет по userID для возможности обновления chat_id
		if c.UserIDString != "" {
			h.widgetsByUserID[c.UserIDString] = c
			log.Printf("Виджет сохранен по userID %s (string) для последующего обновления chat_id", c.UserIDString)
		} else {
			// Fallback на UUID если по какой-то причине строкового ID нет
			h.widgetsByUserID[c.ID] = c
			log.Printf("Виджет сохранен по userID %s (UUID) для последующего обновления chat_id", c.ID)
		}
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
		close(c.Send)
	}

	// Удаляем по типу клиента
	if c.ClientType == ClientTypeAdmin {
		delete(h.adminsByID, c.ID)
		log.Printf("Админ %s удален из adminsByID, осталось админов: %d", c.ID, len(h.adminsByID))
	} else if c.ClientType == ClientTypeWidget {
		chatID := c.ChatID.String()
		if widgets, ok := h.widgetsByID[chatID]; ok {
			delete(widgets, c)
			if len(widgets) == 0 {
				delete(h.widgetsByID, chatID)
			}
		}
		// Удаляем из widgetsByUserID если там есть (попробуем оба варианта)
		if c.UserIDString != "" {
			delete(h.widgetsByUserID, c.UserIDString)
		}
		delete(h.widgetsByUserID, c.ID)
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
		case client.Send <- msg:
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

	// Примечание: TotalMessages теперь увеличивается только для реальных сообщений чата
	// через IncrementChatMessage(), а не для служебных broadcast'ов
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
		case c.Send <- message:
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

	if len(clients) == 0 {
		return 0
	}

	sent := 0
	for _, c := range clients {
		select {
		case c.Send <- message:
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
		ID:         c.ID,
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

	// Копируем только данные, без мьютекса
	return HubStats{
		TotalConnections:    h.stats.TotalConnections,
		ActiveConnections:   h.stats.ActiveConnections,
		TotalMessages:       h.stats.TotalMessages,
		DisconnectedClients: h.stats.DisconnectedClients,
		// mu не копируем - это избегает проблемы с go vet
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

// SendToAllAdmins отправляет сообщение всем подключенным админам
func (h *Hub) SendToAllAdmins(message []byte) int {
	h.mu.RLock()
	admins := make([]*Client, 0, len(h.adminsByID))
	for _, admin := range h.adminsByID {
		admins = append(admins, admin)
	}
	h.mu.RUnlock()

	if len(admins) == 0 {
		log.Printf("SendToAllAdmins: нет подключенных админов")
		return 0
	}

	sent := 0
	for _, admin := range admins {
		log.Printf("SendToAllAdmins: попытка отправить админу %s, message: %s", admin.ID, string(message))
		select {
		case admin.Send <- message:
			sent++
			log.Printf("SendToAllAdmins: сообщение успешно добавлено в канал админа %s", admin.ID)
		default:
			log.Printf("SendToAllAdmins: не удалось отправить админу %s (канал занят)", admin.ID)
			go h.cleanupClient(admin)
		}
	}

	log.Printf("Отправлено %d сообщений всем админам (всего админов: %d)", sent, len(admins))
	return sent
}

// SendToChatAndAdmins отправляет сообщение как в чат, так и всем админам
func (h *Hub) SendToChatAndAdmins(chatID string, message []byte) int {
	chatSent := h.SendToChat(chatID, message)
	adminSent := h.SendToAllAdmins(message)
	return chatSent + adminSent
}

// UpdateWidgetChatID обновляет chat_id для виджета после создания чата
// БЕЗОПАСНОСТЬ: проверяет что виджет принадлежит этому userID
func (h *Hub) UpdateWidgetChatID(userID string, newChatID uuid.UUID, chatUserSourceID string) bool {
	h.mu.Lock()

	// Находим виджет по userID
	client, exists := h.widgetsByUserID[userID]
	if !exists {
		h.mu.Unlock()
		log.Printf("UpdateWidgetChatID: виджет с userID %s не найден", userID)
		return false
	}

	// ПРОВЕРКА БЕЗОПАСНОСТИ: виджет может обновлять только свой чат
	if chatUserSourceID != userID {
		h.mu.Unlock()
		log.Printf("UpdateWidgetChatID: SECURITY WARNING - попытка обновить чужой чат! widgetUserID=%s, chatUserSourceID=%s",
			userID, chatUserSourceID)
		return false
	}

	oldChatID := client.ChatID
	log.Printf("UpdateWidgetChatID: [SAFE] обновление chat_id для виджета %s: %s -> %s",
		userID, oldChatID, newChatID)

	// Удаляем из старых маппингов (только если был chat_id)
	oldChatIDStr := oldChatID.String()
	if oldChatIDStr != "" && oldChatIDStr != "00000000-0000-0000-0000-000000000000" {
		if widgets, ok := h.widgetsByID[oldChatIDStr]; ok {
			delete(widgets, client)
			if len(widgets) == 0 {
				delete(h.widgetsByID, oldChatIDStr)
			}
		}
		if clients, ok := h.chatClients[oldChatIDStr]; ok {
			delete(clients, client)
			if len(clients) == 0 {
				delete(h.chatClients, oldChatIDStr)
			}
		}
	}

	// Обновляем chat_id у клиента
	client.ChatID = newChatID

	// Добавляем в новые маппинги
	newChatIDStr := newChatID.String()
	if _, ok := h.widgetsByID[newChatIDStr]; !ok {
		h.widgetsByID[newChatIDStr] = make(map[*Client]bool)
	}
	h.widgetsByID[newChatIDStr][client] = true

	if _, ok := h.chatClients[newChatIDStr]; !ok {
		h.chatClients[newChatIDStr] = make(map[*Client]bool)
	}
	h.chatClients[newChatIDStr][client] = true

	// Удаляем из widgetsByUserID, так как chat_id теперь известен
	delete(h.widgetsByUserID, userID)

	log.Printf("UpdateWidgetChatID: виджет успешно перерегистрирован с новым chat_id %s", newChatID)

	// ВАЖНО: Разблокируем мьютекс ПЕРЕД отправкой в канал, чтобы избежать deadlock
	h.mu.Unlock()

	// Отправляем виджету уведомление о новом chat_id
	payload := struct {
		ChatID string `json:"chatId"`
	}{
		ChatID: newChatIDStr,
	}
	if msg, err := NewMessage("chat_created", payload); err == nil {
		select {
		case client.Send <- msg:
			log.Printf("UpdateWidgetChatID: отправлено уведомление виджету о новом chat_id %s", newChatID)
		default:
			log.Printf("UpdateWidgetChatID: WARNING - не удалось отправить уведомление виджету (канал занят)")
		}
	}

	return true
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

// GetAllClients возвращает копию всех активных клиентов для безопасного итерирования
func (h *Hub) GetAllClients() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	return clients
}

// GetClientByID находит клиента по ID сессии
func (h *Hub) GetClientByID(sessionID string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for c := range h.clients {
		if c.ID == sessionID {
			return c
		}
	}
	return nil
}

// IncrementChatMessage увеличивает счетчик реальных сообщений чата (не служебных)
func (h *Hub) IncrementChatMessage() {
	h.stats.mu.Lock()
	h.stats.TotalMessages++
	h.stats.mu.Unlock()
}
