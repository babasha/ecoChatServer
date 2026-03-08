package adkagent

import (
	"time"

	"github.com/egor/ecochatserver/models"
)

// EscalationState tracks escalation status for a chat
type EscalationState struct {
	EscalatedAt   time.Time
	AdminNotified bool
	ReturnedAt    *time.Time
}

// AgentResult holds the response text and captured tool call details
type AgentResult struct {
	Response    string
	ToolsCalled []models.ToolCall
}

// truncate truncates a string to maxLen
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
