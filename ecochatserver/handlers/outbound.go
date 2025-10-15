package handlers

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
)

// dispatchExternalMessage отправляет сообщение во внешний канал, если это необходимо
func dispatchExternalMessage(chatID uuid.UUID, message *models.Message) {
	if message == nil {
		return
	}

	if !strings.EqualFold(message.Sender, "admin") {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	chat, err := database.GetChatLightweight(chatID)
	if err != nil {
		log.Printf("dispatchExternalMessage: не удалось загрузить чат %s: %v", chatID, err)
		return
	}

	switch strings.ToLower(chat.Source) {
	case instagramSource:
		if err := sendInstagramOutgoingMessage(ctx, chat, message); err != nil {
			log.Printf("dispatchExternalMessage: ошибка отправки сообщения в Instagram: %v", err)
		}
	default:
		// Для других источников пока ничего не делаем
	}
}
