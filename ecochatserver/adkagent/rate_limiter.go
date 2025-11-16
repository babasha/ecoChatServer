package adkagent

import (
	"log"
	"sync"
	"time"
)

// RateLimiter защита от превышения Gemini free tier лимитов
type RateLimiter struct {
	mu sync.Mutex

	// Лимиты для Gemini 2.5 Flash Free Tier
	maxRPM int // Requests Per Minute (default: 10)
	maxRPD int // Requests Per Day (default: 250)

	// Счетчики
	requestsThisMinute int
	requestsToday      int

	// Временные метки
	minuteStart time.Time
	dayStart    time.Time
}

// NewRateLimiter создает новый rate limiter с дефолтными лимитами Gemini Free Tier
func NewRateLimiter(maxRPM, maxRPD int) *RateLimiter {
	now := time.Now()
	return &RateLimiter{
		maxRPM:             maxRPM,
		maxRPD:             maxRPD,
		requestsThisMinute: 0,
		requestsToday:      0,
		minuteStart:        now,
		dayStart:           now,
	}
}

// NewGeminiFreeTierLimiter создает rate limiter с лимитами Gemini 2.5 Flash Free Tier
func NewGeminiFreeTierLimiter() *RateLimiter {
	return NewRateLimiter(10, 250) // 10 RPM, 250 RPD
}

// AllowRequest проверяет можно ли выполнить запрос
// Возвращает true если можно, false если превышен лимит
func (rl *RateLimiter) AllowRequest() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Сброс счетчика минуты если прошла минута
	if now.Sub(rl.minuteStart) >= time.Minute {
		rl.requestsThisMinute = 0
		rl.minuteStart = now
		log.Printf("[RATE_LIMITER] Сброс счетчика минуты (было %d запросов)", rl.requestsThisMinute)
	}

	// Сброс счетчика дня если прошел день
	if now.Sub(rl.dayStart) >= 24*time.Hour {
		rl.requestsToday = 0
		rl.dayStart = now
		log.Printf("[RATE_LIMITER] Сброс счетчика дня (было %d запросов)", rl.requestsToday)
	}

	// Проверка лимита RPM
	if rl.requestsThisMinute >= rl.maxRPM {
		log.Printf("[RATE_LIMITER] ⚠️ Превышен лимит RPM: %d/%d", rl.requestsThisMinute, rl.maxRPM)
		return false
	}

	// Проверка лимита RPD
	if rl.requestsToday >= rl.maxRPD {
		log.Printf("[RATE_LIMITER] ⚠️ Превышен дневной лимит: %d/%d", rl.requestsToday, rl.maxRPD)
		return false
	}

	// Разрешаем запрос и увеличиваем счетчики
	rl.requestsThisMinute++
	rl.requestsToday++

	log.Printf("[RATE_LIMITER] ✅ Запрос разрешен: RPM=%d/%d, RPD=%d/%d",
		rl.requestsThisMinute, rl.maxRPM,
		rl.requestsToday, rl.maxRPD)

	return true
}

// GetStats возвращает текущую статистику использования
func (rl *RateLimiter) GetStats() (requestsThisMinute, requestsToday, maxRPM, maxRPD int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	// Учитываем сброс счетчиков
	rpm := rl.requestsThisMinute
	if now.Sub(rl.minuteStart) >= time.Minute {
		rpm = 0
	}

	rpd := rl.requestsToday
	if now.Sub(rl.dayStart) >= 24*time.Hour {
		rpd = 0
	}

	return rpm, rpd, rl.maxRPM, rl.maxRPD
}

// WaitUntilAllowed блокирует выполнение до момента когда можно будет сделать запрос
// Возвращает время ожидания
func (rl *RateLimiter) WaitUntilAllowed() time.Duration {
	startWait := time.Now()

	for !rl.AllowRequest() {
		rl.mu.Lock()
		now := time.Now()

		// Вычисляем когда освободится лимит
		var waitTime time.Duration

		// Если превышен дневной лимит - ждем до следующего дня
		if rl.requestsToday >= rl.maxRPD {
			waitTime = rl.dayStart.Add(24 * time.Hour).Sub(now)
			log.Printf("[RATE_LIMITER] ⏰ Дневной лимит исчерпан, ожидание %v до сброса", waitTime)
		} else {
			// Если превышен минутный лимит - ждем до следующей минуты
			waitTime = rl.minuteStart.Add(time.Minute).Sub(now)
			log.Printf("[RATE_LIMITER] ⏰ Минутный лимит исчерпан, ожидание %v до сброса", waitTime)
		}

		rl.mu.Unlock()

		// Ждем минимум 100ms чтобы не спамить
		if waitTime < 100*time.Millisecond {
			waitTime = 100 * time.Millisecond
		}

		time.Sleep(waitTime)
	}

	return time.Since(startWait)
}

// ResetCounters сбрасывает все счетчики (для тестирования)
func (rl *RateLimiter) ResetCounters() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.requestsThisMinute = 0
	rl.requestsToday = 0
	rl.minuteStart = now
	rl.dayStart = now

	log.Printf("[RATE_LIMITER] 🔄 Счетчики сброшены")
}
