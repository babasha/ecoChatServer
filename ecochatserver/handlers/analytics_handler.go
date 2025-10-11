package handlers

import (
	"log"
	"net/http"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
)

// AnalyticsData представляет аналитические данные из базы данных
type AnalyticsData struct {
	TotalMessages            int                    `json:"totalMessages"`
	MessagesBySender         []MessagesBySenderStat `json:"messagesBySender"`
	AdminStats               []AdminStat            `json:"adminStats"`
	ArchivedChatsCount       int                    `json:"archivedChatsCount"`
	TotalChatsCount          int                    `json:"totalChatsCount"`
	AutoResponderChatsCount  int                    `json:"autoResponderChatsCount"`
	TranslationRequestsCount int                    `json:"translationRequestsCount"`
	ChatsByStatus            []ChatStatusStat       `json:"chatsByStatus"`
	AvgResponseTimeMinutes   *string                `json:"avgResponseTimeMinutes"`
	ReadStats                ReadStatsSummary       `json:"readStats"`
}

// MessagesBySenderStat представляет статистику сообщений по отправителям
type MessagesBySenderStat struct {
	Sender string `json:"sender"`
	Count  int    `json:"count"`
}

// AdminStat представляет статистику по админу
type AdminStat struct {
	AdminID      string `json:"adminId"`
	AdminName    string `json:"adminName"`
	MessageCount int    `json:"messageCount"`
	ChatCount    int    `json:"chatCount"`
}

// ChatStatusStat представляет статистику по статусам чатов
type ChatStatusStat struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// ReadStatsSummary представляет статистику прочитанных сообщений
type ReadStatsSummary struct {
	Read   int `json:"read"`
	Unread int `json:"unread"`
}

// GetAnalytics возвращает аналитические данные из БД
func GetAnalytics(c *gin.Context) {
	analytics := AnalyticsData{}

	// 1. Общее количество сообщений
	var totalMessages int
	err := database.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&totalMessages)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества сообщений: %v", err)
		totalMessages = 0
	}
	analytics.TotalMessages = totalMessages

	// 2. Сообщения по отправителям (user, admin)
	rows, err := database.DB.Query(`
		SELECT sender, COUNT(*) as count
		FROM messages
		GROUP BY sender
	`)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения статистики по отправителям: %v", err)
	} else {
		defer rows.Close()
		messagesBySender := make([]MessagesBySenderStat, 0)
		for rows.Next() {
			var stat MessagesBySenderStat
			if err := rows.Scan(&stat.Sender, &stat.Count); err == nil {
				messagesBySender = append(messagesBySender, stat)
			}
		}
		analytics.MessagesBySender = messagesBySender
	}

	// 3. Статистика по админам
	rows, err = database.DB.Query(`
		SELECT
			u.id as admin_id,
			u.username as admin_name,
			COUNT(DISTINCT m.id) as message_count,
			COUNT(DISTINCT c.id) as chat_count
		FROM users u
		LEFT JOIN messages m ON u.id = m.sender_id AND m.sender = 'admin'
		LEFT JOIN chats c ON u.id = c.admin_id
		WHERE u.role = 'admin'
		GROUP BY u.id, u.username
	`)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения статистики по админам: %v", err)
	} else {
		defer rows.Close()
		adminStats := make([]AdminStat, 0)
		for rows.Next() {
			var stat AdminStat
			if err := rows.Scan(&stat.AdminID, &stat.AdminName, &stat.MessageCount, &stat.ChatCount); err == nil {
				adminStats = append(adminStats, stat)
			}
		}
		analytics.AdminStats = adminStats
	}

	// 4. Количество архивированных чатов
	var archivedCount int
	err = database.DB.QueryRow(`SELECT COUNT(*) FROM chats WHERE archived = true`).Scan(&archivedCount)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества архивированных чатов: %v", err)
		archivedCount = 0
	}
	analytics.ArchivedChatsCount = archivedCount

	// 5. Общее количество чатов
	var totalChats int
	err = database.DB.QueryRow(`SELECT COUNT(*) FROM chats`).Scan(&totalChats)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения общего количества чатов: %v", err)
		totalChats = 0
	}
	analytics.TotalChatsCount = totalChats

	// 6. Количество чатов с автоответчиком
	var autoResponderCount int
	err = database.DB.QueryRow(`SELECT COUNT(*) FROM chats WHERE auto_responder_enabled = true`).Scan(&autoResponderCount)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества чатов с автоответчиком: %v", err)
		autoResponderCount = 0
	}
	analytics.AutoResponderChatsCount = autoResponderCount

	// 7. Количество запросов на перевод (примерная оценка по metadata)
	var translationCount int
	err = database.DB.QueryRow(`
		SELECT COUNT(*)
		FROM messages
		WHERE metadata IS NOT NULL
		AND metadata::text LIKE '%translations%'
	`).Scan(&translationCount)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества переводов: %v", err)
		translationCount = 0
	}
	analytics.TranslationRequestsCount = translationCount

	// 8. Чаты по статусам
	rows, err = database.DB.Query(`
		SELECT
			CASE
				WHEN archived = true THEN 'archived'
				WHEN admin_id IS NULL THEN 'unassigned'
				ELSE 'active'
			END as status,
			COUNT(*) as count
		FROM chats
		GROUP BY status
	`)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения статистики по статусам чатов: %v", err)
	} else {
		defer rows.Close()
		chatsByStatus := make([]ChatStatusStat, 0)
		for rows.Next() {
			var stat ChatStatusStat
			if err := rows.Scan(&stat.Status, &stat.Count); err == nil {
				chatsByStatus = append(chatsByStatus, stat)
			}
		}
		analytics.ChatsByStatus = chatsByStatus
	}

	// 9. Среднее время ответа (в минутах)
	var avgResponseTime *string
	err = database.DB.QueryRow(`
		SELECT
			ROUND(AVG(EXTRACT(EPOCH FROM (admin_msg.created_at - user_msg.created_at)) / 60)::numeric, 1)::text
		FROM messages user_msg
		JOIN messages admin_msg ON user_msg.chat_id = admin_msg.chat_id
		WHERE user_msg.sender = 'user'
		AND admin_msg.sender = 'admin'
		AND admin_msg.created_at > user_msg.created_at
		AND admin_msg.created_at = (
			SELECT MIN(created_at)
			FROM messages
			WHERE chat_id = user_msg.chat_id
			AND sender = 'admin'
			AND created_at > user_msg.created_at
		)
	`).Scan(&avgResponseTime)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения среднего времени ответа: %v", err)
		avgResponseTime = nil
	}
	analytics.AvgResponseTimeMinutes = avgResponseTime

	// 10. Статистика прочитанных/непрочитанных сообщений
	var readCount, unreadCount int
	err = database.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE read = true`).Scan(&readCount)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества прочитанных сообщений: %v", err)
		readCount = 0
	}
	err = database.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE read = false`).Scan(&unreadCount)
	if err != nil {
		log.Printf("GetAnalytics: ошибка получения количества непрочитанных сообщений: %v", err)
		unreadCount = 0
	}
	analytics.ReadStats = ReadStatsSummary{
		Read:   readCount,
		Unread: unreadCount,
	}

	c.JSON(http.StatusOK, gin.H{
		"analytics": analytics,
	})
}
