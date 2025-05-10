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
        // В продакшене этот код должен выдавать ошибку или использовать защищенное хранилище секретов
        log.Println("Предупреждение: JWT_SECRET_KEY не установлен, используется стандартный ключ")
        jwtSecret = "временный_ключ_для_разработки_не_использовать_в_продакшене"
    }
    jwtKey = []byte(jwtSecret)
}

// AuthMiddleware проверяет JWT токен и авторизует запрос
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // Получаем токен из заголовка
        authHeader := c.GetHeader("Authorization")
        if authHeader == "" {
            c.JSON(http.StatusUnauthorized, gin.H{"error": "требуется авторизация"})
            c.Abort()
            return
        }

        // Обрабатываем токен
        tokenString := strings.Replace(authHeader, "Bearer ", "", 1)
        claims, err := validateToken(tokenString)
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

// ValidateToken проверяет и парсит JWT токен (экспортированная версия)
func ValidateToken(tokenString string) (*JWTClaims, error) {
    return validateToken(tokenString)
}

// validateToken проверяет и парсит JWT токен (приватная версия)
func validateToken(tokenString string) (*JWTClaims, error) {
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
    if err != nil {
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