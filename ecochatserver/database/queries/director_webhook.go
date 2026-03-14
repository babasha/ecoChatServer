package queries

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertWebhookEvent logs an incoming webhook event.
func InsertWebhookEvent(db *sql.DB, evt *models.WebhookEvent) error {
	metaJSON, err := json.Marshal(evt.Metadata)
	if err != nil {
		metaJSON = []byte("{}")
	}

	_, err = db.Exec(`
		INSERT INTO director_webhook_events
			(id, source, event_type, message, metadata, idempotency_key, action, priority, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		evt.ID, evt.Source, evt.EventType, evt.Message, metaJSON,
		evt.IdempotencyKey, evt.Action, evt.Priority, evt.Status, evt.CreatedAt,
	)
	return err
}

// CheckIdempotencyKey returns true if this key was already processed (within 24h).
func CheckIdempotencyKey(db *sql.DB, key string) (bool, error) {
	if key == "" {
		return false, nil
	}

	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM director_webhook_events
			WHERE idempotency_key = $1
			  AND created_at > NOW() - INTERVAL '24 hours'
		)`, key).Scan(&exists)
	return exists, err
}

// UpdateWebhookEventStatus updates the processing status of a webhook event.
func UpdateWebhookEventStatus(db *sql.DB, id uuid.UUID, status, errMsg string) error {
	var processedAt *time.Time
	if status == "completed" || status == "failed" {
		now := time.Now()
		processedAt = &now
	}

	_, err := db.Exec(`
		UPDATE director_webhook_events
		SET status = $2, error = $3, processed_at = $4
		WHERE id = $1`,
		id, status, errMsg, processedAt,
	)
	return err
}

// GetRecentWebhookEvents returns recent webhook events for monitoring.
func GetRecentWebhookEvents(db *sql.DB, limit int) ([]models.WebhookEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := db.Query(`
		SELECT id, source, event_type, message, metadata,
		       idempotency_key, action, priority, status,
		       COALESCE(error, ''), processed_at, created_at
		FROM director_webhook_events
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.WebhookEvent
	for rows.Next() {
		var evt models.WebhookEvent
		var metaJSON []byte
		err := rows.Scan(
			&evt.ID, &evt.Source, &evt.EventType, &evt.Message, &metaJSON,
			&evt.IdempotencyKey, &evt.Action, &evt.Priority, &evt.Status,
			&evt.Error, &evt.ProcessedAt, &evt.CreatedAt,
		)
		if err != nil {
			continue
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &evt.Metadata)
		}
		events = append(events, evt)
	}
	return events, nil
}

// GetWebhookStats returns aggregated webhook usage statistics.
func GetWebhookStats(db *sql.DB) (*models.WebhookStats, error) {
	stats := &models.WebhookStats{
		BySources:   make(map[string]int),
		ByEventType: make(map[string]int),
	}

	// Total counts by status
	rows, err := db.Query(`
		SELECT status, COUNT(*)
		FROM director_webhook_events
		GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		switch status {
		case "completed", "processing":
			stats.TotalProcessed += count
		case "duplicate":
			stats.TotalDuplicate += count
		case "failed":
			stats.TotalFailed += count
		}
		stats.TotalReceived += count
	}

	// By source
	sourceRows, err := db.Query(`
		SELECT source, COUNT(*)
		FROM director_webhook_events
		GROUP BY source ORDER BY COUNT(*) DESC LIMIT 20`)
	if err == nil {
		defer sourceRows.Close()
		for sourceRows.Next() {
			var source string
			var count int
			if err := sourceRows.Scan(&source, &count); err == nil {
				stats.BySources[source] = count
			}
		}
	}

	// By event type
	typeRows, err := db.Query(`
		SELECT event_type, COUNT(*)
		FROM director_webhook_events
		GROUP BY event_type ORDER BY COUNT(*) DESC LIMIT 20`)
	if err == nil {
		defer typeRows.Close()
		for typeRows.Next() {
			var eventType string
			var count int
			if err := typeRows.Scan(&eventType, &count); err == nil {
				stats.ByEventType[eventType] = count
			}
		}
	}

	// Last event timestamp
	err = db.QueryRow(`
		SELECT MAX(created_at) FROM director_webhook_events`).Scan(&stats.LastEventAt)
	if err != nil {
		stats.LastEventAt = nil
	}

	return stats, nil
}

// CleanupOldWebhookEvents removes events older than the given number of days.
func CleanupOldWebhookEvents(db *sql.DB, retentionDays int) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM director_webhook_events
		WHERE created_at < NOW() - ($1 || ' days')::INTERVAL`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
