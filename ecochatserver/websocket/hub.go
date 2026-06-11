package websocket

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	ClientTypeAdmin    = "admin"
	ClientTypeWidget   = "widget"
	ClientTypeDriver   = "driver"   // moooving: водитель
	ClientTypeMoClient = "mo_client" // moooving: авторизованный клиент
)

// Hub отвечает за регистрацию  клиентов и  вещание сообщений.
type Hub struct {
	clients              map[*Client]bool
	adminsByID           map[string]*Client
	widgetsByID          map[string]map[*Client]bool
	chatClients          map[string]map[*Client]bool
	widgetsByUserID      map[string]*Client // Виджеты по userID для обновления chat_id
	pendingWidgetChatIDs map[string]pendingWidgetChat

	// moooving: индексация по chatID, по одному клиенту на (chat, role)
	driversByChatID   map[string]*Client
	moClientsByChatID map[string]*Client

	Register   chan *Client
	Unregister chan *Client

	mu sync.RWMutex

	// Статистика для мониторинга
	stats hubStatsInternal
}

type pendingWidgetChat struct {
	chatID       uuid.UUID
	userSourceID string
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
		clients:              make(map[*Client]bool),
		adminsByID:           make(map[string]*Client),
		widgetsByID:          make(map[string]map[*Client]bool),
		chatClients:          make(map[string]map[*Client]bool),
		widgetsByUserID:      make(map[string]*Client),
		pendingWidgetChatIDs: make(map[string]pendingWidgetChat),
		driversByChatID:      make(map[string]*Client),
		moClientsByChatID:    make(map[string]*Client),
		Register:             make(chan *Client),
		Unregister:           make(chan *Client),
	}

	return hub
}

// Run слушает события регистрации и отключения клиентов.
func (h *Hub) Run() {
	// Запускаем горутину для периодического логирования статистики
	go h.logStats()

	for {
		select {
		case c := <-h.Register:
			h.registerClient(c)

		case c := <-h.Unregister:
			h.unregisterClient(c)
		}
	}
}

// registerClient регистрирует нового клиента
func (h *Hub) registerClient(c *Client) {
	h.mu.Lock()

	notifyChatCreated := false
	var notifyChatID uuid.UUID

	h.clients[c] = true

	// Регистрируем по типу клиента
	if c.ClientType == ClientTypeAdmin {
		h.adminsByID[c.ID] = c
		// DEBUG: log.Printf("Админ %s зарегистрирован в adminsByID, всего админов: %d", c.ID, len(h.adminsByID))
	} else if c.ClientType == ClientTypeWidget {
		widgetChatID := c.GetChatID().String()
		if _, ok := h.widgetsByID[widgetChatID]; !ok {
			h.widgetsByID[widgetChatID] = make(map[*Client]bool)
		}
		h.widgetsByID[widgetChatID][c] = true

		// Всегда сохраняем виджет по userID для возможности обновления chat_id
		if c.UserIDString != "" {
			h.widgetsByUserID[c.UserIDString] = c
			// DEBUG: log.Printf("Виджет сохранен по userID %s (string) для последующего обновления chat_id", c.UserIDString)
		} else {
			// Fallback на UUID если по какой-то причине строкового ID нет
			h.widgetsByUserID[c.ID] = c
			// DEBUG: log.Printf("Виджет сохранен по userID %s (UUID) для последующего обновления chat_id", c.ID)
		}

		// Проверяем, есть ли отложенное обновление chat_id для этого виджета
		if c.UserIDString != "" {
			if pending, ok := h.pendingWidgetChatIDs[c.UserIDString]; ok {
				if pending.userSourceID != "" && pending.userSourceID != c.UserIDString {
					log.Printf("UpdateWidgetChatID: SECURITY WARNING - отложенное обновление chat_id не применено: widgetUserID=%s, chatUserSourceID=%s",
						c.UserIDString, pending.userSourceID)
				} else {
					h.applyWidgetChatIDUpdateLocked(c, pending.chatID)
					notifyChatCreated = true
					notifyChatID = pending.chatID
					log.Printf("registerClient: применено отложенное обновление chat_id для виджета %s -> %s",
						c.UserIDString, pending.chatID)
					// После успешного обновления убираем виджет из мапы обновлений по userID
					delete(h.widgetsByUserID, c.UserIDString)
				}
				delete(h.pendingWidgetChatIDs, c.UserIDString)
			}
		}
	} else if c.ClientType == ClientTypeDriver {
		// moooving driver: один driver на чат
		if cid := c.GetChatID(); cid != uuid.Nil {
			h.driversByChatID[cid.String()] = c
		}
	} else if c.ClientType == ClientTypeMoClient {
		// moooving client: один client на чат
		if cid := c.GetChatID(); cid != uuid.Nil {
			h.moClientsByChatID[cid.String()] = c
		}
	}

	// Добавляем в карту клиентов чата — только для клиентов, реально
	// привязанных к чату (uuid.Nil — admin/виджет до создания чата — не группируем,
	// иначе все они попадали в общий бакет "00000000-..." и могли получать чужие
	// сообщения/typing). GetChatID читаем заново — для виджета он мог измениться
	// выше в applyWidgetChatIDUpdateLocked.
	if cid := c.GetChatID(); cid != uuid.Nil {
		chatID := cid.String()
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

	// DEBUG: лог уже выводится в ServeWs
	// log.Printf("Клиент зарегистрирован: type=%s, id=%s, chatID=%s", c.ClientType, c.ID, c.ChatID)

	h.mu.Unlock()

	if notifyChatCreated {
		if h.emitChatCreated(c, notifyChatID) {
			log.Printf("registerClient: отправлено уведомление виджету о новом chat_id %s", notifyChatID)
		} else {
			log.Printf("registerClient: WARNING - не удалось отправить уведомление виджету (канал занят)")
		}
	}
}

// unregisterClient отменяет регистрацию клиента
func (h *Hub) unregisterClient(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Проверяем существование клиента - защита второго уровня (sync.Once в Client + проверка здесь)
	if _, ok := h.clients[c]; !ok {
		// Клиент уже был отключен, выходим (не обновляем статистику!)
		return
	}

	// Удаляем из основной мапы
	delete(h.clients, c)
	c.closeSend()

	// Удаляем по типу клиента
	if c.ClientType == ClientTypeAdmin {
		delete(h.adminsByID, c.ID)
		// DEBUG: log.Printf("Админ %s удален из adminsByID, осталось админов: %d", c.ID, len(h.adminsByID))
	} else if c.ClientType == ClientTypeWidget {
		chatID := c.GetChatID().String()
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
	} else if c.ClientType == ClientTypeDriver {
		if cid := c.GetChatID(); cid != uuid.Nil {
			chatStr := cid.String()
			if existing, ok := h.driversByChatID[chatStr]; ok && existing == c {
				delete(h.driversByChatID, chatStr)
			}
		}
	} else if c.ClientType == ClientTypeMoClient {
		if cid := c.GetChatID(); cid != uuid.Nil {
			chatStr := cid.String()
			if existing, ok := h.moClientsByChatID[chatStr]; ok && existing == c {
				delete(h.moClientsByChatID, chatStr)
			}
		}
	}

	// Удаляем из карты клиентов чата (симметрично registerClient — только не-Nil)
	if cid := c.GetChatID(); cid != uuid.Nil {
		chatID := cid.String()
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

	// DEBUG: лог уже выводится в client.Disconnect()
	// log.Printf("Клиент отключен: type=%s, id=%s", c.ClientType, c.ID)

	// Уведомляем админов об отключении клиента
	// Важно: вызываем ПОСЛЕ unlock, чтобы избежать deadlock
	go h.SendConnectionStatus(c, false)
}

// cleanupClient асинхронно очищает клиента
func (h *Hub) cleanupClient(client *Client) {
	go func() {
		// Даем клиенту короткое время для восстановления
		time.Sleep(100 * time.Millisecond)
		client.Disconnect()
	}()
}

// trySend безопасно кладёт сообщение в канал клиента.
// Если канал переполнен/закрыт — планирует очистку клиента и возвращает false.
func (h *Hub) trySend(c *Client, message []byte) bool {
	if c.TrySend(message) {
		return true
	}
	go h.cleanupClient(c)
	return false
}

// snapshotAdmins возвращает снапшот подключённых админов под RLock,
// чтобы рассылка шла без удержания мьютекса.
func (h *Hub) snapshotAdmins() []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	admins := make([]*Client, 0, len(h.adminsByID))
	for _, admin := range h.adminsByID {
		admins = append(admins, admin)
	}
	return admins
}

// fanout рассылает сообщения снапшоту клиентов. messageFor вызывается для
// каждого клиента и может вернуть персонализированный payload; nil — пропуск.
// Возвращает число успешно поставленных в очередь сообщений.
func (h *Hub) fanout(clients []*Client, messageFor func(*Client) []byte) int {
	sent := 0
	for _, c := range clients {
		msg := messageFor(c)
		if msg == nil {
			continue
		}
		if h.trySend(c, msg) {
			sent++
		}
	}
	return sent
}

// SendToAdmin пытается отправить сообщение конкретному админу.
func (h *Hub) SendToAdmin(adminID string, message []byte) bool {
	h.mu.RLock()
	c, ok := h.adminsByID[adminID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return h.trySend(c, message)
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

	sent := h.fanout(clients, func(*Client) []byte { return message })
	log.Printf("Отправлено %d сообщений в чат %s", sent, chatID)
	return sent
}

// SendConnectionStatus уведомляет о подключении/отключении.
// ОПТИМИЗИРОВАНО: отправляет уведомления только админам, а не всем клиентам
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
		ChatID:     c.GetChatID().String(),
		Online:     online,
		Timestamp:  time.Now().Format(time.RFC3339),
	}
	msg, _ := NewMessage("connection_status", payload)

	// Отправляем только админам вместо broadcast всем клиентам
	h.SendToAllAdmins(msg)
	// DEBUG: log.Printf("SendConnectionStatus: уведомление о %s %s отправлено %d админам",
	//	c.ClientType, map[bool]string{true: "подключении", false: "отключении"}[online], sent)
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

// GetOnlineAdminIDs возвращает список ID всех подключенных (онлайн) админов
func (h *Hub) GetOnlineAdminIDs() []uuid.UUID {
	h.mu.RLock()
	defer h.mu.RUnlock()

	adminIDs := make([]uuid.UUID, 0, len(h.adminsByID))
	for _, admin := range h.adminsByID {
		if admin.UserID != uuid.Nil {
			adminIDs = append(adminIDs, admin.UserID)
		}
	}

	return adminIDs
}

// SendToAllAdmins отправляет сообщение всем подключенным админам
func (h *Hub) SendToAllAdmins(message []byte) int {
	return h.fanout(h.snapshotAdmins(), func(*Client) []byte { return message })
}

// SendToAllAdminsWithCallback отправляет персонализированное сообщение каждому админу.
// callback вызывается для каждого админа и должен вернуть сообщение для этого админа.
func (h *Hub) SendToAllAdminsWithCallback(callback func(*Client) []byte) int {
	return h.fanout(h.snapshotAdmins(), callback)
}

// SendToAllAdminsExcept отправляет сообщение всем админам КРОМЕ указанного.
// Используется чтобы не отправлять админу его собственное сообщение (оптимистичный UI).
func (h *Hub) SendToAllAdminsExcept(message []byte, excludeAdminID uuid.UUID) int {
	return h.fanout(h.snapshotAdmins(), func(c *Client) []byte {
		if c.UserID == excludeAdminID {
			return nil // пропускаем админа-отправителя
		}
		return message
	})
}

// SendToChatAndAdmins отправляет сообщение как в чат, так и всем админам
func (h *Hub) SendToChatAndAdmins(chatID string, message []byte) int {
	chatSent := h.SendToChat(chatID, message)
	adminSent := h.SendToAllAdmins(message)
	return chatSent + adminSent
}

func (h *Hub) applyWidgetChatIDUpdateLocked(client *Client, newChatID uuid.UUID) {
	oldChatIDStr := client.GetChatID().String()
	if oldChatIDStr != "" {
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

	client.SetChatID(newChatID)

	newChatIDStr := newChatID.String()
	if _, ok := h.widgetsByID[newChatIDStr]; !ok {
		h.widgetsByID[newChatIDStr] = make(map[*Client]bool)
	}
	h.widgetsByID[newChatIDStr][client] = true

	if _, ok := h.chatClients[newChatIDStr]; !ok {
		h.chatClients[newChatIDStr] = make(map[*Client]bool)
	}
	h.chatClients[newChatIDStr][client] = true
}

func (h *Hub) emitChatCreated(client *Client, newChatID uuid.UUID) bool {
	payload := struct {
		ChatID string `json:"chatId"`
	}{
		ChatID: newChatID.String(),
	}
	msg, err := NewMessage("chat_created", payload)
	if err != nil {
		return false
	}
	return client.TrySend(msg)
}

// UpdateWidgetChatID обновляет chat_id для виджета после создания чата
// БЕЗОПАСНОСТЬ: проверяет что виджет принадлежит этому userID
func (h *Hub) UpdateWidgetChatID(userID string, newChatID uuid.UUID, chatUserSourceID string) bool {
	h.mu.Lock()

	// Находим виджет по userID
	client, exists := h.widgetsByUserID[userID]
	if !exists {
		// Возможно, виджет уже привязан к этому чату (например, после отложенного обновления)
		if clients, ok := h.chatClients[newChatID.String()]; ok {
			for existing := range clients {
				if existing.ClientType == ClientTypeWidget && existing.UserIDString == userID {
					h.mu.Unlock()
					log.Printf("UpdateWidgetChatID: виджет %s уже привязан к чату %s", userID, newChatID)
					return true
				}
			}
		}

		// Проверяем безопасность перед сохранением отложенного обновления
		if chatUserSourceID != "" && chatUserSourceID != userID {
			h.mu.Unlock()
			log.Printf("UpdateWidgetChatID: SECURITY WARNING - попытка обновить чужой чат без активной сессии! widgetUserID=%s, chatUserSourceID=%s",
				userID, chatUserSourceID)
			return false
		}

		h.pendingWidgetChatIDs[userID] = pendingWidgetChat{
			chatID:       newChatID,
			userSourceID: chatUserSourceID,
		}
		h.mu.Unlock()
		log.Printf("UpdateWidgetChatID: виджет с userID %s не найден, откладываем обновление до подключения", userID)
		return false
	}

	// ПРОВЕРКА БЕЗОПАСНОСТИ: виджет может обновлять только свой чат
	if chatUserSourceID != userID {
		h.mu.Unlock()
		log.Printf("UpdateWidgetChatID: SECURITY WARNING - попытка обновить чужой чат! widgetUserID=%s, chatUserSourceID=%s",
			userID, chatUserSourceID)
		return false
	}

	oldChatID := client.GetChatID()
	log.Printf("UpdateWidgetChatID: [SAFE] обновление chat_id для виджета %s: %s -> %s",
		userID, oldChatID, newChatID)

	h.applyWidgetChatIDUpdateLocked(client, newChatID)
	delete(h.widgetsByUserID, userID)
	delete(h.pendingWidgetChatIDs, userID)

	log.Printf("UpdateWidgetChatID: виджет успешно перерегистрирован с новым chat_id %s", newChatID)

	// ВАЖНО: Разблокируем мьютекс ПЕРЕД отправкой в канал, чтобы избежать deadlock
	h.mu.Unlock()

	if h.emitChatCreated(client, newChatID) {
		log.Printf("UpdateWidgetChatID: отправлено уведомление виджету о новом chat_id %s", newChatID)
	} else {
		log.Printf("UpdateWidgetChatID: WARNING - не удалось отправить уведомление виджету (канал занят)")
	}

	return true
}

// GetActiveClients возвращает текущее количество активных клиентов
func (h *Hub) GetActiveClients() map[string]int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return map[string]int{
		"total":     len(h.clients),
		"admin":     len(h.adminsByID),
		"widget":    len(h.widgetsByID),
		"driver":    len(h.driversByChatID),
		"mo_client": len(h.moClientsByChatID),
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

// SendToDriverInChat отправляет сообщение водителю, подключенному к указанному чату.
// Возвращает true, если сообщение поставлено в канал.
func (h *Hub) SendToDriverInChat(chatID string, message []byte) bool {
	h.mu.RLock()
	c, ok := h.driversByChatID[chatID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return h.trySend(c, message)
}

// SendToMoClientInChat отправляет сообщение moooving-клиенту, подключенному к чату.
func (h *Hub) SendToMoClientInChat(chatID string, message []byte) bool {
	h.mu.RLock()
	c, ok := h.moClientsByChatID[chatID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return h.trySend(c, message)
}

// CloseMooovingChat отключает WS-клиентов moooving (driver, mo_client),
// привязанных к этому чату, после рассылки финального сообщения.
// Используется когда заказ выполнен и чат архивирован.
func (h *Hub) CloseMooovingChat(chatID string, finalMessage []byte) {
	h.mu.Lock()
	var toDisconnect []*Client
	if c, ok := h.driversByChatID[chatID]; ok {
		toDisconnect = append(toDisconnect, c)
	}
	if c, ok := h.moClientsByChatID[chatID]; ok {
		toDisconnect = append(toDisconnect, c)
	}
	h.mu.Unlock()

	for _, c := range toDisconnect {
		if finalMessage != nil {
			c.TrySend(finalMessage)
		}
		go func(client *Client) {
			time.Sleep(200 * time.Millisecond) // дать клиенту получить финальное сообщение
			client.Disconnect()
		}(c)
	}
}
