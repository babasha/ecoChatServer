package handlers

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ResolveChat помечает чат как решенный и архивирует его
func ResolveChat(c *gin.Context) {
	chatID := c.Param("id")
	db := database.DB

	// Получаем чат с сообщениями
	var chat models.Chat
	var clientID, userID string
	err := db.QueryRow(`
		SELECT id, client_id, user_id, status, is_archived
		FROM chats WHERE id = $1
	`, chatID).Scan(&chat.ID, &clientID, &userID, &chat.Status, &chat.IsArchived)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}
	if err != nil {
		log.Printf("Error fetching chat: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if chat.IsArchived {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Chat is already archived"})
		return
	}

	// Получаем все сообщения чата
	rows, err := db.Query(`
		SELECT id, content, sender, sender_id, timestamp, read, read_at, type, metadata, chat_id
		FROM messages WHERE chat_id = $1 ORDER BY timestamp ASC
	`, chatID)
	if err != nil {
		log.Printf("Error fetching messages: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	messages := []models.Message{}
	for rows.Next() {
		var msg models.Message
		var metadata sql.NullString
		var readAt sql.NullTime
		var senderID sql.NullString

		err := rows.Scan(&msg.ID, &msg.Content, &msg.Sender, &senderID,
			&msg.Timestamp, &msg.Read, &readAt, &msg.Type, &metadata, &msg.ChatID)
		if err != nil {
			log.Printf("Error scanning message: %v", err)
			continue
		}

		if senderID.Valid {
			if id, err := uuid.Parse(senderID.String); err == nil {
				msg.SenderID = id
			}
		}
		if readAt.Valid {
			msg.ReadAt = &readAt.Time
		}
		if metadata.Valid {
			json.Unmarshal([]byte(metadata.String), &msg.Metadata)
		}

		messages = append(messages, msg)
	}

	// Начинаем транзакцию
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer tx.Rollback()

	now := time.Now()
	deleteAt := now.AddDate(0, 1, 0) // Удалить через месяц

	// Сохраняем сообщения в JSON
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		log.Printf("Error marshaling messages: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
		return
	}

	// Создаем архивную запись
	_, err = tx.Exec(`
		INSERT INTO archived_chats (chat_id, client_id, user_id, archived_at, delete_at, messages)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, chatID, clientID, userID, now, deleteAt, messagesJSON)
	if err != nil {
		log.Printf("Error creating archived chat: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	// Помечаем чат как решенный и архивированный
	_, err = tx.Exec(`
		UPDATE chats
		SET resolved_at = $1, is_archived = true, status = 'closed'
		WHERE id = $2
	`, now, chatID)
	if err != nil {
		log.Printf("Error updating chat: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	if err = tx.Commit(); err != nil {
		log.Printf("Error committing transaction: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"message":  "Chat resolved and archived",
		"deleteAt": deleteAt,
	})
}

// GetArchivedChats получает список архивных чатов (для всех клиентов или конкретного)
func GetArchivedChats(c *gin.Context) {
	clientID := c.Query("clientId")
	db := database.DB

	var rows *sql.Rows
	var err error

	// Если clientId не указан, показываем все архивы
	if clientID == "" {
		rows, err = db.Query(`
			SELECT ac.id, ac.chat_id, ac.archived_at, ac.delete_at,
				   jsonb_array_length(ac.messages) as message_count,
				   u.name, u.email
			FROM archived_chats ac
			JOIN users u ON ac.user_id = u.id
			ORDER BY ac.archived_at DESC
		`)
	} else {
		rows, err = db.Query(`
			SELECT ac.id, ac.chat_id, ac.archived_at, ac.delete_at,
				   jsonb_array_length(ac.messages) as message_count,
				   u.name, u.email
			FROM archived_chats ac
			JOIN users u ON ac.user_id = u.id
			WHERE ac.client_id = $1
			ORDER BY ac.archived_at DESC
		`, clientID)
	}

	if err != nil {
		log.Printf("Error fetching archived chats: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	defer rows.Close()

	summaries := []models.ArchivedChatSummary{}
	for rows.Next() {
		var s models.ArchivedChatSummary
		err := rows.Scan(&s.ID, &s.ChatID, &s.ArchivedAt, &s.DeleteAt, &s.MessageCount, &s.UserName, &s.UserEmail)
		if err != nil {
			log.Printf("Error scanning archived chat: %v", err)
			continue
		}
		summaries = append(summaries, s)
	}

	c.JSON(http.StatusOK, summaries)
}

// GetArchivedChatMessages получает сообщения из архивного чата
func GetArchivedChatMessages(c *gin.Context) {
	archivedChatID := c.Param("id")
	db := database.DB

	var messagesJSON []byte
	var deleteAt time.Time
	err := db.QueryRow(`
		SELECT messages, delete_at FROM archived_chats WHERE id = $1
	`, archivedChatID).Scan(&messagesJSON, &deleteAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Archived chat not found"})
		return
	}
	if err != nil {
		log.Printf("Error fetching archived chat: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	var messages []models.Message
	if err := json.Unmarshal(messagesJSON, &messages); err != nil {
		log.Printf("Error unmarshaling messages: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": messages,
		"deleteAt": deleteAt,
	})
}

// ToggleArchiveTimer приостанавливает/возобновляет таймер удаления
func ToggleArchiveTimer(c *gin.Context) {
	archivedChatID := c.Param("id")
	db := database.DB

	var paused bool
	err := db.QueryRow(`
		UPDATE archived_chats
		SET delete_timer_paused = NOT delete_timer_paused
		WHERE id = $1
		RETURNING delete_timer_paused
	`, archivedChatID).Scan(&paused)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "Archived chat not found"})
		return
	}
	if err != nil {
		log.Printf("Error toggling timer: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"paused":  paused,
	})
}

// AutoArchiveInactiveChats автоматически архивирует неактивные чаты
// Вызывается периодически из main.go
func AutoArchiveInactiveChats() {
	db := database.DB

	// Архивируем чаты, которые:
	// 1. Не обновлялись более 12 часов
	// 2. Не архивированы
	// 3. Не имеют приостановленного таймера
	// Ограничиваем обработку 100 чатами за раз для безопасности
	inactiveThreshold := time.Now().Add(-12 * time.Hour)

	rows, err := db.Query(`
		SELECT id, client_id, user_id
		FROM chats
		WHERE updated_at < $1
			AND is_archived = false
			AND archive_timer_paused = false
		ORDER BY updated_at ASC
		LIMIT 100
	`, inactiveThreshold)

	if err != nil {
		log.Printf("Error fetching inactive chats: %v", err)
		return
	}
	defer rows.Close()

	archivedCount := 0
	for rows.Next() {
		var chatID, clientID, userID string
		if err := rows.Scan(&chatID, &clientID, &userID); err != nil {
			log.Printf("Error scanning chat: %v", err)
			continue
		}

		// Получаем сообщения чата
		msgRows, err := db.Query(`
			SELECT id, content, sender, sender_id, timestamp, read, read_at, type, metadata, chat_id
			FROM messages WHERE chat_id = $1 ORDER BY timestamp ASC
		`, chatID)
		if err != nil {
			log.Printf("Error fetching messages for chat %s: %v", chatID, err)
			continue
		}

		messages := []models.Message{}
		for msgRows.Next() {
			var msg models.Message
			var metadata sql.NullString
			var readAt sql.NullTime
			var senderID sql.NullString

			err := msgRows.Scan(&msg.ID, &msg.Content, &msg.Sender, &senderID,
				&msg.Timestamp, &msg.Read, &readAt, &msg.Type, &metadata, &msg.ChatID)
			if err != nil {
				log.Printf("Error scanning message: %v", err)
				continue
			}

			if senderID.Valid {
				if id, err := uuid.Parse(senderID.String); err == nil {
					msg.SenderID = id
				}
			}
			if readAt.Valid {
				msg.ReadAt = &readAt.Time
			}
			if metadata.Valid {
				json.Unmarshal([]byte(metadata.String), &msg.Metadata)
			}

			messages = append(messages, msg)
		}
		msgRows.Close()

		// Пропускаем чаты без сообщений
		if len(messages) == 0 {
			log.Printf("Skipping chat %s - no messages to archive", chatID)
			continue
		}

		// Начинаем транзакцию
		tx, err := db.Begin()
		if err != nil {
			log.Printf("Error starting transaction for chat %s: %v", chatID, err)
			continue
		}

		now := time.Now()
		deleteAt := now.AddDate(0, 1, 0) // Удалить через месяц

		// Сохраняем сообщения в JSON
		messagesJSON, err := json.Marshal(messages)
		if err != nil {
			log.Printf("Error marshaling messages for chat %s: %v", chatID, err)
			tx.Rollback()
			continue
		}

		// Создаем архивную запись
		_, err = tx.Exec(`
			INSERT INTO archived_chats (chat_id, client_id, user_id, archived_at, delete_at, messages)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, chatID, clientID, userID, now, deleteAt, messagesJSON)
		if err != nil {
			log.Printf("Error creating archived chat for %s: %v", chatID, err)
			tx.Rollback()
			continue
		}

		// Помечаем чат как архивированный
		_, err = tx.Exec(`
			UPDATE chats
			SET is_archived = true, status = 'closed', resolved_at = $1
			WHERE id = $2
		`, now, chatID)
		if err != nil {
			log.Printf("Error updating chat %s: %v", chatID, err)
			tx.Rollback()
			continue
		}

		if err = tx.Commit(); err != nil {
			log.Printf("Error committing transaction for chat %s: %v", chatID, err)
			continue
		}

		archivedCount++
	}

	if archivedCount > 0 {
		log.Printf("Auto-archived %d inactive chats", archivedCount)
	}
}

// DeleteExpiredArchives удаляет архивы старше месяца (если таймер не приостановлен)
func DeleteExpiredArchives() {
	db := database.DB

	// Начинаем транзакцию для атомарного удаления
	tx, err := db.Begin()
	if err != nil {
		log.Printf("Error starting transaction for archive deletion: %v", err)
		return
	}
	defer tx.Rollback()

	// Получаем ID чатов для удаления
	rows, err := tx.Query(`
		SELECT chat_id FROM archived_chats
		WHERE delete_at < $1 AND delete_timer_paused = false
	`, time.Now())
	if err != nil {
		log.Printf("Error fetching expired archives: %v", err)
		return
	}
	defer rows.Close()

	chatIDs := []string{}
	for rows.Next() {
		var chatID string
		if err := rows.Scan(&chatID); err != nil {
			log.Printf("Error scanning chat_id: %v", err)
			continue
		}
		chatIDs = append(chatIDs, chatID)
	}
	rows.Close()

	if len(chatIDs) == 0 {
		return // Нет чатов для удаления
	}

	// Удаляем архивы
	result, err := tx.Exec(`
		DELETE FROM archived_chats
		WHERE delete_at < $1 AND delete_timer_paused = false
	`, time.Now())
	if err != nil {
		log.Printf("Error deleting expired archives: %v", err)
		return
	}

	// Удаляем сообщения архивированных чатов
	_, err = tx.Exec(`
		DELETE FROM messages
		WHERE chat_id = ANY($1::uuid[])
	`, "{"+strings.Join(chatIDs, ",")+"}")
	if err != nil {
		log.Printf("Error deleting messages of archived chats: %v", err)
		return
	}

	// Удаляем соответствующие чаты
	_, err = tx.Exec(`
		DELETE FROM chats
		WHERE id = ANY($1::uuid[])
	`, "{"+strings.Join(chatIDs, ",")+"}")
	if err != nil {
		log.Printf("Error deleting archived chats: %v", err)
		return
	}

	if err = tx.Commit(); err != nil {
		log.Printf("Error committing archive deletion: %v", err)
		return
	}

	if count, err := result.RowsAffected(); err == nil && count > 0 {
		log.Printf("Deleted %d expired archives and their chats", count)
	}
}
