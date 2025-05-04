// internal/database/queries.go
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"github.com/egor/ecochatserver/models"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	dbQueryTimeout  = 5 * time.Second
)

/*────────────────────────── helpers */



func nullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	u, err := uuid.Parse(ns.String)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// isPartitionDDLConflict — true если SQLSTATE 55006
func isPartitionDDLConflict(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "55006" {
		return true
	}
	return strings.Contains(err.Error(), "55006") ||
		strings.Contains(err.Error(), "PARTITION OF") ||
		strings.Contains(err.Error(), "is being used by active queries")
}

/*────────────────────────── GetAdmin */

func GetAdmin(email string) (*models.Admin, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var admin models.Admin
	var avatarNull sql.NullString

	const q = `
		SELECT id,name,email,password_hash,avatar,role,client_id,active
		  FROM admins
		 WHERE email=$1`
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

/*────────────────────────── GetChats */

func GetChats(clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > MaxPageSize {
		size = DefaultPageSize
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var total int
	if err := DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM chats
		WHERE client_id=$1 AND (assigned_to=$2 OR assigned_to IS NULL)`,
		clientID, adminID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	const q = `
	  SELECT
		c.id,c.created_at,c.updated_at,c.status,
		u.id,u.name,u.email,u.avatar,
		COUNT(CASE WHEN m.sender='user' AND m.read=false THEN 1 END) AS unread,
		l.id,l.content,l.sender,l.timestamp
	  FROM chats c
	  JOIN users u ON c.user_id=u.id
	  LEFT JOIN messages m ON m.chat_id=c.id
	  LEFT JOIN LATERAL (
		SELECT id,content,sender,timestamp
		  FROM messages
		 WHERE chat_id=c.id
		 ORDER BY timestamp DESC
		 LIMIT 1
	  ) l ON TRUE
	  WHERE c.client_id=$1 AND (c.assigned_to=$2 OR c.assigned_to IS NULL)
	  GROUP BY c.id,u.id,l.id,l.content,l.sender,l.timestamp
	  ORDER BY c.updated_at DESC
	  LIMIT $3 OFFSET $4
	`
	rows, err := DB.QueryContext(ctx, q, clientID, adminID, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var list []models.ChatResponse
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
			&unread, &lastID, &lastCont, &lastSender, &lastTime,
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
		list = append(list, chat)
	}
	return list, total, rows.Err()
}

/*────────────────────────── GetChatByID */

func GetChatByID(chatID uuid.UUID, page, size int) (*models.Chat, int, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > MaxPageSize {
		size = DefaultPageSize
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var (
		chat         models.Chat
		userID       uuid.UUID
		assignedNull sql.NullString
	)
	if err := DB.QueryRowContext(ctx, `
		SELECT id,created_at,updated_at,status,user_id,
		       source,bot_id,client_id,assigned_to
		  FROM chats WHERE id=$1`,
		chatID,
	).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
		&userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedNull,
	); err != nil {
		return nil, 0, err
	}
	var err error
	chat.AssignedTo, err = nullUUIDToPointer(assignedNull)
	if err != nil {
		return nil, 0, err
	}

	// user data
	var (
		user       models.User
		avatarNull sql.NullString
	)
	if err := DB.QueryRowContext(ctx, `
		SELECT id,name,email,avatar,source,source_id
		  FROM users WHERE id=$1`,
		userID,
	).Scan(&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID); err != nil {
		return nil, 0, err
	}
	user.Avatar = nullStringToPointer(avatarNull)
	chat.User = user

	// total messages
	var total int
	if err := DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE chat_id=$1",
		chatID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	// fetch messages
	rows, err := DB.QueryContext(ctx, `
		SELECT id,content,sender,sender_id,timestamp,read,type,metadata
		  FROM messages
		 WHERE chat_id=$1
		 ORDER BY timestamp ASC
		 LIMIT $2 OFFSET $3`,
		chatID, size, (page-1)*size,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	for rows.Next() {
		var m models.Message
		var raw []byte
		if err := rows.Scan(
			&m.ID, &m.Content, &m.Sender, &m.SenderID,
			&m.Timestamp, &m.Read, &m.Type, &raw,
		); err != nil {
			return nil, 0, err
		}
		m.ChatID = chatID
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &m.Metadata)
		}
		chat.Messages = append(chat.Messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// last message
	var last models.Message
	var raw []byte
	err = DB.QueryRowContext(ctx, `
		SELECT id,content,sender,sender_id,timestamp,read,type,metadata
		  FROM messages
		 WHERE chat_id=$1
		 ORDER BY timestamp DESC LIMIT 1`,
		chatID,
	).Scan(
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

/*────────────────────────── ensurePartitionExists */

func ensurePartitionExists(ts time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	weekStart := ts.AddDate(0, 0, -int(ts.Weekday())+1)
	partition := fmt.Sprintf("messages_week_%s", weekStart.Format("2006_01_02"))

	var exists bool
	if err := DB.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM pg_class WHERE relname=$1 AND relkind='r')",
		partition,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	conn, err := DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT public.create_future_partitions(8)")
	return err
}

/*────────────────────────── AddMessage + retry */
// AddMessage пытается вставить сообщение, создавая недостающую
// недельную партицию и перезапуская попытку в новом соединении.
func AddMessage(
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	msgType string,
	meta map[string]any,
) (*models.Message, error) {

	now := time.Now()

	// Шаг 1 — гарантируем, что нужная партиция уже есть
	if err := ensurePartitionExists(now); err != nil {
		log.Printf("ensurePartitionExists warning: %v", err)
	}

	// Шаг 2 — до трёх попыток вставки
	const maxRetries = 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {

		// ▸▸▸ каждая попытка — В НОВОМ соединении
		ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
		conn, err := DB.Conn(ctx)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("get conn: %w", err)
		}

		msg, err := tryAddMessage(conn, chatID, content, sender, senderID, msgType, meta, now)

		conn.Close() // всегда возвращаем соединение в пул
		cancel()
		// ◂◂◂

		if err == nil {
			return msg, nil // успех
		}
		lastErr = err

		if isPartitionDDLConflict(err) && attempt < maxRetries {
			log.Printf("partition conflict (attempt %d/%d)", attempt, maxRetries)
			time.Sleep(time.Duration(200*attempt) * time.Millisecond)
			continue
		}
		break
	}

	return nil, fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

/*────────────────────────── tryAddMessage */


// tryAddMessage выполняет вставку, работая через переданное соединение.
func tryAddMessage(
	conn *sql.Conn,
	chatID uuid.UUID,
	content, sender string,
	senderID uuid.UUID,
	msgType string,
	meta map[string]any,
	ts time.Time,
) (*models.Message, error) {

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var ok bool
	if err := tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM chats WHERE id=$1)", chatID,
	).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("chat not found")
	}

	msgID := uuid.New()
	var metaJSON []byte
	if meta != nil {
		metaJSON, _ = json.Marshal(meta)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO messages
		       (id,chat_id,content,sender,sender_id,
		        timestamp,read,type,metadata)
		VALUES ($1,$2,$3,$4,$5,$6,false,$7,$8)`,
		msgID, chatID, content, sender, senderID, ts, msgType, metaJSON); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx,
		"UPDATE chats SET updated_at=$1 WHERE id=$2", ts, chatID); err != nil {
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
		Timestamp: ts,
		Read:      false,
		Type:      msgType,
		Metadata:  meta,
	}, nil
}

/*────────────────────────── MarkMessagesAsRead */

func MarkMessagesAsRead(chatID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	_, err := DB.ExecContext(ctx,
		"UPDATE messages SET read=true WHERE chat_id=$1 AND sender='user' AND read=false",
		chatID,
	)
	return err
}

/*────────────────────────── getClientUUIDByAPIKey */

func getClientUUIDByAPIKey(ctx context.Context, tx *sql.Tx, apiKey string) (uuid.UUID, error) {
	if u, err := uuid.Parse(apiKey); err == nil {
		return u, nil
	}
	var clientID uuid.UUID
	err := tx.QueryRowContext(ctx,
		"SELECT id FROM clients WHERE api_key=$1", apiKey,
	).Scan(&clientID)
	if err == sql.ErrNoRows {
		clientID = uuid.New()
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
			clientID, "Клиент "+apiKey, apiKey, time.Now(),
		); err != nil {
			return uuid.Nil, err
		}
		log.Printf("Created client %s for key %s", clientID, apiKey)
	} else if err != nil {
		return uuid.Nil, err
	}
	return clientID, nil
}

/*────────────────────────── getOrCreateUser */

func getOrCreateUser(
	ctx context.Context, tx *sql.Tx,
	userID, userName, userEmail, source, sourceID string,
) (*models.User, error) {
	var user models.User
	var avatarNull sql.NullString

	err := tx.QueryRowContext(ctx,
		"SELECT id,name,email,avatar,source,source_id FROM users WHERE source=$1 AND source_id=$2 LIMIT 1",
		source, sourceID,
	).Scan(&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil {
		user.Avatar = nullStringToPointer(avatarNull)
		return &user, nil
	}

	user.ID = uuid.New()
	if parsed, err := uuid.Parse(userID); err == nil {
		user.ID = parsed
	}
	user.Name, user.Email, user.Source, user.SourceID = userName, userEmail, source, sourceID
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO users(id,name,email,source,source_id,created_at) VALUES($1,$2,$3,$4,$5,$6)",
		user.ID, user.Name, user.Email, user.Source, user.SourceID, time.Now(),
	); err != nil {
		return nil, err
	}
	log.Printf("Created user %s from %s/%s", user.ID, source, sourceID)
	return &user, nil
}

/*────────────────────────── GetOrCreateChat */

func GetOrCreateChat(
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	user, err := getOrCreateUser(ctx, tx, userID, userName, userEmail, source, sourceID)
	if err != nil {
		return nil, err
	}

	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, clientAPIKey)
	if err != nil {
		return nil, err
	}

	var chatID uuid.UUID
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM chats WHERE user_id=$1 AND source=$2 AND bot_id=$3 AND client_id=$4 LIMIT 1",
		user.ID, source, botID, clientUUID,
	).Scan(&chatID)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == sql.ErrNoRows {
		chatID = uuid.New()
		now := time.Now()
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO chats(id,user_id,created_at,updated_at,status,source,bot_id,client_id) VALUES($1,$2,$3,$4,'active',$5,$6,$7)",
			chatID, user.ID, now, now, source, botID, clientUUID,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	chat, _, err := GetChatByID(chatID, 1, DefaultPageSize)
	return chat, err
}

/*────────────────────────── EnsureClientWithAPIKey */

func EnsureClientWithAPIKey(apiKey, clientName string) (uuid.UUID, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := DB.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback()

	var clientID uuid.UUID
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM clients WHERE api_key=$1", apiKey,
	).Scan(&clientID)
	if err == sql.ErrNoRows {
		clientID = uuid.New()
		if clientName == "" {
			clientName = "Клиент " + apiKey
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO clients(id,name,api_key,subscription,active,created_at) VALUES($1,$2,$3,'free',true,$4)",
			clientID, clientName, apiKey, time.Now(),
		); err != nil {
			return uuid.Nil, err
		}
	} else if err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return clientID, nil
}