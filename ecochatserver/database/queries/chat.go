package queries

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
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
    countQuery := `
        SELECT COUNT(*) FROM chats
        WHERE client_id=$1 AND (assigned_to=$2 OR assigned_to IS NULL)`
    
    log.Printf("GetChats: выполняем запрос подсчета: %s", countQuery)
    log.Printf("GetChats: параметры подсчета: clientID=%s, adminID=%s", clientID, adminID)
    
    if err := db.QueryRowContext(ctx, countQuery, clientID, adminID).Scan(&total); err != nil {
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
    
    offset := (page - 1) * size
    log.Printf("GetChats: выполняем основной запрос с LIMIT=%d OFFSET=%d", size, offset)
    
    rows, err := db.QueryContext(ctx, q, clientID, adminID, size, offset)
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
            &chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
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

func GetChatByID(db *sql.DB, chatID uuid.UUID, page, size int) (*models.Chat, int, error) {
    log.Printf("GetChatByID: начало, chatID=%s, page=%d, size=%d", chatID, page, size)
    
    if page < 1 {
        page = 1
        log.Printf("GetChatByID: page скорректирован на 1")
    }
    if size < 1 || size > MaxPageSize {
        oldSize := size
        size = DefaultPageSize
        log.Printf("GetChatByID: size скорректирован с %d на %d", oldSize, size)
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
               source,bot_id,client_id,assigned_to
          FROM chats WHERE id=$1`
    
    log.Printf("GetChatByID: выполняем запрос чата: %s", chatQuery)
    
    if err := db.QueryRowContext(ctx, chatQuery, chatID).Scan(
        &chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
        &userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedNull,
    ); err != nil {
        log.Printf("GetChatByID: ошибка получения чата: %v", err)
        if err == sql.ErrNoRows {
            return nil, 0, fmt.Errorf("чат не найден")
        }
        return nil, 0, fmt.Errorf("ошибка получения чата: %w", err)
    }
    
    var err error
    chat.AssignedTo, err = nullUUIDToPointer(assignedNull)
    if err != nil {
        log.Printf("GetChatByID: ошибка преобразования assigned_to: %v", err)
        return nil, 0, fmt.Errorf("ошибка преобразования assigned_to: %w", err)
    }
    
    log.Printf("GetChatByID: найден чат ID=%s, userID=%s, clientID=%s, status=%s, source=%s, botID=%s", 
        chat.ID, userID, chat.ClientID, chat.Status, chat.Source, chat.BotID)

    // Получаем данные пользователя
    var (
        user       models.User
        avatarNull sql.NullString
    )
    userQuery := `
        SELECT id,name,email,avatar,source,source_id
          FROM users WHERE id=$1`
    
    log.Printf("GetChatByID: получаем пользователя ID=%s", userID)
    
    if err := db.QueryRowContext(ctx, userQuery, userID).Scan(
        &user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
    ); err != nil {
        log.Printf("GetChatByID: ошибка получения пользователя: %v", err)
        return nil, 0, fmt.Errorf("ошибка получения пользователя: %w", err)
    }
    
    user.Avatar = nullStringToPointer(avatarNull)
    chat.User = user
    
    log.Printf("GetChatByID: пользователь: ID=%s, name='%s', email='%s', source=%s, sourceID=%s", 
        user.ID, user.Name, user.Email, user.Source, user.SourceID)

    // Подсчитываем общее количество сообщений
    var total int
    countQuery := "SELECT COUNT(*) FROM messages WHERE chat_id=$1"
    if err := db.QueryRowContext(ctx, countQuery, chatID).Scan(&total); err != nil {
        log.Printf("GetChatByID: ошибка подсчета сообщений: %v", err)
        return nil, 0, fmt.Errorf("ошибка подсчета сообщений: %w", err)
    }
    
    log.Printf("GetChatByID: всего сообщений в чате: %d", total)

    // Получаем сообщения с пагинацией
    offset := (page - 1) * size
    messagesQuery := `
        SELECT id,content,sender,sender_id,timestamp,read,type,metadata
          FROM messages
         WHERE chat_id=$1
         ORDER BY timestamp ASC
         LIMIT $2 OFFSET $3`
    
    log.Printf("GetChatByID: получаем сообщения с LIMIT=%d OFFSET=%d", size, offset)
    
    rows, err := db.QueryContext(ctx, messagesQuery, chatID, size, offset)
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
            &m.Timestamp, &m.Read, &m.Type, &raw,
        ); err != nil {
            log.Printf("GetChatByID: ошибка сканирования сообщения %d: %v", msgNum, err)
            return nil, 0, fmt.Errorf("ошибка сканирования сообщения: %w", err)
        }
        
        m.ChatID = chatID
        if len(raw) > 0 {
            _ = json.Unmarshal(raw, &m.Metadata)
        }
        
        log.Printf("GetChatByID: сообщение %d: ID=%s, sender=%s, senderID=%s, content='%s', timestamp=%v, read=%v, type=%s", 
            msgNum, m.ID, m.Sender, m.SenderID, m.Content, m.Timestamp, m.Read, m.Type)
        
        chat.Messages = append(chat.Messages, m)
        msgNum++
    }
    
    if err := rows.Err(); err != nil {
        log.Printf("GetChatByID: ошибка после обработки сообщений: %v", err)
        return nil, 0, fmt.Errorf("ошибка обработки сообщений: %w", err)
    }

    // Получаем последнее сообщение
    var last models.Message
    var raw []byte
    lastMsgQuery := `
        SELECT id,content,sender,sender_id,timestamp,read,type,metadata
          FROM messages
         WHERE chat_id=$1
         ORDER BY timestamp DESC LIMIT 1`
    
    log.Printf("GetChatByID: получаем последнее сообщение")
    
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
        log.Printf("GetChatByID: последнее сообщение: ID=%s, sender=%s, content='%s', timestamp=%v, ChatID=%s", 
            last.ID, last.Sender, last.Content, last.Timestamp, last.ChatID)
    } else if err != sql.ErrNoRows {
        log.Printf("GetChatByID: ошибка получения последнего сообщения: %v", err)
        return nil, 0, fmt.Errorf("ошибка получения последнего сообщения: %w", err)
    } else {
        log.Printf("GetChatByID: нет сообщений в чате")
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

    // Проверяем, существует ли чат
    var chatID uuid.UUID
    checkQuery := "SELECT id FROM chats WHERE user_id=$1 AND source=$2 AND bot_id=$3 AND client_id=$4 LIMIT 1"
    log.Printf("GetOrCreateChat: проверяем существование чата: user_id=%s, source=%s, bot_id=%s, client_id=%s", 
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

    // Получаем полные данные созданного/найденного чата
    chat, _, err := GetChatByID(db, chatID, 1, DefaultPageSize)
    if err != nil {
        log.Printf("GetOrCreateChat: ошибка получения созданного чата: %v", err)
        return nil, fmt.Errorf("ошибка получения чата: %w", err)
    }
    
    log.Printf("GetOrCreateChat: успешно, возвращаем чат ID=%s, clientID=%s, userID=%s", 
        chat.ID, chat.ClientID, chat.User.ID)
    return chat, nil
}