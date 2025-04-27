// internal/database/queries.go
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/egor/ecochatserver/models"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	dbQueryTimeout  = 5 * time.Second
)

// ─────────────────────────── GetAdmin

func GetAdmin(email string) (*models.Admin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var admin models.Admin
	var avatarNull sql.NullString

	const q = `
		SELECT id, name, email, password_hash, avatar, role, client_id, active
		FROM admins
		WHERE email = $1`
	if err := DB.QueryRowContext(ctx, q, email).Scan(
		&admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash,
		&avatarNull, &admin.Role, &admin.ClientID, &admin.Active,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("GetAdmin: %w", err)
	}
	admin.Avatar = nullStringToPointer(avatarNull)
	return &admin, nil
}

func VerifyPassword(pw, hash string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw))
}

// ─────────────────────────── GetChats

func GetChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > MaxPageSize {
		size = DefaultPageSize
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// 1) общее количество
	var total int
	countQ := `
		SELECT COUNT(*)
		FROM chats
		WHERE client_id = $1 AND (assigned_to = $2 OR assigned_to IS NULL)`
	if err := DB.QueryRowContext(ctx, countQ, clientID, adminID).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 2) сами чаты
	const q = `
		SELECT
			c.id, c.created_at, c.updated_at, c.status,
			u.id, u.name, u.email, u.avatar,
			COUNT(CASE WHEN m.sender = 'user' AND m.read = false THEN 1 END) AS unread,
			l.id, l.content, l.sender, l.timestamp
		FROM chats c
		JOIN users u ON c.user_id = u.id
		LEFT JOIN messages m ON m.chat_id = c.id
		LEFT JOIN LATERAL (
			SELECT id, content, sender, timestamp
			FROM messages
			WHERE chat_id = c.id
			ORDER BY timestamp DESC
			LIMIT 1
		) l ON TRUE
		WHERE c.client_id = $1 AND (c.assigned_to = $2 OR c.assigned_to IS NULL)
		GROUP BY
			c.id, u.id, l.id, l.content, l.sender, l.timestamp
		ORDER BY c.updated_at DESC
		LIMIT $3 OFFSET $4`
	rows, err := DB.QueryContext(ctx, q, clientID, adminID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []models.ChatResponse
	for rows.Next() {
		var (
			chat       models.ChatResponse
			user       models.User
			avatarNull sql.NullString
			unread     int
			lastID     sql.NullString
			lastCont   sql.NullString
			lastSender sql.NullString
			lastTime   sql.NullTime
		)

		if err := rows.Scan(
			&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
			&user.ID, &user.Name, &user.Email, &avatarNull,
			&unread,
			&lastID, &lastCont, &lastSender, &lastTime,
		); err != nil {
			return nil, 0, err
		}

		user.Avatar = nullStringToPointer(avatarNull)
		chat.User = user
		chat.UnreadCount = unread

		if lastID.Valid {
			chat.LastMessage = &models.Message{
				ID:        uuid.MustParse(lastID.String),
				Content:   lastCont.String,
				Sender:    lastSender.String,
				Timestamp: lastTime.Time,
			}
		}
		result = append(result, chat)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return result, total, nil
}

// ─────────────────────────── GetChatByID

func GetChatByID(chatID uuid.UUID, page, size int) (*models.Chat, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > MaxPageSize {
		size = DefaultPageSize
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// 1) метаданные чата
	var (
		chat         models.Chat
		userID       uuid.UUID
		assignedNull sql.NullString
	)
	metaQ := `
		SELECT id, created_at, updated_at, status, user_id,
		       source, bot_id, client_id, assigned_to
		FROM chats WHERE id = $1`
	if err := DB.QueryRowContext(ctx, metaQ, chatID).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
		&userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedNull,
	); err != nil {
		return nil, 0, err
	}
	chat.AssignedTo = nullStringToPointer(assignedNull)

	// 2) пользователь
	var (
		user       models.User
		avatarNull sql.NullString
	)
	userQ := `SELECT id, name, email, avatar, source, source_id FROM users WHERE id = $1`
	if err := DB.QueryRowContext(ctx, userQ, userID).Scan(
		&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
	); err != nil {
		return nil, 0, err
	}
	user.Avatar = nullStringToPointer(avatarNull)
	chat.User = user

	// 3) общее кол-во сообщений
	var total int
	if err := DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE chat_id=$1", chatID).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 4) список сообщений
	msgQ := `
		SELECT id, content, sender, sender_id, timestamp, read, type, metadata
		FROM messages
		WHERE chat_id=$1
		ORDER BY timestamp ASC
		LIMIT $2 OFFSET $3`
	rows, err := DB.QueryContext(ctx, msgQ, chatID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			m       models.Message
			rawMeta []byte
		)
		if err := rows.Scan(
			&m.ID, &m.Content, &m.Sender, &m.SenderID,
			&m.Timestamp, &m.Read, &m.Type, &rawMeta,
		); err != nil {
			return nil, 0, err
		}
		m.ChatID = chatID
		if len(rawMeta) > 0 {
			_ = json.Unmarshal(rawMeta, &m.Metadata)
		}
		chat.Messages = append(chat.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// 5) последнее сообщение
	var last models.Message
	var raw []byte
	lastQ := `
		SELECT id, content, sender, sender_id, timestamp, read, type, metadata
		FROM messages WHERE chat_id=$1 ORDER BY timestamp DESC LIMIT 1`
	err = DB.QueryRowContext(ctx, lastQ, chatID).Scan(
		&last.ID, &last.Content, &last.Sender, &last.SenderID,
		&last.Timestamp, &last.Read, &last.Type, &raw,
	)
	if err == nil {
		last.ChatID = chatID
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &last.Metadata)
		}
		chat.LastMessage = &last
	} else if err != sql.ErrNoRows {
		return nil, 0, err
	}

	return &chat, total, nil
}

// ─────────────────────────── AddMessage

func AddMessage(
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	msgType string,
	meta map[string]any,
) (*models.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// chat exists?
	var ok bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM chats WHERE id=$1)", chatID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("chat not found")
	}

	now := time.Now()
	msgID := uuid.New()

	var raw interface{}
	if meta != nil {
		b, _ := json.Marshal(meta)
		raw = b
	}

	ins := `
		INSERT INTO messages
		    (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,false,$7,$8)`
	if _, err := tx.ExecContext(ctx, ins, msgID, chatID, content, sender, senderID, now, msgType, raw); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, "UPDATE chats SET updated_at=$1 WHERE id=$2", now, chatID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
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

// ─────────────────────────── MarkMessagesAsRead

func MarkMessagesAsRead(chatID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	res, err := DB.ExecContext(ctx,
		"UPDATE messages SET read=true WHERE chat_id=$1 AND sender='user' AND read=false",
		chatID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}