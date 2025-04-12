package main

import (
	"database/sql"
	"log"

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

	// Создаем тестового клиента
	clientID := uuid.New().String()
	_, err = db.Exec(`
		INSERT INTO clients (id, name, subscription, active)
		VALUES (?, ?, ?, ?)
	`, clientID, "ЭкоТестКомпания", "premium", true)
	if err != nil {
		log.Fatalf("Ошибка создания тестового клиента: %v", err)
	}

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

	log.Println("База данных успешно инициализирована с тестовыми данными")
}