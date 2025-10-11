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

	return nil
}

// initializePartitions создает партиции заранее
func initializePartitions() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer conn.Close()

	// Создаем партиции на 8 недель вперед
	_, err = conn.ExecContext(ctx, "SELECT public.create_future_partitions(8)")
	if err != nil {
		return fmt.Errorf("create partitions: %w", err)
	}

	log.Println("[database] Партиции успешно созданы")
	return nil
}

// RefreshPartitions обновляет партиции
func RefreshPartitions() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn: %w", err)
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT public.create_future_partitions(8)")
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("refresh partitions: %w", err)
	}

	return nil
}

// Close закрывает пул (вызывайте defer database.Close()).
func Close() {
	if DB != nil {
		_ = DB.Close()
	}
	if UsersDB != nil && UsersDB != DB {
		_ = UsersDB.Close()
	}
}

// ─────────────────────────────── helpers

func buildDSN() string {
	host := env("PG_HOST", "localhost")
	port := env("PG_PORT", "5432")
	user := env("PG_USER", "postgres")
	password := os.Getenv("PG_PASSWORD") // может быть пустым
	dbname := env("PG_DATABASE", "ecochat")
	sslmode := env("PG_SSL_MODE", "disable")

	// Формируем DSN, пропуская пустой пароль
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

	// Логируем DSN без пароля для отладки
	log.Printf("[DB] Connecting (chats): host=%s port=%s user=%s dbname=%s sslmode=%s", host, port, user, dbname, sslmode)

	return dsn
}

// buildUsersDSN строит DSN для БД пользователей (ballast)
func buildUsersDSN() string {
	// Если переменные не заданы, возвращаем пустую строку
	host := os.Getenv("USERS_PG_HOST")
	log.Printf("[DB] DEBUG: USERS_PG_HOST = '%s'", host)
	if host == "" {
		log.Println("[DB] DEBUG: USERS_PG_HOST пустой, используем основную БД")
		return "" // Используем основную БД
	}

	port := env("USERS_PG_PORT", "5432")
	user := env("USERS_PG_USER", "postgres")
	password := os.Getenv("USERS_PG_PASSWORD")
	dbname := env("USERS_PG_DATABASE", "railway")
	sslmode := env("USERS_PG_SSL_MODE", "require")

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

	log.Printf("[DB] Connecting (users): host=%s port=%s user=%s dbname=%s sslmode=%s", host, port, user, dbname, sslmode)

	return dsn
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
