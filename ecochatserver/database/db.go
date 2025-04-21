package database

import (
    "database/sql"
    "fmt"
    "log"
    "os"
    "time"

    _ "github.com/lib/pq" // регистрирует драйвер PostgreSQL
)

var DB *sql.DB

// InitDB инициализирует соединение с базой данных
func InitDB() error {
    // Читаем параметры из окружения
    pgHost     := os.Getenv("PG_HOST")
    pgPort     := os.Getenv("PG_PORT")
    pgUser     := os.Getenv("PG_USER")
    pgPassword := os.Getenv("PG_PASSWORD")
    pgDatabase := os.Getenv("PG_DATABASE")
    pgSSLMode  := os.Getenv("PG_SSL_MODE")

    // Значения по умолчанию
    if pgHost == ""     { pgHost     = "localhost" }
    if pgPort == ""     { pgPort     = "5432" }
    if pgUser == ""     { pgUser     = "postgres" }
    if pgDatabase == "" { pgDatabase = "ecochat" }
    if pgSSLMode == ""  { pgSSLMode  = "disable" }

    // Формируем DSN
    dsn := fmt.Sprintf(
        "host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
        pgHost, pgPort, pgUser, pgPassword, pgDatabase, pgSSLMode,
    )

    var err error
    DB, err = sql.Open("postgres", dsn)
    if err != nil {
        return fmt.Errorf("sql.Open: %w", err)
    }

    // Настройка пула соединений
    DB.SetMaxOpenConns(25)
    DB.SetMaxIdleConns(5)
    DB.SetConnMaxLifetime(5 * time.Minute)

    // Проверяем фактическое подключение
    if err = DB.Ping(); err != nil {
        return fmt.Errorf("DB.Ping: %w", err)
    }
    log.Println("База данных успешно подключена")

    // Создание схемы и индексов
    if err = createTables(); err != nil {
        return err
    }
    if err = createIndices(); err != nil {
        return err
    }

    return nil
}

// createTables создаёт необходимые таблицы, если их нет
func createTables() error {
    stmts := []string{
        `CREATE TABLE IF NOT EXISTS clients (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            subscription TEXT NOT NULL,
            active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`,
        `CREATE TABLE IF NOT EXISTS admins (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            email TEXT NOT NULL UNIQUE,
            password_hash TEXT NOT NULL,
            avatar TEXT,
            role TEXT NOT NULL,
            client_id TEXT NOT NULL REFERENCES clients(id),
            active BOOLEAN NOT NULL DEFAULT TRUE,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`,
        `CREATE TABLE IF NOT EXISTS users (
            id TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            email TEXT,
            avatar TEXT,
            source TEXT,
            source_id TEXT,
            created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`,
        `CREATE TABLE IF NOT EXISTS chats (
            id TEXT PRIMARY KEY,
            user_id TEXT NOT NULL REFERENCES users(id),
            created_at TIMESTAMP NOT NULL,
            updated_at TIMESTAMP NOT NULL,
            status TEXT NOT NULL,
            source TEXT NOT NULL,
            bot_id TEXT NOT NULL,
            client_id TEXT NOT NULL REFERENCES clients(id),
            assigned_to TEXT REFERENCES admins(id),
            metadata JSONB
        )`,
        `CREATE TABLE IF NOT EXISTS messages (
            id TEXT PRIMARY KEY,
            chat_id TEXT NOT NULL REFERENCES chats(id),
            content TEXT NOT NULL,
            sender TEXT NOT NULL,
            sender_id TEXT NOT NULL,
            timestamp TIMESTAMP NOT NULL,
            read BOOLEAN NOT NULL DEFAULT FALSE,
            type TEXT DEFAULT 'text',
            metadata JSONB
        )`,
    }

    for _, sqlStmt := range stmts {
        if _, err := DB.Exec(sqlStmt); err != nil {
            return fmt.Errorf("createTables: %w\nSQL: %s", err, sqlStmt)
        }
    }
    log.Println("Таблицы успешно созданы")
    return nil
}

// createIndices создаёт индексы для ускорения выборок
func createIndices() error {
    indices := []string{
        `CREATE INDEX IF NOT EXISTS idx_users_source_source_id ON users(source, source_id)`,
        `CREATE INDEX IF NOT EXISTS idx_admins_client_id ON admins(client_id)`,
        `CREATE INDEX IF NOT EXISTS idx_admins_email ON admins(email)`,
        `CREATE INDEX IF NOT EXISTS idx_chats_user_id ON chats(user_id)`,
        `CREATE INDEX IF NOT EXISTS idx_chats_client_id ON chats(client_id)`,
        `CREATE INDEX IF NOT EXISTS idx_chats_assigned_to ON chats(assigned_to)`,
        `CREATE INDEX IF NOT EXISTS idx_chats_source_bot_id ON chats(source, bot_id)`,
        `CREATE INDEX IF NOT EXISTS idx_chats_updated_at ON chats(updated_at)`,
        `CREATE INDEX IF NOT EXISTS idx_messages_chat_id ON messages(chat_id)`,
        `CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender)`,
        `CREATE INDEX IF NOT EXISTS idx_messages_read ON messages(read)`,
        `CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp)`,
        `CREATE INDEX IF NOT EXISTS idx_messages_chat_id_timestamp ON messages(chat_id, timestamp)`,
    }

    for _, idx := range indices {
        if _, err := DB.Exec(idx); err != nil {
            return fmt.Errorf("createIndices: %w\nSQL: %s", err, idx)
        }
    }
    log.Println("Индексы успешно созданы")
    return nil
}

// CloseDB закрывает соединение с базой
func CloseDB() {
    if DB != nil {
        _ = DB.Close()
    }
}

// nullStringToPointer превращает sql.NullString в *string
func nullStringToPointer(ns sql.NullString) *string {
    if ns.Valid {
        s := ns.String
        return &s
    }
    return nil
}