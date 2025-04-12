package main

import (
	"ecochatserver/handlers"
	"ecochatserver/websocket"
	"log"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Инициализация роутера Gin
	r := gin.Default()

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

	// API эндпоинты
	api := r.Group("/api")
	{
		// Эндпоинты для чатов
		chats := api.Group("/chats")
		{
			chats.GET("", handlers.GetChats)
			chats.GET("/:id", handlers.GetChatByID)
			chats.POST("/:id/messages", handlers.SendMessage)
		}

		// Эндпоинт для Telegram бота
		api.POST("/telegram/webhook", handlers.TelegramWebhook)

		// Эндпоинт для авторизации админов
		api.POST("/auth/login", handlers.Login)
	}

	// WebSocket эндпоинт
	r.GET("/ws", func(c *gin.Context) {
		websocket.ServeWs(hub, c.Writer, c.Request)
	})

	// Статические файлы (если нужно)
	r.Static("/static", "./static")

	// Запуск сервера
	log.Println("Сервер запущен на порту :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}