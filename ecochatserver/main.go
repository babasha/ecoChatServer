package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/browser"
	"github.com/egor/ecochatserver/cronservice"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/handlers"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/middleware"
	"github.com/egor/ecochatserver/models"
	"github.com/egor/ecochatserver/websocket"
)

func main() {
	
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	logInfo("EcoChat server starting...")

	// Создаем context для graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Загружаем .env (только для dev)
	_ = godotenv.Load() // Игнорируем ошибку - используем переменные окружения

	// ─── PostgreSQL ──────────────────────────────────────────────────────────
	if err := database.Init(); err != nil {
		log.Fatalf("Ошибка инициализации базы данных: %v", err)
	}
	defer database.Close()

	// ─── LLM Usage Logger ────────────────────────────────────────────────────
	if err := llm.InitUsageLogger(); err != nil {
		logWarning("WARNING: LLM usage logger initialization failed: " + err.Error())
	}
	defer llm.CloseUsageLogger()

	// Wire embedding function to break database→llm import cycle
	database.EmbedFunc = func(ctx context.Context, text string) ([]float64, error) {
		client := llm.GetEmbeddingClient()
		if client == nil {
			return nil, nil
		}
		return client.Embed(ctx, text)
	}

	// Периодически обновляем партиции
	go func(ctx context.Context) {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := database.RefreshPartitions(); err != nil {
					logError("Ошибка обновления партиций: " + err.Error())
				}
			case <-ctx.Done():
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
				handlers.AutoArchiveInactiveChats()
				handlers.DeleteExpiredArchives()
			case <-ctx.Done():
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

	// ─── CronService (гибкое расписание вместо фиксированного ticker) ──────
	cronSvc := cronservice.New()

	// Register action handlers
	cronSvc.RegisterAction("analyze", func(actionCtx context.Context, job *models.CronJob) error {
		adkAR, ok := handlers.AutoResponder.(*adkagent.ADKAutoResponderV2)
		if !ok || adkAR == nil {
			return fmt.Errorf("autoresponder not initialized")
		}
		log.Printf("[CRON] Running analysis for job: %s", job.Name)
		return adkAR.TriggerDirectorAnalysis(actionCtx)
	})

	cronSvc.RegisterAction("send_message", func(actionCtx context.Context, job *models.CronJob) error {
		cfg, err := cronservice.ParseSendMessageConfig(job.ActionConfig)
		if err != nil {
			return err
		}
		// Save to Director memory as a reminder/note
		database.SaveAutoMemory("fact",
			fmt.Sprintf("cron_msg:%s:%s", job.Name, time.Now().Format("20060102-1504")),
			cfg.Message, []string{"cron", "scheduled"}, nil)
		log.Printf("[CRON] Message saved to memory: %s", cfg.Message[:min(len(cfg.Message), 80)])
		return nil
	})

	// Make CronService available to handlers (for Director tools)
	handlers.CronService = cronSvc

	// Start with warmup delay
	go func() {
		time.Sleep(5 * time.Minute) // дать системе прогреться
		if err := cronSvc.Start(ctx); err != nil {
			log.Printf("[CRON] Failed to start: %v", err)
			return
		}

		// Seed default analysis job if no jobs exist
		jobs, _ := cronSvc.ListJobs()
		if len(jobs) == 0 {
			log.Println("[CRON] No jobs found, seeding default 6h analysis job")
			cronSvc.AddJob(&models.CronJobRequest{
				Name:         "director_analysis_6h",
				Description:  "Периодический анализ Director каждые 6 часов",
				ScheduleType: "every",
				Schedule:     "6h",
				Action:       "analyze",
			}, "system")
		}
	}()

	// ─── Browser service (headless Chrome for Director) ─────────────────────
	if database.GetSettingBool("DIRECTOR_BROWSER_ENABLED", false) {
		browserSvc := browser.New()

		// Load allowlist from DB setting (comma-separated domains/patterns)
		if allowListStr := database.GetSetting("DIRECTOR_BROWSER_ALLOWLIST", ""); allowListStr != "" {
			patterns := strings.Split(allowListStr, ",")
			for i := range patterns {
				patterns[i] = strings.TrimSpace(patterns[i])
			}
			browserSvc.SetAllowList(patterns)
			log.Printf("[BROWSER] Allowlist: %v", patterns)
		}

		if err := browserSvc.Start(); err != nil {
			log.Printf("[BROWSER] Warning: failed to start: %v", err)
		} else {
			handlers.BrowserService = browserSvc
		}
	}

	// ─── Инициализация буферов логов ─────────────────────────────────────────
	handlers.InitLogBuffers()

	// Устанавливаем функцию логирования для middleware
	middleware.SetLogFunc(func(level, message, source string) {
		handlers.AddServerLog(handlers.LogLevel(level), message, source)
	})

	// ─── Инициализация настроек сервера ───────────────────────────────────────
	handlers.InitServerSettings()

	// ─── REST API & WebSocket ───────────────────────────────────────────────
	setupAPIRoutes(r)

	// ─── HTTP-server ─────────────────────────────────────────────────────────
	addr := ":" + getEnv("PORT", "8080")
	logInfo("HTTP сервер запускается на " + addr)

	server := &http.Server{
		Addr:    addr,
		Handler: r,
		// Для WebSocket соединений не используем жесткие таймауты
		// ReadTimeout и WriteTimeout устанавливаются в 0 для WebSocket
		ReadTimeout:  0, // Отключаем для WebSocket
		WriteTimeout: 0, // Отключаем для WebSocket
		IdleTimeout:  120 * time.Second, // Увеличено для WebSocket
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
				ReadTimeout:  0, // Отключаем для WebSocket
				WriteTimeout: 0, // Отключаем для WebSocket
				IdleTimeout:  120 * time.Second, // Увеличено для WebSocket
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

	logInfo("Получен сигнал остановки, начинаем graceful shutdown...")

	// Отменяем context для всех горутин
	cancel()

	// Graceful shutdown HTTP сервера
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logError("Ошибка при остановке сервера: " + err.Error())
	}

	if httpsServer != nil {
		if err := httpsServer.Shutdown(shutdownCtx); err != nil {
			logError("Ошибка при остановке HTTPS сервера: " + err.Error())
		}
	}

	logInfo("✓ Сервер остановлен успешно")
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
		logInfo("Статистический сервер запускается на порту " + statsPort)
		if err := statsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logError("Ошибка запуска статистического сервера: " + err.Error())
		}
	}()

	// Ждем сигнала остановки
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := statsServer.Shutdown(shutdownCtx); err != nil {
		logError("Ошибка остановки статистического сервера: " + err.Error())
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
		logWarning("ВНИМАНИЕ: Разрешены все источники CORS (ALLOW_ALL_ORIGINS=true)")
	} else {
		// Разрешаем только указанные домены
		allow := []string{"http://localhost:3000", "http://localhost:1420"}

		// Добавляем адреса из переменных окружения. ALLOWED_ORIGINS — основное
		// имя, под которое заточен .env на проде (CORS — only ucargo frontend);
		// FRONTEND_URL/ADDITIONAL_ALLOWED_ORIGINS оставлены для legacy-конфигов.
		for _, key := range []string{"ALLOWED_ORIGINS", "FRONTEND_URL", "ADDITIONAL_ALLOWED_ORIGINS"} {
			if v := os.Getenv(key); v != "" {
				for _, u := range strings.Split(v, ",") {
					u = strings.TrimSpace(u)
					if u != "" && !slices.Contains(allow, u) {
						allow = append(allow, u)
					}
				}
			}
		}

		conf = cors.Config{
			AllowOriginFunc: func(origin string) bool {
				// Разрешаем все домены из списка
				if slices.Contains(allow, origin) {
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

		// Gemini OAuth (бесплатный доступ через Google аккаунт)
		geminiOAuth := handlers.NewGeminiOAuthConfig()
		api.GET("/llm/gemini-oauth/start", handlers.MakeStartOAuthHandler(geminiOAuth))
		api.GET("/llm/gemini-oauth/status", handlers.MakeOAuthStatusHandler(geminiOAuth))
		api.POST("/llm/gemini-oauth/manual", handlers.MakeOAuthManualCallbackHandler(geminiOAuth))
		api.POST("/llm/gemini-oauth/poll", handlers.MakeOAuthPollHandler(geminiOAuth))
		api.POST("/llm/gemini-oauth/disconnect", handlers.MakeOAuthDisconnectHandler(geminiOAuth))

		// OpenAI OAuth (ChatGPT через подписку Plus/Pro)
		openaiOAuth := handlers.NewOpenAIOAuthConfig()
		api.GET("/llm/openai-oauth/start", handlers.MakeStartOAuthHandler(openaiOAuth))
		api.GET("/llm/openai-oauth/status", handlers.MakeOAuthStatusHandler(openaiOAuth))
		api.POST("/llm/openai-oauth/manual", handlers.MakeOAuthManualCallbackHandler(openaiOAuth))
		api.POST("/llm/openai-oauth/poll", handlers.MakeOAuthPollHandler(openaiOAuth))
		api.POST("/llm/openai-oauth/disconnect", handlers.MakeOAuthDisconnectHandler(openaiOAuth))

		// Claude OAuth (Claude через подписку Pro/Max)
		claudeOAuth := handlers.NewClaudeOAuthConfig()
		api.GET("/llm/claude-oauth/start", handlers.MakeStartOAuthHandler(claudeOAuth))
		api.GET("/llm/claude-oauth/status", handlers.MakeOAuthStatusHandler(claudeOAuth))
		api.POST("/llm/claude-oauth/callback", handlers.MakeOAuthManualCallbackHandler(claudeOAuth))
		api.POST("/llm/claude-oauth/disconnect", handlers.MakeOAuthDisconnectHandler(claudeOAuth))

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
		api.POST("/auth/register", middleware.StrictRateLimitMiddleware(), handlers.RegisterHandler)
		api.POST("/auth/login", middleware.StrictRateLimitMiddleware(), handlers.LoginHandler)
		api.GET("/auth/me", middleware.SessionMiddleware(), handlers.MeHandler)
		api.GET("/auth/ws-token", middleware.SessionMiddleware(), handlers.WSTokenHandler)
		api.POST("/auth/session", handlers.SessionRestoreHandler) // OAuth login session restore
		api.POST("/auth/logout", handlers.LogoutHandler)

		// OAuth 2.0 token endpoint для админки (DEPRECATED - используйте /auth/login)
		api.POST("/auth/token", middleware.StrictRateLimitMiddleware(), handlers.TokenHandler)

		// ─── moooving integration ─────────────────────────────────────────
		// Server-to-server endpoints (вызываются из moooving Rust-бекенда)
		moo := api.Group("/moooving")
		// Strict (5/min) был скопирован из /auth/login — он отбивает легитимный
		// трафик: все s2s-вызовы летят с одного IP moooving-VPS, а unread-poll
		// + auto_open_chat на двух драйверах уже выбирает квоту за секунды.
		// Шаред-секрет (для s2s) и JWT (для fe) — настоящий гейт; здесь
		// держим только мягкий per-IP cap как страховку от DDoS.
		moo.Use(middleware.ModerateRateLimitMiddleware())
		{
			s2s := moo.Group("/")
			s2s.Use(handlers.MooovingSharedSecretMiddleware())
			{
				s2s.POST("/chat/open", handlers.MooovingOpenChat)
				s2s.POST("/chat/close", handlers.MooovingCloseChat)
				s2s.POST("/chat/token", handlers.MooovingIssueToken)
				s2s.POST("/chats/unread", handlers.MooovingUnread)
				s2s.POST("/push/subscribe",   handlers.MooovingPushSubscribe)
				s2s.POST("/push/unsubscribe", handlers.MooovingPushUnsubscribe)
				s2s.GET("/push/vapid-key",    handlers.MooovingVAPIDKey)
			}

			// Frontend endpoints (driver/client из moooving приложения)
			fe := moo.Group("/")
			fe.Use(handlers.MooovingTokenMiddleware())
			{
				fe.GET("/chats", handlers.MooovingMyChats)
				fe.GET("/chats/:id/messages", handlers.MooovingChatMessages)
			}
		}

		// Webhook для Telegram и других внешних сервисов
		api.POST("/telegram/webhook", handlers.TelegramWebhook)
		api.GET("/instagram/webhook", handlers.InstagramWebhookVerify)
		api.POST("/instagram/webhook", handlers.InstagramWebhook)
		// Instagram demo endpoint - поиск существующего чата (ТОЛЬКО для demo страницы)
		api.POST("/instagram/chat/find", handlers.FindInstagramChat)

		// Instagram OAuth flow (публичный доступ для callback от Facebook)
		api.GET("/instagram/oauth/init", handlers.InstagramOAuthInitiate)
		api.GET("/instagram/oauth/callback", handlers.InstagramOAuthCallback)

		// WhatsApp webhook
		api.GET("/whatsapp/webhook", handlers.WhatsAppWebhookVerify)
		api.POST("/whatsapp/webhook", handlers.WhatsAppWebhook)

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

			// Глобальные настройки автоответчика
			admin.GET("/admin/autoresponder/global", handlers.GetGlobalAutoResponderStatus)
			admin.PUT("/admin/autoresponder/global", handlers.ToggleGlobalAutoResponder)

			// Управление командой (supervisors и managers)
			admin.GET("/admin/supervisors", handlers.GetAllSupervisors)
			admin.POST("/admin/supervisors/access", handlers.AddSupervisorAccess)
			admin.DELETE("/admin/supervisors/access", handlers.RemoveSupervisorAccess)
			admin.GET("/admin/supervisors/:id/businesses", handlers.GetSupervisorBusinesses)
			admin.GET("/admin/clients", handlers.GetAllClients)
			admin.POST("/admin/managers", handlers.AddManager)
			admin.DELETE("/admin/managers/:id", handlers.RemoveManager)

			// Управление пользователями (регистрация и подтверждение)
			admin.GET("/admin/users/pending", handlers.GetPendingUsers)
			admin.PUT("/admin/users/role", handlers.UpdateUserRole)
			admin.DELETE("/admin/users/:id", handlers.DeleteUser)

			// LLM Usage Analytics (защищённые эндпоинты для админки)
			admin.GET("/llm-usage/stats", handlers.GetLLMUsageStats)
			admin.GET("/llm-usage/summary", handlers.GetLLMUsageSummary)

			// Архивирование чатов
			admin.POST("/chats/:id/resolve", handlers.ResolveChat)
			admin.GET("/archived-chats", handlers.GetArchivedChats)
			admin.GET("/archived-chats/:id/messages", handlers.GetArchivedChatMessages)
			admin.POST("/archived-chats/:id/toggle-timer", handlers.ToggleArchiveTimer)

			// Instagram OAuth management (требует авторизации)
			admin.GET("/instagram/status", handlers.GetInstagramAccountStatus)
			admin.POST("/instagram/disconnect", handlers.DisconnectInstagramAccount)

			// Director chat (Level 2 AI agent conversation)
			admin.POST("/director/chat", handlers.DirectorChatMessage)
			admin.GET("/director/chat/history", handlers.DirectorChatHistory)
			admin.DELETE("/director/chat/history", handlers.DirectorChatClear)
			admin.POST("/director/analyze", handlers.DirectorAnalyze)
			admin.GET("/director/data", handlers.DirectorData)
			admin.GET("/director/tasks", handlers.DirectorTasks)
			admin.POST("/director/tasks/comment", handlers.DirectorTaskAddComment)
			admin.GET("/director/agent-conversations", handlers.DirectorAgentConversations)
			admin.POST("/director/agent-conversations", handlers.DirectorAgentConversationSend)
			admin.GET("/director/settings", handlers.GetDirectorSettings)
			admin.PUT("/director/settings", handlers.UpdateDirectorSettings)

			// Director webhook monitoring (admin-only)
			admin.GET("/director/webhooks/events", handlers.DirectorWebhookEvents)
			admin.GET("/director/webhooks/stats", handlers.DirectorWebhookStats)

			// AI Chat (generalized role-based chat: Director, Responder, Translator, Global)
			admin.POST("/ai/chat", handlers.AIChatMessage)
			admin.GET("/ai/chat", handlers.AIChatHistory)
			admin.DELETE("/ai/chat", handlers.AIChatClear)
			admin.GET("/ai/chat/roles", handlers.AIChatRoles)

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

	// Meta webhook endpoints — доступны без /api префикса (как требует Meta Developer)
	r.GET("/webhook/instagram", handlers.InstagramWebhookVerify)
	r.POST("/webhook/instagram", handlers.InstagramWebhook)
	r.GET("/webhook/whatsapp", handlers.WhatsAppWebhookVerify)
	r.POST("/webhook/whatsapp", handlers.WhatsAppWebhook)

	// Director webhook — внешние системы триггерят анализ (Grafana, CRM, мониторинг)
	// Auth: Bearer token / X-Webhook-Token / HMAC (X-Hub-Signature-256)
	r.POST("/webhook/director", handlers.DirectorWebhookTrigger)
	r.GET("/webhook/director/verify", handlers.DirectorWebhookVerify)

	// Instagram OAuth callback (redirect URI настроен в Meta как /auth/instagram/callback)
	// Используем Facebook Business Login flow — он поддерживает Instagram DM permissions
	r.GET("/auth/instagram/callback", handlers.InstagramOAuthCallback)

	// WebSocket точка подключения - основной механизм взаимодействия с сервером
	r.GET("/ws", handlers.ServeWs)

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
