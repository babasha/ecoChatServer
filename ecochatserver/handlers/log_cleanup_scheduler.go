package handlers

import (
	"context"
	"log"
	"time"
)

// StartLogCleanupScheduler запускает периодическую очистку старых логов
func StartLogCleanupScheduler() {
	// Запускаем очистку раз в час
	ticker := time.NewTicker(1 * time.Hour)

	go func() {
		// Первая очистка через 5 минут после старта
		time.Sleep(5 * time.Minute)
		cleanupOldLogs()

		// Затем каждый час
		for range ticker.C {
			cleanupOldLogs()
		}
	}()

	log.Println("[LOGS_CLEANUP] Scheduler started: cleanup runs every hour")
}

// cleanupOldLogs вызывает функцию очистки старых логов в БД
func cleanupOldLogs() {
	if logsDB == nil {
		log.Println("[LOGS_CLEANUP] WARNING: logsDB is nil, skipping cleanup")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()

	// Вызываем функцию PostgreSQL
	_, err := logsDB.ExecContext(ctx, "SELECT cleanup_old_logs()")
	if err != nil {
		log.Printf("[LOGS_CLEANUP] ERROR: Failed to cleanup old logs: %v", err)
		return
	}

	duration := time.Since(start)
	log.Printf("[LOGS_CLEANUP] ✅ Old logs cleaned up successfully (took %v)", duration)
}
