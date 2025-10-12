package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LogLevel определяет уровень логирования
type LogLevel string

const (
	LogLevelInfo    LogLevel = "info"
	LogLevelWarning LogLevel = "warning"
	LogLevelError   LogLevel = "error"
	LogLevelDebug   LogLevel = "debug"
)

// LogEntry представляет одну запись лога
type LogEntry struct {
	Timestamp string   `json:"timestamp"`
	Level     LogLevel `json:"level"`
	Message   string   `json:"message"`
	Source    string   `json:"source,omitempty"`
}

// LogBuffer хранит логи в памяти с ограничением по размеру
type LogBuffer struct {
	entries     []LogEntry
	maxSize     int
	mu          sync.RWMutex
	subscribers map[string]chan LogEntry // Подписчики на real-time логи
	subMu       sync.RWMutex             // Мьютекс для подписчиков
}

// NewLogBuffer создает новый буфер логов
func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		entries:     make([]LogEntry, 0, maxSize),
		maxSize:     maxSize,
		subscribers: make(map[string]chan LogEntry),
	}
}

// AddLog добавляет новую запись в буфер
func (lb *LogBuffer) AddLog(level LogLevel, message, source string) {
	lb.AddLogWithMetadata(level, message, source, nil)
}

// AddLogWithMetadata добавляет новую запись в буфер с дополнительными метаданными
func (lb *LogBuffer) AddLogWithMetadata(level LogLevel, message, source string, metadata map[string]interface{}) {
	lb.mu.Lock()
	entry := LogEntry{
		Timestamp: time.Now().Format(time.RFC3339),
		Level:     level,
		Message:   message,
		Source:    source,
	}

	// Если буфер заполнен, удаляем самую старую запись
	if len(lb.entries) >= lb.maxSize {
		lb.entries = lb.entries[1:]
	}

	lb.entries = append(lb.entries, entry)
	lb.mu.Unlock()

	// Отправляем лог всем подписчикам (не блокируя основной буфер)
	lb.subMu.RLock()
	for _, ch := range lb.subscribers {
		select {
		case ch <- entry:
		default:
			// Если канал заполнен, пропускаем (не блокируем)
		}
	}
	lb.subMu.RUnlock()

	// Добавляем в очередь для batch записи в БД (не блокируем)
	if logWriteQueue != nil && database.DB != nil {
		select {
		case logWriteQueue <- logQueueItem{entry: entry, source: source, metadata: metadata}:
		default:
			// Очередь заполнена, пропускаем (это нормально при очень высокой нагрузке)
		}
	}
}

// GetLogs возвращает все логи
func (lb *LogBuffer) GetLogs() []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	// Возвращаем копию, чтобы избежать race conditions
	logs := make([]LogEntry, len(lb.entries))
	copy(logs, lb.entries)
	return logs
}

// GetLastN возвращает последние N логов
func (lb *LogBuffer) GetLastN(n int) []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if n >= len(lb.entries) {
		logs := make([]LogEntry, len(lb.entries))
		copy(logs, lb.entries)
		return logs
	}

	logs := make([]LogEntry, n)
	copy(logs, lb.entries[len(lb.entries)-n:])
	return logs
}

// Clear очищает буфер логов
func (lb *LogBuffer) Clear() {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.entries = make([]LogEntry, 0, lb.maxSize)
}

// Глобальные буферы для разных типов логов
var (
	ServerLogsBuffer    *LogBuffer
	WebSocketLogsBuffer *LogBuffer
	logWriteQueue       chan logQueueItem
	logBatchTicker      *time.Ticker
)

// logQueueItem представляет элемент очереди для записи логов
type logQueueItem struct {
	entry    LogEntry
	source   string
	metadata map[string]interface{}
}

// InitLogBuffers инициализирует буферы логов
func InitLogBuffers() {
	ServerLogsBuffer = NewLogBuffer(1000)    // Храним последние 1000 серверных логов
	WebSocketLogsBuffer = NewLogBuffer(1000) // Храним последние 1000 WebSocket логов

	// Инициализируем очередь для batch записи в БД
	logWriteQueue = make(chan logQueueItem, 1000) // Буфер на 1000 логов
	logBatchTicker = time.NewTicker(2 * time.Second) // Flush каждые 2 секунды

	// Запускаем воркер для batch записи логов
	go logBatchWriter()

	// Добавляем приветственные логи
	ServerLogsBuffer.AddLog(LogLevelInfo, "Server log buffer initialized", "LogsHandler")
	WebSocketLogsBuffer.AddLog(LogLevelInfo, "WebSocket log buffer initialized", "LogsHandler")
}

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

// Subscribe подписывает клиента на real-time логи
func (lb *LogBuffer) Subscribe(subscriberID string) chan LogEntry {
	lb.subMu.Lock()
	defer lb.subMu.Unlock()

	ch := make(chan LogEntry, 100) // Буфер на 100 логов
	lb.subscribers[subscriberID] = ch
	return ch
}

// Unsubscribe отписывает клиента от логов
func (lb *LogBuffer) Unsubscribe(subscriberID string) {
	lb.subMu.Lock()
	defer lb.subMu.Unlock()

	if ch, exists := lb.subscribers[subscriberID]; exists {
		close(ch)
		delete(lb.subscribers, subscriberID)
	}
}

// logBatchWriter обрабатывает batch запись логов в БД
func logBatchWriter() {
	const batchSize = 100 // Максимальный размер batch
	serverBatch := make([]logQueueItem, 0, batchSize)
	wsBatch := make([]logQueueItem, 0, batchSize)

	flushBatches := func() {
		if len(serverBatch) > 0 {
			if err := batchInsertServerLogs(serverBatch); err != nil {
				log.Printf("Error batch inserting server logs: %v", err)
			}
			serverBatch = serverBatch[:0] // Очищаем, сохраняя capacity
		}

		if len(wsBatch) > 0 {
			if err := batchInsertWebSocketLogs(wsBatch); err != nil {
				log.Printf("Error batch inserting websocket logs: %v", err)
			}
			wsBatch = wsBatch[:0]
		}
	}

	for {
		select {
		case item := <-logWriteQueue:
			// Определяем тип лога
			isWebSocketLog := item.source == "WebSocket" || item.source == "Connection" || item.source == "Message"

			if isWebSocketLog {
				wsBatch = append(wsBatch, item)
				if len(wsBatch) >= batchSize {
					flushBatches()
				}
			} else {
				serverBatch = append(serverBatch, item)
				if len(serverBatch) >= batchSize {
					flushBatches()
				}
			}

		case <-logBatchTicker.C:
			// Периодический flush
			flushBatches()
		}
	}
}

// batchInsertServerLogs выполняет batch insert серверных логов
func batchInsertServerLogs(batch []logQueueItem) error {
	if database.DB == nil || len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Строим bulk insert
	valueStrings := make([]string, 0, len(batch))
	valueArgs := make([]interface{}, 0, len(batch)*5)

	for i, item := range batch {
		timestamp, err := time.Parse(time.RFC3339, item.entry.Timestamp)
		if err != nil {
			timestamp = time.Now()
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d)",
			i*5+1, i*5+2, i*5+3, i*5+4, i*5+5))
		valueArgs = append(valueArgs, timestamp, string(item.entry.Level), item.entry.Message, item.source, item.metadata)
	}

	query := fmt.Sprintf("INSERT INTO server_logs (timestamp, level, message, source, metadata) VALUES %s",
		strings.Join(valueStrings, ","))

	_, err := database.DB.ExecContext(ctx, query, valueArgs...)
	return err
}

// batchInsertWebSocketLogs выполняет batch insert WebSocket логов
func batchInsertWebSocketLogs(batch []logQueueItem) error {
	if database.DB == nil || len(batch) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	valueStrings := make([]string, 0, len(batch))
	valueArgs := make([]interface{}, 0, len(batch)*7)

	for i, item := range batch {
		timestamp, err := time.Parse(time.RFC3339, item.entry.Timestamp)
		if err != nil {
			timestamp = time.Now()
		}

		// Извлекаем client_id и client_type из metadata если есть
		var clientID *uuid.UUID
		var clientType *string

		if item.metadata != nil {
			if cidStr, ok := item.metadata["client_id"].(string); ok {
				if cid, err := uuid.Parse(cidStr); err == nil {
					clientID = &cid
				}
			}
			if ctStr, ok := item.metadata["client_type"].(string); ok {
				clientType = &ctStr
			}
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			i*7+1, i*7+2, i*7+3, i*7+4, i*7+5, i*7+6, i*7+7))
		valueArgs = append(valueArgs, timestamp, string(item.entry.Level), item.entry.Message, item.source, clientID, clientType, item.metadata)
	}

	query := fmt.Sprintf("INSERT INTO websocket_logs (timestamp, level, message, source, client_id, client_type, metadata) VALUES %s",
		strings.Join(valueStrings, ","))

	_, err := database.DB.ExecContext(ctx, query, valueArgs...)
	return err
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

// CleanupOldLogs запускает очистку старых логов (вызывается периодически)
func CleanupOldLogs() error {
	if database.DB == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := database.DB.ExecContext(ctx, "SELECT cleanup_old_logs()")
	if err != nil {
		log.Printf("Error cleaning up old logs: %v", err)
		return err
	}

	log.Println("Old logs cleaned up successfully")
	return nil
}

// DBLogEntry представляет запись лога из БД
type DBLogEntry struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Source    string                 `json:"source"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	ClientID  *string                `json:"client_id,omitempty"`
	ClientType *string               `json:"client_type,omitempty"`
}

// buildServerLogsFilter строит WHERE условия для запросов логов
func buildServerLogsFilter(level, source, startTime, endTime string) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	argIndex := 1

	if level != "" {
		where += fmt.Sprintf(" AND level = $%d", argIndex)
		args = append(args, level)
		argIndex++
	}
	if source != "" {
		where += fmt.Sprintf(" AND source = $%d", argIndex)
		args = append(args, source)
		argIndex++
	}
	if startTime != "" {
		where += fmt.Sprintf(" AND timestamp >= $%d", argIndex)
		args = append(args, startTime)
		argIndex++
	}
	if endTime != "" {
		where += fmt.Sprintf(" AND timestamp <= $%d", argIndex)
		args = append(args, endTime)
		argIndex++
	}
	return where, args
}

// GetServerLogsFromDB возвращает серверные логи из БД с пагинацией и фильтрами
func GetServerLogsFromDB(c *gin.Context) {
	if database.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not available"})
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
	where, args := buildServerLogsFilter(level, source, startTime, endTime)

	// Основной запрос
	query := "SELECT id, timestamp, level, message, source, metadata FROM server_logs WHERE 1=1" + where + " ORDER BY timestamp DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limitInt, (pageInt-1)*limitInt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := database.DB.QueryContext(ctx, query, args...)
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
	countWhere, countArgs := buildServerLogsFilter(level, source, startTime, endTime)
	database.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM server_logs WHERE 1=1"+countWhere, countArgs...).Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"total": total,
		"page":  pageInt,
		"limit": limitInt,
	})
}

// buildWebSocketLogsFilter строит WHERE условия для запросов WebSocket логов
func buildWebSocketLogsFilter(level, clientType string) (string, []interface{}) {
	where := ""
	args := []interface{}{}
	argIndex := 1

	if level != "" {
		where += fmt.Sprintf(" AND level = $%d", argIndex)
		args = append(args, level)
		argIndex++
	}
	if clientType != "" {
		where += fmt.Sprintf(" AND client_type = $%d", argIndex)
		args = append(args, clientType)
		argIndex++
	}
	return where, args
}

// GetWebSocketLogsFromDB возвращает WebSocket логи из БД
func GetWebSocketLogsFromDB(c *gin.Context) {
	if database.DB == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not available"})
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
	where, args := buildWebSocketLogsFilter(level, clientType)

	// Основной запрос
	query := "SELECT id, timestamp, level, message, source, client_id, client_type, metadata FROM websocket_logs WHERE 1=1" + where + " ORDER BY timestamp DESC LIMIT $" + fmt.Sprint(len(args)+1) + " OFFSET $" + fmt.Sprint(len(args)+2)
	args = append(args, limitInt, (pageInt-1)*limitInt)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := database.DB.QueryContext(ctx, query, args...)
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
	countWhere, countArgs := buildWebSocketLogsFilter(level, clientType)
	database.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM websocket_logs WHERE 1=1"+countWhere, countArgs...).Scan(&total)

	c.JSON(http.StatusOK, gin.H{
		"logs":  logs,
		"count": len(logs),
		"total": total,
		"page":  pageInt,
		"limit": limitInt,
	})
}
