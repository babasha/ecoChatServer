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
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/middleware"
	"github.com/egor/ecochatserver/websocket"
)

func main() {
	// Логи по файлу и строке
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	logInfo("EcoChat server starting with anti-duplication optimizations…")

	// Создаем context для graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Загружаем .env (только для dev)
	if err := godotenv.Load(); err != nil {
		logInfo("Примечание: файл .env не найден или не загружен, используем переменные окружения")
	}

	// ─── PostgreSQL ──────────────────────────────────────────────────────────
	if err := database.Init(); err != nil {
		log.Fatalf("Ошибка инициализации базы данных: %v", err)
	}
	defer database.Close()

	// ─── LLM Usage Logger ────────────────────────────────────────────────────
	if err := llm.InitUsageLogger(); err != nil {
		logWarning("WARNING: LLM usage logger initialization failed: " + err.Error())
		logWarning("LLM usage logging will be disabled")
	} else {
		logInfo("✓ LLM usage logger initialized")
	}
	defer llm.CloseUsageLogger()

	// Простое кэширование инициализировано
	logInfo("Простое кэширование инициализировано")

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

	// Примечание: Очистка старых логов запускается автоматически в handlers.InitLogBuffers()
	// через StartLogCleanupScheduler() - каждый час, первый раз через 5 минут после старта

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
	logInfo("Автоответчик инициализирован")

	// ─── Инициализация буферов логов ─────────────────────────────────────────
	handlers.InitLogBuffers()

	// Устанавливаем функцию логирования для middleware
	middleware.SetLogFunc(func(level, message, source string) {
		handlers.AddServerLog(handlers.LogLevel(level), message, source)
	})

	logInfo("Буферы логов инициализированы")

	// ─── Инициализация настроек сервера ───────────────────────────────────────
	handlers.InitServerSettings()
	logInfo("Настройки сервера инициализированы")

	// ─── REST API & WebSocket ───────────────────────────────────────────────
	setupAPIRoutes(r)
	logInfo("API маршруты настроены")

	// ─── HTTP-server ─────────────────────────────────────────────────────────
	addr := ":" + getEnv("PORT", "8080")
	logInfo("HTTP сервер запускается на " + addr)

	server := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Запускаем HTTP сервер в отдельной горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска HTTP сервера: %v", err)
		}
	}()

	var httpsServer *http.Server
	tlsCertFile := os.Getenv("TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("TLS_KEY_FILE")
	enableHTTPS := strings.EqualFold(os.Getenv("ENABLE_HTTPS"), "true") ||
		(tlsCertFile != "" && tlsKeyFile != "")

	if enableHTTPS {
		if tlsCertFile == "" || tlsKeyFile == "" {
			logWarning("HTTPS не запущен: TLS_CERT_FILE и TLS_KEY_FILE должны быть заданы")
		} else {
			httpsAddr := ":" + getEnv("HTTPS_PORT", "8443")
			logInfo("HTTPS сервер запускается на " + httpsAddr)

			httpsServer = &http.Server{
				Addr:         httpsAddr,
				Handler:      r,
				ReadTimeout:  15 * time.Second,
				WriteTimeout: 15 * time.Second,
				IdleTimeout:  60 * time.Second,
			}

			go func() {
				if err := httpsServer.ListenAndServeTLS(tlsCertFile, tlsKeyFile); err != nil && err != http.ErrServerClosed {
					log.Fatalf("Ошибка запуска HTTPS сервера: %v", err)
				}
			}()
		}
	} else {
		logInfo("HTTPS сервер отключен (ENABLE_HTTPS=false и путь к сертификату не указан)")
	}

	logInfo("✓ Сервер запущен успешно")

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

	if httpsServer != nil {
		if err := httpsServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Ошибка при остановке HTTPS сервера: %v", err)
		}
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
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Widget-User-ID", "X-API-Key", "X-Hub-Signature-256"},
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
		log.Printf("CORS также разрешает все Vercel preview deployments (*.vercel.app)")

		conf = cors.Config{
			AllowOriginFunc: func(origin string) bool {
				// Разрешаем все домены из списка
				if contains(allow, origin) {
					return true
				}
				// Разрешаем все Vercel preview deployments
				if strings.HasSuffix(origin, ".vercel.app") {
					return true
				}
				return false
			},
			AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Widget-User-ID", "X-API-Key", "Cookie", "X-Hub-Signature-256"},
			ExposeHeaders:    []string{"Content-Length", "X-Request-ID", "Set-Cookie"},
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

		// Логи для админки (in-memory буферы)
		api.GET("/logs/server", handlers.GetServerLogs)
		api.GET("/logs/websocket", handlers.GetWebSocketLogs)

		// Логи из БД (для аналитики, с фильтрами и пагинацией)
		api.GET("/logs/server/db", handlers.GetServerLogsFromDB)
		api.GET("/logs/websocket/db", handlers.GetWebSocketLogsFromDB)

		// Активные сессии и управление ими
		api.GET("/sessions/active", handlers.GetActiveSessions)
		api.POST("/sessions/:id/disconnect", handlers.DisconnectSession)

		// Аналитика базы данных
		api.GET("/analytics", handlers.GetAnalytics)

		// Настройки сервера
		api.GET("/server-settings", handlers.GetServerSettings)
		api.PUT("/server-settings", handlers.UpdateServerSettings)

		// LLM Settings API (app_settings based)
		api.GET("/settings", handlers.GetSettings)                         // Все настройки как map
		api.GET("/settings/llm", handlers.GetLLMSettingsSimple)            // LLM настройки в структуре для фронтенда
		api.PUT("/settings/llm", handlers.UpdateLLMSettingsSimple)         // Обновление LLM настроек с hot-swap
		api.PUT("/settings/batch", handlers.UpdateSettingsBatch)           // Batch обновление настроек
		api.POST("/settings/llm/test", handlers.TestLLMProviderConnection) // Тестирование LLM подключения
		api.POST("/llm/reload", handlers.ReloadLLMProvider)                // Ручной reload провайдера
		api.POST("/llm/test", handlers.TestLLMProviderConnection)          // Альтернативный endpoint для теста

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

		// Session cookie authentication для Next.js admin panel
		api.POST("/auth/login", middleware.StrictRateLimitMiddleware(), handlers.LoginHandler)
		api.GET("/auth/me", middleware.SessionMiddleware(), handlers.MeHandler)
		api.POST("/auth/logout", handlers.LogoutHandler)

		// OAuth 2.0 token endpoint для админки (DEPRECATED - используйте /auth/login)
		api.POST("/auth/token", middleware.StrictRateLimitMiddleware(), handlers.TokenHandler)

		// Webhook для Telegram и других внешних сервисов
		api.POST("/telegram/webhook", handlers.TelegramWebhook)
		api.GET("/instagram/webhook", handlers.InstagramWebhookVerify)
		api.POST("/instagram/webhook", handlers.InstagramWebhook)
		// Instagram demo endpoint - поиск существующего чата (ТОЛЬКО для demo страницы)
		api.POST("/instagram/chat/find", handlers.FindInstagramChat)

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

		// Защищенные API-маршруты для админки (session cookie authentication)
		admin := api.Group("/")
		admin.Use(middleware.SessionMiddleware()) // ← Session cookie вместо Bearer token
		admin.Use(middleware.ModerateRateLimitMiddleware())
		{
			// Получение списка чатов для админки
			admin.GET("/chats", handlers.GetChats)

			// Получение конкретного чата с сообщениями
			admin.GET("/chats/:id", handlers.GetChatByID)

			// Отправка сообщения в чат от админа
			admin.POST("/chats/:id/messages", handlers.SendMessageToChat)

			// Управление автоответчиком для чата
			admin.PUT("/chats/:id/auto-responder", handlers.ToggleAutoResponder)

			// Пометка сообщений как прочитанных
			admin.POST("/chats/:id/read", handlers.MarkChatMessagesAsRead)

			// Push notifications for admin panel
			admin.POST("/push/subscribe", handlers.PushSubscribeHandler)
			admin.POST("/push/unsubscribe", handlers.PushUnsubscribeHandler)
			admin.POST("/push/send", handlers.PushSendHandler)

			// Настройки админа (языковые предпочтения)
			admin.GET("/admin/settings", handlers.GetAdminSettings)
			admin.PUT("/admin/settings", handlers.UpdateAdminSettings)

			// LLM Usage Analytics (защищённые эндпоинты для админки)
			admin.GET("/llm-usage/stats", handlers.GetLLMUsageStats)
			admin.GET("/llm-usage/summary", handlers.GetLLMUsageSummary)

			// Архивирование чатов
			admin.POST("/chats/:id/resolve", handlers.ResolveChat)
			admin.GET("/archived-chats", handlers.GetArchivedChats)
			admin.GET("/archived-chats/:id/messages", handlers.GetArchivedChatMessages)
			admin.POST("/archived-chats/:id/toggle-timer", handlers.ToggleArchiveTimer)

			// Статистика для администраторов
			admin.GET("/admin/stats", func(c *gin.Context) {
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

// logInfo логирует информационное сообщение в консоль и буфер
func logInfo(msg string) {
	log.Println(msg)
	handlers.AddServerLog(handlers.LogLevelInfo, msg, "Server")
}

// logError логирует ошибку в консоль и буфер
func logError(msg string) {
	log.Println(msg)
	handlers.AddServerLog(handlers.LogLevelError, msg, "Server")
}

// logWarning логирует предупреждение в консоль и буфер
func logWarning(msg string) {
	log.Println(msg)
	handlers.AddServerLog(handlers.LogLevelWarning, msg, "Server")
}
