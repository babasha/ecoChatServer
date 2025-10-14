package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/egor/ecochatserver/middleware"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// wsUpgrader апгрейдит HTTP→WebSocket с улучшенной проверкой Origin
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     checkOrigin,
}

// checkOrigin проверяет, разрешен ли Origin для подключения
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Разрешаем локальные подключения без Origin
		host := r.Host
		if strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:") {
			return true
		}
		return false
	}

	// Получаем разрешенные origins из переменных окружения
	allowedOrigins := []string{}

	// Основной URL фронтенда
	if frontendURL := os.Getenv("FRONTEND_URL"); frontendURL != "" {
		allowedOrigins = append(allowedOrigins, frontendURL)
	}

	// Дополнительные разрешенные origins
	if additional := os.Getenv("ADDITIONAL_ALLOWED_ORIGINS"); additional != "" {
		for _, url := range strings.Split(additional, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				allowedOrigins = append(allowedOrigins, url)
			}
		}
	}

	// Проверяем, есть ли origin в списке разрешенных
	for _, allowed := range allowedOrigins {
		if allowed == origin {
			return true
		}
	}

	// Для разработки можно разрешить все origins
	if os.Getenv("ALLOW_ALL_ORIGINS") == "true" {
		log.Printf("ВНИМАНИЕ: Разрешен origin %s (ALLOW_ALL_ORIGINS=true)", origin)
		return true
	}

	log.Printf("Отклонен origin: %s", origin)
	return false
}

// ServeWs обрабатывает WebSocket соединение
func ServeWs(c *gin.Context) {
	logMsg := fmt.Sprintf("ServeWs: новое соединение от %s, origin: %s",
		c.ClientIP(), c.Request.Header.Get("Origin"))
	log.Println(logMsg)
	AddWebSocketLog(LogLevelInfo, logMsg, "WebSocket")

	// Получаем параметры и токен
	token := c.Query("token")
	clientType := c.DefaultQuery("type", "admin")
	chatIDStr := c.Query("chat_id")

	// Для виджета chat_id необязателен - может быть создан позже
	if clientType == "widget" && chatIDStr == "" {
		log.Printf("ServeWs: виджет подключается без chat_id - чат будет создан при первом сообщении")
	}

	// Проверяем токен для админа
	var adminID, clientID, chatID uuid.UUID
	var err error

	if clientType == "admin" {
		// Для admin подключений поддерживаем как query ?token=, так и session cookie
		if token == "" {
			if cookieToken, cookieErr := c.Cookie("session"); cookieErr == nil && cookieToken != "" {
				token = cookieToken
				log.Printf("ServeWs: использован session cookie для admin подключения")
			} else {
				log.Printf("ServeWs: отсутствует токен и session cookie для admin подключения: %v", cookieErr)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Отсутствует токен авторизации"})
				return
			}
		}

		// Валидируем JWT токен
		claims, err := middleware.ValidateToken(token)
		if err != nil {
			log.Printf("ServeWs: ошибка валидации токена: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Неверный токен"})
			return
		}

		adminID, err = uuid.Parse(claims.AdminID)
		if err != nil {
			log.Printf("ServeWs: ошибка парсинга adminID: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный adminID"})
			return
		}

		if claims.ClientID != "" {
			clientID, err = uuid.Parse(claims.ClientID)
			if err != nil {
				log.Printf("ServeWs: некорректный clientID в токене (%s): %v. Используем uuid.Nil", claims.ClientID, err)
				clientID = uuid.Nil
			}
		} else {
			clientID = uuid.Nil
		}

		// Сохраняем данные в контексте для использования в обработчиках
		c.Set("adminID", claims.AdminID)
		c.Set("clientID", claims.ClientID)
		c.Set("role", claims.Role)

		log.Printf("ServeWs: аутентифицирован admin %s (client: %s)", adminID, clientID)
	} else if clientType == "widget" {
		// Для виджета парсим chatID только если он передан
		if chatIDStr != "" {
			chatID, err = uuid.Parse(chatIDStr)
			if err != nil {
				log.Printf("ServeWs: ошибка парсинга chatID: %v", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат chatID"})
				return
			}
		}

		// Получаем userID из токена для виджета (токен содержит строковый userID)
		userIDStr := token
		if userIDStr == "" {
			// Fallback на заголовок, если токен не передан
			userIDStr = c.GetHeader("X-Widget-User-ID")
		}

		if userIDStr != "" {
			// Преобразуем userID в UUID таким же образом, как в telegram_handler
			if parsedUUID, err := uuid.Parse(userIDStr); err == nil {
				adminID = parsedUUID
				log.Printf("ServeWs: UserID %s уже является валидным UUID", userIDStr)
			} else {
				adminID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(userIDStr))
				log.Printf("ServeWs: создан детерминированный UUID для userID %s: %s", userIDStr, adminID.String())
			}
		}

		log.Printf("ServeWs: подключение виджета, chatID: %s, userID: %s", chatID, adminID)
	} else {
		log.Printf("ServeWs: неверный тип клиента или отсутствует токен")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный тип клиента или отсутствует токен"})
		return
	}

	// Апгрейдим соединение
	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ServeWs: ошибка апгрейда соединения: %v", err)
		return
	}

	// Создаем нового клиента
	client := websocketpkg.NewClient(WebSocketHub, conn, clientType, adminID, chatID)
	client.Context = c

	// Устанавливаем IP и UserAgent для мониторинга
	client.IP = c.ClientIP()
	client.UserAgent = c.Request.UserAgent()
	log.Printf("ServeWs: клиент подключен с IP=%s, UA=%s", client.IP, client.UserAgent)

	// Для виджета сохраняем исходный строковый userID
	if clientType == "widget" {
		// Используем токен как строковый userID
		userIDStr := token
		if userIDStr == "" {
			// Fallback на заголовок
			userIDStr = c.GetHeader("X-Widget-User-ID")
		}
		if userIDStr != "" {
			client.UserIDString = userIDStr
			log.Printf("ServeWs: сохранен строковый userID для виджета: %s", userIDStr)
		}
	}

	// Регистрируем клиента в хабе
	WebSocketHub.Register <- client

	// Запускаем горутины обработки
	go client.WritePump()
	go client.ReadPump(processWebSocketMessage)

	// Отправляем статус подключения
	WebSocketHub.SendConnectionStatus(client, true)

	successMsg := fmt.Sprintf("ServeWs: клиент %s успешно подключен (type=%s)", client.ID, clientType)
	log.Println(successMsg)
	AddWebSocketLog(LogLevelInfo, successMsg, "WebSocket")
}
