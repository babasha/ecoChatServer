package middleware

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	// Путь к локальному пакету должен начинаться с module path из go.mod
	"github.com/egor/ecochatserver/database"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
)

// jwtKey - ключ для подписи JWT токена
var jwtKey []byte

func init() {
	// Получаем ключ из переменных окружения
	jwtSecret := os.Getenv("JWT_SECRET_KEY")
	if jwtSecret == "" {
		// Проверяем режим работы
		if os.Getenv("GIN_MODE") == "release" {
			log.Fatal("ОШИБКА: JWT_SECRET_KEY обязательно должен быть установлен в production режиме")
		}
		// В dev режиме используем временный ключ
		log.Println("⚠️  ВНИМАНИЕ: JWT_SECRET_KEY не установлен, используется временный ключ (только для разработки)")
		jwtSecret = "dev_secret_key_change_in_production"
	}
	jwtKey = []byte(jwtSecret)
}

// SessionMiddleware проверяет session cookie (httpOnly) и авторизует запрос
// Используется для запросов от Next.js admin panel
// Поддерживает: 1) Cookie "session", 2) Authorization: Bearer <token> (для Next.js proxy)
func SessionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1) Пытаемся получить session cookie
		sessionToken, err := c.Cookie("session")

		// 2) Fallback: Authorization header (для Next.js proxy через cross-domain)
		if err != nil || sessionToken == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				sessionToken = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if sessionToken == "" {
			log.Printf("[SessionMiddleware] Cookie и Authorization не найдены, origin: %s", c.Request.Header.Get("Origin"))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется авторизация"})
			c.Abort()
			return
		}

		log.Printf("[SessionMiddleware] Cookie получена, длина токена: %d", len(sessionToken))

		// Валидируем JWT токен из cookie
		claims, err := ValidateToken(sessionToken)
		if err != nil {
			log.Printf("[SessionMiddleware] Ошибка валидации токена: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "неверный или устаревший токен"})
			c.Abort()
			return
		}

		log.Printf("[SessionMiddleware] Токен валиден для adminID: %s, role: %s", claims.AdminID, claims.Role)

		// Устанавливаем данные пользователя в контексте
		c.Set("adminID", claims.AdminID)
		c.Set("adminId", claims.AdminID)  // camelCase — используется в director/ai chat handlers
		c.Set("admin_id", claims.AdminID) // Для совместимости
		c.Set("clientID", claims.ClientID)
		c.Set("role", claims.Role)
		c.Set("admin_role", claims.Role) // Для совместимости с admin_team_handler

		c.Next()
	}
}

// AuthMiddleware проверяет JWT токен и авторизует запрос
// DEPRECATED: Используйте SessionMiddleware для admin panel
// Оставлено для обратной совместимости с Bearer token API
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Получаем токен из заголовка
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется авторизация"})
			c.Abort()
			return
		}

		// Обрабатываем JWT токен
		tokenString := strings.Replace(authHeader, "Bearer ", "", 1)
		claims, err := ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "неверный или устаревший токен"})
			c.Abort()
			return
		}

		// Устанавливаем данные пользователя в контексте
		c.Set("adminID", claims.AdminID)
		c.Set("adminId", claims.AdminID) // camelCase — используется в director/ai chat handlers
		c.Set("clientID", claims.ClientID)
		c.Set("role", claims.Role)

		c.Next()
	}
}

// JWTClaims определяет структуру данных токена.
// Для админских токенов используются AdminID/ClientID/Role.
// Для moooving-токенов (Issuer="moooving") используются UserType+ExtUserID+OrderID.
type JWTClaims struct {
	AdminID  string `json:"adminId"`
	ClientID string `json:"clientId"`
	Role     string `json:"role"`

	// moooving chat integration (Issuer="moooving")
	UserType  string `json:"userType,omitempty"`  // "driver" | "client"
	ExtUserID int64  `json:"extUserId,omitempty"` // moooving user.id (int)
	OrderID   int64  `json:"orderId,omitempty"`   // moooving order.id (int)

	// morada chat integration (Issuer="morada-chat")
	MoradaUserType string `json:"mUserType,omitempty"` // "visitor" | "agent"
	MoradaExtID    int64  `json:"mExtId,omitempty"`    // morada user id (посетитель или агент)
	MoradaChatID   string `json:"mChatId,omitempty"`   // целевой чат (UUID)

	jwt.RegisteredClaims
}

// MooovingTokenIssuer — Issuer для JWT токенов чата, выдаваемых moooving-юзерам
const MooovingTokenIssuer = "moooving-chat"

// GenerateMooovingChatToken выпускает короткоживущий JWT для подключения
// клиента или водителя moooving к WebSocket-чату конкретного заказа.
// userType: "driver" или "client". extUserID — int-ID из moooving. orderID — заказ.
func GenerateMooovingChatToken(userType string, extUserID, orderID int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}

	claims := &JWTClaims{
		UserType:  userType,
		ExtUserID: extUserID,
		OrderID:   orderID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    MooovingTokenIssuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// MoradaTokenIssuer — Issuer для JWT токенов чата, выдаваемых morada-юзерам
// (посетителям сайта и владельцам/агентам).
const MoradaTokenIssuer = "morada-chat"

// GenerateMoradaChatToken выпускает короткоживущий JWT для подключения
// посетителя или агента morada к WebSocket-чату конкретного объекта.
// userType: "visitor" или "agent". extUserID — morada user id. chatID — UUID чата.
func GenerateMoradaChatToken(userType string, extUserID int64, chatID string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}

	claims := &JWTClaims{
		MoradaUserType: userType,
		MoradaExtID:    extUserID,
		MoradaChatID:   chatID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    MoradaTokenIssuer,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// GenerateToken генерирует JWT токен
func GenerateToken(adminID, clientID, role string) (string, error) {
	// Устанавливаем время истечения токена (24 часа)
	expirationTime := time.Now().Add(24 * time.Hour)

	// Создаем структуру с данными (claims)
	claims := &JWTClaims{
		AdminID:  adminID,
		ClientID: clientID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ecochat-server",
		},
	}

	// Создаем токен с указанным методом подписи
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Подписываем токен нашим секретным ключом
	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}

// ValidateToken проверяет и парсит JWT токен
func ValidateToken(tokenString string) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Проверяем, что используется правильный алгоритм подписи
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("неожиданный метод подписи: %v", token.Header["alg"])
		}
		return jwtKey, nil
	})

	if err != nil {
		return nil, err
	}

	// Проверяем, что токен действителен
	if !token.Valid {
		return nil, errors.New("недействительный токен")
	}

	// Получаем claims
	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, errors.New("неверный формат токена")
	}

	return claims, nil
}

// Authenticate аутентифицирует пользователя по email и паролю
func Authenticate(email, password string) (string, error) {
	// Получаем администратора из базы данных
	admin, err := database.GetAdmin(email)
	if err != nil || admin == nil {
		return "", errors.New("неверные учетные данные")
	}

	// Проверяем статус аккаунта
	if admin.Status != "active" {
		return "", errors.New("аккаунт деактивирован")
	}

	// Проверяем пароль (хешированный в базе)
	if err := database.VerifyPassword(password, admin.PasswordHash); err != nil {
		return "", errors.New("неверные учетные данные")
	}

	// Генерируем JWT токен (clientID больше не используется)
	token, err := GenerateToken(admin.ID.String(), "", admin.Role)
	if err != nil {
		return "", err
	}

	return token, nil
}

// GenerateRefreshToken генерирует refresh token (долгоживущий JWT)
func GenerateRefreshToken(adminID, clientID, role string) (string, error) {
	// Устанавливаем время истечения refresh token (7 дней)
	expirationTime := time.Now().Add(7 * 24 * time.Hour)

	// Создаем структуру с данными (claims)
	claims := &JWTClaims{
		AdminID:  adminID,
		ClientID: clientID,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ecochat-server-refresh",
		},
	}

	// Создаем токен с указанным методом подписи
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Подписываем токен нашим секретным ключом
	tokenString, err := token.SignedString(jwtKey)
	if err != nil {
		return "", err
	}

	return tokenString, nil
}
