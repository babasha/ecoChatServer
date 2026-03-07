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

	// Проверяем статус аккаунта (active, inactive, suspended, banned)
	if admin.Status != "active" {
		log.Printf("LoginHandler: аккаунт недоступен (status=%s): %s", admin.Status, req.Email)
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

	// Генерируем JWT токен (clientID больше не используется, передаем пустую строку)
	token, err := middleware.GenerateToken(admin.ID.String(), "", admin.Role)
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

	// В production всегда SameSite=None; Secure для cross-domain (фронтенд на Vercel, бекенд на Railway)
	// В dev режиме можно использовать обычные cookies
	if isProduction || os.Getenv("ENABLE_CROSS_DOMAIN_COOKIES") == "true" {
		cookieHeader := buildCookieHeader(token, true, domain)
		c.Header("Set-Cookie", cookieHeader)
		log.Printf("LoginHandler: установлен cross-domain cookie для %s", req.Email)
	} else {
		c.SetCookie(
			"session",    // name
			token,        // value
			86400,        // maxAge (24 часа в секундах)
			"/",          // path
			domain,       // domain (пустая строка = текущий домен)
			false,        // secure
			true,         // httpOnly
		)
		log.Printf("LoginHandler: установлен same-origin cookie для: %s", req.Email)
	}

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
	cookie += "; Secure"        // SameSite=None требует Secure (всегда для cross-domain)

	if domain != "" {
		cookie += "; Domain=" + domain
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

	// Получаем полные данные админа из базы по ID
	admin, err := database.GetAdminByID(adminID.(string))
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

// SessionRestoreHandler принимает JWT токен и устанавливает session cookie.
// Используется после OAuth-логина: бекенд генерирует токен, передаёт через URL,
// фронтенд вызывает этот endpoint через Vercel прокси → cookie устанавливается
// на домене фронтенда (same-origin).
// POST /api/auth/session
func SessionRestoreHandler(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}

	// Валидируем токен
	claims, err := middleware.ValidateToken(req.Token)
	if err != nil {
		log.Printf("SessionRestoreHandler: невалидный токен: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	// Устанавливаем session cookie
	isProduction := os.Getenv("GIN_MODE") == "release"
	domain := os.Getenv("COOKIE_DOMAIN")

	if isProduction || os.Getenv("ENABLE_CROSS_DOMAIN_COOKIES") == "true" {
		cookieHeader := buildCookieHeader(req.Token, true, domain)
		c.Header("Set-Cookie", cookieHeader)
	} else {
		c.SetCookie("session", req.Token, 86400, "/", domain, false, true)
	}

	// Возвращаем данные админа
	admin, err := database.GetAdminByID(claims.AdminID)
	if err != nil || admin == nil {
		log.Printf("SessionRestoreHandler: админ не найден: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "admin not found"})
		return
	}

	var response LoginResponse
	response.Admin.ID = admin.ID.String()
	response.Admin.Email = admin.Email
	response.Admin.Name = admin.Name
	response.Admin.Role = claims.Role

	log.Printf("SessionRestoreHandler: сессия восстановлена для %s", admin.Email)
	c.JSON(http.StatusOK, response)
}

// WSTokenHandler возвращает короткоживущий токен для WebSocket подключения.
// Используется когда фронтенд проксирует API через Vercel (same-origin cookies),
// но WebSocket подключается напрямую к бекенду и не имеет доступа к cookie.
// GET /api/auth/ws-token
func WSTokenHandler(c *gin.Context) {
	adminID, _ := c.Get("adminID")
	clientID, _ := c.Get("clientID")
	role, _ := c.Get("role")

	token, err := middleware.GenerateToken(adminID.(string), clientID.(string), role.(string))
	if err != nil {
		log.Printf("WSTokenHandler: ошибка генерации токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token})
}

// LogoutHandler обрабатывает logout и удаляет session cookie
// POST /api/auth/logout
func LogoutHandler(c *gin.Context) {
	domain := os.Getenv("COOKIE_DOMAIN")
	isProduction := os.Getenv("GIN_MODE") == "release"

	// Удаляем cookie — атрибуты должны совпадать с установкой
	if isProduction || os.Getenv("ENABLE_CROSS_DOMAIN_COOKIES") == "true" {
		cookie := "session=; Path=/; Max-Age=0; HttpOnly; SameSite=None; Secure"
		if domain != "" {
			cookie += "; Domain=" + domain
		}
		c.Header("Set-Cookie", cookie)
	} else {
		c.SetCookie("session", "", -1, "/", domain, false, true)
	}

	log.Printf("LogoutHandler: session cookie удалена")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Успешный выход",
	})
}
