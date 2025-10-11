package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// UsageLogger отвечает за логирование использования LLM токенов
type UsageLogger struct {
	db      *sqlx.DB
	logChan chan UsageLogEntry // Канал для асинхронного логирования
	done    chan struct{}       // Канал для graceful shutdown
}

// UsageLogEntry представляет запись о использовании LLM
type UsageLogEntry struct {
	ClientID         *uuid.UUID
	ChatID           *uuid.UUID
	AdminID          *uuid.UUID
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	RequestType      string // "chat", "translation", "autoresponder", "analysis", "function_call"
	Metadata         map[string]interface{}
}

var globalUsageLogger *UsageLogger

// InitUsageLogger инициализирует глобальный логгер для LLM usage
func InitUsageLogger() error {
	// Получаем креденшалы для отдельной БД логов
	dbHost := os.Getenv("LLM_LOGS_DB_HOST")
	dbPort := os.Getenv("LLM_LOGS_DB_PORT")
	dbUser := os.Getenv("LLM_LOGS_DB_USER")
	dbPassword := os.Getenv("LLM_LOGS_DB_PASSWORD")
	dbName := os.Getenv("LLM_LOGS_DB_NAME")

	if dbHost == "" || dbPassword == "" {
		log.Println("[LLM_USAGE_LOGGER] WARNING: LLM logs DB credentials not set, logging disabled")
		return nil
	}

	// Формируем connection string
	connStr := "host=" + dbHost + " port=" + dbPort + " user=" + dbUser +
		" password=" + dbPassword + " dbname=" + dbName + " sslmode=require"

	// Подключаемся к БД
	db, err := sqlx.Connect("postgres", connStr)
	if err != nil {
		log.Printf("[LLM_USAGE_LOGGER] ERROR: Failed to connect to LLM logs DB: %v", err)
		return err
	}

	// Настраиваем пул соединений для оптимальной производительности
	db.SetMaxOpenConns(25)                    // Максимум 25 открытых соединений
	db.SetMaxIdleConns(5)                     // 5 idle соединений в пуле
	db.SetConnMaxLifetime(5 * time.Minute)    // Переподключение каждые 5 минут
	db.SetConnMaxIdleTime(1 * time.Minute)    // Закрывать idle соединения через 1 минуту

	// Проверяем подключение
	if err := db.Ping(); err != nil {
		log.Printf("[LLM_USAGE_LOGGER] ERROR: Failed to ping LLM logs DB: %v", err)
		return err
	}

	// Создаём логгер с асинхронным каналом (буфер 1000 записей)
	globalUsageLogger = &UsageLogger{
		db:      db,
		logChan: make(chan UsageLogEntry, 1000),
		done:    make(chan struct{}),
	}

	// Запускаем worker goroutine для асинхронной записи
	go globalUsageLogger.worker()

	log.Println("[LLM_USAGE_LOGGER] ✅ Successfully connected to LLM logs DB (async mode)")

	return nil
}

// LogUsage записывает использование токенов в БД (асинхронно, неблокирующая операция)
func LogUsage(ctx context.Context, entry UsageLogEntry) error {
	if globalUsageLogger == nil {
		// Логирование отключено - ничего не делаем
		return nil
	}

	// Неблокирующая отправка в канал
	select {
	case globalUsageLogger.logChan <- entry:
		// Успешно отправлено
		return nil
	default:
		// Канал заполнен, пропускаем запись (чтобы не блокировать основной поток)
		log.Printf("[LLM_USAGE_LOGGER] WARNING: Log channel full, dropping entry")
		return nil
	}
}

// log записывает запись в БД
func (l *UsageLogger) log(ctx context.Context, entry UsageLogEntry) error {
	// Сериализуем metadata в JSON
	metadataJSON, err := json.Marshal(entry.Metadata)
	if err != nil {
		log.Printf("[LLM_USAGE_LOGGER] WARNING: Failed to marshal metadata: %v", err)
		metadataJSON = []byte("{}")
	}

	query := `
		INSERT INTO llm_usage_logs (
			client_id,
			chat_id,
			admin_id,
			provider,
			model,
			prompt_tokens,
			completion_tokens,
			total_tokens,
			request_type,
			metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	_, err = l.db.ExecContext(
		ctx,
		query,
		entry.ClientID,
		entry.ChatID,
		entry.AdminID,
		entry.Provider,
		entry.Model,
		entry.PromptTokens,
		entry.CompletionTokens,
		entry.TotalTokens,
		entry.RequestType,
		metadataJSON,
	)

	if err != nil {
		log.Printf("[LLM_USAGE_LOGGER] ERROR: Failed to log usage: %v", err)
		return err
	}

	// Убрали избыточное логирование для производительности
	return nil
}

// worker обрабатывает записи из канала в фоновом режиме
func (l *UsageLogger) worker() {
	log.Println("[LLM_USAGE_LOGGER] Worker started")

	for {
		select {
		case entry := <-l.logChan:
			// Записываем в БД (без блокировки основного потока)
			if err := l.log(context.Background(), entry); err != nil {
				log.Printf("[LLM_USAGE_LOGGER] Worker error: %v", err)
			}
		case <-l.done:
			// Получен сигнал остановки
			log.Println("[LLM_USAGE_LOGGER] Worker shutting down...")

			// Обрабатываем оставшиеся записи в канале
			for {
				select {
				case entry := <-l.logChan:
					_ = l.log(context.Background(), entry)
				default:
					log.Println("[LLM_USAGE_LOGGER] Worker stopped")
					return
				}
			}
		}
	}
}

// GetDailyStats получает агрегированную статистику по дням
func GetDailyStats(ctx context.Context, startDate, endDate time.Time, clientID *uuid.UUID) ([]map[string]interface{}, error) {
	if globalUsageLogger == nil {
		return nil, sql.ErrNoRows
	}

	query := `
		SELECT
			DATE(timestamp) as date,
			provider,
			model,
			request_type,
			COUNT(*) as request_count,
			SUM(prompt_tokens) as total_prompt_tokens,
			SUM(completion_tokens) as total_completion_tokens,
			SUM(total_tokens) as total_tokens,
			AVG(total_tokens) as avg_tokens_per_request
		FROM llm_usage_logs
		WHERE timestamp BETWEEN $1 AND $2
	`

	args := []interface{}{startDate, endDate}

	if clientID != nil {
		query += " AND client_id = $3"
		args = append(args, clientID)
	}

	query += " GROUP BY DATE(timestamp), provider, model, request_type ORDER BY DATE(timestamp) DESC"

	rows, err := globalUsageLogger.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}

	for rows.Next() {
		var (
			date          time.Time
			provider      string
			model         string
			requestType   sql.NullString
			requestCount  int64
			promptTokens  int64
			compTokens    int64
			totalTokens   int64
			avgTokens     float64
		)

		if err := rows.Scan(&date, &provider, &model, &requestType, &requestCount, &promptTokens, &compTokens, &totalTokens, &avgTokens); err != nil {
			log.Printf("[LLM_USAGE_LOGGER] ERROR scanning row: %v", err)
			continue
		}

		result := map[string]interface{}{
			"date":                     date.Format("2006-01-02"),
			"provider":                 provider,
			"model":                    model,
			"request_type":             requestType.String,
			"request_count":            requestCount,
			"total_prompt_tokens":      promptTokens,
			"total_completion_tokens":  compTokens,
			"total_tokens":             totalTokens,
			"avg_tokens_per_request":   avgTokens,
		}

		results = append(results, result)
	}

	return results, nil
}

// GetCostEstimates получает оценку стоимости с группировкой по провайдерам
func GetCostEstimates(ctx context.Context, startDate, endDate time.Time, clientID *uuid.UUID) ([]map[string]interface{}, error) {
	if globalUsageLogger == nil {
		return nil, sql.ErrNoRows
	}

	query := `
		SELECT
			DATE(timestamp) as date,
			provider,
			COUNT(*) as request_count,
			SUM(total_tokens) as total_tokens,
			CASE provider
				WHEN 'openai' THEN SUM(total_tokens)::NUMERIC * 0.00015 / 1000
				WHEN 'gemini' THEN SUM(total_tokens)::NUMERIC * 0.00010 / 1000
				WHEN 'claude' THEN SUM(total_tokens)::NUMERIC * 0.00300 / 1000
				WHEN 'anthropic' THEN SUM(total_tokens)::NUMERIC * 0.00300 / 1000
				ELSE 0
			END as estimated_cost_usd
		FROM llm_usage_logs
		WHERE timestamp BETWEEN $1 AND $2
	`

	args := []interface{}{startDate, endDate}

	if clientID != nil {
		query += " AND client_id = $3"
		args = append(args, clientID)
	}

	query += " GROUP BY DATE(timestamp), provider ORDER BY DATE(timestamp) DESC, provider"

	rows, err := globalUsageLogger.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}

	for rows.Next() {
		var (
			date         time.Time
			provider     string
			requestCount int64
			totalTokens  int64
			cost         float64
		)

		if err := rows.Scan(&date, &provider, &requestCount, &totalTokens, &cost); err != nil {
			log.Printf("[LLM_USAGE_LOGGER] ERROR scanning row: %v", err)
			continue
		}

		result := map[string]interface{}{
			"date":              date.Format("2006-01-02"),
			"provider":          provider,
			"request_count":     requestCount,
			"total_tokens":      totalTokens,
			"estimated_cost_usd": cost,
		}

		results = append(results, result)
	}

	return results, nil
}

// GetDailyAggregates получает данные агрегированные только по датам (без детализации по провайдерам)
// Используется для графиков на фронтенде, чтобы избежать агрегации на клиенте
func GetDailyAggregates(ctx context.Context, startDate, endDate time.Time, clientID *uuid.UUID) ([]map[string]interface{}, error) {
	if globalUsageLogger == nil {
		return nil, sql.ErrNoRows
	}

	query := `
		SELECT
			DATE(timestamp) as date,
			COUNT(*) as requests,
			SUM(total_tokens) as tokens
		FROM llm_usage_logs
		WHERE timestamp BETWEEN $1 AND $2
	`

	args := []interface{}{startDate, endDate}

	if clientID != nil {
		query += " AND client_id = $3"
		args = append(args, clientID)
	}

	query += " GROUP BY DATE(timestamp) ORDER BY DATE(timestamp) DESC"

	rows, err := globalUsageLogger.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}

	for rows.Next() {
		var (
			date     time.Time
			requests int64
			tokens   int64
		)

		if err := rows.Scan(&date, &requests, &tokens); err != nil {
			log.Printf("[LLM_USAGE_LOGGER] ERROR scanning row: %v", err)
			continue
		}

		result := map[string]interface{}{
			"date":     date.Format("2006-01-02"),
			"requests": requests,
			"tokens":   tokens,
		}

		results = append(results, result)
	}

	return results, nil
}

// CloseUsageLogger корректно завершает работу логгера (graceful shutdown)
func CloseUsageLogger() error {
	if globalUsageLogger == nil {
		return nil
	}

	log.Println("[LLM_USAGE_LOGGER] Initiating graceful shutdown...")

	// Сигнализируем worker'у о завершении
	close(globalUsageLogger.done)

	// Даём worker'у время обработать оставшиеся записи (максимум 5 секунд)
	time.Sleep(2 * time.Second)

	// Закрываем подключение к БД
	if globalUsageLogger.db != nil {
		if err := globalUsageLogger.db.Close(); err != nil {
			log.Printf("[LLM_USAGE_LOGGER] ERROR closing DB: %v", err)
			return err
		}
	}

	log.Println("[LLM_USAGE_LOGGER] ✅ Shutdown complete")
	return nil
}
