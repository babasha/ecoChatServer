package main

import (
    "log"
    "net/http"
    "os"
    "strings"
    "time"

    "github.com/gin-contrib/cors"
    "github.com/gin-gonic/gin"
    "github.com/joho/godotenv"

    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/handlers"
    "github.com/egor/ecochatserver/middleware"
    "github.com/egor/ecochatserver/websocket"
)

func main() {
    // Настройка логирования
    log.SetFlags(log.LstdFlags | log.Lshortfile)
    log.Println("Запуск EcoChat сервера...")

    // Загрузка переменных окружения из .env (если есть)
    if err := godotenv.Load(); err != nil {
        log.Println("Файл .env не найден, используются системные переменные")
    }

    // Проверяем ключевые переменные окружения
    checkEnvironmentVariables()

    // Устанавливаем режим Gin (release/debug)
    if mode := os.Getenv("GIN_MODE"); mode != "" {
        gin.SetMode(mode)
    }

    // Инициализация базы данных (PostgreSQL по PG_* переменным)
    log.Println("Инициализация соединения с базой данных...")
    if err := database.InitDB(); err != nil {
        log.Fatalf("Критическая ошибка подключения к базе данных: %v", err)
    }
    defer database.CloseDB()
    log.Println("База данных успешно инициализирована")

    // Создаём роутер Gin и прикручиваем middleware
    r := gin.Default()
    r.Use(middleware.Logger())

    // Настраиваем CORS
    setupCORS(r)

    // Запускаем WebSocket Hub
    hub := websocket.NewHub()
    go hub.Run()
    handlers.SetWebSocketHub(hub)
    log.Println("WebSocket Hub запущен")

    // Инициализируем автоответчик на базе LLM
    handlers.InitAutoResponder()

    // Конфигурируем API-роуты
    setupAPIRoutes(r, hub)

    // Запускаем HTTP-сервер
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    log.Printf("Сервер запущен на порту :%s", port)
    if err := r.Run(":" + port); err != nil {
        log.Fatalf("Критическая ошибка запуска сервера: %v", err)
    }
}

// checkEnvironmentVariables проверяет обязательные переменные окружения
func checkEnvironmentVariables() {
    if os.Getenv("JWT_SECRET_KEY") == "" {
        log.Println("Предупреждение: JWT_SECRET_KEY не установлен, будет использоваться временный ключ")
    }
    if os.Getenv("LLM_API_URL") == "" {
        log.Println("Предупреждение: LLM_API_URL не установлен, используется http://localhost:1234/v1")
    }
    if os.Getenv("ENABLE_AUTO_RESPONDER") == "" {
        log.Println("ENABLE_AUTO_RESPONDER не установлен, автоответчик будет включён по умолчанию")
    }
}

// contains проверяет наличие элемента в слайсе
func contains(slice []string, item string) bool {
    for _, s := range slice {
        if s == item {
            return true
        }
    }
    return false
}

// setupCORS настраивает CORS middleware
func setupCORS(r *gin.Engine) {
    allowOrigins := []string{
        "http://localhost:3000",
        "https://ecp-chat-widget.vercel.app",
    }

    if envURL := os.Getenv("FRONTEND_URL"); envURL != "" && !contains(allowOrigins, envURL) {
        allowOrigins = append(allowOrigins, envURL)
    }

    if extra := os.Getenv("ADDITIONAL_ALLOWED_ORIGINS"); extra != "" {
        for _, origin := range strings.Split(extra, ",") {
            origin = strings.TrimSpace(origin)
            if origin != "" && !contains(allowOrigins, origin) {
                allowOrigins = append(allowOrigins, origin)
            }
        }
    }

    if gin.Mode() == gin.DebugMode {
        devOrigins := []string{
            "http://localhost:5000",
            "http://localhost:5500",
            "http://localhost:8000",
            "http://127.0.0.1:5500",
            "http://127.0.0.1:5000",
            "http://127.0.0.1:8000",
        }
        for _, o := range devOrigins {
            if !contains(allowOrigins, o) {
                allowOrigins = append(allowOrigins, o)
            }
        }
    }

    config := cors.Config{
        AllowOrigins:     allowOrigins,
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept", "X-Requested-With"},
        ExposeHeaders:    []string{"Content-Length", "X-Total-Count", "X-Total-Pages"},
        AllowCredentials: true,
        MaxAge:           12 * time.Hour,
    }

    if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
        config.AllowAllOrigins = true
        log.Println("ВНИМАНИЕ: CORS открыт для всех источников (только для отладки)")
    }

    r.Use(cors.New(config))

    // Специальная OPTIONS для webhook
    r.OPTIONS("/api/telegram/webhook", func(c *gin.Context) {
        origin := c.GetHeader("Origin")
        if origin == "" {
            c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
        } else {
            c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
        }
        c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
        c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
        c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
        c.Writer.Header().Set("Access-Control-Max-Age", "86400")
        c.Status(http.StatusOK)
    })

    log.Printf("CORS настроен для: %v", allowOrigins)
}

// setupAPIRoutes регистрирует HTTP-эндпоинты
func setupAPIRoutes(r *gin.Engine, hub *websocket.Hub) {
    api := r.Group("/api")
    {
        api.GET("/health", func(c *gin.Context) {
            c.JSON(http.StatusOK, gin.H{
                "status":  "ok",
                "time":    time.Now(),
                "version": "1.1.0",
            })
        })
        api.POST("/auth/login", handlers.Login)
        api.POST("/telegram/webhook", handlers.TelegramWebhook)

        // Защищённые маршруты
        authorized := api.Group("/")
        authorized.Use(middleware.AuthMiddleware())
        {
            chats := authorized.Group("/chats")
            {
                chats.GET("", handlers.GetChats)
                chats.GET("/:id", handlers.GetChatByID)
                chats.POST("/:id/messages", handlers.SendMessage)
            }
        }
    }

    // WebSocket endpoint
    r.GET("/ws", func(c *gin.Context) {
        token := c.Query("token")
        clientType := c.Query("type")
        chatID := c.Query("chat_id")
        if token == "" {
            c.JSON(http.StatusBadRequest, gin.H{"error": "Отсутствует параметр token"})
            return
        }
        log.Printf("Попытка WS-подключения: type=%s, token=%s, chat_id=%s", clientType, token, chatID)
        websocket.ServeWs(hub, c.Writer, c.Request)
    })
}