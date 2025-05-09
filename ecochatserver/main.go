// main.go
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
    // Логи по файлу и строке
    log.SetFlags(log.LstdFlags | log.Lshortfile)
    log.Println("EcoChat server starting…")

    // Загружаем .env (только для dev)
    _ = godotenv.Load()

    // ─── PostgreSQL ──────────────────────────────────────────────────────────
    if err := database.Init(); err != nil {
        log.Fatalf("db init: %v", err)
    }
    defer database.Close()

    // Периодически обновляем партиции
    go func() {
        ticker := time.NewTicker(1 * time.Hour)
        defer ticker.Stop()
        for range ticker.C {
            if err := database.RefreshPartitions(); err != nil {
                log.Printf("Error refreshing partitions: %v", err)
            }
        }
    }()

    // ─── Gin & middleware ───────────────────────────────────────────────────
    gin.SetMode(getEnv("GIN_MODE", gin.DebugMode))
    r := gin.New()
    r.Use(gin.Recovery(), middleware.Logger())
    setupCORS(r)

    // ─── WebSocket hub ───────────────────────────────────────────────────────
    hub := websocket.NewHub()
    go hub.Run()
    handlers.SetWebSocketHub(hub)

    // ─── Автоответчик (если используется) ───────────────────────────────────
    handlers.InitAutoResponder()

    // ─── REST API & WebSocket ───────────────────────────────────────────────
    setupAPIRoutes(r)

    // ─── HTTP-server ─────────────────────────────────────────────────────────
    addr := ":" + getEnv("PORT", "8080")
    log.Printf("HTTP listen %s", addr)
    if err := r.Run(addr); err != nil {
        log.Fatalf("server: %v", err)
    }
}

// getEnv возвращает значение или дефолт
func getEnv(k, def string) string {
    if v := os.Getenv(k); v != "" {
        return v
    }
    return def
}

// setupCORS настраивает CORS по FRONTEND_URL и т.п.
func setupCORS(r *gin.Engine) {
    allow := []string{"http://localhost:3000"}
    for _, key := range []string{"FRONTEND_URL", "ADDITIONAL_ALLOWED_ORIGINS"} {
        if v := os.Getenv(key); v != "" {
            for _, u := range strings.Split(v, ",") {
                u = strings.TrimSpace(u)
                if u != "" && !contains(allow, u) {
                    allow = append(allow, u)
                }
            }
        }
    }
    conf := cors.Config{
        AllowOrigins:     allow,
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Widget-User-ID", "X-API-Key"},
        AllowCredentials: true,
        MaxAge:           12 * time.Hour,
    }
    if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
        conf.AllowAllOrigins = true
    }
    r.Use(cors.New(conf))
}

func contains(slice []string, val string) bool {
    for _, s := range slice {
        if s == val {
            return true
        }
    }
    return false
}

// setupAPIRoutes регистрирует и REST, и WebSocket
func setupAPIRoutes(r *gin.Engine) {
    api := r.Group("/api")
    {
        // Health-check
        api.GET("/health", func(c *gin.Context) {
            c.JSON(http.StatusOK, gin.H{
                "status":  "ok",
                "time":    time.Now(),
                "version": "1.1.0",
            })
        })

        // Авторизация и вебхук Telegram
        api.POST("/auth/login", handlers.Login)
        api.POST("/telegram/webhook", handlers.TelegramWebhook)

        // Виджет (на случай iframe/web widget)
        widget := api.Group("/widget")
        {
            widget.GET("/chat/:id/messages", handlers.GetWidgetChatMessages)
        }

        // Остальные API требуют авторизации
        auth := api.Group("/")
        auth.Use(middleware.AuthMiddleware())
        {
            // HTTP-для чатов убраны → используем только /ws
            // сюда можно добавить другие защищённые эндпойнты
        }
    }

    // Точка входа для WebSocket
    // Клиенты подключаются к ws://yourhost/ws
    r.GET("/ws", handlers.ServeWs)
}