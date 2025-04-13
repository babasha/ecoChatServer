package main

import (
	"database/sql"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	// Загружаем переменные окружения из .env файла
	err := godotenv.Load()
	if err != nil {
		log.Println("Файл .env не найден, используем переменные окружения")
	}

	// Подключаемся к базе данных
	db, err := sql.Open("sqlite3", "ecochat.db")
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v", err)
	}
	defer db.Close()

	// Проверяем соединение
	if err := db.Ping(); err != nil {
		log.Fatalf("Ошибка проверки соединения с БД: %v", err)
	}
	log.Println("Успешное подключение к базе данных")

	// Создаем таблицы если они не существуют
	createTables(db)

	// Создаем тестового клиента
	clientID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO clients (id, name, subscription, active)
		VALUES (?, ?, ?, ?)
	`, clientID, "ЭкоТестКомпания", "premium", true)
	if err != nil {
		log.Fatalf("Ошибка создания тестового клиента: %v", err)
	}
	log.Printf("Создан тестовый клиент с ID: %s", clientID)

	// Создаем тестового администратора
	adminID := uuid.New().String()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password"), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("Ошибка хеширования пароля: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO admins (id, name, email, password_hash, role, client_id, active)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, adminID, "Администратор", "admin@example.com", string(passwordHash), "admin", clientID, true)
	if err != nil {
		log.Fatalf("Ошибка создания тестового администратора: %v", err)
	}
	log.Printf("Создан тестовый администратор с ID: %s", adminID)

	// Создаем нескольких тестовых пользователей
	users := []struct {
		name     string
		email    string
		source   string
		sourceID string
	}{
		{"Иван Петров", "ivan@example.com", "telegram", "12345"},
		{"Мария Сидорова", "maria@example.com", "telegram", "67890"},
		{"Алексей Иванов", "alexey@example.com", "whatsapp", "11223"},
	}

	var userIDs []string
	for _, user := range users {
		userID := uuid.New().String()
		_, err = db.Exec(`
			INSERT INTO users (id, name, email, source, source_id)
			VALUES (?, ?, ?, ?, ?)
		`, userID, user.name, user.email, user.source, user.sourceID)
		if err != nil {
			log.Fatalf("Ошибка создания тестового пользователя %s: %v", user.name, err)
		}
		userIDs = append(userIDs, userID)
		log.Printf("Создан тестовый пользователь %s с ID: %s", user.name, userID)
	}

	// Создаем тестовые чаты
	now := time.Now()
	for i, userID := range userIDs {
		chatID := uuid.New().String()
		source := "telegram"
		if i == 2 {
			source = "whatsapp"
		}

		_, err = db.Exec(`
			INSERT INTO chats (id, user_id, created_at, updated_at, status, source, bot_id, client_id, assigned_to)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, chatID, userID, now.Add(-time.Duration(i*24)*time.Hour), now.Add(-time.Duration(i*2)*time.Hour), 
		   "active", source, "testbot"+string(i+48), clientID, adminID)
		
		if err != nil {
			log.Fatalf("Ошибка создания тестового чата для пользователя %s: %v", userID, err)
		}
		log.Printf("Создан тестовый чат с ID: %s для пользователя %s", chatID, userID)

		// Добавляем несколько тестовых сообщений в каждый чат
		addTestMessages(db, chatID, userID, adminID, i)
	}

	log.Println("База данных успешно инициализирована с тестовыми данными")
}

// Создание таблиц базы данных
func createTables(db *sql.DB) {
	// Таблица клиентов (компаний)
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS clients (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			subscription TEXT NOT NULL,
			active BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatalf("Ошибка создания таблицы clients: %v", err)
	}

	// Таблица администраторов
	_, err = db.Exec(`
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
		log.Fatalf("Ошибка создания таблицы admins: %v", err)
	}

	// Таблица пользователей
	_, err = db.Exec(`
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
		log.Fatalf("Ошибка создания таблицы users: %v", err)
	}

	// Таблица чатов
	_, err = db.Exec(`
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
		log.Fatalf("Ошибка создания таблицы chats: %v", err)
	}

	// Таблица сообщений
	_, err = db.Exec(`
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
		log.Fatalf("Ошибка создания таблицы messages: %v", err)
	}

	log.Println("Все таблицы успешно созданы")
}

// Добавление тестовых сообщений в чат
func addTestMessages(db *sql.DB, chatID, userID, adminID string, chatNum int) {
	messages := []struct {
		content   string
		sender    string
		senderID  string
		timestamp time.Time
		isRead    bool
	}{
		{
			content:   "Здравствуйте! У меня есть вопрос по вашему сервису.",
			sender:    "user",
			senderID:  userID,
			timestamp: time.Now().Add(-time.Duration(20-chatNum) * time.Minute),
			isRead:    true,
		},
		{
			content:   "Добрый день! Чем могу помочь?",
			sender:    "admin",
			senderID:  adminID,
			timestamp: time.Now().Add(-time.Duration(18-chatNum) * time.Minute),
			isRead:    true,
		},
		{
			content:   "Я хотел бы узнать о тарифах.",
			sender:    "user",
			senderID:  userID,
			timestamp: time.Now().Add(-time.Duration(15-chatNum) * time.Minute),
			isRead:    true,
		},
		{
			content:   "Конечно, у нас есть несколько тарифных планов. Базовый, Премиум и Корпоративный.",
			sender:    "admin",
			senderID:  adminID,
			timestamp: time.Now().Add(-time.Duration(10-chatNum) * time.Minute),
			isRead:    true,
		},
	}

	// Добавляем разные последние сообщения для разных чатов
	if chatNum == 0 {
		messages = append(messages, struct {
			content   string
			sender    string
			senderID  string
			timestamp time.Time
			isRead    bool
		}{
			content:   "А какие функции доступны в Премиум?",
			sender:    "user",
			senderID:  userID,
			timestamp: time.Now().Add(-time.Duration(5) * time.Minute),
			isRead:    false,
		})
	} else if chatNum == 1 {
		messages = append(messages, struct {
			content   string
			sender    string
			senderID  string
			timestamp time.Time
			isRead    bool
		}{
			content:   "Спасибо за информацию!",
			sender:    "user",
			senderID:  userID,
			timestamp: time.Now().Add(-time.Duration(2) * time.Hour),
			isRead:    true,
		})
	}

	for _, msg := range messages {
		messageID := uuid.New().String()
		_, err := db.Exec(`
			INSERT INTO messages (id, chat_id, content, sender, sender_id, timestamp, read, type)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, messageID, chatID, msg.content, msg.sender, msg.senderID, msg.timestamp, msg.isRead, "text")
		
		if err != nil {
			log.Fatalf("Ошибка добавления тестового сообщения в чат %s: %v", chatID, err)
		}
	}

	log.Printf("Добавлено %d тестовых сообщений в чат %s", len(messages), chatID)
}