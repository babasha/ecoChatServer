package models

import (
	"time"

	"github.com/google/uuid"
)

// AgentPrompt represents a versioned system prompt for an AI agent
type AgentPrompt struct {
	ID            uuid.UUID      `json:"id"`
	AgentName     string         `json:"agentName"`
	Version       int            `json:"version"`
	Prompt        string         `json:"prompt"`
	CreatedBy     string         `json:"createdBy"` // "human" | "director"
	ParentVersion *int           `json:"parentVersion,omitempty"`
	IsActive      bool           `json:"isActive"`
	Metrics       *PromptMetrics `json:"metrics,omitempty"`
	Notes         string         `json:"notes,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// PromptMetrics tracks performance of a prompt version
type PromptMetrics struct {
	ChatsHandled       int     `json:"chats_handled"`
	EscalationRate     float64 `json:"escalation_rate"`
	EmptyResponseRate  float64 `json:"empty_response_rate"`
	AvgMessagesToResolve float64 `json:"avg_messages_to_resolve"`
	ToolUsageRate      float64 `json:"tool_usage_rate"`
	AvgResponseLength  float64 `json:"avg_response_length"`
	DaysActive         int     `json:"days_active"`
}

// Known agent names
const (
	AgentNameSupport     = "zefir_support"
	AgentNameOrchestrator = "orchestrator"
	AgentNamePlantExpert = "plant_expert"
	AgentNameDeviceSpec  = "device_specialist"
	AgentNameSupportSpec = "support_specialist"
)
