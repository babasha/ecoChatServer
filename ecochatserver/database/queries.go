package database

import (
	"ecochatserver/models"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// GetAdmin получает администратора по электронной почте
func GetAdmin(email string) (*models.Admin, error) {
	var admin models.Admin

	row := DB.QueryRow("SELECT id, name, email, password_hash, avatar, role, client_id, active FROM admins WHERE email = ?", email)
	var passwordHash string
	err := row.Scan(&admin.ID, &admin.Name, &admin.Email, &passwordHash, &admin.Avatar, &admin.Role, &admin.ClientID, &admin.Active)
	if err != nil {
		return nil, err
	}

	return &admin, nil
}

// VerifyPassword проверяет хеш пароля
func VerifyPassword(password, hashedPassword string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}

// GetChats получает список чатов для указанного админа
func GetChats(clientID string, adminID string) ([]models.ChatResponse, error) {
	rows, err := DB.Query(`
		SELECT c.id, c.created_at, c.updated_at, c.status, 
			u.id, u.name, u.email, u.avatar,
			(
				SELECT COUNT(*) FROM messages 
				WHERE chat_id = c.id AND sender = 'user' AND read = false
			) as unread_count,
			(
				SELECT m.id, m.content, m.sender, m.timestamp
				FROM messages m 
				WHERE m.chat_id = c.id 
				ORDER BY m.timestamp DESC 
				LIMIT 1
			) as last_message
		FROM chats c
		JOIN users u ON c.user_id = u.id
		WHERE c.client_id = ? AND (c.assigned_to = ? OR c.assigned_to IS NULL)
		ORDER BY c.updated_at DESC
	`, clientID, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []models.ChatResponse
	for rows.Next() {
		var chat models.ChatResponse
		var user models.User
		var unreadCount int
		var lastMessageID, lastMessageContent, lastMessageSender, lastMessageTimestamp string

		err := rows.Scan(
			&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
			&user.ID, &user.Name, &user.Email, &user.Avatar,
			&unreadCount,
			&lastMessageID, &lastMessageContent, &lastMessageSender, &lastMessageTimestamp,
		)
		if err != nil {
			return nil, err
		}

		chat.User = user
		chat.UnreadCount = unreadCount

		if lastMessageID != "" {
			timestamp, _ := time.Parse(time.RFC3339, lastMessageTimestamp)
			chat.LastMessage = &models.Message{
				ID:        lastMessageID,
				Content:   lastMessageContent,
				Sender:    lastMessageSender,
				Timestamp: timestamp,
			}
		}

		chats = append(chats, chat)
	}

	return chats, nil
}

// GetChatByID получает чат по ID с его сообщениями
func GetChatByID(chatID string) (*models.Chat, error) {
	// Получаем информацию о чате
	var chat models.Chat
	var userID string

	err := DB.QueryRow(`
		SELECT c.id, c.created_at, c.updated_at, c.status, c.user_id, c.source, c.bot_id, c.client_id, c.assigned_to
		FROM chats c
		WHERE c.id = ?
	`, chatID).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status, &userID, &chat.Source, &chat.BotID, &chat.ClientID, &chat.AssignedTo,
	)
	if err != nil {
		return nil, err
	}

	// Получаем информацию о пользователе
	var user models.User
	err = DB.QueryRow(`
		SELECT id, name, email, avatar, source, source_id
		FROM users
		WHERE id = ?
	`, userID).Scan(
		&user.ID, &user.Name, &user.Email, &user.Avatar, &user.Source, &user.SourceID,
	)
	if err != nil {
		return nil, err
	}

	chat.User = user

	// Получаем сообщения чата
	rows, err := DB.Query(`
		SELECT id, content, sender, sender_id, timestamp, read, type, metadata
		FROM messages
		WHERE chat_id = ?
		ORDER BY timestamp ASC
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var message models.Message
		var typeStr, metadataStr string
		var timestamp time.Time

		err := rows.Scan(
			&message.ID, &message.Content, &message.Sender, &message.SenderID,
			&timestamp, &message.Read, &typeStr, &metadataStr,
		)
		if err != nil {
			return nil, err
		}

		message.ChatID = chatID
		message.Timestamp = timestamp
		message.Type = typeStr

		// Парсим метаданные, если они есть
		if metadataStr != "" {
			var metadata map[string]interface{}
			if err := json.Unmarshal([]byte(metadataStr), &metadata); err == nil {
				message.Metadata = metadata
			}
		}

		messages = append(messages, message)
	}

	chat.Messages = messages

	// Последнее сообщение
	if len(messages) > 0 {
		lastMessage := messages[len(messages)-1]
		chat.LastMessage = &lastMessage
	}

	return &chat, nil
}

// AddMessage добавляет новое сообщение в чат
func AddMessage(chatID, content, sender, senderID, messageType string, metadata map[string]interface{}) (*models.Message, error) {
	// Проверяем, существует ли чат
	var exists bool
	err := DB.QueryRow("SELECT EXISTS(SELECT 1 FROM chats WHERE id = ?)", chatID).Scan(&exists)
	if err != nil {
		return nil, err
	}

	if !exists {
		return nil, errors.New("чат не найден")
	}

	// Создаем новое сообщение
	messageID := uuid.New().String()
	now := time.Now()

	// Сериализуем метаданные
	var metadataJSON []byte
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return nil, err
		}
	}

	// Сохраняем в базу
	_, err = DB.Exec(`
		INSERT INTO messages (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, chatID, content, sender, senderID, now, false, messageType, metadataJSON)
	if err != nil {
		return nil, err
	}

	// Обновляем время последнего обновления чата
	_, err = DB.Exec("UPDATE chats SET updated_at = ? WHERE id = ?", now, chatID)
	if err != nil {
		return nil, err
	}

	// Возвращаем созданное сообщение
	message := &models.Message{
		ID:        messageID,
		ChatID:    chatID,
		Content:   content,
		Sender:    sender,
		SenderID:  senderID,
		Timestamp: now,
		Read:      false,
		Type:      messageType,
		Metadata:  metadata,
	}

	return message, nil
}

// CreateOrGetChat создает новый чат или находит существующий
func CreateOrGetChat(userID, userName, userEmail, source, sourceID, botID, clientID string) (*models.Chat, *models.Message, error) {
	var user models.User
	var userExists bool

	// Проверяем, существует ли пользователь
	err := DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE source = ? AND source_id = ?)", source, sourceID).Scan(&userExists)
	if err != nil {
		return nil, nil, err
	}

	if !userExists {
		// Создаем нового пользователя
		user = models.User{
			ID:       uuid.New().String(),
			Name:     userName,
			Email:    userEmail,
			Source:   source,
			SourceID: sourceID,
		}

		_, err = DB.Exec(`
			INSERT INTO users (id, name, email, source, source_id)
			VALUES (?, ?, ?, ?, ?)
		`, user.ID, user.Name, user.Email, user.Source, user.SourceID)
		if err != nil {
			return nil, nil, err
		}
	} else {
		// Получаем существующего пользователя
		err = DB.QueryRow(`
			SELECT id, name, email, avatar, source, source_id
			FROM users
			WHERE source = ? AND source_id = ?
		`, source, sourceID).Scan(
			&user.ID, &user.Name, &user.Email, &user.Avatar, &user.Source, &user.SourceID,
		)
		if err != nil {
			return nil, nil, err
		}
	}

	// Проверяем, существует ли чат для этого пользователя
	var chatID string
	var chatExists bool

	err = DB.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM chats 
			WHERE user_id = ? AND source = ? AND bot_id = ?
		)
	`, user.ID, source, botID).Scan(&chatExists)
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	var chat models.Chat

	if chatExists {
		// Получаем существующий чат
		err = DB.QueryRow(`
			SELECT id FROM chats
			WHERE user_id = ? AND source = ? AND bot_id = ?
		`, user.ID, source, botID).Scan(&chatID)
		if err != nil {
			return nil, nil, err
		}

		// Обновляем статус чата на активный
		_, err = DB.Exec("UPDATE chats SET status = 'active', updated_at = ? WHERE id = ?", now, chatID)
		if err != nil {
			return nil, nil, err
		}

		// Получаем полную информацию о чате
		chat, err := GetChatByID(chatID)
		if err != nil {
			return nil, nil, err
		}

		return chat, nil, nil
	}

	// Создаем новый чат
	chatID = uuid.New().String()
	_, err = DB.Exec(`
		INSERT INTO chats (id, user_id, created_at, updated_at, status, source, bot_id, client_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, chatID, user.ID, now, now, "active", source, botID, clientID)
	if err != nil {
		return nil, nil, err
	}

	chat = models.Chat{
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

	return &chat, nil, nil
}

// MarkMessagesAsRead отмечает сообщения чата как прочитанные
func MarkMessagesAsRead(chatID string) error {
	_, err := DB.Exec(`
		UPDATE messages 
		SET read = true 
		WHERE chat_id = ? AND sender = 'user' AND read = false
	`, chatID)
	return err
}