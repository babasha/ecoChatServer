package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter представляет ограничитель запросов
type RateLimiter struct {
	requests map[string][]time.Time
	mu       sync.Mutex
	rate     int           // максимальное количество запросов
	window   time.Duration // временное окно
}

// NewRateLimiter создает новый ограничитель запросов
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		rate:     rate,
		window:   window,
	}

	// Запускаем очистку старых записей каждую минуту
	go rl.cleanup()

	return rl
}

// cleanup периодически удаляет устаревшие записи
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, times := range rl.requests {
			// Удаляем записи старше окна
			var valid []time.Time
			for _, t := range times {
				if now.Sub(t) < rl.window {
					valid = append(valid, t)
				}
			}
			if len(valid) == 0 {
				delete(rl.requests, ip)
			} else {
				rl.requests[ip] = valid
			}
		}
		rl.mu.Unlock()
	}
}

// Allow проверяет, разрешен ли запрос для данного IP
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	times := rl.requests[ip]

	// Удаляем старые запросы за пределами временного окна
	var valid []time.Time
	for _, t := range times {
		if now.Sub(t) < rl.window {
			valid = append(valid, t)
		}
	}

	// Проверяем лимит
	if len(valid) >= rl.rate {
		return false
	}

	// Добавляем текущий запрос
	valid = append(valid, now)
	rl.requests[ip] = valid

	return true
}

// RateLimitMiddleware создает middleware для ограничения запросов
// rate - количество запросов, window - временное окно
func RateLimitMiddleware(rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(rate, window)

	return func(c *gin.Context) {
		// Получаем IP клиента
		ip := c.ClientIP()

		if !limiter.Allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Слишком много запросов. Пожалуйста, подождите немного.",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// StrictRateLimitMiddleware - строгий лимит для чувствительных эндпоинтов (логин, регистрация)
func StrictRateLimitMiddleware() gin.HandlerFunc {
	// 5 запросов в минуту
	return RateLimitMiddleware(5, 1*time.Minute)
}

// ModerateRateLimitMiddleware - умеренный лимит для API эндпоинтов
func ModerateRateLimitMiddleware() gin.HandlerFunc {
	// 60 запросов в минуту
	return RateLimitMiddleware(60, 1*time.Minute)
}

// RelaxedRateLimitMiddleware - мягкий лимит для общих запросов
func RelaxedRateLimitMiddleware() gin.HandlerFunc {
	// 120 запросов в минуту
	return RateLimitMiddleware(120, 1*time.Minute)
}
