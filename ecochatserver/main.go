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

	// Проверка наличия ключевых переменных окружения
	checkEnvironmentVariables()

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
	setupCORS(r)

	// Инициализация WebSocket хаба
	hub := websocket.NewHub()
	go hub.Run()
	handlers.SetWebSocketHub(hub)
	log.Println("WebSocket Hub запущен")
	
	// Инициализация автоответчика на базе LLM
	handlers.InitAutoResponder()

	// Настройка API эндпоинтов
	setupAPIRoutes(r, hub)

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

// checkEnvironmentVariables проверяет наличие необходимых переменных окружения
func checkEnvironmentVariables() {
	// Проверка JWT_SECRET_KEY
	if os.Getenv("JWT_SECRET_KEY") == "" {
		log.Println("Предупреждение: JWT_SECRET_KEY не установлен, будет использоваться временный ключ")
		// В продакшене можно сделать fatal для обеспечения безопасности
		// log.Fatal("JWT_SECRET_KEY обязателен для безопасной работы приложения")
	}
	
	// Проверка настроек LLM
	if os.Getenv("LLM_API_URL") == "" {
		log.Println("LLM_API_URL не установлен, будет использоваться значение по умолчанию: http://localhost:1234/v1")
	}

	// Проверка включения автоответчика
	enableAutoResponder := os.Getenv("ENABLE_AUTO_RESPONDER")
	if enableAutoResponder == "" {
		log.Println("ENABLE_AUTO_RESPONDER не установлен, автоответчик будет включен по умолчанию")
	}

	// Проверка других ключевых переменных (при необходимости)
}

// setupCORS настраивает CORS для взаимодействия с фронтендом
func setupCORS(r *gin.Engine) {
	allowOrigins := []string{"http://localhost:3000"}
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL != "" {
		allowOrigins = append(allowOrigins, frontendURL)
	}

	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length", "X-Total-Count", "X-Total-Pages"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	log.Printf("CORS настроен для: %v", allowOrigins)
}

// setupAPIRoutes настраивает маршруты API (оставляем как было)
func setupAPIRoutes(r *gin.Engine, hub *websocket.Hub) {
	// API эндпоинты
	api := r.Group("/api")
	{
		// Эндпоинт для проверки работоспособности
		api.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{
				"status":  "ok", 
				"time":    time.Now(),
				"version": "1.1.0", // Добавлена версия API
			})
		})

		// Эндпоинт для авторизации админов (публичный)
		api.POST("/auth/login", handlers.Login)
		
		// Защищенные маршруты
		authorized := api.Group("/")
		authorized.Use(middleware.AuthMiddleware())
		{
			// Эндпоинты для чатов с поддержкой пагинации
			chats := authorized.Group("/chats")
			{
				// GET /api/chats?page=1&pageSize=20
				chats.GET("", handlers.GetChats)
				
				// GET /api/chats/:id?page=1&pageSize=20
				chats.GET("/:id", handlers.GetChatByID)
				
				// POST /api/chats/:id/messages
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
}