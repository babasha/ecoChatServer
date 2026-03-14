package models

import (
	"time"

	"github.com/google/uuid"
)

// CronJob represents a scheduled task that the Director can create and manage.
type CronJob struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	ScheduleType string     `json:"schedule_type"` // "at", "every", "cron"
	Schedule     string     `json:"schedule"`       // ISO datetime for "at", duration for "every", cron expr for "cron"
	Timezone     string     `json:"timezone"`       // IANA timezone (e.g. "Europe/Moscow"), used with "cron"
	Action       string     `json:"action"`         // "analyze", "run_tool", "send_message"
	ActionConfig string     `json:"action_config"`  // JSON config for the action (tool name, message text, etc.)
	Enabled      bool       `json:"enabled"`
	CreatedBy    string     `json:"created_by"`     // "director", "admin", "system"
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	NextRunAt    *time.Time `json:"next_run_at,omitempty"`
	RunCount     int        `json:"run_count"`
	LastError    string     `json:"last_error,omitempty"`
	MaxRuns      int        `json:"max_runs"`       // 0 = unlimited, 1 = one-shot (for "at" type)
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// CronJobRequest is the payload for creating/updating a cron job.
type CronJobRequest struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	ScheduleType string `json:"schedule_type" binding:"required"` // "at", "every", "cron"
	Schedule     string `json:"schedule" binding:"required"`
	Timezone     string `json:"timezone,omitempty"`
	Action       string `json:"action,omitempty"`       // default: "analyze"
	ActionConfig string `json:"action_config,omitempty"` // JSON
	MaxRuns      int    `json:"max_runs,omitempty"`
}

// CronRunLog records the execution of a cron job.
type CronRunLog struct {
	ID        uuid.UUID `json:"id"`
	JobID     uuid.UUID `json:"job_id"`
	Status    string    `json:"status"`     // "success", "failed", "skipped"
	Duration  int       `json:"duration_ms"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
