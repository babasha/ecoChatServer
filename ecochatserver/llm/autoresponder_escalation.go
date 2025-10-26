package llm

import (
	"context"
	"log"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// handleEscalatedChat обрабатывает сообщения в эскалированном чате
func (ar *AutoResponder) handleEscalatedChat(ctx context.Context, chat *models.Chat, msg *models.Message, escalation *EscalationState) (*models.Message, error) {
	// Проверяем, прошло ли EscalationTimeout с момента эскалации
	if time.Since(escalation.EscalatedAt) > EscalationTimeout && escalation.ReturnedAt == nil {
		// Проверяем, отвечал ли админ после эскалации
		adminAnswered := ar.checkAdminResponse(chat, escalation.EscalatedAt)

		if !adminAnswered {
			// Админ не ответил, возвращаем LLM с извинением
			now := time.Now()
			ar.mu.Lock()
			escalation.ReturnedAt = &now
			ar.mu.Unlock()

			apologeticMsg := &models.Message{
				ChatID:    chat.ID,
				Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
				Sender:    "admin",
				SenderID:  uuid.Nil,
				Timestamp: now,
				Read:      true,
				Type:      "text",
				Metadata: map[string]interface{}{
					"isAutoResponse": true,
					"isApology":      true,
					"escalationEnd":  true,
				},
			}

			// Продолжаем нормальную обработку
			return apologeticMsg, nil
		}
	}

	// Если админ уже ответил или время еще не истекло, LLM не отвечает
	return nil, nil
}

// checkAdminResponse проверяет, отвечал ли админ после указанного времени
func (ar *AutoResponder) checkAdminResponse(chat *models.Chat, after time.Time) bool {
	// Загружаем последние сообщения из базы данных
	chatWithMessages, _, err := database.GetChatByID(chat.ID, MaxMessagesLoad, "") // Последние сообщения
	if err != nil {
		log.Printf("checkAdminResponse: ошибка загрузки сообщений: %v", err)
		return false
	}

	// Проверяем сообщения
	for i := len(chatWithMessages.Messages) - 1; i >= 0; i-- {
		msg := chatWithMessages.Messages[i]
		if msg.Timestamp.After(after) && msg.Sender == "admin" {
			// Проверяем, что это не автоответ
			if metadata, ok := msg.Metadata["isAutoResponse"].(bool); !ok || !metadata {
				return true
			}
		}
	}

	return false
}

// escalationWatcher периодически проверяет эскалированные чаты и возвращает LLM при необходимости
func (ar *AutoResponder) escalationWatcher() {
	ticker := time.NewTicker(EscalationCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		ar.checkEscalations()
	}
}

// checkEscalations проверяет все эскалированные чаты и отправляет извинения если нужно
func (ar *AutoResponder) checkEscalations() {
	ar.mu.RLock()
	escalationsToCheck := make(map[string]*EscalationState)
	for chatID, escalation := range ar.escalations {
		escalationsToCheck[chatID] = escalation
	}
	ar.mu.RUnlock()

	now := time.Now()
	escalationsToCleanup := make([]string, 0)

	for chatID, escalation := range escalationsToCheck {
		// Очищаем старые завершенные эскалации (старше EscalationTTL после возврата)
		if escalation.ReturnedAt != nil {
			if now.Sub(*escalation.ReturnedAt) > EscalationTTL {
				escalationsToCleanup = append(escalationsToCleanup, chatID)
				continue
			}
			// Эскалация уже завершена, пропускаем
			continue
		}

		if time.Since(escalation.EscalatedAt) > EscalationTimeout {
			// Загружаем чат из базы
			chatUUID, err := uuid.Parse(chatID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка парсинга chatID %s: %v", chatID, err)
				continue
			}

			lightChat, err := queries.GetChatLightweight(database.DB, chatUUID)
			if err != nil {
				log.Printf("escalationWatcher: ошибка загрузки чата %s: %v", chatID, err)
				continue
			}

			// Проверяем отвечал ли админ
			adminAnswered := ar.checkAdminResponse(lightChat, escalation.EscalatedAt)
			if !adminAnswered {
				// Отправляем извинение
				ar.sendApologyMessage(lightChat)
			}
		}
	}

	// Очищаем завершенные эскалации
	if len(escalationsToCleanup) > 0 {
		ar.mu.Lock()
		for _, chatID := range escalationsToCleanup {
			delete(ar.escalations, chatID)
			log.Printf("[AUTORESPONDER] [ESCALATION] Очищена завершенная эскалация для чата %s", chatID)
		}
		ar.mu.Unlock()
	}
}

// sendApologyMessage отправляет сообщение-извинение от LLM
func (ar *AutoResponder) sendApologyMessage(chat *models.Chat) {
	now := time.Now()
	chatKey := chat.ID.String()

	// Обновляем состояние эскалации
	ar.mu.Lock()
	if escalation := ar.escalations[chatKey]; escalation != nil {
		escalation.ReturnedAt = &now
	}
	ar.mu.Unlock()

	// Создаем сообщение извинения
	apologeticMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   "Прошу прощения за ожидание! Наш специалист временно недоступен. Я постараюсь помочь вам сам. Пожалуйста, повторите свой вопрос. 🙏",
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"isApology":      true,
			"escalationEnd":  true,
		},
	}

	// Сохраняем в базу данных
	saved, err := database.AddMessage(
		chat.ID,
		apologeticMsg.Content,
		apologeticMsg.Sender,
		apologeticMsg.SenderID,
		apologeticMsg.Type,
		apologeticMsg.Metadata,
	)
	if err != nil {
		log.Printf("sendApologyMessage: ошибка сохранения сообщения: %v", err)
		return
	}

	log.Printf("escalationWatcher: отправлено извинение в чат %s", chatKey)

	// Здесь нужно отправить WebSocket уведомление
	// Но у нас нет доступа к WebSocketHub отсюда
	// Поэтому добавим callback функцию
	if ar.onApologyMessage != nil {
		ar.onApologyMessage(chat.ID, saved)
	}
}
