// main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/handlers"
	"github.com/egor/ecochatserver/middleware"
	"github.com/egor/ecochatserver/websocket"
)

func main() {
	// Логи по файлу и строке
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("EcoChat server starting with anti-duplication optimizations…")

	// Создаем context для graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Загружаем .env (только для dev)
	if err := godotenv.Load(); err != nil {
		log.Println("Примечание: файл .env не найден или не загружен, используем переменные окружения")
	}

	// ─── PostgreSQL ──────────────────────────────────────────────────────────
	if err := database.Init(); err != nil {
		log.Fatalf("Ошибка инициализации базы данных: %v", err)
	}
	defer database.Close()

	// Простое кэширование инициализировано
	log.Println("Простое кэширование инициализировано")

	// Периодически обновляем партиции
	go func(ctx context.Context) {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := database.RefreshPartitions(); err != nil {
					log.Printf("Ошибка обновления партиций: %v", err)
				} else {
					log.Println("Успешное обновление партиций")
				}
			case <-ctx.Done():
				log.Println("Остановка обновления партиций...")
				return
			}
		}
	}(ctx)

	// Периодически архивируем неактивные чаты и удаляем старые архивы
	go func(ctx context.Context) {
		// Первый запуск через минуту после старта
		time.Sleep(1 * time.Minute)

		ticker := time.NewTicker(30 * time.Minute) // Каждые 30 минут
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				log.Println("Запуск автоархивации неактивных чатов...")
				handlers.AutoArchiveInactiveChats()
				handlers.DeleteExpiredArchives()
			case <-ctx.Done():
				log.Println("Остановка автоархивации...")
				return
			}
		}
	}(ctx)

	// ─── Gin & middleware ───────────────────────────────────────────────────
	gin.SetMode(getEnv("GIN_MODE", gin.DebugMode))
	r := gin.New()
	r.Use(gin.Recovery(), middleware.Logger())

	// Простой middleware для дедупликации HTTP запросов
	r.Use(SimpleDeduplicationMiddleware())

	setupCORS(r)

	// ─── WebSocket hub ───────────────────────────────────────────────────────
	hub := websocket.NewHub()
	go hub.Run()

	// Устанавливаем хаб для использования в обработчиках
	handlers.WebSocketHub = hub

	// Запускаем веб-сервер для статистики WebSocket (опционально)
	go startStatsServer(ctx, hub)

	// ─── Автоответчик (если используется) ───────────────────────────────────
	handlers.InitAutoResponder()
	log.Println("Автоответчик инициализирован")

	// ─── Инициализация буферов логов ─────────────────────────────────────────
	handlers.InitLogBuffers()
	log.Println("Буферы логов инициализированы")

	// ─── Инициализация настроек сервера ───────────────────────────────────────
	handlers.InitServerSettings()
	log.Println("Настройки сервера инициализированы")

	// ─── REST API & WebSocket ───────────────────────────────────────────────
	setupAPIRoutes(r)
	log.Println("API маршруты настроены")

	// ─── HTTP-server ─────────────────────────────────────────────────────────
	addr := ":" + getEnv("PORT", "8080")
	log.Printf("HTTP сервер запускается на %s", addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Запускаем сервер в отдельной горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска HTTP сервера: %v", err)
		}
	}()

	log.Println("✓ Сервер запущен успешно")

	// Ожидаем сигнал остановки (Ctrl+C или SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Получен сигнал остановки, начинаем graceful shutdown...")

	// Отменяем context для всех горутин
	cancel()

	// Graceful shutdown HTTP сервера
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Ошибка при остановке сервера: %v", err)
	}

	log.Println("✓ Сервер остановлен успешно")
}

// SimpleDeduplicationMiddleware простой middleware для дедупликации HTTP запросов
func SimpleDeduplicationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Только для POST запросов
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}

		// Исключаем некоторые пути из дедупликации
		if strings.Contains(c.Request.URL.Path, "/auth/login") ||
			strings.Contains(c.Request.URL.Path, "/health") {
			c.Next()
			return
		}

		// Пропускаем дедупликацию для main.go пока функции не перенесены
		// TODO: Перенести логику дедупликации из telegram_handler в общий пакет
		c.Next()
	}
}

// startStatsServer запускает отдельный сервер для статистики WebSocket
func startStatsServer(ctx context.Context, hub *websocket.Hub) {
	if os.Getenv("ENABLE_STATS_SERVER") != "true" {
		return
	}

	statsPort := getEnv("STATS_PORT", "8081")
	statsRouter := gin.New()
	statsRouter.Use(gin.Recovery())

	// Добавляем middleware для базовой аутентификации
	statsRouter.Use(gin.BasicAuth(gin.Accounts{
		"admin": getEnv("STATS_PASSWORD", "password"),
	}))

	statsRouter.GET("/stats", func(c *gin.Context) {
		stats := hub.GetStats()
		activeClients := hub.GetActiveClients()

		c.JSON(http.StatusOK, gin.H{
			"stats":         stats,
			"activeClients": activeClients,
			"timestamp":     time.Now().Format(time.RFC3339),
			"optimizations": gin.H{
				"deduplication": "active",
			},
		})
	})

	statsServer := &http.Server{
		Addr:    ":" + statsPort,
		Handler: statsRouter,
	}

	// Запускаем сервер в отдельной горутине
	go func() {
		log.Printf("Статистический сервер запускается на порту %s", statsPort)
		if err := statsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Ошибка запуска статистического сервера: %v", err)
		}
	}()

	// Ждем сигнала остановки
	<-ctx.Done()
	log.Println("Остановка статистического сервера...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := statsServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Ошибка остановки статистического сервера: %v", err)
	}
}

// getEnv возвращает значение или дефолт
func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// setupCORS настраивает CORS с улучшенной логикой
func setupCORS(r *gin.Engine) {
	var conf cors.Config

	// Если разрешены все источники
	if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
		conf = cors.Config{
			AllowAllOrigins:  true,
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Widget-User-ID", "X-API-Key"},
			ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
			AllowCredentials: false, // Нельзя использовать credentials с AllowAllOrigins
			MaxAge:           12 * time.Hour,
		}
		log.Println("ВНИМАНИЕ: Разрешены все источники CORS (ALLOW_ALL_ORIGINS=true)")
	} else {
		// Разрешаем только указанные домены
		allow := []string{"http://localhost:3000"}

		// Добавляем адреса из переменных окружения
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

		log.Printf("CORS настроен для доменов: %v", allow)

		conf = cors.Config{
			AllowOrigins:     allow,
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Widget-User-ID", "X-API-Key"},
			ExposeHeaders:    []string{"Content-Length", "X-Request-ID"},
			AllowCredentials: true,
			MaxAge:           12 * time.Hour,
		}
	}

	r.Use(cors.New(conf))

	// Добавляем Request ID middleware
	r.Use(func(c *gin.Context) {
		requestID := uuid.New().String()
		c.Set("requestId", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	})
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
	// API-группа для HTTP-запросов
	api := r.Group("/api")
	{
		// Health-check с расширенными метриками системы
		api.GET("/health", handlers.GetHealthData)

		// Логи для админки
		api.GET("/logs/server", handlers.GetServerLogs)
		api.GET("/logs/websocket", handlers.GetWebSocketLogs)

		// Активные сессии и управление ими
		api.GET("/sessions/active", handlers.GetActiveSessions)
		api.POST("/sessions/:id/disconnect", handlers.DisconnectSession)

		// Аналитика базы данных
		api.GET("/analytics", handlers.GetAnalytics)

		// Настройки сервера
		api.GET("/server-settings", handlers.GetServerSettings)
		api.PUT("/server-settings", handlers.UpdateServerSettings)

		// Публичная статистика WebSocket (для Dashboard без аутентификации)
		api.GET("/stats/websocket", func(c *gin.Context) {
			stats := handlers.WebSocketHub.GetStats()
			activeClients := handlers.WebSocketHub.GetActiveClients()

			c.JSON(http.StatusOK, gin.H{
				"websocket": gin.H{
					"stats":         stats,
					"activeClients": activeClients,
				},
				"timestamp": time.Now().Format(time.RFC3339),
			})
		})

		// Авторизация через HTTP (строгий rate limit для защиты от brute force)
		api.POST("/auth/login", middleware.StrictRateLimitMiddleware(), handlers.Login)

		// Webhook для Telegram и других внешних сервисов
		api.POST("/telegram/webhook", handlers.TelegramWebhook)

		// Виджетный API (публичный, для iframe/web widget + мягкий rate limit)
		// Оставляем для обратной совместимости, но рекомендуем использовать WebSocket
		widget := api.Group("/widget")
		widget.Use(middleware.RelaxedRateLimitMiddleware())
		{
			// Получение информации о подключении к WebSocket
			widget.GET("/chat/:id/messages", handlers.GetWidgetChatMessages)

			// Добавляем новые эндпоинты для миграции на WebSocket
			widget.GET("/info", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{
					"websocket": gin.H{
						"url": "/ws",
						"params": gin.H{
							"type":    "widget",
							"chat_id": "CHAT_ID",
							"token":   "optional",
						},
					},
					"message": "Все функции чата доступны через WebSocket",
				})
			})
		}

		// Защищенные API-маршруты (требуется токен + rate limiting)
		auth := api.Group("/")
		auth.Use(middleware.AuthMiddleware())
		auth.Use(middleware.ModerateRateLimitMiddleware())
		{
			// Получение списка чатов для админки
			auth.GET("/chats", handlers.GetChats)

			// Получение конкретного чата с сообщениями
			auth.GET("/chats/:id", handlers.GetChatByID)

			// Отправка сообщения в чат от админа
			auth.POST("/chats/:id/messages", handlers.SendMessageToChat)

			// Управление автоответчиком для чата
			auth.PUT("/chats/:id/auto-responder", handlers.ToggleAutoResponder)

			// Пометка сообщений как прочитанных
			auth.POST("/chats/:id/read", handlers.MarkChatMessagesAsRead)

			// Настройки админа (языковые предпочтения)
			auth.GET("/admin/settings", handlers.GetAdminSettings)
			auth.PUT("/admin/settings", handlers.UpdateAdminSettings)

			// Архивирование чатов
			auth.POST("/chats/:id/resolve", handlers.ResolveChat)
			auth.GET("/archived-chats", handlers.GetArchivedChats)
			auth.GET("/archived-chats/:id/messages", handlers.GetArchivedChatMessages)
			auth.POST("/archived-chats/:id/toggle-timer", handlers.ToggleArchiveTimer)

			// Статистика для администраторов
			auth.GET("/admin/stats", func(c *gin.Context) {
				stats := handlers.WebSocketHub.GetStats()
				activeClients := handlers.WebSocketHub.GetActiveClients()

				c.JSON(http.StatusOK, gin.H{
					"websocket": gin.H{
						"stats":         stats,
						"activeClients": activeClients,
					},
					"timestamp": time.Now().Format(time.RFC3339),
				})
			})
		}
	}

	// WebSocket точка подключения - основной механизм взаимодействия с сервером
	r.GET("/ws", handlers.ServeWs)
	log.Println("WebSocket эндпоинт настроен: /ws")

	// Для обратной совместимости
	r.GET("/api/ws", handlers.ServeWs)

	// Статический контент для теста соединения
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "EcoChat WebSocket Server",
			"version": "1.2.0-optimized",
			"endpoints": gin.H{
				"websocket": "/ws",
				"health":    "/api/health",
				"login":     "/api/auth/login",
			},
			"features": []string{
				"message_deduplication",
				"auto_responder",
				"chat_management",
			},
		})
	})
}
