package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// GetChatLightweight - минимальная загрузка чата без сообщений
func GetChatLightweight(db *sql.DB, chatID uuid.UUID) (*models.Chat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var chat models.Chat
	var userID uuid.UUID

	// Получаем только базовую информацию
	var assignedNull sql.NullString
	var userSourceID sql.NullString

	err := db.QueryRowContext(ctx, `
        SELECT c.id, c.created_at, c.updated_at, c.status,
               c.user_id, c.source, c.bot_id, c.client_id, c.auto_responder_enabled, c.assigned_to,
               u.id, u.name, u.email, u.source, u.source_id
        FROM chats c
        JOIN users u ON c.user_id = u.id
        WHERE c.id = $1
    `, chatID).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
		&userID, &chat.Source, &chat.BotID, &chat.ClientID, &chat.AutoResponderEnabled, &assignedNull,
		&chat.User.ID, &chat.User.Name, &chat.User.Email, &chat.User.Source, &userSourceID,
	)

	if err != nil {
		return nil, err
	}

	if assignedNull.Valid {
		if assignedUUID, err := uuid.Parse(assignedNull.String); err == nil {
			chat.AssignedTo = &assignedUUID
		}
	}

	if userSourceID.Valid {
		chat.User.SourceID = userSourceID.String
	}

	return &chat, nil
}

// UpdateChatTimestamp - быстрое обновление времени
func UpdateChatTimestamp(db *sql.DB, chatID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	_, err := db.ExecContext(ctx,
		"UPDATE chats SET updated_at = $1 WHERE id = $2",
		time.Now(), chatID,
	)
	return err
}

// GetClientLanguageFromChat - получает язык клиента из последних сообщений
func GetClientLanguageFromChat(db *sql.DB, chatID uuid.UUID) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Ищем последнее сообщение от пользователя с detectedLanguage в metadata
	var metadata []byte
	err := db.QueryRowContext(ctx, `
		SELECT metadata
		FROM messages
		WHERE chat_id = $1 AND sender = 'user' AND metadata ? 'detectedLanguage'
		ORDER BY timestamp DESC
		LIMIT 1
	`, chatID).Scan(&metadata)

	if err == sql.ErrNoRows {
		return "", nil // Язык не найден
	}
	if err != nil {
		return "", err
	}

	// Парсим JSON для извлечения detectedLanguage
	var metadataMap map[string]interface{}
	if err := json.Unmarshal(metadata, &metadataMap); err != nil {
		return "", err
	}

	if lang, ok := metadataMap["detectedLanguage"].(string); ok {
		return lang, nil
	}

	return "", nil
}
