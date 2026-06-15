// chat_morada.go — DB-операции для chat-связи visitor↔agent проекта morada
// (недвижимость): посетитель сайта общается с владельцем/агентством об объекте.
//
// Структурно morada — тот же двусторонний внешний чат, что и moooving, поэтому
// переиспользуем колонки chats.client_id_ext (посетитель) и chats.driver_id_ext
// (агент/владелец), добавляя только chats.morada_listing_id (объект). Сообщения
// посетителя — sender='user', агента — sender='driver' (как у moooving driver),
// что переиспользует существующие индексы дедупликации.
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
)

const (
	// MoradaVisitorSource — значение users.source для посетителей morada.
	MoradaVisitorSource = "morada_visitor"
	// MoradaAgentSource — значение users.source для владельцев/агентов morada.
	MoradaAgentSource = "morada_agent"

	// MoradaChatSource — значение chats.source для чатов из morada.
	MoradaChatSource = "morada"
	// MoradaBotID — значение chats.bot_id для чатов из morada.
	MoradaBotID = "morada"
)

// MoradaUserUUID возвращает детерминированный UUID для int-ID из morada.
// Используется как users.id и messages.sender_id, чтобы иметь стабильную связь
// между БД-ками без зависимости от порядка создания записей.
func MoradaUserUUID(role string, extUserID int64) uuid.UUID {
	key := fmt.Sprintf("morada:%s:%d", role, extUserID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key))
}

// upsertMoradaUser создаёт или обновляет запись в users для morada-юзера.
func upsertMoradaUser(ctx context.Context, tx *sql.Tx, source string, extUserID int64, name string) (uuid.UUID, error) {
	id := MoradaUserUUID(source, extUserID)
	sourceIDStr := strconv.FormatInt(extUserID, 10)

	displayName := name
	if displayName == "" {
		switch source {
		case MoradaAgentSource:
			displayName = fmt.Sprintf("Agent #%d", extUserID)
		default:
			displayName = fmt.Sprintf("Visitor #%d", extUserID)
		}
	}

	// Synthetic but UNIQUE email per (source, ext id): the users table has a
	// unique index on email, so the moooving pattern of inserting '' would
	// collide on the second synthetic user. These addresses are non-routable.
	email := fmt.Sprintf("%s+%d@morada.local", source, extUserID)

	_, err := tx.ExecContext(ctx, `
        INSERT INTO users (id, name, email, source, source_id, created_at)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (source, source_id) DO UPDATE
            SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE users.name END
    `, id, displayName, email, source, sourceIDStr, time.Now())

	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert morada user: %w", err)
	}
	return id, nil
}

// MoradaChatRequest — параметры для GetOrCreateMoradaChat.
type MoradaChatRequest struct {
	ListingID    int64  // объект недвижимости (listings.id)
	VisitorID    int64  // посетитель (morada user id)
	AgentID      int64  // владелец/агент (morada user id); 0 если ещё не назначен
	VisitorName  string // опционально, для users.name
	AgentName    string // опционально
	ClientAPIKey string // API key morada в ecoChat (chats.client_id)
}

// GetOrCreateMoradaChat находит активный (не архивированный) чат по паре
// (listing_id, visitor) или создаёт новый. Если чат существует и agentID
// отличается / не задан — agentID обновляется (как driver у moooving).
func GetOrCreateMoradaChat(db *sql.DB, req MoradaChatRequest) (*models.Chat, error) {
	if req.ListingID <= 0 {
		return nil, errors.New("listingID required")
	}
	if req.VisitorID <= 0 {
		return nil, errors.New("visitorID required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	visitorUUID, err := upsertMoradaUser(ctx, tx, MoradaVisitorSource, req.VisitorID, req.VisitorName)
	if err != nil {
		return nil, err
	}

	if req.AgentID > 0 {
		if _, err := upsertMoradaUser(ctx, tx, MoradaAgentSource, req.AgentID, req.AgentName); err != nil {
			return nil, err
		}
	}

	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, req.ClientAPIKey)
	if err != nil {
		return nil, fmt.Errorf("resolve client uuid: %w", err)
	}

	var chatID uuid.UUID
	err = tx.QueryRowContext(ctx, `
        SELECT id FROM chats
         WHERE source = $1 AND morada_listing_id = $2 AND client_id_ext = $3
           AND is_archived = false
         LIMIT 1
    `, MoradaChatSource, req.ListingID, req.VisitorID).Scan(&chatID)

	createdNew := false
	now := time.Now()

	if errors.Is(err, sql.ErrNoRows) {
		chatID = uuid.New()
		var agentIDArg interface{}
		if req.AgentID > 0 {
			agentIDArg = req.AgentID
		} else {
			agentIDArg = nil
		}

		_, err = tx.ExecContext(ctx, `
            INSERT INTO chats (
                id, user_id, created_at, updated_at,
                status, source, bot_id, client_id, auto_responder_enabled,
                morada_listing_id, client_id_ext, driver_id_ext
            ) VALUES (
                $1, $2, $3, $4,
                'active', $5, $6, $7, false,
                $8, $9, $10
            )
        `, chatID, visitorUUID, now, now,
			MoradaChatSource, MoradaBotID, clientUUID,
			req.ListingID, req.VisitorID, agentIDArg)
		if err != nil {
			return nil, fmt.Errorf("insert morada chat: %w", err)
		}
		createdNew = true
		log.Printf("GetOrCreateMoradaChat: создан чат %s для listing=%d visitor=%d agent=%d",
			chatID, req.ListingID, req.VisitorID, req.AgentID)
	} else if err != nil {
		return nil, fmt.Errorf("lookup morada chat: %w", err)
	} else if req.AgentID > 0 {
		// Назначаем/обновляем агента, если он не был задан или изменился.
		_, err = tx.ExecContext(ctx, `
            UPDATE chats
               SET driver_id_ext = $1, updated_at = $2
             WHERE id = $3 AND (driver_id_ext IS NULL OR driver_id_ext <> $1)
        `, req.AgentID, now, chatID)
		if err != nil {
			return nil, fmt.Errorf("update agent id: %w", err)
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

// GetMoradaChatByID возвращает чат по UUID, только если это morada-чат.
// Используется при WS-подключении для проверки принадлежности.
func GetMoradaChatByID(db *sql.DB, chatID uuid.UUID) (*models.Chat, error) {
	chat, _, err := GetChatByID(db, chatID, 1, "")
	if err != nil {
		return nil, err
	}
	if chat == nil || chat.Source != MoradaChatSource {
		return nil, nil
	}
	return chat, nil
}

// CloseMoradaChat архивирует чат (например, объект снят с публикации / сделка закрыта).
func CloseMoradaChat(db *sql.DB, chatID uuid.UUID) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	res, err := db.ExecContext(ctx, `
        UPDATE chats
           SET is_archived = true, status = 'closed', updated_at = $1, resolved_at = $1
         WHERE id = $2 AND source = 'morada' AND is_archived = false
    `, time.Now(), chatID)
	if err != nil {
		return fmt.Errorf("close morada chat: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("чат не найден или уже закрыт")
	}
	return nil
}

// MoradaChatSummary — лёгкая сводка чата для списков (инбокс агента / чаты посетителя).
type MoradaChatSummary struct {
	ChatID      uuid.UUID       `json:"chatId"`
	ListingID   *int64          `json:"listingId,omitempty"`
	VisitorID   *int64          `json:"visitorId,omitempty"`
	AgentID     *int64          `json:"agentId,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`
	UnreadCount int             `json:"unreadCount"`
	LastMessage *moradaLastMsg  `json:"lastMessage,omitempty"`
}

type moradaLastMsg struct {
	Content   string    `json:"content"`
	Sender    string    `json:"sender"`
	Timestamp time.Time `json:"timestamp"`
}

// GetMoradaChatsForAgent возвращает активные morada-чаты владельца/агента.
func GetMoradaChatsForAgent(db *sql.DB, agentExtID int64, limit int) ([]MoradaChatSummary, error) {
	return listMoradaChatsBy(db, "driver_id_ext", agentExtID, limit, "agent")
}

// GetMoradaChatsForVisitor возвращает активные morada-чаты посетителя.
func GetMoradaChatsForVisitor(db *sql.DB, visitorExtID int64, limit int) ([]MoradaChatSummary, error) {
	return listMoradaChatsBy(db, "client_id_ext", visitorExtID, limit, "visitor")
}

// listMoradaChatsBy — общий код для списков morada-чатов (фильтр по source='morada').
// unreadFor: для какого получателя считать непрочитанные (sender противоположен).
func listMoradaChatsBy(db *sql.DB, column string, extID int64, limit int, unreadFor string) ([]MoradaChatSummary, error) {
	if column != "driver_id_ext" && column != "client_id_ext" {
		return nil, fmt.Errorf("invalid column %q", column)
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}

	// "Непрочитанные для меня" = сообщения, отправленные не мной.
	// Агент пишет sender='driver', посетитель — sender='user'.
	var unreadCondition string
	switch unreadFor {
	case "agent":
		unreadCondition = "m.sender <> 'driver'"
	case "visitor":
		unreadCondition = "m.sender <> 'user'"
	default:
		unreadCondition = "false"
	}

	q := fmt.Sprintf(`
        SELECT
          c.id, c.morada_listing_id, c.client_id_ext, c.driver_id_ext, c.updated_at,
          COUNT(CASE WHEN %s AND m.read = false THEN 1 END) AS unread,
          l.content, l.sender, l.timestamp
        FROM chats c
        LEFT JOIN messages m ON m.chat_id = c.id
        LEFT JOIN LATERAL (
            SELECT content, sender, timestamp FROM messages
             WHERE chat_id = c.id ORDER BY timestamp DESC LIMIT 1
        ) l ON TRUE
        WHERE c.source = 'morada' AND c.%s = $1 AND c.is_archived = false
        GROUP BY c.id, c.morada_listing_id, c.client_id_ext, c.driver_id_ext, c.updated_at,
                 l.content, l.sender, l.timestamp
        ORDER BY c.updated_at DESC
        LIMIT $2`, unreadCondition, column)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, q, extID, limit)
	if err != nil {
		return nil, fmt.Errorf("list morada chats: %w", err)
	}
	defer rows.Close()

	var out []MoradaChatSummary
	for rows.Next() {
		var (
			s             MoradaChatSummary
			listingNull   sql.NullInt64
			visitorNull   sql.NullInt64
			agentNull     sql.NullInt64
			lastContent   sql.NullString
			lastSender    sql.NullString
			lastTimestamp sql.NullTime
		)
		if err := rows.Scan(
			&s.ChatID, &listingNull, &visitorNull, &agentNull, &s.UpdatedAt,
			&s.UnreadCount,
			&lastContent, &lastSender, &lastTimestamp,
		); err != nil {
			return nil, fmt.Errorf("scan morada chat: %w", err)
		}
		if listingNull.Valid {
			v := listingNull.Int64
			s.ListingID = &v
		}
		if visitorNull.Valid {
			v := visitorNull.Int64
			s.VisitorID = &v
		}
		if agentNull.Valid {
			v := agentNull.Int64
			s.AgentID = &v
		}
		if lastContent.Valid {
			s.LastMessage = &moradaLastMsg{
				Content:   lastContent.String,
				Sender:    lastSender.String,
				Timestamp: lastTimestamp.Time,
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
