package database

import (
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

// InitDB инициализирует соединение с базой данных
func InitDB(dataSourceName string) error {
	var err error
	DB, err = sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return err
	}

	// Настройка пула соединений
	DB.SetMaxOpenConns(25)
	DB.SetMaxIdleConns(5)
	DB.SetConnMaxLifetime(5 * time.Minute)

	// Проверка соединения
	if err = DB.Ping(); err != nil {
		return err
	}

	log.Println("База данных успешно подключена")

	// Создаем таблицы, если их нет
	if err = createTables(); err != nil {
		return err
	}

	return nil
}

// createTables создает необходимые таблицы в базе данных
func createTables() error {
	// Таблица клиентов (компаний)
	_, err := DB.Exec(`
		CREATE TABLE IF NOT EXISTS clients (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			subscription TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Таблица администраторов
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS admins (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			avatar TEXT,
			role TEXT NOT NULL,
			client_id TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (client_id) REFERENCES clients (id)
		)
	`)
	if err != nil {
		return err
	}

	// Таблица пользователей
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			email TEXT,
			avatar TEXT,
			source TEXT,
			source_id TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Таблица чатов
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL,
			status TEXT NOT NULL,
			source TEXT NOT NULL,
			bot_id TEXT NOT NULL,
			client_id TEXT NOT NULL,
			assigned_to TEXT,
			FOREIGN KEY (user_id) REFERENCES users (id),
			FOREIGN KEY (assigned_to) REFERENCES admins (id),
			FOREIGN KEY (client_id) REFERENCES clients (id)
		)
	`)
	if err != nil {
		return err
	}

	// Таблица сообщений
	_, err = DB.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			content TEXT NOT NULL,
			sender TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			timestamp TIMESTAMP NOT NULL,
			read BOOLEAN NOT NULL DEFAULT FALSE,
			type TEXT DEFAULT 'text',
			metadata TEXT,
			FOREIGN KEY (chat_id) REFERENCES chats (id)
		)
	`)
	if err != nil {
		return err
	}

	log.Println("Таблицы успешно созданы")
	return nil
}

// CloseDB закрывает соединение с базой данных
func CloseDB() {
	if DB != nil {
		DB.Close()
	}
}