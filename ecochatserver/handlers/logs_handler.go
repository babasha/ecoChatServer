package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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
	entries []LogEntry
	maxSize int
	mu      sync.RWMutex
}

// NewLogBuffer создает новый буфер логов
func NewLogBuffer(maxSize int) *LogBuffer {
	return &LogBuffer{
		entries: make([]LogEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// AddLog добавляет новую запись в буфер
func (lb *LogBuffer) AddLog(level LogLevel, message, source string) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

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
)

// InitLogBuffers инициализирует буферы логов
func InitLogBuffers() {
	ServerLogsBuffer = NewLogBuffer(1000)    // Храним последние 1000 серверных логов
	WebSocketLogsBuffer = NewLogBuffer(1000) // Храним последние 1000 WebSocket логов

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
