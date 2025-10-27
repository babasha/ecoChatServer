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

	// Основной запрос для получения чатов
	const q = `
      SELECT
        c.id,c.created_at,c.updated_at,c.status,c.source,c.client_id,c.auto_responder_enabled,
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
      GROUP BY c.id,c.created_at,c.updated_at,c.status,c.source,c.client_id,c.auto_responder_enabled,u.id,u.name,u.email,u.avatar,l.id,l.content,l.sender,l.timestamp
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
			&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status, &chat.Source, &chat.ClientID, &chat.AutoResponderEnabled,
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

		log.Printf("GetChats: чат %d: ID=%s, userID=%s, userName='%s', email='%s', status=%s, source='%s', unread=%d, created=%v, updated=%v",
			rowNum, chat.ID, user.ID, user.Name, user.Email, chat.Status, chat.Source, unread, chat.CreatedAt, chat.UpdatedAt)

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

// chatCoreInfo содержит базовую информацию о чате после создания/поиска
type chatCoreInfo struct {
	ChatID           uuid.UUID
	User             *models.User
	ClientID         uuid.UUID
	Source           string
	BotID            string
	WidgetBusinessID *string   // ID бизнеса/виджета (опционально)
	IsNewChat        bool      // true если чат только что создан
	CreatedAt        time.Time // только для новых чатов
	UpdatedAt        time.Time // только для новых чатов
}

// getOrCreateChatCore - внутренняя функция для создания/поиска чата
// Возвращает только базовую информацию без загрузки сообщений и метаданных
func getOrCreateChatCore(
	db *sql.DB,
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
	widgetBusinessID *string,
) (*chatCoreInfo, error) {
	log.Printf("getOrCreateChatCore: начало, userID=%s, source=%s, botID=%s, widgetBusinessID=%v",
		userID, source, botID, widgetBusinessID)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer tx.Rollback()

	// Получаем или создаем пользователя
	user, err := getOrCreateUser(ctx, tx, userID, userName, userEmail, source, sourceID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения/создания пользователя: %w", err)
	}

	// Получаем UUID клиента по API ключу
	clientUUID, err := getClientUUIDByAPIKey(ctx, tx, clientAPIKey)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения клиента: %w", err)
	}

	// Проверяем, существует ли АКТИВНЫЙ (не архивированный) чат
	var chatID uuid.UUID
	checkQuery := "SELECT id FROM chats WHERE user_id=$1 AND source=$2 AND bot_id=$3 AND client_id=$4 AND is_archived=false LIMIT 1"

	err = tx.QueryRowContext(ctx, checkQuery, user.ID, source, botID, clientUUID).Scan(&chatID)

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("ошибка поиска чата: %w", err)
	}

	var isNewChat bool
	var createdAt, updatedAt time.Time

	if err == sql.ErrNoRows {
		// Создаем новый чат
		chatID = uuid.New()
		now := time.Now()
		createdAt = now
		updatedAt = now
		isNewChat = true

		log.Printf("getOrCreateChatCore: создаем новый чат ID=%s", chatID)

		insertQuery := `
            INSERT INTO chats(id,user_id,created_at,updated_at,status,source,bot_id,client_id,widget_business_id)
            VALUES($1,$2,$3,$4,'active',$5,$6,$7,$8)`

		if _, err := tx.ExecContext(ctx, insertQuery,
			chatID, user.ID, createdAt, updatedAt, source, botID, clientUUID, widgetBusinessID,
		); err != nil {
			return nil, fmt.Errorf("ошибка создания чата: %w", err)
		}
	} else {
		// Существующий чат - даты будут загружены позже при необходимости
		isNewChat = false
		// Для существующих чатов createdAt и updatedAt остаются нулевыми
		// Они используются только для новых чатов в GetOrCreateChatMetadata
		log.Printf("getOrCreateChatCore: найден существующий чат ID=%s", chatID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("ошибка коммита транзакции: %w", err)
	}

	log.Printf("getOrCreateChatCore: успешно, chatID=%s, clientID=%s, userID=%s, isNewChat=%v",
		chatID, clientUUID, user.ID, isNewChat)

	return &chatCoreInfo{
		ChatID:           chatID,
		User:             user,
		ClientID:         clientUUID,
		Source:           source,
		BotID:            botID,
		WidgetBusinessID: widgetBusinessID,
		IsNewChat:        isNewChat,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}, nil
}

// GetOrCreateChat создает или получает чат С ЗАГРУЗКОЙ последних 25 сообщений
func GetOrCreateChat(
	db *sql.DB,
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
	widgetBusinessID *string,
) (*models.Chat, error) {
	log.Printf("GetOrCreateChat: начало, userID=%s, userName='%s', userEmail='%s', source=%s, sourceID=%s, botID=%s, clientAPIKey=%s",
		userID, userName, userEmail, source, sourceID, botID, clientAPIKey)

	// Получаем базовую информацию о чате
	info, err := getOrCreateChatCore(db, userID, userName, userEmail, source, sourceID, botID, clientAPIKey, widgetBusinessID)
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка getOrCreateChatCore: %v", err)
		return nil, err
	}

	// Загружаем полные данные чата с сообщениями
	chat, _, err := GetChatByID(db, info.ChatID, 25, "")
	if err != nil {
		log.Printf("GetOrCreateChat: ошибка получения чата с сообщениями: %v", err)
		return nil, fmt.Errorf("ошибка получения чата: %w", err)
	}

	// Сохраняем флаг isNewChat для использования во внешнем слое
	chat.IsNewChat = info.IsNewChat

	log.Printf("GetOrCreateChat: успешно, возвращаем чат ID=%s с %d сообщениями, isNewChat=%v",
		chat.ID, len(chat.Messages), info.IsNewChat)
	return chat, nil
}

// GetOrCreateChatMetadata возвращает или создает чат БЕЗ загрузки истории сообщений
// Используется в webhook для оптимизации производительности
func GetOrCreateChatMetadata(
	db *sql.DB,
	userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
	widgetBusinessID *string,
) (*models.Chat, error) {
	log.Printf("GetOrCreateChatMetadata: начало, userID=%s, source=%s, botID=%s",
		userID, source, botID)

	// Получаем базовую информацию о чате
	info, err := getOrCreateChatCore(db, userID, userName, userEmail, source, sourceID, botID, clientAPIKey, widgetBusinessID)
	if err != nil {
		log.Printf("GetOrCreateChatMetadata: ошибка getOrCreateChatCore: %v", err)
		return nil, err
	}

	var createdAt, updatedAt time.Time
	var status string
	var assignedTo *uuid.UUID
	var autoResponderEnabled, isArchived bool

	if info.IsNewChat {
		// Новый чат - используем известные значения без SELECT к БД
		createdAt = info.CreatedAt
		updatedAt = info.UpdatedAt
		status = "active"
		assignedTo = nil
		autoResponderEnabled = true // DEFAULT в БД тоже true
		isArchived = false
		log.Printf("GetOrCreateChatMetadata: новый чат, используем значения по умолчанию")
	} else {
		// Существующий чат - загружаем метаданные из БД
		ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
		defer cancel()

		var assignedNull sql.NullString

		metadataQuery := `
			SELECT created_at, updated_at, status, assigned_to, auto_responder_enabled, is_archived
			FROM chats
			WHERE id=$1`

		err = db.QueryRowContext(ctx, metadataQuery, info.ChatID).Scan(
			&createdAt, &updatedAt, &status, &assignedNull, &autoResponderEnabled, &isArchived,
		)
		if err != nil {
			log.Printf("GetOrCreateChatMetadata: ошибка загрузки метаданных: %v", err)
			return nil, fmt.Errorf("ошибка загрузки метаданных чата: %w", err)
		}

		assignedTo, err = nullUUIDToPointer(assignedNull)
		if err != nil {
			return nil, fmt.Errorf("ошибка преобразования assigned_to: %w", err)
		}
		log.Printf("GetOrCreateChatMetadata: существующий чат, загружены метаданные из БД")
	}

	chat := &models.Chat{
		ID:                   info.ChatID,
		User:                 *info.User,
		Messages:             []models.Message{}, // Пустой массив
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
		Status:               status,
		Source:               info.Source,
		BotID:                info.BotID,
		ClientID:             info.ClientID,
		WidgetBusinessID:     info.WidgetBusinessID,
		AssignedTo:           assignedTo,
		AutoResponderEnabled: autoResponderEnabled,
		IsArchived:           isArchived,
		IsNewChat:            info.IsNewChat, // Сохраняем флаг
	}

	log.Printf("GetOrCreateChatMetadata: успешно, возвращаем чат ID=%s БЕЗ сообщений, isNewChat=%v, autoResponderEnabled=%v",
		chat.ID, info.IsNewChat, chat.AutoResponderEnabled)
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

// FindChatByUserSourceID ищет чат по source_id пользователя и source
// Используется ТОЛЬКО для Instagram demo, не затрагивает widget систему
func FindChatByUserSourceID(db *sql.DB, userSourceID, source string) (*models.Chat, error) {
	log.Printf("FindChatByUserSourceID: поиск чата для userSourceID=%s, source=%s", userSourceID, source)

	ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
	defer cancel()

	query := `
		SELECT c.id, c.client_id, c.created_at, c.updated_at, c.status, c.bot_id,
		       c.assigned_to, c.is_archived, c.auto_responder_enabled,
		       u.id, u.source_id, u.name, u.email, u.avatar, u.source
		FROM chats c
		JOIN users u ON c.user_id = u.id
		WHERE u.source_id = $1 AND u.source = $2
		ORDER BY c.updated_at DESC
		LIMIT 1
	`

	var chat models.Chat
	var user models.User
	var assignedTo sql.NullString
	var botID sql.NullString

	err := db.QueryRowContext(ctx, query, userSourceID, source).Scan(
		&chat.ID, &chat.ClientID, &chat.CreatedAt, &chat.UpdatedAt,
		&chat.Status, &botID, &assignedTo, &chat.IsArchived, &chat.AutoResponderEnabled,
		&user.ID, &user.SourceID, &user.Name, &user.Email, &user.Avatar, &user.Source,
	)

	if err == sql.ErrNoRows {
		log.Printf("FindChatByUserSourceID: чат не найден для userSourceID=%s, source=%s", userSourceID, source)
		return nil, fmt.Errorf("чат не найден")
	}

	if err != nil {
		log.Printf("FindChatByUserSourceID: ошибка запроса: %v", err)
		return nil, fmt.Errorf("ошибка поиска чата: %w", err)
	}

	if assignedTo.Valid {
		assignedUUID, err := uuid.Parse(assignedTo.String)
		if err == nil {
			chat.AssignedTo = &assignedUUID
		}
	}

	if botID.Valid {
		chat.BotID = botID.String
	}

	chat.User = user

	log.Printf("FindChatByUserSourceID: найден чат %s для userSourceID=%s", chat.ID, userSourceID)
	return &chat, nil
}
