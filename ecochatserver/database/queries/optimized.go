package queries

import (
    "context"
    "database/sql"
    "time"
    
    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
)

// GetChatLightweight - минимальная загрузка чата без сообщений
func GetChatLightweight(db *sql.DB, chatID uuid.UUID) (*models.Chat, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    var chat models.Chat
    var userID uuid.UUID
    
    // Получаем только базовую информацию
    err := db.QueryRowContext(ctx, `
        SELECT c.id, c.created_at, c.updated_at, c.status,
               c.user_id, c.source, c.client_id,
               u.id, u.name, u.email, u.source
        FROM chats c
        JOIN users u ON c.user_id = u.id
        WHERE c.id = $1
    `, chatID).Scan(
        &chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
        &userID, &chat.Source, &chat.ClientID,
        &chat.User.ID, &chat.User.Name, &chat.User.Email, &chat.User.Source,
    )
    
    if err != nil {
        return nil, err
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