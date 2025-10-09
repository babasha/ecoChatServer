package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// APIKeyRequest структура для запроса генерации API ключа
type APIKeyRequest struct {
	UserID    string `json:"userId" binding:"required"`
	Email     string `json:"email" binding:"required"`
	ExpiresIn int    `json:"expiresIn"` // в днях, 0 = без срока
}

// APIKeyResponse структура ответа с API ключом
type APIKeyResponse struct {
	APIKey    string    `json:"apiKey"`
	UserID    string    `json:"userId"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ValidateAPIKeyRequest структура для валидации API ключа
type ValidateAPIKeyRequest struct {
	APIKey string `json:"apiKey" binding:"required"`
}

// generateAPIKey генерирует криптографически стойкий API ключ
func generateAPIKey() (string, error) {
	bytes := make([]byte, 32) // 256 бит
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// hashAPIKey хеширует API ключ для безопасного хранения
func hashAPIKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// GenerateAPIKey генерирует новый API ключ для пользователя
// POST /api/auth/generate-key
func GenerateAPIKey(c *gin.Context) {
	var req APIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("GenerateAPIKey: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
		return
	}

	// ВАЖНО: Мы НЕ проверяем учетные данные здесь!
	// Проверка credentials должна происходить в админке (БД ballast)
	// Этот эндпоинт вызывается ТОЛЬКО после успешной аутентификации в админке
	log.Printf("GenerateAPIKey: генерация ключа для пользователя %s (email: %s)", req.UserID, req.Email)

	// Генерируем API ключ
	apiKey, err := generateAPIKey()
	if err != nil {
		log.Printf("GenerateAPIKey: ошибка генерации ключа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка генерации ключа"})
		return
	}

	// Хешируем ключ для хранения
	keyHash := hashAPIKey(apiKey)

	// Парсим userID
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		log.Printf("GenerateAPIKey: некорректный userID: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный userID"})
		return
	}

	// Вычисляем срок действия
	var expiresAt *time.Time
	if req.ExpiresIn > 0 {
		expiry := time.Now().Add(time.Duration(req.ExpiresIn) * 24 * time.Hour)
		expiresAt = &expiry
	}

	// Сохраняем в БД
	_, err = database.DB.Exec(`
		INSERT INTO api_keys (user_id, key_hash, name, expires_at)
		VALUES ($1, $2, $3, $4)
	`, userID, keyHash, "Admin API Key", expiresAt)

	if err != nil {
		log.Printf("GenerateAPIKey: ошибка сохранения ключа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сохранения ключа"})
		return
	}

	log.Printf("GenerateAPIKey: ключ успешно создан для пользователя %s", req.UserID)

	// Возвращаем API ключ (только один раз!)
	c.JSON(http.StatusOK, APIKeyResponse{
		APIKey:    apiKey,
		UserID:    req.UserID,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	})
}

// ValidateAPIKey проверяет валидность API ключа
// POST /api/auth/validate-key
func ValidateAPIKey(c *gin.Context) {
	var req ValidateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("ValidateAPIKey: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
		return
	}

	// Хешируем переданный ключ
	keyHash := hashAPIKey(req.APIKey)

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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Недействительный API ключ"})
		return
	}

	// Проверяем, не отозван ли ключ
	if revoked {
		log.Printf("ValidateAPIKey: ключ отозван")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "API ключ отозван"})
		return
	}

	// Проверяем срок действия
	if expiresAt != nil && time.Now().After(*expiresAt) {
		log.Printf("ValidateAPIKey: ключ истек")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "API ключ истек"})
		return
	}

	// Обновляем время последнего использования
	_, err = database.DB.Exec(`
		UPDATE api_keys
		SET last_used_at = CURRENT_TIMESTAMP
		WHERE key_hash = $1
	`, keyHash)

	if err != nil {
		log.Printf("ValidateAPIKey: ошибка обновления last_used_at: %v", err)
	}

	// Генерируем JWT токен для дальнейшего использования
	// Здесь нужно будет получить дополнительную информацию о пользователе
	clientID := uuid.Nil // Для админов это может быть специальное значение
	token, err := middleware.GenerateToken(userID.String(), clientID.String(), "admin")
	if err != nil {
		log.Printf("ValidateAPIKey: ошибка генерации JWT: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка генерации токена"})
		return
	}

	log.Printf("ValidateAPIKey: ключ валиден для пользователя %s", userID)

	c.JSON(http.StatusOK, gin.H{
		"valid":  true,
		"userId": userID.String(),
		"token":  token,
	})
}

// RevokeAPIKey отзывает API ключ
// DELETE /api/auth/revoke-key
func RevokeAPIKey(c *gin.Context) {
	var req ValidateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("RevokeAPIKey: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректный формат данных"})
		return
	}

	keyHash := hashAPIKey(req.APIKey)

	result, err := database.DB.Exec(`
		UPDATE api_keys
		SET revoked = true
		WHERE key_hash = $1 AND revoked = false
	`, keyHash)

	if err != nil {
		log.Printf("RevokeAPIKey: ошибка отзыва ключа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка отзыва ключа"})
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		log.Printf("RevokeAPIKey: ключ не найден или уже отозван")
		c.JSON(http.StatusNotFound, gin.H{"error": "Ключ не найден или уже отозван"})
		return
	}

	log.Printf("RevokeAPIKey: ключ успешно отозван")
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "API ключ отозван"})
}
