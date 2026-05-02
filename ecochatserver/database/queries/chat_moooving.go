// chat_moooving.go — DB-операции для chat-связи client↔driver проекта moooving.
// moooving хранит свои сущности с int-ID; ecoChat хранит их в chats.order_id /
// .client_id_ext / .driver_id_ext без преобразования.
package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const (
	// MooovingClientSource — значение users.source для клиентов moooving.
	MooovingClientSource = "moooving_client"
	// MooovingDriverSource — значение users.source для водителей moooving.
	MooovingDriverSource = "moooving_driver"

	// MooovingChatSource — значение chats.source для чатов из moooving.
	MooovingChatSource = "moooving"
	// MooovingBotID — значение chats.bot_id для чатов из moooving.
	MooovingBotID = "moooving"
)

// MooovingUserUUID возвращает детерминированный UUID для int-ID из moooving.
// Используется как users.id и messages.sender_id, чтобы не зависеть от
// последовательного создания записи и иметь стабильную связь между БД-ками.
func MooovingUserUUID(role string, extUserID int64) uuid.UUID {
	key := fmt.Sprintf("moooving:%s:%d", role, extUserID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
}

// upsertMooovingUser создаёт или обновляет запись в users для moooving-юзера.
// Имя обновляется, только если передано непустое значение.
func upsertMooovingUser(ctx context.Context, tx *sql.Tx, source string, extUserID int64, name string) (uuid.UUID, error) {
	id := MooovingUserUUID(source, extUserID)
	sourceIDStr := strconv.FormatInt(extUserID, 10)

	displayName := name
	if displayName == "" {
		switch source {
		case MooovingDriverSource:
			displayName = fmt.Sprintf("Driver #%d", extUserID)
		default:
			displayName = fmt.Sprintf("Client #%d", extUserID)
		}
	}

	_, err := tx.ExecContext(ctx, `
        INSERT INTO users (id, name, email, source, source_id, created_at)
        VALUES ($1, $2, '', $3, $4, $5)
        ON CONFLICT (source, source_id) DO UPDATE
            SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE users.name END
    `, id, displayName, source, sourceIDStr, time.Now())

	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert moooving user: %w", err)
	}
	return id, nil
}

// MooovingChatRequest — параметры для GetOrCreateMooovingChat.
type MooovingChatRequest struct {
	OrderID     int64
	ClientID    int64
	DriverID    int64  // 0 если водитель ещё не назначен
	ClientName  string // опционально, для users.name
	DriverName  string // опционально
	ClientAPIKey string // для chats.client_id (UUID клиента-компании в ecoChat)
}

// GetOrCreateMooovingChat находит активный (не архивированный) чат по order_id
// или создаёт новый, привязывая к нему clientID и driverID из moooving.
// Если чат уже существует и driverID отличается / не задан — driverID обновляется.
func GetOrCreateMooovingChat(db *sql.DB, req MooovingChatRequest) (*models.Chat, error) {
	if req.OrderID <= 0 {
		return nil, errors.New("orderID required")
	}
	if req.ClientID <= 0 {
		return nil, errors.New("clientID required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	clientUserUUID, err := upsertMooovingUser(ctx, tx, MooovingClientSource, req.ClientID, req.ClientName)
	if err != nil {
		return nil, err
	}

	if req.DriverID > 0 {
		if _, err := upsertMooovingUser(ctx, tx, MooovingDriverSource, req.DriverID, req.DriverName); err != nil {
			return nil, err
		}
	}

	// chats.client_id (UUID компании-клиента ecoChat) — берём по API-ключу
	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, req.ClientAPIKey)
	if err != nil {
		return nil, fmt.Errorf("resolve client uuid: %w", err)
	}

	var chatID uuid.UUID
	err = tx.QueryRowContext(ctx, `
        SELECT id FROM chats
         WHERE order_id = $1 AND is_archived = false
         LIMIT 1
    `, req.OrderID).Scan(&chatID)

	createdNew := false
	now := time.Now()

	if errors.Is(err, sql.ErrNoRows) {
		// Создаём новый чат
		chatID = uuid.New()
		var driverIDArg interface{}
		if req.DriverID > 0 {
			driverIDArg = req.DriverID
		} else {
			driverIDArg = nil
		}

		_, err = tx.ExecContext(ctx, `
            INSERT INTO chats (
                id, user_id, created_at, updated_at,
                status, source, bot_id, client_id, auto_responder_enabled,
                order_id, client_id_ext, driver_id_ext
            ) VALUES (
                $1, $2, $3, $4,
                'active', $5, $6, $7, false,
                $8, $9, $10
            )
        `, chatID, clientUserUUID, now, now,
			MooovingChatSource, MooovingBotID, clientUUID,
			req.OrderID, req.ClientID, driverIDArg)
		if err != nil {
			return nil, fmt.Errorf("insert moooving chat: %w", err)
		}
		createdNew = true
		log.Printf("GetOrCreateMooovingChat: создан чат %s для order=%d client=%d driver=%d",
			chatID, req.OrderID, req.ClientID, req.DriverID)
	} else if err != nil {
		return nil, fmt.Errorf("lookup chat by order_id: %w", err)
	} else if req.DriverID > 0 {
		// Обновляем driver_id_ext, если он не был задан или изменился
		_, err = tx.ExecContext(ctx, `
            UPDATE chats
               SET driver_id_ext = $1, updated_at = $2
             WHERE id = $3 AND (driver_id_ext IS NULL OR driver_id_ext <> $1)
        `, req.DriverID, now, chatID)
		if err != nil {
			return nil, fmt.Errorf("update driver_id_ext: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	chat, _, err := GetChatByID(db, chatID, 25, "")
	if err != nil {
		return nil, err
	}
	chat.IsNewChat = createdNew
	return chat, nil
}

// GetMooovingChatByOrderID возвращает активный чат по order_id или nil.
func GetMooovingChatByOrderID(db *sql.DB, orderID int64) (*models.Chat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var chatID uuid.UUID
	err := db.QueryRowContext(ctx, `
        SELECT id FROM chats
         WHERE order_id = $1 AND is_archived = false
         LIMIT 1
    `, orderID).Scan(&chatID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup chat by order_id: %w", err)
	}

	chat, _, err := GetChatByID(db, chatID, 25, "")
	return chat, err
}

// CloseMooovingChat архивирует чат заказа (вызывается, когда заказ выполнен).
func CloseMooovingChat(db *sql.DB, chatID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	res, err := db.ExecContext(ctx, `
        UPDATE chats
           SET is_archived = true, status = 'closed', updated_at = $1, resolved_at = $1
         WHERE id = $2 AND is_archived = false
    `, time.Now(), chatID)
	if err != nil {
		return fmt.Errorf("close moooving chat: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("чат не найден или уже закрыт")
	}
	return nil
}

// MooovingChatSummary — лёгкая сводка чата для списков в driver/client панелях.
type MooovingChatSummary struct {
	ChatID      uuid.UUID  `json:"chatId"`
	OrderID     int64      `json:"orderId"`
	ClientIDExt int64      `json:"clientIdExt"`
	DriverIDExt *int64     `json:"driverIdExt,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	UnreadCount int        `json:"unreadCount"`
	LastMessage *lastMsg   `json:"lastMessage,omitempty"`
}

type lastMsg struct {
	Content   string    `json:"content"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
}

// GetMooovingChatsForDriver возвращает активные чаты водителя.
func GetMooovingChatsForDriver(db *sql.DB, driverExtID int64, limit int) ([]MooovingChatSummary, error) {
	return listMooovingChatsBy(db, "driver_id_ext", driverExtID, limit, "driver")
}

// MooovingUnreadCount — пара (orderID, count) для batch-выдачи unread.
type MooovingUnreadCount struct {
	OrderID int64 `json:"orderId"`
	Count   int   `json:"count"`
}

// GetMooovingUnreadByOrders возвращает количество непрочитанных сообщений в каждом
// чате moooving для конкретного viewer (driver|client) по списку orderIDs.
// "Непрочитанные для меня" = read=false AND sender противоположной стороны.
func GetMooovingUnreadByOrders(db *sql.DB, orderIDs []int64, viewerSender string) ([]MooovingUnreadCount, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	var oppositeSender string
	switch viewerSender {
	case "driver":
		oppositeSender = "user"
	case "user":
		oppositeSender = "driver"
	default:
		return nil, fmt.Errorf("invalid viewerSender %q", viewerSender)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	q := `
        SELECT c.order_id, COUNT(m.id)
          FROM chats c
          LEFT JOIN messages m
            ON m.chat_id = c.id
           AND m.read = false
           AND m.sender = $2
         WHERE c.order_id = ANY($1)
           AND c.is_archived = false
         GROUP BY c.order_id`

	rows, err := db.QueryContext(ctx, q, pq.Int64Array(orderIDs), oppositeSender)
	if err != nil {
		return nil, fmt.Errorf("unread by orders: %w", err)
	}
	defer rows.Close()

	out := make([]MooovingUnreadCount, 0, len(orderIDs))
	for rows.Next() {
		var u MooovingUnreadCount
		if err := rows.Scan(&u.OrderID, &u.Count); err != nil {
			return nil, fmt.Errorf("scan unread: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetMooovingChatsForClient возвращает активные чаты клиента.
func GetMooovingChatsForClient(db *sql.DB, clientExtID int64, limit int) ([]MooovingChatSummary, error) {
	return listMooovingChatsBy(db, "client_id_ext", clientExtID, limit, "user")
}

// listMooovingChatsBy — общий код для GetMooovingChatsForDriver/Client.
// unreadFor: для какого получателя считать непрочитанные (sender противоположен).
func listMooovingChatsBy(db *sql.DB, column string, extID int64, limit int, unreadFor string) ([]MooovingChatSummary, error) {
	if column != "driver_id_ext" && column != "client_id_ext" {
		return nil, fmt.Errorf("invalid column %q", column)
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}

	// "Непрочитанные для меня" = сообщения, отправленные не мной.
	// Для driver: считаем сообщения с sender != 'driver'; для client: sender != 'user'.
	var unreadCondition string
	switch unreadFor {
	case "driver":
		unreadCondition = "m.sender <> 'driver'"
	case "user":
		unreadCondition = "m.sender <> 'user'"
	default:
		unreadCondition = "false"
	}

	q := fmt.Sprintf(`
        SELECT
          c.id, c.order_id, c.client_id_ext, c.driver_id_ext, c.updated_at,
          COUNT(CASE WHEN %s AND m.read = false THEN 1 END) AS unread,
          l.content, l.sender, l.timestamp
        FROM chats c
        LEFT JOIN messages m ON m.chat_id = c.id
        LEFT JOIN LATERAL (
            SELECT content, sender, timestamp FROM messages
             WHERE chat_id = c.id ORDER BY timestamp DESC LIMIT 1
        ) l ON TRUE
        WHERE c.%s = $1 AND c.is_archived = false
        GROUP BY c.id, c.order_id, c.client_id_ext, c.driver_id_ext, c.updated_at,
                 l.content, l.sender, l.timestamp
        ORDER BY c.updated_at DESC
        LIMIT $2`, unreadCondition, column)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, q, extID, limit)
	if err != nil {
		return nil, fmt.Errorf("list moooving chats: %w", err)
	}
	defer rows.Close()

	var out []MooovingChatSummary
	for rows.Next() {
		var (
			s             MooovingChatSummary
			driverNull    sql.NullInt64
			lastContent   sql.NullString
			lastSender    sql.NullString
			lastTimestamp sql.NullTime
		)
		if err := rows.Scan(
			&s.ChatID, &s.OrderID, &s.ClientIDExt, &driverNull, &s.UpdatedAt,
			&s.UnreadCount,
			&lastContent, &lastSender, &lastTimestamp,
		); err != nil {
			return nil, fmt.Errorf("scan moooving chat: %w", err)
		}
		if driverNull.Valid {
			v := driverNull.Int64
			s.DriverIDExt = &v
		}
		if lastContent.Valid {
			s.LastMessage = &lastMsg{
				Content:   lastContent.String,
				Sender:    lastSender.String,
				Timestamp: lastTimestamp.Time,
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
