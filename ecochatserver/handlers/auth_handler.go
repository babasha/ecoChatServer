package handlers

import (
	"database/sql"
	"log"
	"net/http"
	"time"

	"github.com/egor/ecochatserver/database/queries"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// RegisterRequest представляет запрос на регистрацию
type RegisterRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	DisplayName string `json:"displayName" binding:"required"`
}

// RegisterHandler обрабатывает регистрацию нового пользователя
// POST /api/auth/register
func RegisterHandler(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("RegisterHandler: ошибка валидации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Некорректные данные"})
		return
	}

	db := c.MustGet("db").(*sql.DB)

	// Проверяем существует ли пользователь с таким email
	existingAdmin, err := queries.GetAdmin(db, req.Email)
	if err != nil {
		log.Printf("RegisterHandler: ошибка проверки email: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сервера"})
		return
	}

	if existingAdmin != nil {
		log.Printf("RegisterHandler: email уже существует: %s", req.Email)
		c.JSON(http.StatusConflict, gin.H{"error": "Пользователь с таким email уже существует"})
		return
	}

	// Хешируем пароль
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("RegisterHandler: ошибка хеширования пароля: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка сервера"})
		return
	}

	// Создаем пользователя БЕЗ роли (будет назначена админом)
	// Статус "pending" означает что аккаунт неактивен до подтверждения
	userID := uuid.New()
	now := time.Now()

	query := `
		INSERT INTO users (
			id, email, email_verified, display_name, password_hash,
			role_id, status, created_at, updated_at
		) VALUES (
			$1, $2, false, $3, $4,
			NULL, 'pending', $5, $6
		)
	`

	_, err = db.Exec(query, userID, req.Email, req.DisplayName, string(passwordHash), now, now)
	if err != nil {
		log.Printf("RegisterHandler: ошибка создания пользователя: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания аккаунта"})
		return
	}

	log.Printf("RegisterHandler: создан новый пользователь: id=%s, email=%s, status=pending", userID, req.Email)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Регистрация успешна. Ожидайте подтверждения от администратора.",
		"userId":  userID.String(),
	})
}
