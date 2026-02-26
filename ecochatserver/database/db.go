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
