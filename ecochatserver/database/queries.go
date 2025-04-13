package database

import (
	"database/sql"
	"ecochatserver/models"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// GetAdmin получает администратора по электронной почте
func GetAdmin(email string) (*models.Admin, error) {
	var admin models.Admin
	var avatarNull sql.NullString

	row := DB.QueryRow("SELECT id, name, email, password_hash, avatar, role, client_id, active FROM admins WHERE email = ?", email)
	err := row.Scan(&admin.ID, &admin.Name, &admin.Email, &admin.PasswordHash, &avatarNull, &admin.Role, &admin.ClientID, &admin.Active)
	if err != nil {
		log.Printf("Ошибка при получении администратора: %v", err)
		return nil, err
	}

	// Обрабатываем NULL-значение для avatar
	if avatarNull.Valid {
		avatarStr := avatarNull.String
		admin.Avatar = &avatarStr
	} else {
		admin.Avatar = nil
	}

	return &admin, nil
}

// VerifyPassword проверяет хеш пароля
func VerifyPassword(password, hashedPassword string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}

// GetChats получает список чатов для указанного админа
func GetChats(clientID string, adminID string) ([]models.ChatResponse, error) {
	log.Printf("Получение чатов для клиента %s и админа %s", clientID, adminID)
	
	// Исправленный SQL запрос с раздельными подзапросами для каждого поля последнего сообщения
	query := `
		SELECT c.id, c.created_at, c.updated_at, c.status, 
			u.id, u.name, u.email, u.avatar,
			(
				SELECT COUNT(*) FROM messages 
				WHERE chat_id = c.id AND sender = 'user' AND read = false
			) as unread_count,
			(SELECT m.id FROM messages m WHERE m.chat_id = c.id ORDER BY m.timestamp DESC LIMIT 1) as last_message_id,
			(SELECT m.content FROM messages m WHERE m.chat_id = c.id ORDER BY m.timestamp DESC LIMIT 1) as last_message_content,
			(SELECT m.sender FROM messages m WHERE m.chat_id = c.id ORDER BY m.timestamp DESC LIMIT 1) as last_message_sender,
			(SELECT m.timestamp FROM messages m WHERE m.chat_id = c.id ORDER BY m.timestamp DESC LIMIT 1) as last_message_timestamp
		FROM chats c
		JOIN users u ON c.user_id = u.id
		WHERE c.client_id = ? AND (c.assigned_to = ? OR c.assigned_to IS NULL)
		ORDER BY c.updated_at DESC
	`
	
	rows, err := DB.Query(query, clientID, adminID)
	if err != nil {
		log.Printf("Ошибка SQL запроса GetChats: %v", err)
		return nil, err
	}
	defer rows.Close()

	var chats []models.ChatResponse
	for rows.Next() {
		var chat models.ChatResponse
		var user models.User
		var unreadCount int
		var lastMessageID, lastMessageContent, lastMessageSender, lastMessageTimestamp sql.NullString
		var avatarNull sql.NullString

		err := rows.Scan(
			&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status,
			&user.ID, &user.Name, &user.Email, &avatarNull,
			&unreadCount,
			&lastMessageID, &lastMessageContent, &lastMessageSender, &lastMessageTimestamp,
		)
		if err != nil {
			log.Printf("Ошибка сканирования данных чата: %v", err)
			return nil, err
		}

		// Обрабатываем NULL-значение для avatar
		if avatarNull.Valid {
			avatarStr := avatarNull.String
			user.Avatar = &avatarStr
		} else {
			user.Avatar = nil
		}

		chat.User = user
		chat.UnreadCount = unreadCount

		// Обрабатываем последнее сообщение, только если оно существует
		if lastMessageID.Valid && lastMessageContent.Valid && lastMessageSender.Valid && lastMessageTimestamp.Valid {
			timestamp, _ := time.Parse(time.RFC3339, lastMessageTimestamp.String)
			chat.LastMessage = &models.Message{
				ID:        lastMessageID.String,
				Content:   lastMessageContent.String,
				Sender:    lastMessageSender.String,
				Timestamp: timestamp,
			}
		}

		chats = append(chats, chat)
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка после сканирования строк: %v", err)
		return nil, err
	}

	log.Printf("Успешно получено %d чатов", len(chats))
	return chats, nil
}

// GetChatByID получает чат по ID с его сообщениями
func GetChatByID(chatID string) (*models.Chat, error) {
	log.Printf("Получение чата по ID: %s", chatID)
	
	// Получаем информацию о чате
	var chat models.Chat
	var userID string
	var assignedToNull sql.NullString

	err := DB.QueryRow(`
		SELECT c.id, c.created_at, c.updated_at, c.status, c.user_id, c.source, c.bot_id, c.client_id, c.assigned_to
		FROM chats c
		WHERE c.id = ?
	`, chatID).Scan(
		&chat.ID, &chat.CreatedAt, &chat.UpdatedAt, &chat.Status, &userID, &chat.Source, &chat.BotID, &chat.ClientID, &assignedToNull,
	)
	if err != nil {
		log.Printf("Ошибка при получении чата: %v", err)
		return nil, err
	}

	// Обрабатываем NULL значение для assignedTo
	if assignedToNull.Valid {
		assignedToStr := assignedToNull.String
		chat.AssignedTo = &assignedToStr
	} else {
		chat.AssignedTo = nil
	}

	// Получаем информацию о пользователе
	var user models.User
	var avatarNull sql.NullString
	
	err = DB.QueryRow(`
		SELECT id, name, email, avatar, source, source_id
		FROM users
		WHERE id = ?
	`, userID).Scan(
		&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
	)
	if err != nil {
		log.Printf("Ошибка при получении пользователя для чата: %v", err)
		return nil, err
	}

	// Обрабатываем NULL-значение для avatar
	if avatarNull.Valid {
		avatarStr := avatarNull.String
		user.Avatar = &avatarStr
	} else {
		user.Avatar = nil
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
		log.Printf("Ошибка при получении сообщений чата: %v", err)
		return nil, err
	}
	defer rows.Close()

	var messages []models.Message
	for rows.Next() {
		var message models.Message
		var typeStr string
		var metadataNull sql.NullString
		var timestamp time.Time

		err := rows.Scan(
			&message.ID, &message.Content, &message.Sender, &message.SenderID,
			&timestamp, &message.Read, &typeStr, &metadataNull,
		)
		if err != nil {
			log.Printf("Ошибка при сканировании сообщения: %v", err)
			return nil, err
		}

		message.ChatID = chatID
		message.Timestamp = timestamp
		message.Type = typeStr

		// Парсим метаданные, если они есть
		if metadataNull.Valid && metadataNull.String != "" {
			var metadata map[string]interface{}
			if err := json.Unmarshal([]byte(metadataNull.String), &metadata); err == nil {
				message.Metadata = metadata
			}
		}

		messages = append(messages, message)
	}

	if err = rows.Err(); err != nil {
		log.Printf("Ошибка после сканирования сообщений: %v", err)
		return nil, err
	}

	chat.Messages = messages

	// Последнее сообщение
	if len(messages) > 0 {
		lastMessage := messages[len(messages)-1]
		chat.LastMessage = &lastMessage
	}

	log.Printf("Успешно получен чат с %d сообщениями", len(messages))
	return &chat, nil
}

// AddMessage добавляет новое сообщение в чат
func AddMessage(chatID, content, sender, senderID, messageType string, metadata map[string]interface{}) (*models.Message, error) {
	log.Printf("Добавление сообщения в чат %s от %s", chatID, sender)
	
	// Проверяем, существует ли чат
	var exists bool
	err := DB.QueryRow("SELECT EXISTS(SELECT 1 FROM chats WHERE id = ?)", chatID).Scan(&exists)
	if err != nil {
		log.Printf("Ошибка при проверке существования чата: %v", err)
		return nil, err
	}

	if !exists {
		log.Printf("Чат с ID %s не найден", chatID)
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
			log.Printf("Ошибка при сериализации метаданных: %v", err)
			return nil, err
		}
	}

	// Сохраняем в базу
	_, err = DB.Exec(`
		INSERT INTO messages (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, chatID, content, sender, senderID, now, false, messageType, metadataJSON)
	if err != nil {
		log.Printf("Ошибка при добавлении сообщения в БД: %v", err)
		return nil, err
	}

	// Обновляем время последнего обновления чата
	_, err = DB.Exec("UPDATE chats SET updated_at = ? WHERE id = ?", now, chatID)
	if err != nil {
		log.Printf("Ошибка при обновлении времени чата: %v", err)
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

	log.Printf("Сообщение успешно добавлено с ID: %s", messageID)
	return message, nil
}

// CreateOrGetChat создает новый чат или находит существующий
func CreateOrGetChat(userID, userName, userEmail, source, sourceID, botID, clientID string) (*models.Chat, *models.Message, error) {
	log.Printf("Создание/получение чата для пользователя %s (источник: %s)", userID, source)
	
	var user models.User
	var userExists bool

	// Проверяем, существует ли пользователь
	err := DB.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE source = ? AND source_id = ?)", source, sourceID).Scan(&userExists)
	if err != nil {
		log.Printf("Ошибка при проверке существования пользователя: %v", err)
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
			log.Printf("Ошибка при создании пользователя: %v", err)
			return nil, nil, err
		}
		log.Printf("Создан новый пользователь с ID: %s", user.ID)
	} else {
		// Получаем существующего пользователя
		var avatarNull sql.NullString
		err = DB.QueryRow(`
			SELECT id, name, email, avatar, source, source_id
			FROM users
			WHERE source = ? AND source_id = ?
		`, source, sourceID).Scan(
			&user.ID, &user.Name, &user.Email, &avatarNull, &user.Source, &user.SourceID,
		)
		if err != nil {
			log.Printf("Ошибка при получении существующего пользователя: %v", err)
			return nil, nil, err
		}

		// Обрабатываем NULL-значение для avatar
		if avatarNull.Valid {
			avatarStr := avatarNull.String
			user.Avatar = &avatarStr
		} else {
			user.Avatar = nil
		}
		log.Printf("Найден существующий пользователь с ID: %s", user.ID)
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
		log.Printf("Ошибка при проверке существования чата: %v", err)
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
			log.Printf("Ошибка при получении ID существующего чата: %v", err)
			return nil, nil, err
		}

		// Обновляем статус чата на активный
		_, err = DB.Exec("UPDATE chats SET status = 'active', updated_at = ? WHERE id = ?", now, chatID)
		if err != nil {
			log.Printf("Ошибка при обновлении статуса чата: %v", err)
			return nil, nil, err
		}

		// Получаем полную информацию о чате
		chat, err := GetChatByID(chatID)
		if err != nil {
			log.Printf("Ошибка при получении полной информации о чате: %v", err)
			return nil, nil, err
		}
		log.Printf("Найден существующий чат с ID: %s", chatID)

		return chat, nil, nil
	}

	// Создаем новый чат
	chatID = uuid.New().String()
	_, err = DB.Exec(`
		INSERT INTO chats (id, user_id, created_at, updated_at, status, source, bot_id, client_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, chatID, user.ID, now, now, "active", source, botID, clientID)
	if err != nil {
		log.Printf("Ошибка при создании нового чата: %v", err)
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
	log.Printf("Создан новый чат с ID: %s", chatID)

	return &chat, nil, nil
}

// MarkMessagesAsRead отмечает сообщения чата как прочитанные
func MarkMessagesAsRead(chatID string) error {
	log.Printf("Отметка сообщений как прочитанные для чата %s", chatID)
	
	result, err := DB.Exec(`
		UPDATE messages 
		SET read = true 
		WHERE chat_id = ? AND sender = 'user' AND read = false
	`, chatID)
	if err != nil {
		log.Printf("Ошибка при отметке сообщений как прочитанные: %v", err)
		return err
	}
	
	count, _ := result.RowsAffected()
	log.Printf("Отмечено как прочитанные %d сообщений", count)
	return nil
}