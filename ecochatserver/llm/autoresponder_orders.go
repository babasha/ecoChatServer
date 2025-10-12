package llm

import (
	"context"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// handleOrderQueryMessage обрабатывает запросы о заказах
func (ar *AutoResponder) handleOrderQueryMessage(ctx context.Context, chat *models.Chat, msg *models.Message, query *OrderQuery) (*models.Message, error) {
	// Извлекаем user_id из магазина
	userID := ExtractUserIDFromChat(ctx, ar.storeClient, chat)

	if userID == 0 {
		log.Printf("[AUTORESPONDER] Не удалось определить user_id магазина для чата %s (user email: %s)",
			chat.ID, chat.User.Email)

		// Если пользователь не найден, LLM все равно может попробовать помочь
		// Но запросы о заказах будут ограничены
	} else {
		log.Printf("[AUTORESPONDER] Определен user_id магазина: %d для чата %s", userID, chat.ID)

		// Сохраняем найденный user_id в метаданных чата для будущих запросов
		if chat.Metadata == nil {
			chat.Metadata = make(map[string]interface{})
		}
		chat.Metadata["store_user_id"] = userID
	}

	// Обрабатываем запрос через HandleOrderQuery
	orderInfo, err := HandleOrderQuery(ctx, ar.storeClient, query, userID)
	if err != nil {
		log.Printf("[AUTORESPONDER] Ошибка обработки запроса о заказе: %v", err)
		// Если не удалось получить информацию о заказе, передаем обработку LLM
		return nil, nil
	}

	// Формируем ответное сообщение
	now := time.Now()
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   orderInfo,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: now,
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"orderQuery":     true,
		},
	}

	log.Printf("[AUTORESPONDER] Отправка информации о заказе в чат %s", chat.ID)

	return botMsg, nil
}
