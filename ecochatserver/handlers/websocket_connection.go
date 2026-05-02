package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/middleware"
	websocketpkg "github.com/egor/ecochatserver/websocket"
)

// wsUpgrader апгрейдит HTTP→WebSocket
// Origin проверяется на уровне CORS middleware и API-key валидации
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
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

	// Разрешаем localhost и Tauri origins для разработки и десктоп-приложения
	if strings.HasPrefix(origin, "http://localhost:") ||
		strings.HasPrefix(origin, "http://127.0.0.1:") ||
		origin == "tauri://localhost" ||
		origin == "https://tauri.localhost" {
		return true
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
	// DEBUG: детальная информация только для отладки
	// log.Printf("ServeWs: новое соединение от %s, origin: %s", c.ClientIP(), c.Request.Header.Get("Origin"))

	// Получаем параметры и токен
	token := c.Query("token")
	clientType := c.DefaultQuery("type", "admin")
	chatIDStr := c.Query("chat_id")

	// Для виджета chat_id необязателен - может быть создан позже
	// DEBUG: можно убрать этот лог
	// if clientType == "widget" && chatIDStr == "" {
	// 	log.Printf("ServeWs: виджет подключается без chat_id - чат будет создан при первом сообщении")
	// }

	// Проверяем токен для админа
	var adminID, clientID, chatID uuid.UUID
	var err error

	if clientType == "admin" {
		// Для admin подключений поддерживаем как query ?token=, так и session cookie
		if token == "" {
			if cookieToken, cookieErr := c.Cookie("session"); cookieErr == nil && cookieToken != "" {
				token = cookieToken
				// DEBUG: log.Printf("ServeWs: использован session cookie для admin подключения")
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

		// DEBUG: раскомментируйте для отладки
		// log.Printf("ServeWs: аутентифицирован admin %s (client: %s)", adminID, clientID)
		_ = clientID // используется в DEBUG логах
	} else if clientType == websocketpkg.ClientTypeDriver || clientType == websocketpkg.ClientTypeMoClient {
		// moooving: водитель или авторизованный клиент.
		// Аутентификация — через JWT с Issuer=moooving-chat, выпускаемый
		// REST endpoint POST /api/moooving/chat/token (см. moooving_handler.go).
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Отсутствует токен авторизации"})
			return
		}
		claims, err := middleware.ValidateToken(token)
		if err != nil {
			log.Printf("ServeWs[moooving]: ошибка валидации токена: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Неверный токен"})
			return
		}
		if claims.Issuer != middleware.MooovingTokenIssuer {
			log.Printf("ServeWs[moooving]: неверный Issuer=%q (ожидается %q)", claims.Issuer, middleware.MooovingTokenIssuer)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Токен не предназначен для чата moooving"})
			return
		}

		expectedUserType := "client"
		if clientType == websocketpkg.ClientTypeDriver {
			expectedUserType = "driver"
		}
		if claims.UserType != expectedUserType {
			log.Printf("ServeWs[moooving]: token UserType=%q, требуется %q", claims.UserType, expectedUserType)
			c.JSON(http.StatusForbidden, gin.H{"error": "Токен не соответствует типу клиента"})
			return
		}
		if claims.OrderID <= 0 || claims.ExtUserID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "В токене отсутствует order_id/ext_user_id"})
			return
		}

		// Если в URL передан order_id — он должен совпадать с токеном
		if qOrder := c.Query("order_id"); qOrder != "" {
			if v, perr := strconv.ParseInt(qOrder, 10, 64); perr == nil && v != claims.OrderID {
				log.Printf("ServeWs[moooving]: order_id в URL=%d не совпадает с токеном=%d", v, claims.OrderID)
				c.JSON(http.StatusForbidden, gin.H{"error": "order_id mismatch"})
				return
			}
		}

		// Находим существующий чат заказа. Чат создаётся через REST до подключения,
		// но если фронт пришёл первым — допускаем подключение, chat_id придёт позже.
		moChat, err := database.GetMooovingChatByOrderID(claims.OrderID)
		if err != nil {
			log.Printf("ServeWs[moooving]: ошибка поиска чата order_id=%d: %v", claims.OrderID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка поиска чата"})
			return
		}
		if moChat == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Чат для этого заказа не открыт"})
			return
		}

		// Проверка принадлежности: ext_user_id должен совпадать со стороной чата
		if expectedUserType == "driver" {
			if moChat.DriverIDExt == nil || *moChat.DriverIDExt != claims.ExtUserID {
				log.Printf("ServeWs[moooving]: driver_id_ext=%v чата не совпадает с токеном %d",
					moChat.DriverIDExt, claims.ExtUserID)
				c.JSON(http.StatusForbidden, gin.H{"error": "Водитель не назначен на этот заказ"})
				return
			}
		} else {
			if moChat.ClientIDExt == nil || *moChat.ClientIDExt != claims.ExtUserID {
				log.Printf("ServeWs[moooving]: client_id_ext=%v чата не совпадает с токеном %d",
					moChat.ClientIDExt, claims.ExtUserID)
				c.JSON(http.StatusForbidden, gin.H{"error": "Клиент не привязан к этому заказу"})
				return
			}
		}

		chatID = moChat.ID

		// userID для sender_id в messages — детерминированный UUID
		var userSource string
		if expectedUserType == "driver" {
			userSource = database.MooovingDriverSource
		} else {
			userSource = database.MooovingClientSource
		}
		adminID = database.MooovingUserUUID(userSource, claims.ExtUserID)

		// Сохраняем claims в контекст для использования в processSendMessage
		c.Set("moooving_user_type", expectedUserType)
		c.Set("moooving_ext_user_id", claims.ExtUserID)
		c.Set("moooving_order_id", claims.OrderID)
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
				// DEBUG: log.Printf("ServeWs: UserID %s уже является валидным UUID", userIDStr)
			} else {
				adminID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(userIDStr))
				// DEBUG: log.Printf("ServeWs: создан детерминированный UUID для userID %s: %s", userIDStr, adminID.String())
			}
		}

		// DEBUG: log.Printf("ServeWs: подключение виджета, chatID: %s, userID: %s", chatID, adminID)
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
		}
	}

	// Регистрируем клиента в хабе
	WebSocketHub.Register <- client

	// Запускаем горутины обработки
	go client.WritePump()
	go client.ReadPump(processWebSocketMessage)

	// Отправляем статус подключения
	WebSocketHub.SendConnectionStatus(client, true)

	// Один короткий лог вместо 10+ строк
	log.Printf("WebSocket: %s подключен (ID=%s, chatID=%s)", clientType, client.ID, chatID)
	AddWebSocketLog(LogLevelInfo, fmt.Sprintf("%s подключен: %s", clientType, client.ID), "WebSocket")
}
