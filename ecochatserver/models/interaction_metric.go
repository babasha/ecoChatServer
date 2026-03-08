package models

import (
	"time"

	"github.com/google/uuid"
)

// ToolCall represents a single tool invocation within an interaction
type ToolCall struct {
	Name   string `json:"name"`
	Result string `json:"result"` // "success", "empty", "error"
	Args   string `json:"args,omitempty"`
}

// InteractionMetric tracks one bot response with full agent/tool breakdown
type InteractionMetric struct {
	ID             uuid.UUID  `json:"id"`
	ChatID         uuid.UUID  `json:"chatId"`
	MessageID      *uuid.UUID `json:"messageId,omitempty"`
	AgentMode      string     `json:"agentMode"`
	AgentName      string     `json:"agentName"`
	PromptVersion  *int       `json:"promptVersion,omitempty"`
	ToolsCalled    []ToolCall `json:"toolsCalled"`
	ToolCount      int        `json:"toolCount"`
	WasEscalated   bool       `json:"wasEscalated"`
	WasEmpty       bool       `json:"wasEmpty"`
	ResponseLength int        `json:"responseLength"`
	ResponseTimeMs int        `json:"responseTimeMs"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// AgentStats represents aggregated metrics for one agent over a time period
type AgentStats struct {
	AgentName        string      `json:"agentName"`
	TotalCalls       int         `json:"totalCalls"`
	Escalations      int         `json:"escalations"`
	EscalationRate   float64     `json:"escalationRate"`
	EmptyResponses   int         `json:"emptyResponses"`
	EmptyRate        float64     `json:"emptyRate"`
	AvgResponseLen   float64     `json:"avgResponseLen"`
	AvgResponseMs    float64     `json:"avgResponseMs"`
	ToolStats        []ToolStats `json:"toolStats"`
}

// ToolStats represents aggregated metrics for one tool
type ToolStats struct {
	ToolName     string  `json:"toolName"`
	TotalCalls   int     `json:"totalCalls"`
	SuccessCount int     `json:"successCount"`
	EmptyCount   int     `json:"emptyCount"`
	ErrorCount   int     `json:"errorCount"`
	SuccessRate  float64 `json:"successRate"`
}
