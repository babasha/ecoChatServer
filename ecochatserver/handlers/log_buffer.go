package handlers

import (
	"log"
	"sync"
	"time"
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
	if logWriteQueue != nil {
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

// Глобальные буферы для разных типов логов
var (
	ServerLogsBuffer    *LogBuffer
	WebSocketLogsBuffer *LogBuffer
)

// InitLogBuffers инициализирует буферы логов
func InitLogBuffers() {
	// Инициализируем подключение к БД логов
	if err := InitLogsDB(); err != nil {
		log.Printf("[LOGS] WARNING: Failed to initialize logs DB: %v", err)
	}

	ServerLogsBuffer = NewLogBuffer(1000)    // Храним последние 1000 серверных логов
	WebSocketLogsBuffer = NewLogBuffer(1000) // Храним последние 1000 WebSocket логов

	// Инициализируем очередь для batch записи в БД
	logWriteQueue = make(chan logQueueItem, 1000) // Буфер на 1000 логов
	logBatchTicker = time.NewTicker(2 * time.Second) // Flush каждые 2 секунды

	// Запускаем воркер для batch записи логов
	go logBatchWriter()

	// Запускаем scheduler для автоочистки старых логов
	StartLogCleanupScheduler()

	// Добавляем приветственные логи
	ServerLogsBuffer.AddLog(LogLevelInfo, "Server log buffer initialized", "LogsHandler")
	WebSocketLogsBuffer.AddLog(LogLevelInfo, "WebSocket log buffer initialized", "LogsHandler")
}
