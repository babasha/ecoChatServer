// internal/database/db.go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	// pgx-драйвер в режиме database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
)

var DB *sql.DB      // БД для чатов (caboose)
var UsersDB *sql.DB // БД для клиентов и админов (ballast)

// GetDB возвращает основное подключение к БД (для обратной совместимости).
func GetDB() *sql.DB {
	return DB
}

// Init открывает пул соединений и проверяет подключение.
func Init() error {
	// Подключаемся к основной БД (чаты)
	dsn := buildDSN()
	var err error

	DB, err = sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open (main): %w", err)
	}

	// Параметры пула
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(5 * time.Minute)

	// Проверяем подключение (тайм-аут 3 с)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = DB.PingContext(ctx); err != nil {
		_ = DB.Close()
		return fmt.Errorf("db.Ping (main): %w", err)
	}

	log.Println("[database] PostgreSQL (chats) connected ✓")

	// Подключаемся к БД пользователей (если настроена)
	usersDSN := buildUsersDSN()
	if usersDSN != "" {
		UsersDB, err = sql.Open("pgx", usersDSN)
		if err != nil {
			return fmt.Errorf("sql.Open (users): %w", err)
		}

		UsersDB.SetMaxOpenConns(10)
		UsersDB.SetMaxIdleConns(2)
		UsersDB.SetConnMaxLifetime(5 * time.Minute)

		ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel2()
		if err = UsersDB.PingContext(ctx2); err != nil {
			_ = UsersDB.Close()
			return fmt.Errorf("db.Ping (users): %w", err)
		}

		log.Println("[database] PostgreSQL (users) connected ✓")
	} else {
		// Если нет отдельной БД для пользователей, используем основную
		UsersDB = DB
		log.Println("[database] Using main DB for users")
	}

	// Создаем партиции заранее
	if err := initializePartitions(); err != nil {
		log.Printf("Warning: не удалось создать партиции: %v", err)
		// Не прерываем запуск сервера из-за партиций
	}

	// Создаём таблицы для Director AI (если не существуют)
	if err := ensureDirectorTables(); err != nil {
		log.Printf("[database] Warning: не удалось создать таблицы Director: %v", err)
	}

	// Инициализируем Redis (опционально)
	if err := InitRedis(); err != nil {
		log.Printf("[REDIS] Warning: не удалось подключиться к Redis: %v", err)
		log.Println("[REDIS] Сервер будет работать без кеширования")
		// Не прерываем запуск сервера из-за Redis
	}

	return nil
}

// ensurePartitions создаёт партиции на count недель вперед.
func ensurePartitions(count int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, fmt.Sprintf("SELECT public.create_future_partitions(%d)", count))
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("ensure partitions: %w", err)
	}
	return nil
}

// initializePartitions создает партиции при старте.
func initializePartitions() error {
	if err := ensurePartitions(8); err != nil {
		return err
	}
	log.Println("[database] Партиции успешно созданы")
	return nil
}

// RefreshPartitions обновляет партиции (вызывается периодически).
func RefreshPartitions() error {
	return ensurePartitions(8)
}

// Close закрывает пул (вызывайте defer database.Close()).
func Close() {
	if DB != nil {
		_ = DB.Close()
	}
	if UsersDB != nil && UsersDB != DB {
		_ = UsersDB.Close()
	}
	// Закрываем Redis
	CloseRedis()
}

// ─────────────────────────────── helpers

// buildPostgresDSN формирует DSN для PostgreSQL с логированием.
func buildPostgresDSN(prefix, logName string, defaults map[string]string) string {
	host := env(prefix+"HOST", defaults["host"])
	port := env(prefix+"PORT", defaults["port"])
	user := env(prefix+"USER", defaults["user"])
	password := os.Getenv(prefix + "PASSWORD")
	dbname := env(prefix+"DATABASE", defaults["dbname"])
	sslmode := env(prefix+"SSL_MODE", defaults["sslmode"])

	var dsn string
	if password != "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			host, port, user, password, dbname, sslmode,
		)
	} else {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s dbname=%s sslmode=%s",
			host, port, user, dbname, sslmode,
		)
	}

	log.Printf("[DB] Connecting (%s): host=%s port=%s user=%s dbname=%s sslmode=%s", logName, host, port, user, dbname, sslmode)
	return dsn
}

func buildDSN() string {
	return buildPostgresDSN("PG_", "chats", map[string]string{
		"host": "localhost", "port": "5432", "user": "postgres",
		"dbname": "ecochat", "sslmode": "disable",
	})
}

func buildUsersDSN() string {
	host := os.Getenv("USERS_PG_HOST")
	if host == "" {
		return "" // Используем основную БД
	}
	return buildPostgresDSN("USERS_PG_", "users", map[string]string{
		"host": host, "port": "5432", "user": "postgres",
		"dbname": "railway", "sslmode": "require",
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ensureDirectorTables creates tables needed by Director AI if they don't exist.
func ensureDirectorTables() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ddl := `
	CREATE TABLE IF NOT EXISTS director_reports (
		id UUID PRIMARY KEY,
		report_date TIMESTAMPTZ NOT NULL,
		report_type VARCHAR(50) NOT NULL DEFAULT 'periodic',
		trigger_event TEXT DEFAULT '',
		summary_count INT NOT NULL DEFAULT 0,
		analysis TEXT NOT NULL DEFAULT '',
		directives JSONB DEFAULT '[]',
		stats JSONB DEFAULT '{}',
		customer_complaints JSONB DEFAULT '[]',
		key_observations JSONB DEFAULT '[]',
		prompt_changes JSONB DEFAULT '[]',
		expectations TEXT DEFAULT '',
		applied BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS interaction_metrics (
		id UUID PRIMARY KEY,
		chat_id UUID NOT NULL,
		message_id UUID,
		agent_mode VARCHAR(50) NOT NULL DEFAULT '',
		agent_name VARCHAR(100) NOT NULL DEFAULT '',
		prompt_version INT,
		tools_called JSONB DEFAULT '[]',
		tool_count INT NOT NULL DEFAULT 0,
		was_escalated BOOLEAN NOT NULL DEFAULT FALSE,
		was_empty BOOLEAN NOT NULL DEFAULT FALSE,
		response_length INT NOT NULL DEFAULT 0,
		response_time_ms INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS agent_prompts (
		id UUID PRIMARY KEY,
		agent_name VARCHAR(100) NOT NULL,
		version INT NOT NULL,
		prompt TEXT NOT NULL,
		created_by VARCHAR(50) NOT NULL DEFAULT 'human',
		parent_version INT,
		is_active BOOLEAN NOT NULL DEFAULT FALSE,
		metrics JSONB DEFAULT '{}',
		notes TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (agent_name, version)
	);

	CREATE TABLE IF NOT EXISTS chat_summaries (
		id UUID PRIMARY KEY,
		chat_id UUID NOT NULL,
		summary TEXT NOT NULL,
		messages_from TIMESTAMPTZ NOT NULL,
		messages_to TIMESTAMPTZ NOT NULL,
		message_count INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_director_reports_created_at ON director_reports (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_created_at ON interaction_metrics (created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_agent_name ON interaction_metrics (agent_name);
	CREATE INDEX IF NOT EXISTS idx_interaction_metrics_chat_id ON interaction_metrics (chat_id);
	CREATE INDEX IF NOT EXISTS idx_agent_prompts_agent_active ON agent_prompts (agent_name, is_active);
	CREATE INDEX IF NOT EXISTS idx_chat_summaries_chat_id ON chat_summaries (chat_id);
	CREATE INDEX IF NOT EXISTS idx_chat_summaries_created_at ON chat_summaries (created_at DESC);
	`

	_, err := DB.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("ensure director tables: %w", err)
	}
	log.Println("[database] Director tables ready ✓")
	return nil
}
