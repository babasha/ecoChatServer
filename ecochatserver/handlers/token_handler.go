package handlers

import (
	"log"
	"net/http"
	"os"

	"github.com/egor/ecochatserver/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TokenRequest структура для OAuth 2.0 token request
type TokenRequest struct {
	GrantType    string `json:"grant_type" binding:"required"` // "password" или "refresh_token"
	UserID       string `json:"user_id"`                       // для grant_type=password
	Email        string `json:"email"`                         // для grant_type=password
	RefreshToken string `json:"refresh_token"`                 // для grant_type=refresh_token
}

// TokenResponse структура для OAuth 2.0 token response
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`    // в секундах
	RefreshToken string `json:"refresh_token"` // только для grant_type=password
}

// TokenHandler обрабатывает OAuth 2.0 token requests
// POST /api/auth/token
// Поддерживает два grant_type:
// - "password" - первичная авторизация (после проверки credentials в админке)
// - "refresh_token" - обновление access token
func TokenHandler(c *gin.Context) {
	// Проверяем секрет админки для защиты
	adminSecret := c.GetHeader("X-Admin-Secret")
	expectedSecret := os.Getenv("ADMIN_SECRET")

	if expectedSecret == "" {
		log.Printf("⚠️  ВНИМАНИЕ: ADMIN_SECRET не установлен! Доступ разрешен для разработки.")
	} else if adminSecret != expectedSecret {
		log.Printf("TokenHandler: неверный или отсутствующий ADMIN_SECRET")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Неавторизованный запрос"})
		return
	}

	var req TokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("TokenHandler: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "Некорректный формат данных",
		})
		return
	}

	switch req.GrantType {
	case "password":
		handlePasswordGrant(c, req)
	case "refresh_token":
		handleRefreshTokenGrant(c, req)
	default:
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "unsupported_grant_type",
			"error_description": "Поддерживаются только 'password' и 'refresh_token'",
		})
	}
}

// handlePasswordGrant обрабатывает первичную авторизацию
func handlePasswordGrant(c *gin.Context, req TokenRequest) {
	if req.UserID == "" || req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "user_id и email обязательны для grant_type=password",
		})
		return
	}

	// Валидируем UUID
	userUUID, err := uuid.Parse(req.UserID)
	if err != nil {
		log.Printf("TokenHandler: некорректный user_id: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "Некорректный формат user_id",
		})
		return
	}

	log.Printf("TokenHandler: генерация токенов для пользователя %s (email: %s)", userUUID, req.Email)

	// Генерируем оба токена
	clientID := uuid.Nil // Для админов можно использовать nil UUID
	accessToken, err := middleware.GenerateToken(userUUID.String(), clientID.String(), "admin")
	if err != nil {
		log.Printf("TokenHandler: ошибка генерации access token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "Ошибка генерации токена",
		})
		return
	}

	refreshToken, err := middleware.GenerateRefreshToken(userUUID.String(), clientID.String(), "admin")
	if err != nil {
		log.Printf("TokenHandler: ошибка генерации refresh token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "Ошибка генерации refresh токена",
		})
		return
	}

	log.Printf("TokenHandler: токены успешно созданы для пользователя %s", userUUID)

	c.JSON(http.StatusOK, TokenResponse{
		AccessToken:  accessToken,
		TokenType:    "Bearer",
		ExpiresIn:    86400, // 24 часа в секундах
		RefreshToken: refreshToken,
	})
}

// handleRefreshTokenGrant обрабатывает обновление access token
func handleRefreshTokenGrant(c *gin.Context, req TokenRequest) {
	if req.RefreshToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "refresh_token обязателен для grant_type=refresh_token",
		})
		return
	}

	// Валидируем refresh token
	claims, err := middleware.ValidateToken(req.RefreshToken)
	if err != nil {
		log.Printf("TokenHandler: невалидный refresh token: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_grant",
			"error_description": "Недействительный refresh token",
		})
		return
	}

	// Проверяем, что это действительно refresh token (по issuer)
	if claims.Issuer != "ecochat-server-refresh" {
		log.Printf("TokenHandler: токен не является refresh token (issuer: %s)", claims.Issuer)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":             "invalid_grant",
			"error_description": "Предоставлен не refresh token",
		})
		return
	}

	log.Printf("TokenHandler: обновление access token для пользователя %s", claims.AdminID)

	// Генерируем новый access token
	newAccessToken, err := middleware.GenerateToken(claims.AdminID, claims.ClientID, claims.Role)
	if err != nil {
		log.Printf("TokenHandler: ошибка генерации нового access token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "Ошибка генерации токена",
		})
		return
	}

	log.Printf("TokenHandler: access token успешно обновлен для пользователя %s", claims.AdminID)

	// Возвращаем только новый access token, refresh token остается тем же
	c.JSON(http.StatusOK, TokenResponse{
		AccessToken: newAccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   86400, // 24 часа в секундах
		// refresh_token не возвращаем, так как он остается прежним
	})
}
