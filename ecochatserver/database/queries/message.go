package queries

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

func AddMessage(
	db *sql.DB,
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	msgType string,
	meta map[string]any,
) (*models.Message, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	now := time.Now()
	msgID := uuid.New()
	var metaJSON []byte
	if meta != nil {
		metaJSON, _ = json.Marshal(meta)
	}

	// Один атомарный statement вместо транзакции из SELECT+INSERT+UPDATE:
	// проверяем чат, вставляем сообщение и двигаем updated_at чата за один
	// round-trip. В PostgreSQL data-modifying части CTE выполняются ровно один
	// раз и под единым снапшотом, поэтому проверка is_archived согласована со
	// вставкой (без TOCTOU исходной транзакции). Возвращаем существование чата и
	// факт вставки, чтобы отличить "not found" от "archived".
	const q = `
        WITH chat_state AS (
            SELECT id, is_archived FROM chats WHERE id = $2
        ), ins AS (
            INSERT INTO messages
                   (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
            SELECT $1, $2, $3, $4, $5, $6, false, $7, $8
              FROM chat_state
             WHERE is_archived = false
            RETURNING id
        ), upd AS (
            UPDATE chats SET updated_at = $6
             WHERE id = (SELECT id FROM chat_state WHERE is_archived = false)
            RETURNING id
        )
        SELECT
            (SELECT count(*) FROM chat_state) AS chat_exists,
            (SELECT count(*) FROM ins)        AS inserted`

	var chatExists, inserted int
	if err := db.QueryRowContext(ctx, q,
		msgID, chatID, content, sender, senderID, now, msgType, metaJSON,
	).Scan(&chatExists, &inserted); err != nil {
		return nil, fmt.Errorf("вставка сообщения: %w", err)
	}
	if chatExists == 0 {
		return nil, errors.New("chat not found")
	}
	if inserted == 0 {
		return nil, errors.New("chat is archived")
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

// AddMessageWithID добавляет сообщение с заданным ID и временной меткой (идемпотентная операция)
func AddMessageWithID(
	db *sql.DB,
	messageID uuid.UUID,
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	timestamp time.Time,
	msgType string,
	meta map[string]any,
	source string,
) (*models.Message, error) {
	ctx, cancel := WithDBContext()
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

	var metaJSON []byte
	if meta != nil {
		metaJSON, _ = json.Marshal(meta)
	}

	// Используем безопасную функцию вставки из миграции
	var resultMessageID uuid.UUID
	err = tx.QueryRowContext(ctx, `
        SELECT insert_message_safe($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		messageID, chatID, content, sender, senderID, timestamp, msgType, metaJSON, source,
	).Scan(&resultMessageID)

	if err != nil {
		return nil, fmt.Errorf("вставка сообщения: %w", err)
	}

	// Обновляем время последнего изменения чата
	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at=$1 WHERE id=$2", timestamp, chatID,
	); err != nil {
		return nil, fmt.Errorf("обновление чата: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	return &models.Message{
		ID:        resultMessageID,
		ChatID:    chatID,
		Content:   content,
		Sender:    sender,
		SenderID:  senderID,
		Timestamp: timestamp,
		Read:      false,
		Type:      msgType,
		Metadata:  meta,
	}, nil
}

// MarkAdminMessagesAsRead помечает сообщения админа как прочитанные (когда клиент просматривает чат)
func MarkAdminMessagesAsRead(db *sql.DB, chatID uuid.UUID) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx,
		"UPDATE messages SET read=true, read_at=CURRENT_TIMESTAMP WHERE chat_id=$1 AND sender='admin' AND read=false",
		chatID,
	)
	return err
}

// MarkSpecificMessagesAsRead помечает конкретные сообщения как прочитанные по их ID
// Возвращает количество помеченных сообщений
func MarkSpecificMessagesAsRead(db *sql.DB, messageIDs []uuid.UUID) (int, error) {
	if len(messageIDs) == 0 {
		return 0, nil
	}

	ctx, cancel := WithDBContext()
	defer cancel()

	// Конвертируем []uuid.UUID в строку для использования в ANY()
	// PostgreSQL поддерживает массивы UUID напрямую через pq.Array
	query := `
		UPDATE messages
		SET read=true, read_at=CURRENT_TIMESTAMP
		WHERE id = ANY($1) AND read=false
	`

	result, err := db.ExecContext(ctx, query, pq.Array(messageIDs))
	if err != nil {
		return 0, fmt.Errorf("MarkSpecificMessagesAsRead: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("MarkSpecificMessagesAsRead: получение rowsAffected: %w", err)
	}

	return int(rowsAffected), nil
}
