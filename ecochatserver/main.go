package main

import (
	"ecochatserver/database"
	"ecochatserver/handlers"
	"ecochatserver/middleware"
	"ecochatserver/websocket"
	"log"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Настройка логирования
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Запуск EcoChat сервера...")

	// Загрузка переменных окружения
	err := godotenv.Load()
	if err != nil {
		log.Println("Файл .env не найден, используются переменные среды")
	}

	// Установка режима Gin
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = "debug"
	}
	gin.SetMode(ginMode)

	// Инициализация базы данных
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "ecochat.db"
	}

	log.Printf("Инициализация базы данных: %s", dbPath)
	err = database.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Критическая ошибка подключения к базе данных: %v", err)
	}
	defer database.CloseDB()

	log.Println("База данных успешно инициализирована")

	// Инициализация роутера Gin
	r := gin.Default()

	// Добавляем middleware для логирования
	r.Use(middleware.Logger())

	// Настройка CORS для взаимодействия с фронтендом
	allowOrigins := []string{"http://localhost:3000"}
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL != "" {
		allowOrigins = append(allowOrigins, frontendURL)
	}

	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	log.Printf("CORS настроен для: %v", allowOrigins)

	// Инициализация WebSocket хаба
	hub := websocket.NewHub()
	go hub.Run()
	handlers.SetWebSocketHub(hub)
	log.Println("WebSocket Hub запущен")

	// API эндпоинты
	api := r.Group("/api")
	{
		// Эндпоинт для проверки работоспособности
		api.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok", "time": time.Now()})
		})

		// Эндпоинт для авторизации админов (публичный)
		api.POST("/auth/login", handlers.Login)
		
		// Защищенные маршруты
		authorized := api.Group("/")
		authorized.Use(middleware.AuthMiddleware())
		{
			// Эндпоинты для чатов
			chats := authorized.Group("/chats")
			{
				chats.GET("", handlers.GetChats)
				chats.GET("/:id", handlers.GetChatByID)
				chats.POST("/:id/messages", handlers.SendMessage)
			}
		}

		// Эндпоинт для Telegram бота (webhook)
		api.POST("/telegram/webhook", handlers.TelegramWebhook)
	}

	// WebSocket эндпоинт
	r.GET("/ws", func(c *gin.Context) {
		websocket.ServeWs(hub, c.Writer, c.Request)
	})

	// Получаем порт из переменных окружения
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Запуск сервера
	log.Printf("Сервер запущен на порту :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Критическая ошибка запуска сервера: %v", err)
	}
}