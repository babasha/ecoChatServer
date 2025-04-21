package handlers

import (
    "log"
    "net/http"

    "github.com/gin-gonic/gin"

    // Внутренние пакеты через полный путь модуля
    "github.com/egor/ecochatserver/database"
    "github.com/egor/ecochatserver/middleware"
)

// Login обрабатывает авторизацию админов
func Login(c *gin.Context) {
	var credentials struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&credentials); err != nil {
		log.Printf("Ошибка парсинга данных для авторизации: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	
	log.Printf("Попытка авторизации для пользователя: %s", credentials.Email)
	
	// Аутентификация пользователя
	token, err := middleware.Authenticate(credentials.Email, credentials.Password)
	if err != nil {
		log.Printf("Ошибка аутентификации для %s: %v", credentials.Email, err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	
	// Получаем данные администратора
	admin, err := database.GetAdmin(credentials.Email)
	if err != nil {
		log.Printf("Ошибка получения данных администратора %s: %v", credentials.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка получения данных пользователя"})
		return
	}
	
	admin.PasswordHash = ""
	
	log.Printf("Успешная авторизация администратора: %s (ID: %s)", admin.Email, admin.ID)
	c.JSON(http.StatusOK, gin.H{
		"token": token,
		"admin": admin,
	})
}