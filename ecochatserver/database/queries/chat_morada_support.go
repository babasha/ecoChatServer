// chat_morada_support.go — чат «посетитель сайта ↔ поддержка сайта» для
// morada/tudonuma. Это тот же morada-чат (source='morada', посетитель в
// client_id_ext, sender='user'), только без объекта недвижимости и без
// конкретного агента: отвечает команда поддержки (админы сайта), любой из них.
//
//   chats.morada_support = true   — признак чата поддержки
//   chats.morada_listing_id NULL  — объекта нет
//   chats.driver_id_ext NULL      — агента нет; ответы поддержки идут sender='driver',
//                                   а sender_id = MoradaUserUUID("morada_support", adminId)
//   chats.status                  — 'active' (открыт) | 'resolved' (решён);
//                                   новое сообщение посетителя снова открывает чат.
//
// Один активный чат поддержки на посетителя: вся переписка с поддержкой —
// одна лента, как в любом современном мессенджере поддержки.
package queries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// MoradaSupportSource — users.source / роль для сотрудников поддержки morada.
const MoradaSupportSource = "morada_support"

// EnsureMoradaSupportSchema идемпотентно добавляет колонку и индексы чата
// поддержки (migrations/add_morada_support_chat.sql — то же самое). Вызывается
// при старте, чтобы развёртывание не зависело от ручного прогона миграции.
func EnsureMoradaSupportSchema(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx, `
        ALTER TABLE chats ADD COLUMN IF NOT EXISTS morada_listing_id BIGINT;
        ALTER TABLE chats ADD COLUMN IF NOT EXISTS morada_support BOOLEAN NOT NULL DEFAULT false;
        CREATE UNIQUE INDEX IF NOT EXISTS uq_chats_active_morada_support_visitor
            ON chats (client_id_ext)
            WHERE source = 'morada' AND morada_support AND is_archived = false;
        CREATE INDEX IF NOT EXISTS idx_chats_morada_support_updated
            ON chats (updated_at DESC)
            WHERE source = 'morada' AND morada_support AND is_archived = false;
    `)
	if err != nil {
		return fmt.Errorf("ensure morada support schema: %w", err)
	}
	return nil
}

// GetOrCreateMoradaSupportChat возвращает активный чат поддержки посетителя
// или создаёт его. Возвращает (chatID, создан ли новый).
func GetOrCreateMoradaSupportChat(db *sql.DB, visitorID int64, visitorName, clientAPIKey string) (uuid.UUID, bool, error) {
	if visitorID <= 0 {
		return uuid.Nil, false, errors.New("visitorID required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	visitorUUID, err := upsertMoradaUser(ctx, tx, MoradaVisitorSource, visitorID, visitorName)
	if err != nil {
		return uuid.Nil, false, err
	}
	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, clientAPIKey)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("resolve client uuid: %w", err)
	}

	now := time.Now()
	newID := uuid.New()
	// ON CONFLICT по частичному уникальному индексу: два одновременных «открыть
	// поддержку» от одного посетителя не создадут два чата.
	res, err := tx.ExecContext(ctx, `
        INSERT INTO chats (
            id, user_id, created_at, updated_at,
            status, source, bot_id, client_id, auto_responder_enabled,
            client_id_ext, morada_support
        ) VALUES ($1, $2, $3, $3, 'active', $4, $5, $6, false, $7, true)
        ON CONFLICT (client_id_ext) WHERE source = 'morada' AND morada_support AND is_archived = false
        DO NOTHING
    `, newID, visitorUUID, now, MoradaChatSource, MoradaBotID, clientUUID, visitorID)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert support chat: %w", err)
	}
	created, _ := res.RowsAffected()

	chatID := newID
	if created == 0 {
		if err := tx.QueryRowContext(ctx, `
            SELECT id FROM chats
             WHERE source = 'morada' AND morada_support AND client_id_ext = $1 AND is_archived = false
             LIMIT 1
        `, visitorID).Scan(&chatID); err != nil {
			return uuid.Nil, false, fmt.Errorf("lookup support chat: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return uuid.Nil, false, fmt.Errorf("commit: %w", err)
	}
	if created > 0 {
		log.Printf("GetOrCreateMoradaSupportChat: создан чат поддержки %s для visitor=%d", chatID, visitorID)
	}
	return chatID, created > 0, nil
}

// IsMoradaSupportChat — true, если чат существует и это чат поддержки morada.
func IsMoradaSupportChat(db *sql.DB, chatID uuid.UUID) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()
	var support bool
	err := db.QueryRowContext(ctx,
		`SELECT morada_support FROM chats WHERE id = $1 AND source = 'morada'`, chatID,
	).Scan(&support)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return support, err
}

// MoradaSupportSummary — строка инбокса поддержки.
type MoradaSupportSummary struct {
	ChatID       uuid.UUID      `json:"chatId"`
	VisitorID    int64          `json:"visitorId"`
	VisitorName  string         `json:"visitorName"`
	Status       string         `json:"status"` // "open" | "resolved"
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	ResolvedAt   *time.Time     `json:"resolvedAt,omitempty"`
	UnreadCount  int            `json:"unreadCount"`
	MessageCount int            `json:"messageCount"`
	LastMessage  *moradaLastMsg `json:"lastMessage,omitempty"`
}

// MoradaSupportCounts — счётчики для вкладок инбокса и бейджа в админке.
type MoradaSupportCounts struct {
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
	// Unread — число чатов, где есть непрочитанное сообщение посетителя.
	Unread int `json:"unread"`
}

// ListMoradaSupportChats — инбокс поддержки. filter: "open" | "resolved" | "all".
// Сортировка: сначала чаты с непрочитанным, затем по последней активности.
func ListMoradaSupportChats(db *sql.DB, filter string, limit int) ([]MoradaSupportSummary, error) {
	if limit < 1 || limit > 200 {
		limit = 100
	}
	var statusCond string
	switch filter {
	case "resolved":
		statusCond = "AND c.status = 'resolved'"
	case "all":
		statusCond = ""
	default:
		statusCond = "AND c.status <> 'resolved'"
	}

	q := fmt.Sprintf(`
        SELECT c.id, c.client_id_ext, u.name, c.status, c.created_at, c.updated_at, c.resolved_at,
               COALESCE(st.unread, 0), COALESCE(st.total, 0),
               l.content, l.sender, l.timestamp
          FROM chats c
          JOIN users u ON u.id = c.user_id
          LEFT JOIN LATERAL (
              SELECT COUNT(*) FILTER (WHERE m.sender = 'user' AND m.read = false) AS unread,
                     COUNT(*) AS total
                FROM messages m WHERE m.chat_id = c.id
          ) st ON TRUE
          LEFT JOIN LATERAL (
              SELECT content, sender, timestamp FROM messages
               WHERE chat_id = c.id ORDER BY timestamp DESC LIMIT 1
          ) l ON TRUE
         WHERE c.source = 'morada' AND c.morada_support AND c.is_archived = false %s
         ORDER BY (COALESCE(st.unread, 0) > 0) DESC, c.updated_at DESC
         LIMIT $1`, statusCond)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	rows, err := db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list support chats: %w", err)
	}
	defer rows.Close()

	out := []MoradaSupportSummary{}
	for rows.Next() {
		var (
			s             MoradaSupportSummary
			visitorNull   sql.NullInt64
			status        string
			resolvedAt    sql.NullTime
			lastContent   sql.NullString
			lastSender    sql.NullString
			lastTimestamp sql.NullTime
		)
		if err := rows.Scan(
			&s.ChatID, &visitorNull, &s.VisitorName, &status, &s.CreatedAt, &s.UpdatedAt, &resolvedAt,
			&s.UnreadCount, &s.MessageCount,
			&lastContent, &lastSender, &lastTimestamp,
		); err != nil {
			return nil, fmt.Errorf("scan support chat: %w", err)
		}
		s.VisitorID = visitorNull.Int64
		s.Status = "open"
		if status == "resolved" {
			s.Status = "resolved"
		}
		if resolvedAt.Valid {
			t := resolvedAt.Time
			s.ResolvedAt = &t
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

// CountMoradaSupportChats — счётчики открытых / решённых / непрочитанных чатов.
func CountMoradaSupportChats(db *sql.DB) (MoradaSupportCounts, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()
	var c MoradaSupportCounts
	err := db.QueryRowContext(ctx, `
        SELECT COUNT(*) FILTER (WHERE c.status <> 'resolved'),
               COUNT(*) FILTER (WHERE c.status = 'resolved'),
               COUNT(*) FILTER (WHERE EXISTS (
                   SELECT 1 FROM messages m
                    WHERE m.chat_id = c.id AND m.sender = 'user' AND m.read = false))
          FROM chats c
         WHERE c.source = 'morada' AND c.morada_support AND c.is_archived = false
    `).Scan(&c.Open, &c.Resolved, &c.Unread)
	return c, err
}

// SetMoradaSupportStatus отмечает чат поддержки решённым / снова открытым.
// updated_at не трогаем: порядок в инбоксе — по активности переписки.
func SetMoradaSupportStatus(db *sql.DB, chatID uuid.UUID, resolved bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()
	status := "active"
	if resolved {
		status = "resolved"
	}
	res, err := db.ExecContext(ctx, `
        UPDATE chats
           SET status = $2,
               resolved_at = CASE WHEN $3 THEN now() ELSE NULL END
         WHERE id = $1 AND source = 'morada' AND morada_support AND is_archived = false
    `, chatID, status, resolved)
	if err != nil {
		return fmt.Errorf("set support status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("чат поддержки не найден")
	}
	return nil
}

// ReopenMoradaSupportChat снова открывает решённый чат поддержки (посетитель
// написал ещё). Возвращает true, если статус действительно поменялся.
func ReopenMoradaSupportChat(db *sql.DB, chatID uuid.UUID) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()
	res, err := db.ExecContext(ctx, `
        UPDATE chats SET status = 'active', resolved_at = NULL
         WHERE id = $1 AND source = 'morada' AND morada_support AND status = 'resolved'
    `, chatID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
