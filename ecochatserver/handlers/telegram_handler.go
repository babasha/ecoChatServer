package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/egor/ecochatserver/websocket"
)

// AutoResponderInterface определяет интерфейс для автоответчика
type AutoResponderInterface interface {
	ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error)
	ClearEscalation(chatID string)
}

// AutoResponder — единственный экземпляр автоответчика
var AutoResponder AutoResponderInterface

// Translator — сервис для перевода сообщений
var Translator *TranslationService

// InitAutoResponder инициализирует автоответчик (LLMклиент + конфиг)
func InitAutoResponder() {
	// Инициализируем Translator с отдельным провайдером (TRANSLATOR_PROVIDER/MODEL)
	// Если TRANSLATOR_PROVIDER не задан — используется глобальный провайдер
	translatorProvider, err := llm.NewProviderForRole(llm.RoleTranslator)
	if err != nil {
		log.Printf("⚠️ InitAutoResponder: не удалось создать провайдера для переводчика: %v", err)
		log.Printf("⚠️ Переводчик будет недоступен")
	} else {
		// 🎯 TOON FORMAT: для локальных моделей включаем TOON автоматически
		useTOON := database.GetSettingBool("USE_TOON_FORMAT", false)
		translatorType := database.GetSetting("TRANSLATOR_PROVIDER", "")
		if translatorType == "" {
			translatorType = database.GetSetting("LLM_PROVIDER", "gemini")
		}
		if translatorType == "lmstudio" || translatorType == "ollama" {
			useTOON = true
			log.Printf("🎯 TOON автоматически включен для локального переводчика: %s", translatorType)
		}
		Translator = NewTranslationServiceWithTOON(translatorProvider, WebSocketHub, useTOON)

		if useTOON {
			log.Printf("✅ Сервис перевода инициализирован с TOON форматом (экономия ~40%% токенов)")
		} else {
			log.Printf("✅ Сервис перевода инициализирован с JSON форматом")
		}
	}

	// При hot-swap: очищаем кэш агентов старого автоответчика,
	// чтобы новые агенты создались с актуальным LLM провайдером
	if AutoResponder != nil {
		if adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2); ok {
			count, _ := adkAR.GetAgentCacheStats()
			adkAR.ClearAllAgents()
			log.Printf("🔄 Hot-swap: очищен кэш агентов (%d удалено)", count)
		}
	}

	// Проверяем включен ли автоответчик (из БД с fallback на ENV)
	enabled := database.GetSettingBool("ENABLE_AUTO_RESPONDER", true)
	if !enabled {
		log.Println("🔇 Автоответчик отключен в настройках БД/ENV (переводчик работает)")
		AutoResponder = nil
		return
	}
	log.Println("🤖 Автоответчик включен, инициализируем...")

	ctx := context.Background()
	cfg := llm.GetDefaultConfig()

	adkAutoResponder, err := adkagent.InitADKAutoResponder(ctx, cfg)
	if err != nil {
		log.Printf("❌ InitAutoResponder: не удалось создать ADK агента: %v", err)
		log.Println("⚠️ Автоответчик будет недоступен")
		return
	}

	AutoResponder = adkAutoResponder
	log.Printf("✅ ADK AutoResponder инициализирован с hot-swap поддержкой")

	// Initialize embedding client for Director semantic memory search
	// Priority: EMBEDDING_* settings → DIRECTOR_* settings → LLM_* settings
	embProvider := database.GetSetting("EMBEDDING_PROVIDER", "")
	embKey := database.GetSetting("EMBEDDING_API_KEY", "")
	embModel := database.GetSetting("EMBEDDING_MODEL", "")
	if embProvider == "" {
		// Fallback to Director or global LLM settings
		embProvider = database.GetSetting("DIRECTOR_PROVIDER", database.GetSetting("LLM_PROVIDER", ""))
		embKey = database.GetSetting("DIRECTOR_API_KEY", database.GetSetting("LLM_API_KEY", ""))
	}
	// Only openai and gemini support embeddings
	if embProvider == "openai" || embProvider == "gemini" {
		llm.InitEmbeddingClient(embProvider, embKey, embModel)
	} else if embProvider != "" {
		log.Printf("[EMBEDDING] Provider '%s' does not support embeddings, semantic search disabled", embProvider)
	}

	log.Println("✅ Автоответчик успешно инициализирован")
}

// TelegramWebhook обрабатывает вебхук Telegram и виджета
func TelegramWebhook(c *gin.Context) {
	log.Printf("TelegramWebhook: %s %s from %s", c.Request.Method, c.FullPath(), c.ClientIP())

	// OPTIONS для CORS
	if c.Request.Method == http.MethodOptions {
		handleCORS(c)
		c.Status(http.StatusOK)
		return
	}
	handleCORS(c)

	// Проверяем Content-Type
	if !strings.Contains(c.GetHeader("Content-Type"), "application/json") {
		log.Printf("TelegramWebhook: неверный Content-Type: %s", c.GetHeader("Content-Type"))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Content-Type должен быть application/json"})
		return
	}

	// Парсим входящее сообщение
	var in models.IncomingMessage
	if err := c.ShouldBindJSON(&in); err != nil {
		log.Printf("TelegramWebhook: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: получено сообщение: %+v", in)

	// Логируем widgetBusinessID для отладки
	if in.WidgetBusinessID != nil {
		log.Printf("TelegramWebhook: widgetBusinessID = '%s'", *in.WidgetBusinessID)
	} else {
		log.Printf("TelegramWebhook: widgetBusinessID = nil")
	}

	if in.UserID == "" {
		log.Printf("TelegramWebhook: отсутствует UserID")
		c.JSON(http.StatusBadRequest, gin.H{"error": "UserID обязателен"})
		return
	}
	if in.ClientID == "" {
		in.ClientID = "test_client_id"
		log.Printf("TelegramWebhook: ClientID не указан, используем: %s", in.ClientID)
	} else {
		log.Printf("TelegramWebhook: используем ClientID: %s", in.ClientID)
	}

	// Создаём или получаем чат (БЕЗ загрузки истории сообщений для оптимизации)
	log.Printf("TelegramWebhook: создаем/получаем чат для user=%s, source=%s, botID=%s, clientID=%s",
		in.UserID, in.Source, in.BotID, in.ClientID)

	chat, err := database.GetOrCreateChatMetadata(
		in.UserID, in.UserName, in.UserEmail,
		in.Source, in.UserID, in.BotID, in.ClientID,
		in.WidgetBusinessID, // передаем widgetBusinessID из запроса
	)
	if err != nil {
		log.Printf("TelegramWebhook: GetOrCreateChatMetadata error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: получен чат: ID=%s, ClientID=%s, UserID=%s, AutoResponderEnabled=%v, IsNewChat=%v",
		chat.ID, chat.ClientID, chat.User.ID, chat.AutoResponderEnabled, chat.IsNewChat)

	// ВАЖНО: Обновляем chat_id для виджета, если он подключен с временным ID
	// Передаем chat.User.SourceID для проверки безопасности
	if WebSocketHub.UpdateWidgetChatID(in.UserID, chat.ID, chat.User.SourceID) {
		log.Printf("TelegramWebhook: виджет %s успешно обновлен с chat_id=%s", in.UserID, chat.ID)
	}

	// Переводим сообщение пользователя, если включен переводчик
	messageContent := in.Content
	messageMetadata := in.Metadata
	if messageMetadata == nil {
		messageMetadata = make(map[string]interface{})
	}

	// Сохраняем оригинальный текст для LLM
	originalContent := in.Content

	if Translator != nil {
		log.Printf("TelegramWebhook: перевод сообщения пользователя")
		result, err := Translator.TranslateUserMessage(c.Request.Context(), in.Content, chat.ID)
		if err != nil {
			log.Printf("TelegramWebhook: ошибка перевода сообщения: %v", err)
			// Продолжаем с оригинальным текстом
		} else if result != nil {
			// ВАЖНО: Сохраняем ОРИГИНАЛ в content, перевод в metadata
			messageContent = originalContent // Оставляем оригинальный текст!

			// result.Metadata уже содержит translations - просто добавляем все метаданные
			for k, v := range result.Metadata {
				messageMetadata[k] = v
			}

			log.Printf("TelegramWebhook: перевод завершен, wasTranslated=%v, detectedLang=%s",
				result.WasTranslated, result.DetectedLanguage)
		}
	}

	// Создаем детерминированный UUID для отправителя
	var userUUID uuid.UUID
	if parsedUUID, err := uuid.Parse(in.UserID); err == nil {
		userUUID = parsedUUID
		log.Printf("TelegramWebhook: UserID %s уже является валидным UUID", in.UserID)
	} else {
		userUUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(in.UserID))
		log.Printf("TelegramWebhook: создан детерминированный UUID для userID %s: %s", in.UserID, userUUID.String())
	}

	// Добавляем сообщение пользователя
	msgType := "text"
	if in.MessageType != "" {
		msgType = in.MessageType
	}

	log.Printf("TelegramWebhook: добавляем сообщение в чат %s от пользователя %s",
		chat.ID, userUUID)

	userMsg, err := database.AddMessage(
		chat.ID,
		messageContent, // Сохраняем оригинальный текст пользователя
		"user",
		userUUID,
		msgType,
		messageMetadata, // Метаданные содержат translations для админа
	)
	if err != nil {
		log.Printf("TelegramWebhook: AddMessage error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("TelegramWebhook: сообщение добавлено: ID=%s", userMsg.ID)

	// Генерируем автоответ, если включено
	botMsg := runAutoResponder(c.Request.Context(), chat, userMsg, false)

	// ВАЖНО: Отправляем WebSocket уведомления
	if userMsg != nil {
		// Создаем общую информацию о чате (используется в обоих уведомлениях)
		chatInfo := createChatInfo(chat)

		// Отправляем уведомление о сообщении пользователя
		// Виджет получает оригинал (клиент пишет на своём языке)
		widgetNotification := createMessageNotification(chat.ID, userMsg, chatInfo)
		widgetSent := WebSocketHub.SendToChat(chat.ID.String(), widgetNotification)
		log.Printf("TelegramWebhook: уведомление виджету отправлено (%d клиентов)", widgetSent)

		// Админы получают персонализированный перевод (каждый на своём языке)
		adminsSent := WebSocketHub.SendToAllAdminsWithCallback(func(adminClient *websocket.Client) []byte {
			adminLang := getAdminLanguage(adminClient.UserID)
			personalizedMsg := applyTranslationForAdmin(userMsg, adminLang)
			return createMessageNotification(chat.ID, personalizedMsg, chatInfo)
		})
		log.Printf("TelegramWebhook: уведомление отправлено %d админам (персонализировано)", adminsSent)

		// Если есть автоответ бота, отправляем его отдельно
		if botMsg != nil {
			botNotification := createMessageNotification(chat.ID, botMsg, chatInfo)
			totalSent := WebSocketHub.SendToChatAndAdmins(chat.ID.String(), botNotification)
			log.Printf("TelegramWebhook: уведомление о сообщении бота отправлено %d клиентам", totalSent)
		}
	}

	// Ответ клиенту
	response := gin.H{
		"success":    true,
		"message":    "Сообщение обработано",
		"message_id": userMsg.ID.String(),
		"chat_id":    chat.ID.String(),
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	log.Printf("TelegramWebhook: отправляем ответ: %+v", response)
	c.JSON(http.StatusOK, response)
}

// handleCORS выставляет стандартные CORS заголовки
func handleCORS(c *gin.Context) {
	origin := c.GetHeader("Origin")
	if origin == "" {
		origin = "*"
	}
	c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
	c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
	c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
	c.Writer.Header().Set("Access-Control-Max-Age", "86400")
}
