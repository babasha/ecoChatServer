package database

import (
    "database/sql"
    "encoding/json"
    "errors"
    "log"
    "time"

    "github.com/google/uuid"
    "golang.org/x/crypto/bcrypt"

    // Внутренний пакет через полный путь модуля
    "github.com/egor/ecochatserver/models"
)

// Параметры пагинации по умолчанию
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
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

	admin.Avatar = nullStringToPointer(avatarNull)

	return &admin, nil
}

// VerifyPassword проверяет хеш пароля
func VerifyPassword(password, hashedPassword string) error {
	return bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
}

// GetChats получает список чатов для указанного админа с поддержкой пагинации
func GetChats(clientID string, adminID string, page, pageSize int) ([]models.ChatResponse, int, error) {
	log.Printf("Получение чатов для клиента %s и админа %s (страница: %d, размер: %d)", clientID, adminID, page, pageSize)
	
	// Проверка и установка значений пагинации по умолчанию
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > MaxPageSize {
		pageSize = DefaultPageSize
	}
	
	offset := (page - 1) * pageSize
	
	// Сначала получаем общее количество чатов для пагинации
	var totalCount int
	err := DB.QueryRow(`
		SELECT COUNT(*) 
		FROM chats c
		WHERE c.client_id = ? AND (c.assigned_to = ? OR c.assigned_to IS NULL)
	`, clientID, adminID).Scan(&totalCount)
	
	if err != nil {
		log.Printf("Ошибка при получении общего количества чатов: %v", err)
		return nil, 0, err
	}

	// Оптимизированный SQL запрос с JOIN вместо подзапросов
	query := `
		SELECT c.id, c.created_at, c.updated_at, c.status, 
			u.id, u.name, u.email, u.avatar,
			COUNT(CASE WHEN m.sender = 'user' AND m.read = false THEN 1 END) as unread_count,
			last_msg.id, last_msg.content, last_msg.sender, last_msg.timestamp
		FROM chats c
		JOIN users u ON c.user_id = u.id
		LEFT JOIN messages m ON m.chat_id = c.id
		LEFT JOIN (
			SELECT m1.chat_id, m1.id, m1.content, m1.sender, m1.timestamp
			FROM messages m1
			JOIN (
				SELECT chat_id, MAX(timestamp) as max_time 
				FROM messages 
				GROUP BY chat_id
			) m2 ON m1.chat_id = m2.chat_id AND m1.timestamp = m2.max_time
		) last_msg ON c.id = last_msg.chat_id
		WHERE c.client_id = ? AND (c.assigned_to = ? OR c.assigned_to IS NULL)
		GROUP BY c.id
		ORDER BY c.updated_at DESC
		LIMIT ? OFFSET ?
	`
	
	rows, err := DB.Query(query, clientID, adminID, pageSize, offset)
	if err != nil {
		log.Printf("Ошибка SQL запроса GetChats: %v", err)
		return nil, 0, err
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
			return nil, 0, err
		}

		user.Avatar = nullStringToPointer(avatarNull)
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
		return nil, 0, err
	}

	log.Printf("Успешно получено %d чатов (всего: %d)", len(chats), totalCount)
	return chats, totalCount, nil
}

// GetChatByID получает чат по ID с его сообщениями с поддержкой пагинации
func GetChatByID(chatID string, page, pageSize int) (*models.Chat, int, error) {
	log.Printf("Получение чата по ID: %s (страница: %d, размер: %d)", chatID, page, pageSize)
	
	// Проверка и установка значений пагинации по умолчанию
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > MaxPageSize {
		pageSize = DefaultPageSize
	}
	
	offset := (page - 1) * pageSize
	
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
		return nil, 0, err
	}

	chat.AssignedTo = nullStringToPointer(assignedToNull)

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
		return nil, 0, err
	}

	user.Avatar = nullStringToPointer(avatarNull)
	chat.User = user

	// Получаем общее количество сообщений для пагинации
	var totalMessages int
	err = DB.QueryRow(`
		SELECT COUNT(*) FROM messages WHERE chat_id = ?
	`, chatID).Scan(&totalMessages)
	if err != nil {
		log.Printf("Ошибка при получении общего количества сообщений: %v", err)
		return nil, 0, err
	}

	// Получаем сообщения чата с пагинацией
	rows, err := DB.Query(`
		SELECT id, content, sender, sender_id, timestamp, read, type, metadata
		FROM messages
		WHERE chat_id = ?
		ORDER BY timestamp ASC
		LIMIT ? OFFSET ?
	`, chatID, pageSize, offset)
	if err != nil {
		log.Printf("Ошибка при получении сообщений чата: %v", err)
		return nil, 0, err
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
			return nil, 0, err
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
		return nil, 0, err
	}

	chat.Messages = messages

	// Последнее сообщение
	// Получаем последнее сообщение отдельным запросом, 
	// чтобы оно было доступно независимо от пагинации
	var lastMessage models.Message
	var lastMessageTypeStr string
	var lastMessageMetadataNull sql.NullString
	var lastMessageTimestamp time.Time

	err = DB.QueryRow(`
		SELECT id, content, sender, sender_id, timestamp, read, type, metadata
		FROM messages
		WHERE chat_id = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`, chatID).Scan(
		&lastMessage.ID, &lastMessage.Content, &lastMessage.Sender, &lastMessage.SenderID,
		&lastMessageTimestamp, &lastMessage.Read, &lastMessageTypeStr, &lastMessageMetadataNull,
	)
	
	if err != nil && err != sql.ErrNoRows {
		log.Printf("Ошибка при получении последнего сообщения: %v", err)
		return nil, 0, err
	}
	
	if err != sql.ErrNoRows {
		lastMessage.ChatID = chatID
		lastMessage.Timestamp = lastMessageTimestamp
		lastMessage.Type = lastMessageTypeStr
		
		if lastMessageMetadataNull.Valid && lastMessageMetadataNull.String != "" {
			var metadata map[string]interface{}
			if err := json.Unmarshal([]byte(lastMessageMetadataNull.String), &metadata); err == nil {
				lastMessage.Metadata = metadata
			}
		}
		
		chat.LastMessage = &lastMessage
	}

	log.Printf("Успешно получен чат с %d сообщениями (всего: %d)", len(messages), totalMessages)
	return &chat, totalMessages, nil
}

// AddMessage добавляет новое сообщение в чат с использованием транзакции
func AddMessage(chatID, content, sender, senderID, messageType string, metadata map[string]interface{}) (*models.Message, error) {
	log.Printf("Добавление сообщения в чат %s от %s", chatID, sender)
	
	// Начинаем транзакцию
	tx, err := DB.Begin()
	if err != nil {
		log.Printf("Ошибка при начале транзакции: %v", err)
		return nil, err
	}
	
	// Функция для отката транзакции в случае ошибки
	defer func() {
		if err != nil {
			tx.Rollback()
			log.Printf("Транзакция отменена: %v", err)
		}
	}()
	
	// Проверяем, существует ли чат
	var exists bool
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM chats WHERE id = ?)", chatID).Scan(&exists)
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

	// Сохраняем сообщение в базу
	_, err = tx.Exec(`
		INSERT INTO messages (id, chat_id, content, sender, sender_id, timestamp, read, type, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, messageID, chatID, content, sender, senderID, now, false, messageType, metadataJSON)
	if err != nil {
		log.Printf("Ошибка при добавлении сообщения в БД: %v", err)
		return nil, err
	}

	// Обновляем время последнего обновления чата
	_, err = tx.Exec("UPDATE chats SET updated_at = ? WHERE id = ?", now, chatID)
	if err != nil {
		log.Printf("Ошибка при обновлении времени чата: %v", err)
		return nil, err
	}

	// Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		log.Printf("Ошибка при фиксации транзакции: %v", err)
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

// CreateOrGetChat создает новый чат или находит существующий с использованием транзакции
func CreateOrGetChat(userID, userName, userEmail, source, sourceID, botID, clientID string) (*models.Chat, *models.Message, error) {
	log.Printf("Создание/получение чата для пользователя %s (источник: %s)", userID, source)
	
	// Начинаем транзакцию
	tx, err := DB.Begin()
	if err != nil {
		log.Printf("Ошибка при начале транзакции: %v", err)
		return nil, nil, err
	}
	
	// Функция для отката транзакции в случае ошибки
	defer func() {
		if err != nil {
			tx.Rollback()
			log.Printf("Транзакция отменена: %v", err)
		}
	}()
	
	var user models.User
	var userExists bool

	// Проверяем, существует ли пользователь
	err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE source = ? AND source_id = ?)", source, sourceID).Scan(&userExists)
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

		_, err = tx.Exec(`
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
		err = tx.QueryRow(`
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

		user.Avatar = nullStringToPointer(avatarNull)
		log.Printf("Найден существующий пользователь с ID: %s", user.ID)
	}

	// Проверяем, существует ли чат для этого пользователя
	var chatID string
	var chatExists bool

	err = tx.QueryRow(`
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
		err = tx.QueryRow(`
			SELECT id FROM chats
			WHERE user_id = ? AND source = ? AND bot_id = ?
		`, user.ID, source, botID).Scan(&chatID)
		if err != nil {
			log.Printf("Ошибка при получении ID существующего чата: %v", err)
			return nil, nil, err
		}

		// Обновляем статус чата на активный
		_, err = tx.Exec("UPDATE chats SET status = 'active', updated_at = ? WHERE id = ?", now, chatID)
		if err != nil {
			log.Printf("Ошибка при обновлении статуса чата: %v", err)
			return nil, nil, err
		}
		
		// Фиксируем транзакцию
		err = tx.Commit()
		if err != nil {
			log.Printf("Ошибка при фиксации транзакции: %v", err)
			return nil, nil, err
		}

		// Получаем полную информацию о чате
		// Используем 1 в качестве параметров пагинации, чтобы получить первую страницу сообщений
		chat, _, err := GetChatByID(chatID, 1, DefaultPageSize)
		if err != nil {
			log.Printf("Ошибка при получении полной информации о чате: %v", err)
			return nil, nil, err
		}
		log.Printf("Найден существующий чат с ID: %s", chatID)

		return chat, nil, nil
	}

	// Создаем новый чат
	chatID = uuid.New().String()
	_, err = tx.Exec(`
		INSERT INTO chats (id, user_id, created_at, updated_at, status, source, bot_id, client_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, chatID, user.ID, now, now, "active", source, botID, clientID)
	if err != nil {
		log.Printf("Ошибка при создании нового чата: %v", err)
		return nil, nil, err
	}
	
	// Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		log.Printf("Ошибка при фиксации транзакции: %v", err)
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
	
	// Начинаем транзакцию
	tx, err := DB.Begin()
	if err != nil {
		log.Printf("Ошибка при начале транзакции: %v", err)
		return err
	}
	
	// Функция для отката транзакции в случае ошибки
	defer func() {
		if err != nil {
			tx.Rollback()
			log.Printf("Транзакция отменена: %v", err)
		}
	}()
	
	result, err := tx.Exec(`
		UPDATE messages 
		SET read = true 
		WHERE chat_id = ? AND sender = 'user' AND read = false
	`, chatID)
	if err != nil {
		log.Printf("Ошибка при отметке сообщений как прочитанные: %v", err)
		return err
	}
	
	// Фиксируем транзакцию
	err = tx.Commit()
	if err != nil {
		log.Printf("Ошибка при фиксации транзакции: %v", err)
		return err
	}
	
	count, _ := result.RowsAffected()
	log.Printf("Отмечено как прочитанные %d сообщений", count)
	return nil
}