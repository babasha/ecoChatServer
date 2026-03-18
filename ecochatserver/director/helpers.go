package director

import (
	"fmt"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
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
	"insight": true, "preference": true, "lesson": true,
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

// savePendingDecision saves a high-risk Critic-approved decision as pending for admin review.
func savePendingDecision(decisionType, name, description string, verdict *CriticVerdict, extraTags []string) {
	key := fmt.Sprintf("pending_%s:%s:%s", decisionType, name, time.Now().Format("2006-01-02"))
	content := fmt.Sprintf("Pending %s '%s': %s\nConcerns: %s",
		decisionType, name, truncate(description, 200), strings.Join(verdict.Concerns, "; "))
	tags := append([]string{"pending", decisionType, "high_risk"}, extraTags...)

	database.SaveAutoMemory("decision", key, content, tags, nil)
}

// stripCodeBlock removes ``` wrappers from LLM output.
func stripCodeBlock(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	// Skip language tag on first line
	if idx := strings.Index(text[3:], "\n"); idx >= 0 {
		text = text[3+idx+1:]
	} else {
		text = text[3:]
	}
	if idx := strings.LastIndex(text, "```"); idx >= 0 {
		text = text[:idx]
	}
	return strings.TrimSpace(text)
}

// truncate trims a string to maxLen and appends "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
