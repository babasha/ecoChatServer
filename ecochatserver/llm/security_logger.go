package llm

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// SecurityEventType представляет тип события безопасности
type SecurityEventType string

const (
	SecurityEventUnauthorizedOrderAccess SecurityEventType = "UNAUTHORIZED_ORDER_ACCESS"
	SecurityEventOrderAccessDenied       SecurityEventType = "ORDER_ACCESS_DENIED"
	SecurityEventOrderAccessGranted      SecurityEventType = "ORDER_ACCESS_GRANTED"
	SecurityEventSuspiciousActivity      SecurityEventType = "SUSPICIOUS_ACTIVITY"
)

// SecurityEvent представляет событие безопасности
type SecurityEvent struct {
	Timestamp   time.Time
	EventType   SecurityEventType
	ChatID      uuid.UUID
	UserID      int
	OrderID     string
	UserEmail   string
	Message     string
	IPAddress   string
	UserAgent   string
	Severity    string // "low", "medium", "high", "critical"
	Blocked     bool
	Description string
}

// LogSecurityEvent записывает событие безопасности в лог
func LogSecurityEvent(event SecurityEvent) {
	severity := event.Severity
	if severity == "" {
		severity = "medium"
	}

	blocked := ""
	if event.Blocked {
		blocked = " [BLOCKED]"
	}

	logMessage := fmt.Sprintf(
		"[SECURITY%s] [%s] %s | ChatID=%s | UserID=%d | OrderID=%s | Email=%s | Message=%s",
		blocked,
		severity,
		event.EventType,
		event.ChatID,
		event.UserID,
		event.OrderID,
		event.UserEmail,
		event.Message,
	)

	// В зависимости от серьезности используем разные уровни логирования
	switch severity {
	case "critical", "high":
		log.Printf("🚨 %s", logMessage)
	case "medium":
		log.Printf("⚠️  %s", logMessage)
	default:
		log.Printf("ℹ️  %s", logMessage)
	}

	// Здесь можно добавить отправку в систему мониторинга
	// например, отправку в Sentry, DataDog, или специализированную SIEM систему
}

// LogUnauthorizedOrderAccess логирует попытку неавторизованного доступа к заказу
func LogUnauthorizedOrderAccess(chatID uuid.UUID, orderID string, userEmail string) {
	LogSecurityEvent(SecurityEvent{
		Timestamp:   time.Now(),
		EventType:   SecurityEventUnauthorizedOrderAccess,
		ChatID:      chatID,
		UserID:      0,
		OrderID:     orderID,
		UserEmail:   userEmail,
		Message:     "Попытка доступа к заказу без авторизации",
		Severity:    "high",
		Blocked:     true,
		Description: "Пользователь пытался получить доступ к информации о заказе без авторизации",
	})
}

// LogOrderAccessDenied логирует отказ в доступе к чужому заказу
func LogOrderAccessDenied(chatID uuid.UUID, userID int, orderID string, userEmail string) {
	LogSecurityEvent(SecurityEvent{
		Timestamp:   time.Now(),
		EventType:   SecurityEventOrderAccessDenied,
		ChatID:      chatID,
		UserID:      userID,
		OrderID:     orderID,
		UserEmail:   userEmail,
		Message:     "Попытка доступа к чужому заказу",
		Severity:    "critical",
		Blocked:     true,
		Description: fmt.Sprintf("Авторизованный пользователь (ID=%d) пытался получить доступ к заказу, который ему не принадлежит", userID),
	})
}

// LogOrderAccessGranted логирует успешный доступ к заказу
func LogOrderAccessGranted(chatID uuid.UUID, userID int, orderID string, userEmail string) {
	LogSecurityEvent(SecurityEvent{
		Timestamp:   time.Now(),
		EventType:   SecurityEventOrderAccessGranted,
		ChatID:      chatID,
		UserID:      userID,
		OrderID:     orderID,
		UserEmail:   userEmail,
		Message:     "Доступ к заказу предоставлен",
		Severity:    "low",
		Blocked:     false,
		Description: fmt.Sprintf("Пользователь (ID=%d) успешно получил доступ к своему заказу", userID),
	})
}

// LogSuspiciousActivity логирует подозрительную активность
func LogSuspiciousActivity(chatID uuid.UUID, userID int, userEmail string, reason string) {
	LogSecurityEvent(SecurityEvent{
		Timestamp:   time.Now(),
		EventType:   SecurityEventSuspiciousActivity,
		ChatID:      chatID,
		UserID:      userID,
		UserEmail:   userEmail,
		Message:     reason,
		Severity:    "high",
		Blocked:     true,
		Description: "Обнаружена подозрительная активность: " + reason,
	})
}
