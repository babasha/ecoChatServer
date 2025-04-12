package main

import (
	"ecochatserver/database"
	"ecochatserver/handlers"
	"ecochatserver/middleware"
	"ecochatserver/websocket"
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Инициализация базы данных
	err := database.InitDB("ecochat.db")
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v", err)
	}
	defer database.CloseDB()

	// Инициализация роутера Gin
	r := gin.Default()

	// Добавляем middleware для логирования
	r.Use(middleware.Logger())

	// Настройка CORS для взаимодействия с фронтендом
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000"}, // URL вашего Next.js приложения
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	// Инициализация WebSocket хаба
	hub := websocket.NewHub()
	go hub.Run()
	handlers.SetWebSocketHub(hub)

	// API эндпоинты
	api := r.Group("/api")
	{
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

	// Запуск сервера
	log.Println("Сервер запущен на порту :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}