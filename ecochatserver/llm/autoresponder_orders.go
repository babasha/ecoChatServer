package llm

import (
	"context"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// handleOrderQueryMessage обрабатывает запросы о заказах
// userID передается как параметр, чтобы избежать повторных HTTP запросов
func (ar *AutoResponder) handleOrderQueryMessage(ctx context.Context, chat *models.Chat, msg *models.Message, query *OrderQuery, userID int) (*models.Message, error) {
	// userID уже получен в ProcessMessage, не делаем повторный запрос
	log.Printf("[AUTORESPONDER] Обработка запроса о заказе: user_id=%d, chat=%s", userID, chat.ID)

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
