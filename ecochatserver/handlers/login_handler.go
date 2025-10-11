package handlers

import (
	"log"
	"net/http"
	"os"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/middleware"
	"github.com/gin-gonic/gin"
)

// LoginRequest структура для login request
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// LoginResponse структура для login response
type LoginResponse struct {
	Admin struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	} `json:"admin"`
}

// LoginHandler обрабатывает login и устанавливает session cookie (httpOnly)
// POST /api/auth/login
func LoginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("LoginHandler: ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Некорректный формат данных",
		})
		return
	}

	log.Printf("LoginHandler: попытка входа для email: %s", req.Email)

	// Аутентифицируем пользователя через базу данных
	admin, err := database.GetAdmin(req.Email)
	if err != nil || admin == nil {
		log.Printf("LoginHandler: администратор не найден: %s", req.Email)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Неверный email или пароль",
		})
		return
	}

	// Проверяем активен ли аккаунт
	if !admin.Active {
		log.Printf("LoginHandler: аккаунт деактивирован: %s", req.Email)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Аккаунт деактивирован",
		})
		return
	}

	// Проверяем пароль (хешированный в базе)
	if err := database.VerifyPassword(req.Password, admin.PasswordHash); err != nil {
		log.Printf("LoginHandler: неверный пароль для: %s", req.Email)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Неверный email или пароль",
		})
		return
	}

	log.Printf("LoginHandler: успешная аутентификация для: %s", req.Email)

	// Генерируем JWT токен
	token, err := middleware.GenerateToken(admin.ID.String(), admin.ClientID.String(), admin.Role)
	if err != nil {
		log.Printf("LoginHandler: ошибка генерации токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Внутренняя ошибка сервера",
		})
		return
	}

	// Устанавливаем session cookie (httpOnly)
	isProduction := os.Getenv("GIN_MODE") == "release"
	domain := os.Getenv("COOKIE_DOMAIN") // например, ".vercel.app" для поддоменов

	c.SetCookie(
		"session",    // name
		token,        // value
		86400,        // maxAge (24 часа в секундах)
		"/",          // path
		domain,       // domain (пустая строка = текущий домен)
		isProduction, // secure (только HTTPS в production)
		true,         // httpOnly
	)

	// CORS: Для поддоменов или разных доменов нужно явно указать SameSite=None
	// Gin не поддерживает SameSite напрямую в SetCookie, устанавливаем через заголовок
	if os.Getenv("ENABLE_CROSS_DOMAIN_COOKIES") == "true" {
		c.Header("Set-Cookie", buildCookieHeader(token, isProduction, domain))
	}

	log.Printf("LoginHandler: session cookie установлена для: %s", req.Email)

	// Возвращаем данные администратора (без токена в JSON)
	var response LoginResponse
	response.Admin.ID = admin.ID.String()
	response.Admin.Email = admin.Email
	response.Admin.Name = admin.Name
	response.Admin.Role = admin.Role

	c.JSON(http.StatusOK, response)
}

// buildCookieHeader строит заголовок Set-Cookie с поддержкой SameSite=None для cross-domain
func buildCookieHeader(token string, secure bool, domain string) string {
	cookie := "session=" + token
	cookie += "; Path=/"
	cookie += "; Max-Age=86400"
	cookie += "; HttpOnly"
	cookie += "; SameSite=None" // Для cross-domain requests

	if domain != "" {
		cookie += "; Domain=" + domain
	}

	if secure {
		cookie += "; Secure"
	}

	return cookie
}

// MeHandler проверяет текущую сессию и возвращает данные пользователя
// GET /api/auth/me
func MeHandler(c *gin.Context) {
	// Middleware уже проверил токен и установил adminID в контекст
	adminID, exists := c.Get("adminID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized",
		})
		return
	}

	role, _ := c.Get("role")

	// Получаем полные данные админа из базы
	admin, err := database.GetAdmin(adminID.(string))
	if err != nil || admin == nil {
		log.Printf("MeHandler: не удалось найти администратора: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Unauthorized",
		})
		return
	}

	var response LoginResponse
	response.Admin.ID = admin.ID.String()
	response.Admin.Email = admin.Email
	response.Admin.Name = admin.Name
	response.Admin.Role = role.(string)

	c.JSON(http.StatusOK, response)
}

// LogoutHandler обрабатывает logout и удаляет session cookie
// POST /api/auth/logout
func LogoutHandler(c *gin.Context) {
	domain := os.Getenv("COOKIE_DOMAIN")
	isProduction := os.Getenv("GIN_MODE") == "release"

	// Удаляем cookie установкой MaxAge в -1
	c.SetCookie(
		"session",
		"",
		-1, // MaxAge = -1 удаляет cookie
		"/",
		domain,
		isProduction,
		true,
	)

	log.Printf("LogoutHandler: session cookie удалена")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Успешный выход",
	})
}
