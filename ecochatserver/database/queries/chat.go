package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

func GetChats(db *sql.DB, clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
	log.Printf("GetChats: начало, clientID=%s, adminID=%s, page=%d, size=%d",
		clientID, adminID, page, size)

	if page < 1 {
		page = 1
		log.Printf("GetChats: page скорректирован на 1")
	}
	if size < 1 || size > MaxPageSize {
		oldSize := size
		size = DefaultPageSize
		log.Printf("GetChats: size скорректирован с %d на %d", oldSize, size)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	// Подсчитываем общее количество чатов
	var total int
	var countQuery string
	var countArgs []interface{}

	// Если clientID = uuid.Nil, то показываем ВСЕ чаты (для админки), но только неархивные
	if clientID == uuid.Nil {
		countQuery = `SELECT COUNT(*) FROM chats WHERE is_archived = false`
		countArgs = []interface{}{}
	} else {
		countQuery = `SELECT COUNT(*) FROM chats WHERE client_id=$1 AND (assigned_to=$2 OR assigned_to IS NULL) AND is_archived = false`
		countArgs = []interface{}{clientID, adminID}
	}

	log.Printf("GetChats: выполняем запрос подсчета: %s", countQuery)
	log.Printf("GetChats: параметры подсчета: clientID=%s, adminID=%s", clientID, adminID)

	if err := db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		log.Printf("GetChats: ошибка подсчета: %v", err)
		return nil, 0, fmt.Errorf("ошибка подсчета чатов: %w", err)
	}
	log.Printf("GetChats: найдено всего чатов с фильтром: %d", total)

	// Для отладки - проверим ВСЕ чаты клиента без фильтра по assigned_to
	var totalWithoutFilter int
	debugQuery := "SELECT COUNT(*) FROM chats WHERE client_id=$1"
	if err := db.QueryRowContext(ctx, debugQuery, clientID).Scan(&totalWithoutFilter); err == nil {
		log.Printf("GetChats: всего чатов клиента без фильтра по assigned_to: %d", totalWithoutFilter)

		// Проверим, есть ли чаты с assigned_to не равным текущему админу
		var assignedToOthers int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM chats WHERE client_id=$1 AND assigned_to IS NOT NULL AND assigned_to != $2",
			clientID, adminID,
		).Scan(&assignedToOthers); err == nil {
			log.Printf("GetChats: чатов назначенных другим админам: %d", assignedToOthers)
		}
	}

	// Для отладки - выведем все чаты клиента
	debugRows, err := db.QueryContext(ctx,
		"SELECT id, user_id, client_id, assigned_to, status, created_at FROM chats WHERE client_id=$1 ORDER BY created_at DESC LIMIT 10",
		clientID)
	if err == nil {
		defer debugRows.Close()
		log.Printf("GetChats: последние 10 чатов клиента для отладки:")
		i := 0
		for debugRows.Next() {
			var chatID, userID, clientID uuid.UUID
			var assignedTo sql.NullString
			var status string
			var createdAt time.Time
			if err := debugRows.Scan(&chatID, &userID, &clientID, &assignedTo, &status, &createdAt); err == nil {
				assignedToStr := "NULL"
				if assignedTo.Valid {
					assignedToStr = assignedTo.String
				}
				log.Printf("  чат %d: ID=%s, userID=%s, clientID=%s, assignedTo=%s, status=%s, created=%v",
					i, chatID, userID, clientID, assignedToStr, status, createdAt)
				i++
			}
		}
	}

	// Основной запрос для получения чатов
	const q = `
      SELECT
        c.id,c.created_at,c.updated_at,c.status,c.client_id,c.auto_responder_enabled,
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
      WHERE %s
      GROUP BY c.id,c.client_id,u.id,l.id,l.content,l.sender,l.timestamp
      ORDER BY c.updated_at DESC
      LIMIT $%d OFFSET $%d
    `

	var mainQuery string
	var mainArgs []interface{}
	offset := (page - 1) * size

	// Если clientID = uuid.Nil, то показываем ВСЕ чаты (для админки), но только неархивные
	if clientID == uuid.Nil {
		mainQuery = fmt.Sprintf(q, "c.is_archived = false", 1, 2)
		mainArgs = []interface{}{size, offset}
	} else {
		mainQuery = fmt.Sprintf(q, "c.client_id=$1 AND (c.assigned_to=$2 OR c.assigned_to IS NULL) AND c.is_archived = false", 3, 4)
		mainArgs = []interface{}{clientID, adminID, size, offset}
	}

	log.Printf("GetChats: выполняем основной запрос с LIMIT=%d OFFSET=%d", size, offset)

	rows, err := db.QueryContext(ctx, mainQuery, mainArgs...)
	if err != nil {
		log.Printf("GetChats: ошибка основного запроса: %v", err)
		return nil, 0, fmt.Errorf("ошибка получения чатов: %w", err)
	}
	defer rows.Close()

	var list []models.ChatResponse
	rowNum := 0
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
			&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status, &chat.ClientID, &chat.AutoResponderEnabled,
			&user.ID, &user.Name, &user.Email, &avatarNull,
			&unread, &lastID, &lastCont, &lastSender, &lastTime,
		); err != nil {
			log.Printf("GetChats: ошибка сканирования строки %d: %v", rowNum, err)
			return nil, 0, fmt.Errorf("ошибка сканирования чата: %w", err)
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
				ChatID:    chat.ID, // Добавляем ChatID для правильной связи
			}
			log.Printf("GetChats: чат %d имеет последнее сообщение ID=%s, ChatID=%s",
				rowNum, lastID.String, chat.ID)
		} else {
			log.Printf("GetChats: чат %d не имеет сообщений", rowNum)
		}

		log.Printf("GetChats: чат %d: ID=%s, userID=%s, userName='%s', email='%s', status=%s, unread=%d, created=%v, updated=%v",
			rowNum, chat.ID, user.ID, user.Name, user.Email, chat.Status, unread, chat.CreatedAt, chat.UpdatedAt)

		list = append(list, chat)
		rowNum++
	}

	if err := rows.Err(); err != nil {
		log.Printf("GetChats: ошибка после обработки строк: %v", err)
		return nil, 0, fmt.Errorf("ошибка обработки результатов: %w", err)
	}

	log.Printf("GetChats: успешно, возвращаем %d чатов из %d", len(list), total)
	return list, total, nil
}

// GetChatByID получает чат с сообщениями (поддерживает infinite scroll через параметр beforeTimestamp)
func GetChatByID(db *sql.DB, chatID uuid.UUID, limit int, beforeTimestamp string) (*models.Chat, int, error) {
	log.Printf("GetChatByID: начало, chatID=%s, limit=%d, before=%s", chatID, limit, beforeTimestamp)

	if limit < 1 || limit > MaxPageSize {
		oldLimit := limit
		limit = 25
		log.Printf("GetChatByID: limit скорректирован с %d на %d", oldLimit, limit)
	}

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	var (
		chat         models.Chat
		userID       uuid.UUID
		assignedNull sql.NullString
	)

	chatQuery := `
        SELECT id,created_at,updated_at,status,user_id,
               source,bot_id,client_id,assigned_to,auto_responder_enabled,is_archived
          FROM chats WHERE id=$1`

	if err := db.QueryRowContext(ctx, chatQuery, chatID).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
		&userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedNull, &chat.AutoResponderEnabled, &chat.IsArchived,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, 0, fmt.Errorf("чат не найден")
		}
		return nil, 0, fmt.Errorf("ошибка получения чата: %w", err)
	}

	var err error
	chat.AssignedTo, err = nullUUIDToPointer(assignedNull)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка преобразования assigned_to: %w", err)
	}

	log.Printf("GetChatByID: найден чат ID=%s, userID=%s", chat.ID, userID)

	// Получаем данные пользователя
	var (
		user       models.User
		avatarNull sql.NullString
	)
	userQuery := `SELECT id,name,email,avatar,source,source_id FROM users WHERE id=$1`

	if err := db.QueryRowContext(ctx, userQuery, userID).Scan(
		&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
	); err != nil {
		return nil, 0, fmt.Errorf("ошибка получения пользователя: %w", err)
	}

	user.Avatar = nullStringToPointer(avatarNull)
	chat.User = user

	// Подсчитываем общее количество сообщений
	var total int
	countQuery := "SELECT COUNT(*) FROM messages WHERE chat_id=$1"
	if err := db.QueryRowContext(ctx, countQuery, chatID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("ошибка подсчета сообщений: %w", err)
	}

	log.Printf("GetChatByID: всего сообщений в чате: %d", total)

	// Получаем сообщения до указанной временной метки
	var messagesQuery string
	var rows *sql.Rows

	if beforeTimestamp != "" {
		// Загружаем сообщения старше указанной временной метки
		messagesQuery = `
            SELECT id,content,sender,sender_id,timestamp,read,read_at,type,metadata
              FROM (
                SELECT id,content,sender,sender_id,timestamp,read,read_at,type,metadata
                  FROM messages
                 WHERE chat_id=$1 AND timestamp < $2
                 ORDER BY timestamp DESC
                 LIMIT $3
              ) AS older_messages
             ORDER BY timestamp ASC`
		log.Printf("GetChatByID: получаем сообщения до %s с LIMIT=%d", beforeTimestamp, limit)
		rows, err = db.QueryContext(ctx, messagesQuery, chatID, beforeTimestamp, limit)
	} else {
		// Загружаем последние сообщения (как обычно)
		messagesQuery = `
            SELECT id,content,sender,sender_id,timestamp,read,read_at,type,metadata
              FROM (
                SELECT id,content,sender,sender_id,timestamp,read,read_at,type,metadata
                  FROM messages
                 WHERE chat_id=$1
                 ORDER BY timestamp DESC
                 LIMIT $2
              ) AS recent_messages
             ORDER BY timestamp ASC`
		log.Printf("GetChatByID: получаем последние %d сообщений", limit)
		rows, err = db.QueryContext(ctx, messagesQuery, chatID, limit)
	}

	if err != nil {
		log.Printf("GetChatByID: ошибка получения сообщений: %v", err)
		return nil, 0, fmt.Errorf("ошибка получения сообщений: %w", err)
	}
	defer rows.Close()

	msgNum := 0
	for rows.Next() {
		var m models.Message
		var raw []byte
		if err := rows.Scan(
			&m.ID, &m.Content, &m.Sender, &m.SenderID,
			&m.Timestamp, &m.Read, &m.ReadAt, &m.Type, &raw,
		); err != nil {
			log.Printf("GetChatByID: ошибка сканирования сообщения %d: %v", msgNum, err)
			return nil, 0, fmt.Errorf("ошибка сканирования сообщения: %w", err)
		}

		m.ChatID = chatID
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &m.Metadata)
		}

		chat.Messages = append(chat.Messages, m)
		msgNum++
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ошибка обработки сообщений: %w", err)
	}

	// Получаем последнее сообщение в чате
	var last models.Message
	var raw []byte
	lastMsgQuery := `
        SELECT id,content,sender,sender_id,timestamp,read,type,metadata
          FROM messages
         WHERE chat_id=$1
         ORDER BY timestamp DESC LIMIT 1`

	err = db.QueryRowContext(ctx, lastMsgQuery, chatID).Scan(
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
		return nil, 0, fmt.Errorf("ошибка получения последнего сообщения: %w", err)
	}

	log.Printf("GetChatByID: успешно, возвращаем чат с %d сообщениями", len(chat.Messages))
	return &chat, total, nil
}

func GetOrCreateChat(
	db *sql.DB,
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
	log.Printf("GetOrCreateChat: начало, userID=%s, userName='%s', userEmail='%s', source=%s, sourceID=%s, botID=%s, clientAPIKey=%s",
		userID, userName, userEmail, source, sourceID, botID, clientAPIKey)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка начала транзакции: %v", err)
		return nil, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer tx.Rollback()

	// Получаем или создаем пользователя
	user, err := getOrCreateUser(ctx, tx, userID, userName, userEmail, source, sourceID)
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка getOrCreateUser: %v", err)
		return nil, fmt.Errorf("ошибка получения/создания пользователя: %w", err)
	}
	log.Printf("GetOrCreateChat: получен/создан пользователь ID=%s, name='%s', email='%s'",
		user.ID, user.Name, user.Email)

	// Получаем UUID клиента по API ключу
	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, clientAPIKey)
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка getClientUUIDByAPIKey: %v", err)
		return nil, fmt.Errorf("ошибка получения клиента: %w", err)
	}
	log.Printf("GetOrCreateChat: получен clientUUID=%s для API key=%s", clientUUID, clientAPIKey)

	// Проверяем, существует ли АКТИВНЫЙ (не архивированный) чат
	// Если чат архивирован, создаем новый чат для клиента
	var chatID uuid.UUID
	checkQuery := "SELECT id FROM chats WHERE user_id=$1 AND source=$2 AND bot_id=$3 AND client_id=$4 AND is_archived=false LIMIT 1"
	log.Printf("GetOrCreateChat: проверяем существование активного чата: user_id=%s, source=%s, bot_id=%s, client_id=%s",
		user.ID, source, botID, clientUUID)

	err = tx.QueryRowContext(ctx, checkQuery, user.ID, source, botID, clientUUID).Scan(&chatID)

	if err != nil && err != sql.ErrNoRows {
		log.Printf("GetOrCreateChat: ошибка поиска чата: %v", err)
		return nil, fmt.Errorf("ошибка поиска чата: %w", err)
	}

	if err == sql.ErrNoRows {
		// Создаем новый чат
		chatID = uuid.New()
		now := time.Now()
		log.Printf("GetOrCreateChat: создаем новый чат ID=%s для user=%s, client=%s",
			chatID, user.ID, clientUUID)

		insertQuery := `
            INSERT INTO chats(id,user_id,created_at,updated_at,status,source,bot_id,client_id) 
            VALUES($1,$2,$3,$4,'active',$5,$6,$7)`

		if _, err := tx.ExecContext(ctx, insertQuery,
			chatID, user.ID, now, now, source, botID, clientUUID,
		); err != nil {
			log.Printf("GetOrCreateChat: ошибка создания чата: %v", err)
			return nil, fmt.Errorf("ошибка создания чата: %w", err)
		}
		log.Printf("GetOrCreateChat: чат успешно создан")
	} else {
		log.Printf("GetOrCreateChat: найден существующий чат ID=%s", chatID)
	}

	if err := tx.Commit(); err != nil {
		log.Printf("GetOrCreateChat: ошибка коммита транзакции: %v", err)
		return nil, fmt.Errorf("ошибка коммита транзакции: %w", err)
	}

	log.Printf("GetOrCreateChat: транзакция успешно закоммичена")

	// Получаем полные данные созданного/найденного чата (последние 25 сообщений)
	chat, _, err := GetChatByID(db, chatID, 25, "")
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка получения созданного чата: %v", err)
		return nil, fmt.Errorf("ошибка получения чата: %w", err)
	}

	log.Printf("GetOrCreateChat: успешно, возвращаем чат ID=%s, clientID=%s, userID=%s",
		chat.ID, chat.ClientID, chat.User.ID)
	return chat, nil
}

// UpdateAutoResponder обновляет статус автоответчика для чата
func UpdateAutoResponder(db *sql.DB, chatID uuid.UUID, enabled bool) error {
	log.Printf("UpdateAutoResponder: начало, chatID=%s, enabled=%t", chatID, enabled)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `UPDATE chats SET auto_responder_enabled=$1 WHERE id=$2`

	result, err := db.ExecContext(ctx, query, enabled, chatID)
	if err != nil {
		log.Printf("UpdateAutoResponder: ошибка обновления: %v", err)
		return fmt.Errorf("ошибка обновления автоответчика: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Printf("UpdateAutoResponder: ошибка получения количества обновленных строк: %v", err)
		return fmt.Errorf("ошибка проверки обновления: %w", err)
	}

	if rowsAffected == 0 {
		log.Printf("UpdateAutoResponder: чат не найден: %s", chatID)
		return fmt.Errorf("чат не найден")
	}

	log.Printf("UpdateAutoResponder: успешно обновлен, chatID=%s, enabled=%t", chatID, enabled)
	return nil
}
