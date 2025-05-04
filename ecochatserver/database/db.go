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

var DB *sql.DB

// Init открывает пул соединений и проверяет подключение.
func Init() error {
	dsn := buildDSN()
	var err error

	DB, err = sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("sql.Open: %w", err)
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
		return fmt.Errorf("db.Ping: %w", err)
	}

	log.Println("[database] PostgreSQL connected ✓")
	
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
func Close() { _ = DB.Close() }

// ─────────────────────────────── helpers

func buildDSN() string {
	host     := env("PG_HOST",     "localhost")
	port     := env("PG_PORT",     "5432")
	user     := env("PG_USER",     "postgres")
	password := os.Getenv("PG_PASSWORD") // может быть пустым
	dbname   := env("PG_DATABASE", "ecochat")
	sslmode  := env("PG_SSL_MODE", "disable")

	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode,
	)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
} 