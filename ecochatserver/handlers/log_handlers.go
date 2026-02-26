package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetServerLogs возвращает серверные логи
func GetServerLogs(c *gin.Context) {
	if ServerLogsBuffer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Log buffer not initialized",
			"logs":  []LogEntry{},
		})
		return
	}

	logs := ServerLogsBuffer.GetLogs()

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"type":  "server",
	})
}

// GetWebSocketLogs возвращает WebSocket логи
func GetWebSocketLogs(c *gin.Context) {
	if WebSocketLogsBuffer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Log buffer not initialized",
			"logs":  []LogEntry{},
		})
		return
	}

	logs := WebSocketLogsBuffer.GetLogs()

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"type":  "websocket",
	})
}

// AddServerLog добавляет серверный лог (хелпер функция для использования в других частях кода)
func AddServerLog(level LogLevel, message, source string) {
	if ServerLogsBuffer != nil {
		ServerLogsBuffer.AddLog(level, message, source)
	}
}

// AddWebSocketLog добавляет WebSocket лог (хелпер функция для использования в других частях кода)
func AddWebSocketLog(level LogLevel, message, source string) {
	if WebSocketLogsBuffer != nil {
		WebSocketLogsBuffer.AddLog(level, message, source)
	}
}

// AddWebSocketLogWithClient добавляет WebSocket лог с информацией о клиенте
func AddWebSocketLogWithClient(level LogLevel, message, source string, clientID *uuid.UUID, clientType string, metadata map[string]interface{}) {
	// Добавляем client_id и client_type в metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	if clientID != nil {
		metadata["client_id"] = clientID.String()
	}
	if clientType != "" {
		metadata["client_type"] = clientType
	}

	// Используем общую систему логирования с batch записью
	if WebSocketLogsBuffer != nil {
		WebSocketLogsBuffer.AddLogWithMetadata(level, message, source, metadata)
	}
}

// DBLogEntry представляет запись лога из БД
type DBLogEntry struct {
	ID         string                 `json:"id"`
	Timestamp  time.Time              `json:"timestamp"`
	Level      string                 `json:"level"`
	Message    string                 `json:"message"`
	Source     string                 `json:"source"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	ClientID   *string                `json:"client_id,omitempty"`
	ClientType *string                `json:"client_type,omitempty"`
}

// filterParam описывает один фильтр: SQL условие (напр. "level =") + значение
type filterParam struct {
	expr  string // SQL expression, e.g. "level =", "timestamp >="
	value string
}

// buildFilter строит WHERE условия для запросов логов
func buildFilter(params []filterParam) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	argIndex := 1

	for _, p := range params {
		if p.value != "" {
			where += fmt.Sprintf(" AND %s $%d", p.expr, argIndex)
			args = append(args, p.value)
			argIndex++
		}
	}
	return where, args
}

// GetServerLogsFromDB возвращает серверные логи из БД с пагинацией и фильтрами
func GetServerLogsFromDB(c *gin.Context) {
	if logsDB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Logs database not available"})
		return
	}

	// Парсим параметры
	var pageInt, limitInt int
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &pageInt)
	fmt.Sscanf(c.DefaultQuery("limit", "100"), "%d", &limitInt)

	if pageInt < 1 {
		pageInt = 1
	}
	if limitInt < 1 || limitInt > 1000 {
		limitInt = 100
	}

	level := c.Query("level")
	source := c.Query("source")
	startTime := c.Query("startTime")
	endTime := c.Query("endTime")

	// Строим WHERE условия
	where, args := buildFilter([]filterParam{
		{"level =", level},
		{"source =", source},
		{"timestamp >=", startTime},
		{"timestamp <=", endTime},
	})

	// Основной запрос
	query := "SELECT id, timestamp, level, message, source, metadata FROM server_logs WHERE 1=1" + where + " ORDER BY timestamp DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limitInt, (pageInt-1)*limitInt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := logsDB.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("Error querying server logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching logs"})
		return
	}
	defer rows.Close()

	logs := []DBLogEntry{}
	for rows.Next() {
		var entry DBLogEntry
		var metadataBytes []byte

		if err := rows.Scan(&entry.ID, &entry.Timestamp, &entry.Level, &entry.Message, &entry.Source, &metadataBytes); err != nil {
			log.Printf("Error scanning log entry: %v", err)
			continue
		}

		if len(metadataBytes) > 0 {
			json.Unmarshal(metadataBytes, &entry.Metadata)
		}
		logs = append(logs, entry)
	}

	// COUNT запрос - переиспользуем WHERE условия
	var total int
	countWhere, countArgs := buildFilter([]filterParam{
		{"level =", level},
		{"source =", source},
		{"timestamp >=", startTime},
		{"timestamp <=", endTime},
	})
	logsDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM server_logs WHERE 1=1"+countWhere, countArgs...).Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"total": total,
		"page":  pageInt,
		"limit": limitInt,
	})
}

// GetWebSocketLogsFromDB возвращает WebSocket логи из БД
func GetWebSocketLogsFromDB(c *gin.Context) {
	if logsDB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Logs database not available"})
		return
	}

	// Парсим параметры
	var pageInt, limitInt int
	fmt.Sscanf(c.DefaultQuery("page", "1"), "%d", &pageInt)
	fmt.Sscanf(c.DefaultQuery("limit", "100"), "%d", &limitInt)

	if pageInt < 1 {
		pageInt = 1
	}
	if limitInt < 1 || limitInt > 1000 {
		limitInt = 100
	}

	level := c.Query("level")
	clientType := c.Query("clientType")

	// Строим WHERE условия
	where, args := buildFilter([]filterParam{
		{"level =", level},
		{"client_type =", clientType},
	})

	// Основной запрос
	query := "SELECT id, timestamp, level, message, source, client_id, client_type, metadata FROM websocket_logs WHERE 1=1" + where + " ORDER BY timestamp DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limitInt, (pageInt-1)*limitInt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := logsDB.QueryContext(ctx, query, args...)
	if err != nil {
		log.Printf("Error querying websocket logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error fetching logs"})
		return
	}
	defer rows.Close()

	logs := []DBLogEntry{}
	for rows.Next() {
		var entry DBLogEntry
		var metadataBytes []byte

		if err := rows.Scan(&entry.ID, &entry.Timestamp, &entry.Level, &entry.Message, &entry.Source, &entry.ClientID, &entry.ClientType, &metadataBytes); err != nil {
			log.Printf("Error scanning log entry: %v", err)
			continue
		}

		if len(metadataBytes) > 0 {
			json.Unmarshal(metadataBytes, &entry.Metadata)
		}
		logs = append(logs, entry)
	}

	// COUNT запрос - переиспользуем WHERE условия
	var total int
	countWhere, countArgs := buildFilter([]filterParam{
		{"level =", level},
		{"client_type =", clientType},
	})
	logsDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM websocket_logs WHERE 1=1"+countWhere, countArgs...).Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"total": total,
		"page":  pageInt,
		"limit": limitInt,
	})
}
