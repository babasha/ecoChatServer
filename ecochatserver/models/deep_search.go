package models

import (
	"time"

	"github.com/google/uuid"
)

// DeepSearchResult represents a unified search result from any data source.
type DeepSearchResult struct {
	Type    string    `json:"type"`    // memory, report, chat_summary, directive, digest
	ID      string    `json:"id"`
	Snippet string    `json:"snippet"` // first 150 chars
	Date    time.Time `json:"date"`
	Rank    float64   `json:"rank"`
	Meta    string    `json:"meta,omitempty"` // extra context (category/key, report_type, etc.)
}

// TimelineData holds aggregated metrics for a given time period.
type TimelineData struct {
	PeriodStart       time.Time        `json:"period_start"`
	PeriodEnd         time.Time        `json:"period_end"`
	TotalInteractions int              `json:"total_interactions"`
	AvgEscalationRate float64          `json:"avg_escalation_rate"`
	AvgEmptyRate      float64          `json:"avg_empty_rate"`
	AvgResponseMs     float64          `json:"avg_response_ms"`
	ReportCount       int              `json:"report_count"`
	ReportSummaries   []string         `json:"report_summaries,omitempty"`
	DirectiveStats    map[string]int   `json:"directive_stats,omitempty"`
	TopAgents         []AgentStatsBrief `json:"top_agents,omitempty"`
}

// AgentStatsBrief is compact agent stats for timeline display.
type AgentStatsBrief struct {
	Name           string  `json:"name"`
	Calls          int     `json:"calls"`
	EscalationRate float64 `json:"escalation_rate"`
	EmptyRate      float64 `json:"empty_rate"`
}

// DirectorDigest is a consolidated summary for a time period.
type DirectorDigest struct {
	ID                uuid.UUID `json:"id"`
	PeriodType        string    `json:"period_type"` // weekly, monthly, quarterly
	PeriodStart       time.Time `json:"period_start"`
	PeriodEnd         time.Time `json:"period_end"`
	TotalChats        int       `json:"total_chats"`
	TotalInteractions int       `json:"total_interactions"`
	AvgEscalationRate float64   `json:"avg_escalation_rate"`
	AvgEmptyRate      float64   `json:"avg_empty_rate"`
	AvgResponseMs     float64   `json:"avg_response_ms"`
	Summary           string    `json:"summary"`
	KeyEvents         []string  `json:"key_events"`
	Trends            []string  `json:"trends"`
	Lessons           []string  `json:"lessons"`
	CreatedAt         time.Time `json:"created_at"`
}
