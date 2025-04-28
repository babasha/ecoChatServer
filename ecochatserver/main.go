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
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("EcoChat server starting…")

	_ = godotenv.Load() // .env (dev)

	// ─── PostgreSQL ──────────────────────────────────────────────────────────
	if err := database.Init(); err != nil {
		log.Fatalf("db init: %v", err)
	}
	defer database.Close()

	// ─── Gin & middleware ───────────────────────────────────────────────────
	gin.SetMode(getEnv("GIN_MODE", gin.DebugMode))
	r := gin.New()
	r.Use(gin.Recovery(), middleware.Logger())
	setupCORS(r)

	// ─── WebSocket hub ───────────────────────────────────────────────────────
	hub := websocket.NewHub()
	go hub.Run()
	handlers.SetWebSocketHub(hub)
	
	// ─── Инициализация автоответчика ────────────────────────────────────────
	handlers.InitAutoResponder()

	// ─── REST API ────────────────────────────────────────────────────────────
	setupAPIRoutes(r, hub)

	// ─── HTTP-server ─────────────────────────────────────────────────────────
	addr := ":" + getEnv("PORT", "8080")
	log.Printf("HTTP listen %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// ─────────────────────────── helpers

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func setupCORS(r *gin.Engine) {
	allow := []string{"http://localhost:3000"}
	for _, k := range []string{"FRONTEND_URL", "ADDITIONAL_ALLOWED_ORIGINS"} {
		if v := os.Getenv(k); v != "" {
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

func contains(sl []string, v string) bool {
	for _, s := range sl {
		if s == v {
			return true
		}
	}
	return false
}

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
		api.POST("/auth/login",       handlers.Login)
		api.POST("/telegram/webhook", handlers.TelegramWebhook)

		// API для виджета
		widget := api.Group("/widget")
		{
			widget.GET("/chat/:id/messages", handlers.GetWidgetChatMessages)
		}

		auth := api.Group("/")
		auth.Use(middleware.AuthMiddleware())
		{
			ch := auth.Group("/chats")
			ch.GET("",               handlers.GetChats)
			ch.GET("/:id",           handlers.GetChatByID)
			ch.POST("/:id/messages", handlers.SendMessage)
		}
	}
	r.GET("/ws", func(c *gin.Context) {
		websocket.ServeWs(hub, c.Writer, c.Request)
	})
}