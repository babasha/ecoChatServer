// internal/database/db.go
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
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

// ─────────────────────────────── UUID-утилиты

func StringToUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, errors.New("empty UUID string")
	}
	return uuid.Parse(s)
}

func UUIDToString(u uuid.UUID) string {
	if u == uuid.Nil {
		return ""
	}
	return u.String()
}

func NullUUIDToPointer(ns sql.NullString) (*uuid.UUID, error) {
	if !ns.Valid {
		return nil, nil
	}
	u, err := uuid.Parse(ns.String)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func UUIDPointerToNullString(u *uuid.UUID) sql.NullString {
	if u == nil {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{
		String: u.String(),
		Valid:  true,
	}
}