package middleware

import (
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/google/uuid"
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

// AuthMiddleware проверяет JWT токен или API ключ и авторизует запрос
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Получаем токен из заголовка
		authHeader := c.GetHeader("Authorization")
		apiKeyHeader := c.GetHeader("X-API-Key")

		// Проверяем API ключ сначала (если есть)
		if apiKeyHeader != "" {
			log.Printf("AuthMiddleware: проверка API ключа")
			claims, err := ValidateAPIKey(apiKeyHeader)
			if err != nil {
				log.Printf("AuthMiddleware: неверный API ключ: %v", err)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "неверный или устаревший API ключ"})
				c.Abort()
				return
			}

			// Устанавливаем данные пользователя в контексте
			c.Set("adminID", claims.AdminID)
			c.Set("clientID", claims.ClientID)
			c.Set("role", claims.Role)
			c.Next()
			return
		}

		// Если нет API ключа, проверяем JWT токен
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
		c.Set("clientID", claims.ClientID)
		c.Set("role", claims.Role)

		c.Next()
	}
}

// JWTClaims определяет структуру данных токена
type JWTClaims struct {
	AdminID  string `json:"adminId"`
	ClientID string `json:"clientId"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
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

	// Проверяем активен ли аккаунт
	if !admin.Active {
		return "", errors.New("аккаунт деактивирован")
	}

	// Проверяем пароль (хешированный в базе)
	if err := database.VerifyPassword(password, admin.PasswordHash); err != nil {
		return "", errors.New("неверные учетные данные")
	}

	// Генерируем JWT токен, передавая строки вместо uuid.UUID
	token, err := GenerateToken(admin.ID.String(), admin.ClientID.String(), admin.Role)
	if err != nil {
		return "", err
	}

	return token, nil
}

// hashAPIKey хеширует API ключ для безопасного хранения
func hashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// ValidateAPIKey проверяет валидность API ключа и возвращает claims
func ValidateAPIKey(apiKey string) (*JWTClaims, error) {
	// Хешируем переданный ключ
	keyHash := hashAPIKey(apiKey)

	// Проверяем в БД
	var userID uuid.UUID
	var expiresAt *time.Time
	var revoked bool

	err := database.DB.QueryRow(`
		SELECT user_id, expires_at, revoked
		FROM api_keys
		WHERE key_hash = $1
	`, keyHash).Scan(&userID, &expiresAt, &revoked)

	if err != nil {
		log.Printf("ValidateAPIKey: ключ не найден или ошибка: %v", err)
		return nil, errors.New("недействительный API ключ")
	}

	// Проверяем, не отозван ли ключ
	if revoked {
		log.Printf("ValidateAPIKey: ключ отозван")
		return nil, errors.New("API ключ отозван")
	}

	// Проверяем срок действия
	if expiresAt != nil && time.Now().After(*expiresAt) {
		log.Printf("ValidateAPIKey: ключ истек")
		return nil, errors.New("API ключ истек")
	}

	// Обновляем время последнего использования (асинхронно)
	go func() {
		_, err := database.DB.Exec(`
			UPDATE api_keys
			SET last_used_at = CURRENT_TIMESTAMP
			WHERE key_hash = $1
		`, keyHash)
		if err != nil {
			log.Printf("ValidateAPIKey: ошибка обновления last_used_at: %v", err)
		}
	}()

	// Возвращаем claims как для обычного JWT
	clientID := uuid.Nil // Для админов
	claims := &JWTClaims{
		AdminID:  userID.String(),
		ClientID: clientID.String(),
		Role:     "admin",
	}

	return claims, nil
}
