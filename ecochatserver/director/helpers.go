package director

import (
	"github.com/egor/ecochatserver/models"
)

// AgentNames is the canonical list of L1 agent names used across the system.
var AgentNames = []string{
	"zefir_support", "plant_expert", "device_specialist",
	"support_specialist", "orchestrator",
}

// ValidMemoryCategories defines allowed memory category values.
var ValidMemoryCategories = map[string]bool{
	"fact": true, "decision": true, "pattern": true,
	"insight": true, "preference": true,
}

// AggregatedMetrics holds computed aggregate stats across all agents.
type AggregatedMetrics struct {
	TotalCalls int
	EscRate    float64
	EmptyRate  float64
	AvgMs      float64
}

// AggregateAgentStats computes combined metrics from per-agent stats.
func AggregateAgentStats(stats []models.AgentStats) AggregatedMetrics {
	var totalCalls, totalEsc, totalEmpty int
	var totalResponseMs float64
	for _, s := range stats {
		totalCalls += s.TotalCalls
		totalEsc += s.Escalations
		totalEmpty += s.EmptyResponses
		totalResponseMs += s.AvgResponseMs * float64(s.TotalCalls)
	}

	var m AggregatedMetrics
	m.TotalCalls = totalCalls
	if totalCalls > 0 {
		m.EscRate = float64(totalEsc) / float64(totalCalls)
		m.EmptyRate = float64(totalEmpty) / float64(totalCalls)
		m.AvgMs = totalResponseMs / float64(totalCalls)
	}
	return m
}

// truncate trims a string to maxLen and appends "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
