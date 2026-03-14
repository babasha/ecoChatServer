package models

import (
	"time"

	"github.com/google/uuid"
)

// WebhookEvent represents an incoming webhook trigger for the Director.
type WebhookEvent struct {
	ID             uuid.UUID              `json:"id"`
	Source         string                 `json:"source"`          // e.g. "grafana", "crm", "monitoring", "custom"
	EventType      string                 `json:"event_type"`      // e.g. "alert", "escalation", "metric_anomaly"
	Message        string                 `json:"message"`         // human-readable description
	Metadata       map[string]interface{} `json:"metadata"`        // arbitrary payload from external system
	IdempotencyKey string                 `json:"idempotency_key"` // for deduplication
	Action         string                 `json:"action"`          // "analyze", "notify", "chat" — what Director should do
	Priority       string                 `json:"priority"`        // "low", "normal", "high", "critical"
	Status         string                 `json:"status"`          // "received", "processing", "completed", "duplicate", "failed"
	Error          string                 `json:"error,omitempty"`
	ProcessedAt    *time.Time             `json:"processed_at,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
}

// WebhookEventRequest is the incoming payload from external systems.
type WebhookEventRequest struct {
	Source         string                 `json:"source" binding:"required"`
	EventType      string                 `json:"event_type" binding:"required"`
	Message        string                 `json:"message" binding:"required"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
	Action         string                 `json:"action,omitempty"`  // default: "analyze"
	Priority       string                 `json:"priority,omitempty"` // default: "normal"
}

// WebhookStats provides usage statistics for the webhook endpoint.
type WebhookStats struct {
	TotalReceived  int            `json:"total_received"`
	TotalProcessed int            `json:"total_processed"`
	TotalDuplicate int            `json:"total_duplicate"`
	TotalFailed    int            `json:"total_failed"`
	BySources      map[string]int `json:"by_sources"`
	ByEventType    map[string]int `json:"by_event_type"`
	LastEventAt    *time.Time     `json:"last_event_at,omitempty"`
}
