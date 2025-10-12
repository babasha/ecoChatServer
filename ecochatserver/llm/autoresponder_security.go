package llm

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// UnauthorizedAttemptTracker отслеживает попытки доступа без авторизации
type UnauthorizedAttemptTracker struct {
	Count        int
	FirstAttempt time.Time
	LastAttempt  time.Time
	Blocked      bool
	BlockedUntil *time.Time
}

// handleSecurityCheck обрабатывает проверку безопасности для запросов о заказах
func (ar *AutoResponder) handleSecurityCheck(ctx context.Context, chat *models.Chat, msg *models.Message, orderQuery *OrderQuery, chatKey string) (*models.Message, error) {
	log.Printf("[AUTORESPONDER] [SECURITY] Обнаружен запрос о заказе в чате %s", chatKey)

	// 🔒 КРИТИЧЕСКАЯ ПРОВЕРКА: проверяем авторизацию ДО обработки
	userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)
	if userID == 0 {
		return ar.handleUnauthorizedAccess(chat, orderQuery, chatKey)
	}

	// ✅ Пользователь авторизован - очищаем счетчик попыток
	ar.mu.Lock()
	delete(ar.unauthorizedAttempts, chatKey)
	ar.mu.Unlock()

	log.Printf("[AUTORESPONDER] [SECURITY] ✓ Авторизованный запрос о заказе: user_id=%d, chat=%s", userID, chatKey)
	return ar.handleOrderQueryMessage(ctx, chat, msg, orderQuery)
}

// handleUnauthorizedAccess обрабатывает неавторизованные попытки доступа к заказам
func (ar *AutoResponder) handleUnauthorizedAccess(chat *models.Chat, orderQuery *OrderQuery, chatKey string) (*models.Message, error) {
	// 🔒 RATE LIMITING: Проверяем количество попыток
	ar.mu.Lock()
	tracker := ar.unauthorizedAttempts[chatKey]
	if tracker == nil {
		tracker = &UnauthorizedAttemptTracker{
			Count:        0,
			FirstAttempt: time.Now(),
		}
		ar.unauthorizedAttempts[chatKey] = tracker
	}

	tracker.Count++
	tracker.LastAttempt = time.Now()

	// Проверяем, не заблокирован ли чат
	if tracker.Blocked && tracker.BlockedUntil != nil && time.Now().Before(*tracker.BlockedUntil) {
		ar.mu.Unlock()
		log.Printf("[AUTORESPONDER] [SECURITY] 🚫 Чат %s ЗАБЛОКИРОВАН до %s (попытка %d)",
			chatKey, tracker.BlockedUntil.Format("15:04:05"), tracker.Count)

		blockedUntil := tracker.BlockedUntil.Format("15:04")
		now := time.Now()
		return &models.Message{
			ChatID:    chat.ID,
			Content:   fmt.Sprintf("🚫 Слишком много попыток доступа к заказам без авторизации.\n\nПовторите попытку после %s или обратитесь к оператору.", blockedUntil),
			Sender:    "admin",
			SenderID:  uuid.Nil,
			Timestamp: now,
			Read:      true,
			Type:      "text",
			Metadata: map[string]interface{}{
				"isAutoResponse": true,
				"securityBlock":  true,
				"rateLimited":    true,
			},
		}, nil
	}

	// После 3 попыток - блокируем на 5 минут
	if tracker.Count >= 3 {
		blockUntil := time.Now().Add(5 * time.Minute)
		tracker.Blocked = true
		tracker.BlockedUntil = &blockUntil

		LogSuspiciousActivity(chat.ID, 0, chat.User.Email, fmt.Sprintf("Множественные попытки доступа к заказам без авторизации (%d попыток)", tracker.Count))
		ar.mu.Unlock()

		log.Printf("[AUTORESPONDER] [SECURITY] 🚨 Чат %s ЗАБЛОКИРОВАН на 5 минут (попытки: %d)", chatKey, tracker.Count)

		now := time.Now()
		return &models.Message{
			ChatID:    chat.ID,
			Content:   "🚫 Обнаружено слишком много попыток доступа к заказам без авторизации.\n\nВаш чат временно заблокирован на 5 минут. Пожалуйста, укажите корректный email для идентификации или обратитесь к оператору.",
			Sender:    "admin",
			SenderID:  uuid.Nil,
			Timestamp: now,
			Read:      true,
			Type:      "text",
			Metadata: map[string]interface{}{
				"isAutoResponse": true,
				"securityBlock":  true,
				"rateLimited":    true,
				"blocked":        true,
			},
		}, nil
	}
	ar.mu.Unlock()

	// 🚨 SECURITY EVENT: Неавторизованная попытка доступа
	LogUnauthorizedOrderAccess(chat.ID, orderQuery.OrderID, chat.User.Email)
	log.Printf("[AUTORESPONDER] [SECURITY] 🚨 БЛОКИРОВАН неавторизованный запрос о заказе в чате %s (email: %s, orderID: %s, попытка: %d)",
		chatKey, chat.User.Email, orderQuery.OrderID, tracker.Count)

	// 🔒 ЖЕСТКАЯ БЛОКИРОВКА: Возвращаем отказ напрямую, НЕ давая LLM шанс обработать
	// Это предотвращает prompt injection и социальную инженерию
	now := time.Now()
	blockedMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   fmt.Sprintf("🔒 Для просмотра информации о заказах необходимо авторизоваться.\n\nПожалуйста, укажите ваш email, который вы использовали при регистрации в магазине.\n\n⚠️ Попытка %d/3", tracker.Count),
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse":     true,
			"botName":            ar.config.BotName,
			"securityBlock":      true,
			"unauthorizedAccess": true,
			"attemptCount":       tracker.Count,
		},
	}

	log.Printf("[AUTORESPONDER] [SECURITY] ✓ Отправлен автоматический отказ в доступе (попытка %d/3)", tracker.Count)
	return blockedMsg, nil
}

// cleanupUnauthorizedAttempts периодически очищает старые записи попыток доступа
func (ar *AutoResponder) cleanupUnauthorizedAttempts() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		ar.mu.Lock()
		now := time.Now()
		for chatKey, tracker := range ar.unauthorizedAttempts {
			// Удаляем записи старше 1 часа
			if now.Sub(tracker.LastAttempt) > 1*time.Hour {
				delete(ar.unauthorizedAttempts, chatKey)
				log.Printf("[AUTORESPONDER] [SECURITY] Очищена запись о попытках доступа для чата %s", chatKey)
			}
			// Разблокируем чаты, у которых истекло время блокировки
			if tracker.Blocked && tracker.BlockedUntil != nil && now.After(*tracker.BlockedUntil) {
				tracker.Blocked = false
				tracker.BlockedUntil = nil
				tracker.Count = 0
				log.Printf("[AUTORESPONDER] [SECURITY] Чат %s разблокирован", chatKey)
			}
		}
		ar.mu.Unlock()
	}
}

// ResetUnauthorizedAttempts сбрасывает счетчик попыток для чата (вызывается при успешной авторизации)
func (ar *AutoResponder) ResetUnauthorizedAttempts(chatID string) {
	ar.mu.Lock()
	delete(ar.unauthorizedAttempts, chatID)
	ar.mu.Unlock()
	log.Printf("[AUTORESPONDER] [SECURITY] Сброшен счетчик попыток для чата %s", chatID)
}
