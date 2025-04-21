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

// GetAdmin получает администратора по электронной почте.
func GetAdmin(email string) (*models.Admin, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    var admin models.Admin
    var avatarNull sql.NullString

    const query = `
        SELECT id, name, email, password_hash, avatar, role, client_id, active
        FROM admins
        WHERE email = $1
    `
    row := DB.QueryRowContext(ctx, query, email)
    if err := row.Scan(
        &admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash,
        &avatarNull, &admin.Role, &admin.ClientID, &admin.Active,
    ); err != nil {
        if err == sql.ErrNoRows {
            return nil, nil
        }
        return nil, fmt.Errorf("GetAdmin: scan: %w", err)
    }

    admin.Avatar = nullStringToPointer(avatarNull)
    return &admin, nil
}

// VerifyPassword сравнивает plain‑текст пароля с bcrypt‑хэшем.
func VerifyPassword(password, hashedPassword string) error {
    return bcrypt.CompareHashAndPassword(
        []byte(hashedPassword),
        []byte(password),
    )
}

// GetChats возвращает список чатов для клиента и админа с пагинацией
// и общее количество чатов для расчёта страниц.
func GetChats(clientID, adminID string, page, pageSize int) ([]models.ChatResponse, int, error) {
    if page < 1 {
        page = 1
    }
    if pageSize < 1 || pageSize > MaxPageSize {
        pageSize = DefaultPageSize
    }
    offset := (page - 1) * pageSize

    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    // Считаем общее количество
    var totalCount int
    const countQ = `
        SELECT COUNT(*) 
        FROM chats c
        WHERE c.client_id = $1 AND (c.assigned_to = $2 OR c.assigned_to IS NULL)
    `
    if err := DB.QueryRowContext(ctx, countQ, clientID, adminID).Scan(&totalCount); err != nil {
        return nil, 0, fmt.Errorf("GetChats: count: %w", err)
    }

    // Основной запрос
    const query = `
        SELECT 
            c.id, c.created_at, c.updated_at, c.status,
            u.id, u.name, u.email, u.avatar,
            COUNT(CASE WHEN m.sender = 'user' AND m.read = false THEN 1 END) AS unread_count,
            last_msg.id, last_msg.content, last_msg.sender, last_msg.timestamp
        FROM chats c
        JOIN users u ON c.user_id = u.id
        LEFT JOIN messages m ON m.chat_id = c.id
        LEFT JOIN (
            SELECT m1.chat_id, m1.id, m1.content, m1.sender, m1.timestamp
            FROM messages m1
            JOIN (
                SELECT chat_id, MAX(timestamp) AS max_time 
                FROM messages GROUP BY chat_id
            ) m2 ON m1.chat_id = m2.chat_id AND m1.timestamp = m2.max_time
        ) last_msg ON c.id = last_msg.chat_id
        WHERE c.client_id = $1 AND (c.assigned_to = $2 OR c.assigned_to IS NULL)
        GROUP BY 
            c.id, c.created_at, c.updated_at, c.status,
            u.id, u.name, u.email, u.avatar,
            last_msg.id, last_msg.content, last_msg.sender, last_msg.timestamp
        ORDER BY c.updated_at DESC
        LIMIT $3 OFFSET $4
    `
    rows, err := DB.QueryContext(ctx, query, clientID, adminID, pageSize, offset)
    if err != nil {
        return nil, 0, fmt.Errorf("GetChats: query: %w", err)
    }
    defer rows.Close()

    var chats []models.ChatResponse
    for rows.Next() {
        var chat models.ChatResponse
        var user models.User
        var avatarNull sql.NullString
        var unreadCount int
        var lastID, lastContent, lastSender sql.NullString
        var lastTime sql.NullTime

        if err := rows.Scan(
            &chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
            &user.ID, &user.Name, &user.Email, &avatarNull,
            &unreadCount,
            &lastID, &lastContent, &lastSender, &lastTime,
        ); err != nil {
            return nil, 0, fmt.Errorf("GetChats: scan row: %w", err)
        }

        user.Avatar = nullStringToPointer(avatarNull)
        chat.User = user
        chat.UnreadCount = unreadCount

        if lastID.Valid && lastContent.Valid && lastSender.Valid && lastTime.Valid {
            chat.LastMessage = &models.Message{
                ID:        lastID.String,
                Content:   lastContent.String,
                Sender:    lastSender.String,
                Timestamp: lastTime.Time,
            }
        }

        chats = append(chats, chat)
    }
    if err := rows.Err(); err != nil {
        return nil, 0, fmt.Errorf("GetChats: rows err: %w", err)
    }

    return chats, totalCount, nil
}

// GetChatByID возвращает чат по его ID, список сообщений с пагинацией
// и общее количество сообщений.
func GetChatByID(chatID string, page, pageSize int) (*models.Chat, int, error) {
    if page < 1 {
        page = 1
    }
    if pageSize < 1 || pageSize > MaxPageSize {
        pageSize = DefaultPageSize
    }
    offset := (page - 1) * pageSize

    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    // Основные данные чата
    var chat models.Chat
    var userID string
    var assignedNull sql.NullString

    const chatQ = `
        SELECT id, created_at, updated_at, status, user_id, source, bot_id, client_id, assigned_to
        FROM chats
        WHERE id = $1
    `
    if err := DB.QueryRowContext(ctx, chatQ, chatID).Scan(
        &chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
        &userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedNull,
    ); err != nil {
        return nil, 0, fmt.Errorf("GetChatByID: chat meta: %w", err)
    }
    chat.AssignedTo = nullStringToPointer(assignedNull)

    // Данные пользователя
    var user models.User
    var avatarNull sql.NullString

    const userQ = `
        SELECT id, name, email, avatar, source, source_id
        FROM users
        WHERE id = $1
    `
    if err := DB.QueryRowContext(ctx, userQ, userID).Scan(
        &user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
    ); err != nil {
        return nil, 0, fmt.Errorf("GetChatByID: user meta: %w", err)
    }
    user.Avatar = nullStringToPointer(avatarNull)
    chat.User = user

    // Общее количество сообщений
    var totalMessages int
    if err := DB.QueryRowContext(ctx,
        "SELECT COUNT(*) FROM messages WHERE chat_id = $1", chatID,
    ).Scan(&totalMessages); err != nil {
        return nil, 0, fmt.Errorf("GetChatByID: count messages: %w", err)
    }

    // Список сообщений
    const msgQ = `
        SELECT id, content, sender, sender_id, timestamp, read, type, metadata
        FROM messages
        WHERE chat_id = $1
        ORDER BY timestamp ASC
        LIMIT $2 OFFSET $3
    `
    rows, err := DB.QueryContext(ctx, msgQ, chatID, pageSize, offset)
    if err != nil {
        return nil, 0, fmt.Errorf("GetChatByID: query messages: %w", err)
    }
    defer rows.Close()

    var messages []models.Message
    for rows.Next() {
        var m models.Message
        var rawMeta []byte
        var ts time.Time

        if err := rows.Scan(
            &m.ID, &m.Content, &m.Sender, &m.SenderID,
            &ts, &m.Read, &m.Type, &rawMeta,
        ); err != nil {
            return nil, 0, fmt.Errorf("GetChatByID: scan message: %w", err)
        }
        m.ChatID = chatID
        m.Timestamp = ts
        if len(rawMeta) > 0 {
            if err := json.Unmarshal(rawMeta, &m.Metadata); err != nil {
                return nil, 0, fmt.Errorf("GetChatByID: unmarshal metadata: %w", err)
            }
        }
        messages = append(messages, m)
    }
    if err := rows.Err(); err != nil {
        return nil, 0, fmt.Errorf("GetChatByID: rows err: %w", err)
    }
    chat.Messages = messages

    // Последнее сообщение
    var last models.Message
    var rawMeta []byte
    var lastTS time.Time

    const lastQ = `
        SELECT id, content, sender, sender_id, timestamp, read, type, metadata
        FROM messages
        WHERE chat_id = $1
        ORDER BY timestamp DESC
        LIMIT 1
    `
    err = DB.QueryRowContext(ctx, lastQ, chatID).Scan(
        &last.ID, &last.Content, &last.Sender, &last.SenderID,
        &lastTS, &last.Read, &last.Type, &rawMeta,
    )
    if err != nil && err != sql.ErrNoRows {
        return nil, 0, fmt.Errorf("GetChatByID: last message: %w", err)
    }
    if err == nil {
        last.ChatID = chatID
        last.Timestamp = lastTS
        if len(rawMeta) > 0 {
            if err := json.Unmarshal(rawMeta, &last.Metadata); err != nil {
                return nil, 0, fmt.Errorf("GetChatByID: last unmarshal metadata: %w", err)
            }
        }
        chat.LastMessage = &last
    }

    return &chat, totalMessages, nil
}

// AddMessage вставляет новое сообщение и обновляет время чата.
func AddMessage(chatID, content, sender, senderID, messageType string, metadata map[string]interface{}) (*models.Message, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := DB.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("AddMessage: begin tx: %w", err)
    }
    defer tx.Rollback()

    var exists bool
    if err := tx.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM chats WHERE id = $1)", chatID,
    ).Scan(&exists); err != nil {
        return nil, fmt.Errorf("AddMessage: exists: %w", err)
    }
    if !exists {
        return nil, errors.New("chat not found")
    }

    messageID := uuid.New().String()
    now := time.Now()

    var rawMeta interface{} = nil
    if metadata != nil {
        b, err := json.Marshal(metadata)
        if err != nil {
            return nil, fmt.Errorf("AddMessage: marshal metadata: %w", err)
        }
        rawMeta = b
    }

    const insertQ = `
        INSERT INTO messages
            (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
        VALUES ($1,$2,$3,$4,$5,$6,false,$7,$8)
    `
    if _, err := tx.ExecContext(ctx, insertQ,
        messageID, chatID, content, sender, senderID, now, messageType, rawMeta,
    ); err != nil {
        return nil, fmt.Errorf("AddMessage: insert: %w", err)
    }

    if _, err := tx.ExecContext(ctx,
        "UPDATE chats SET updated_at = $1 WHERE id = $2", now, chatID,
    ); err != nil {
        return nil, fmt.Errorf("AddMessage: update chat: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("AddMessage: commit: %w", err)
    }

    return &models.Message{
        ID:        messageID,
        ChatID:    chatID,
        Content:   content,
        Sender:    sender,
        SenderID:  senderID,
        Timestamp: now,
        Read:      false,
        Type:      messageType,
        Metadata:  metadata,
    }, nil
}

// CreateOrGetChat создаёт новый чат для пользователя или возвращает существующий.
func CreateOrGetChat(
    userID, userName, userEmail,
    source, sourceID, botID, clientID string,
) (*models.Chat, *models.Message, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := DB.BeginTx(ctx, nil)
    if err != nil {
        return nil, nil, fmt.Errorf("CreateOrGetChat: begin tx: %w", err)
    }
    defer tx.Rollback()

    // 1) Пользователь
    var user models.User
    var avatarNull sql.NullString
    var userExists bool
    if err := tx.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM users WHERE source = $1 AND source_id = $2)",
        source, sourceID,
    ).Scan(&userExists); err != nil {
        return nil, nil, fmt.Errorf("CreateOrGetChat: check user exists: %w", err)
    }

    if !userExists {
        user = models.User{
            ID:       uuid.New().String(),
            Name:     userName,
            Email:    userEmail,
            Source:   source,
            SourceID: sourceID,
        }
        if _, err := tx.ExecContext(ctx,
            "INSERT INTO users (id, name, email, source, source_id) VALUES ($1,$2,$3,$4,$5)",
            user.ID, user.Name, user.Email, user.Source, user.SourceID,
        ); err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: insert user: %w", err)
        }
    } else {
        const selUserQ = `
            SELECT id, name, email, avatar, source, source_id
            FROM users
            WHERE source = $1 AND source_id = $2
        `
        if err := tx.QueryRowContext(ctx, selUserQ, source, sourceID).Scan(
            &user.ID, &user.Name, &user.Email,
            &avatarNull, &user.Source, &user.SourceID,
        ); err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: select user: %w", err)
        }
        user.Avatar = nullStringToPointer(avatarNull)
    }

    // 2) Чат
    var chatExists bool
    if err := tx.QueryRowContext(ctx,
        "SELECT EXISTS(SELECT 1 FROM chats WHERE user_id = $1 AND source = $2 AND bot_id = $3)",
        user.ID, source, botID,
    ).Scan(&chatExists); err != nil {
        return nil, nil, fmt.Errorf("CreateOrGetChat: check chat exists: %w", err)
    }

    now := time.Now()
    if chatExists {
        var chatID string
        if err := tx.QueryRowContext(ctx,
            "SELECT id FROM chats WHERE user_id = $1 AND source = $2 AND bot_id = $3",
            user.ID, source, botID,
        ).Scan(&chatID); err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: select chat id: %w", err)
        }
        if _, err := tx.ExecContext(ctx,
            "UPDATE chats SET status = 'active', updated_at = $1 WHERE id = $2",
            now, chatID,
        ); err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: update chat: %w", err)
        }
        if err := tx.Commit(); err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: commit existing chat: %w", err)
        }
        chat, _, err := GetChatByID(chatID, 1, DefaultPageSize)
        if err != nil {
            return nil, nil, fmt.Errorf("CreateOrGetChat: GetChatByID: %w", err)
        }
        return chat, nil, nil
    }

    // 3) Новый чат
    chatID := uuid.New().String()
    if _, err := tx.ExecContext(ctx,
        `INSERT INTO chats
            (id, user_id, created_at, updated_at, status, source, bot_id, client_id)
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
        chatID, user.ID, now, now, "active", source, botID, clientID,
    ); err != nil {
        return nil, nil, fmt.Errorf("CreateOrGetChat: insert chat: %w", err)
    }
    if err := tx.Commit(); err != nil {
        return nil, nil, fmt.Errorf("CreateOrGetChat: commit new chat: %w", err)
    }

    chat := &models.Chat{
        ID:        chatID,
        User:      user,
        Messages:  []models.Message{},
        CreatedAt: now,
        UpdatedAt: now,
        Status:    "active",
        Source:    source,
        BotID:     botID,
        ClientID:  clientID,
    }
    return chat, nil, nil
}

// MarkMessagesAsRead помечает все непрочитанные сообщения пользователя как прочитанные.
func MarkMessagesAsRead(chatID string) error {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := DB.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("MarkMessagesAsRead: begin tx: %w", err)
    }
    defer tx.Rollback()

    const updQ = `
        UPDATE messages
        SET read = true
        WHERE chat_id = $1 AND sender = 'user' AND read = false
    `
    if _, err := tx.ExecContext(ctx, updQ, chatID); err != nil {
        return fmt.Errorf("MarkMessagesAsRead: exec: %w", err)
    }
    if err := tx.Commit(); err != nil {
        return fmt.Errorf("MarkMessagesAsRead: commit: %w", err)
    }
    return nil
}

// nullStringToPointer конвертирует sql.NullString в *string.
func nullStringToPointer(ns sql.NullString) *string {
    if ns.Valid {
        s := ns.String
        return &s
    }
    return nil
}