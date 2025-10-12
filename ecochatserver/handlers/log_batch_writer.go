package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

// logQueueItem представляет элемент очереди для записи логов
type logQueueItem struct {
	entry    LogEntry
	source   string
	metadata map[string]interface{}
}

var (
	logWriteQueue  chan logQueueItem
	logBatchTicker *time.Ticker
	logsDB         *sql.DB // Отдельное подключение для логов
)

// InitLogsDB инициализирует отдельное подключение к БД для логов
func InitLogsDB() error {
	var connStr string

	// Вариант 1: Используем DATABASE_URL (приоритет)
	// Это удобно для Railway Variable Reference: ${{ Logs.DATABASE_URL }}
	logsDBURL := os.Getenv("LOGS_DB_URL")
	if logsDBURL != "" {
		connStr = logsDBURL
	} else {
		// Вариант 2: Собираем из отдельных переменных
		dbHost := os.Getenv("LOGS_DB_HOST")
		dbPort := os.Getenv("LOGS_DB_PORT")
		dbUser := os.Getenv("LOGS_DB_USER")
		dbPassword := os.Getenv("LOGS_DB_PASSWORD")
		dbName := os.Getenv("LOGS_DB_NAME")

		// Если переменные не установлены, используем основную БД
		if dbHost == "" || dbPassword == "" {
			log.Println("[LOGS_DB] Logs DB credentials not set, using main DB")
			logsDB = database.DB
			return nil
		}

		// Устанавливаем значения по умолчанию
		if dbPort == "" {
			dbPort = "5432"
		}
		if dbUser == "" {
			dbUser = "postgres"
		}
		if dbName == "" {
			dbName = "railway"
		}

		// Определяем SSL mode
		sslMode := "require"
		if os.Getenv("LOGS_DB_SSL_MODE") != "" {
			sslMode = os.Getenv("LOGS_DB_SSL_MODE")
		}

		// Формируем connection string
		connStr = fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			dbHost, dbPort, dbUser, dbPassword, dbName, sslMode)
	}

	// Подключаемся к БД
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Printf("[LOGS_DB] ERROR: Failed to open logs DB connection: %v", err)
		return err
	}

	// Настраиваем пул соединений
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Проверяем подключение
	if err := db.Ping(); err != nil {
		log.Printf("[LOGS_DB] ERROR: Failed to ping logs DB: %v", err)
		return err
	}

	logsDB = db
	log.Printf("[LOGS_DB] ✅ Connected to separate logs DB")
	return nil
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
	if logsDB == nil || len(batch) == 0 {
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

		// Конвертируем metadata в JSON для PostgreSQL JSONB
		var metadataJSON interface{}
		if item.metadata != nil {
			var jsonBytes []byte
			jsonBytes, err = json.Marshal(item.metadata)
			if err != nil {
				log.Printf("[LOGS_DB] Failed to marshal metadata: %v", err)
				metadataJSON = nil
			} else {
				metadataJSON = jsonBytes
			}
		} else {
			metadataJSON = nil
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d)",
			i*5+1, i*5+2, i*5+3, i*5+4, i*5+5))
		valueArgs = append(valueArgs, timestamp, string(item.entry.Level), item.entry.Message, item.source, metadataJSON)
	}

	query := fmt.Sprintf("INSERT INTO server_logs (timestamp, level, message, source, metadata) VALUES %s",
		strings.Join(valueStrings, ","))

	_, err := logsDB.ExecContext(ctx, query, valueArgs...)
	return err
}

// batchInsertWebSocketLogs выполняет batch insert WebSocket логов
func batchInsertWebSocketLogs(batch []logQueueItem) error {
	if logsDB == nil || len(batch) == 0 {
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

		// Конвертируем metadata в JSON для PostgreSQL JSONB
		var metadataJSON interface{}
		if item.metadata != nil {
			var jsonBytes []byte
			jsonBytes, err = json.Marshal(item.metadata)
			if err != nil {
				log.Printf("[LOGS_DB] Failed to marshal metadata: %v", err)
				metadataJSON = nil
			} else {
				metadataJSON = jsonBytes
			}
		} else {
			metadataJSON = nil
		}

		valueStrings = append(valueStrings, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			i*7+1, i*7+2, i*7+3, i*7+4, i*7+5, i*7+6, i*7+7))
		valueArgs = append(valueArgs, timestamp, string(item.entry.Level), item.entry.Message, item.source, clientID, clientType, metadataJSON)
	}

	query := fmt.Sprintf("INSERT INTO websocket_logs (timestamp, level, message, source, client_id, client_type, metadata) VALUES %s",
		strings.Join(valueStrings, ","))

	_, err := logsDB.ExecContext(ctx, query, valueArgs...)
	return err
}
