package adkagent

import "time"

// EscalationState tracks escalation status for a chat
type EscalationState struct {
	EscalatedAt   time.Time
	AdminNotified bool
	ReturnedAt    *time.Time
}

// truncate truncates a string to maxLen
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
