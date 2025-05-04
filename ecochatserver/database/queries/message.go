package queries

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "time"

    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
)

func AddMessage(
    db *sql.DB,
    chatID uuid.UUID,
    content, sender string,
    senderID uuid.UUID,
    msgType string,
    meta map[string]any,
) (*models.Message, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer tx.Rollback()

    // Проверяем, существует ли чат
    var ok bool
    if err := tx.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM chats WHERE id=$1)", chatID,
    ).Scan(&ok); err != nil {
        return nil, fmt.Errorf("проверка чата: %w", err)
    }
    if !ok {
        return nil, errors.New("chat not found")
    }

    now := time.Now()
    msgID := uuid.New()
    var metaJSON []byte
    if meta != nil {
        metaJSON, _ = json.Marshal(meta)
    }

    // Вставляем сообщение
    if _, err := tx.ExecContext(ctx, `
        INSERT INTO messages
               (id,chat_id,content,sender,sender_id,
                timestamp,read,type,metadata)
        VALUES ($1,$2,$3,$4,$5,$6,false,$7,$8)`,
        msgID, chatID, content, sender, senderID, now, msgType, metaJSON,
    ); err != nil {
        return nil, fmt.Errorf("вставка сообщения: %w", err)
    }

    // Обновляем время последнего изменения чата
    if _, err := tx.ExecContext(ctx,
        "UPDATE chats SET updated_at=$1 WHERE id=$2", now, chatID,
    ); err != nil {
        return nil, fmt.Errorf("обновление чата: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("commit tx: %w", err)
    }

    return &models.Message{
        ID:        msgID,
        ChatID:    chatID,
        Content:   content,
        Sender:    sender,
        SenderID:  senderID,
        Timestamp: now,
        Read:      false,
        Type:      msgType,
        Metadata:  meta,
    }, nil
}

func MarkMessagesAsRead(db *sql.DB, chatID uuid.UUID) error {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    _, err := db.ExecContext(ctx,
        "UPDATE messages SET read=true WHERE chat_id=$1 AND sender='user' AND read=false",
        chatID,
    )
    return err
}