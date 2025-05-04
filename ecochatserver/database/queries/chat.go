package queries

import (
    "context"
    "database/sql"
    "encoding/json"
    "time"

    "github.com/google/uuid"
    "github.com/egor/ecochatserver/models"
)

func GetChats(db *sql.DB, clientID, adminID uuid.UUID, page, size int) ([]models.ChatResponse, int, error) {
    if page < 1 {
        page = 1
    }
    if size < 1 || size > MaxPageSize {
        size = DefaultPageSize
    }
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    var total int
    if err := db.QueryRowContext(ctx, `
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
    rows, err := db.QueryContext(ctx, q, clientID, adminID, size, (page-1)*size)
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

func GetChatByID(db *sql.DB, chatID uuid.UUID, page, size int) (*models.Chat, int, error) {
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
    if err := db.QueryRowContext(ctx, `
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
    if err := db.QueryRowContext(ctx, `
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
    if err := db.QueryRowContext(ctx,
        "SELECT COUNT(*) FROM messages WHERE chat_id=$1",
        chatID,
    ).Scan(&total); err != nil {
        return nil, 0, err
    }

    // fetch messages
    rows, err := db.QueryContext(ctx, `
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
    err = db.QueryRowContext(ctx, `
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

func GetOrCreateChat(
    db *sql.DB,
    userID, userName, userEmail, source, sourceID, botID, clientAPIKey string,
) (*models.Chat, error) {
    ctx, cancel := context.WithTimeout(context.Background(), dbQueryTimeout)
    defer cancel()

    tx, err := db.BeginTx(ctx, nil)
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

    chat, _, err := GetChatByID(db, chatID, 1, DefaultPageSize)
    return chat, err
}