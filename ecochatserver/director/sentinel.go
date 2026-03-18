package director

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// ============================================================================
// Sentinel — pattern-based anomaly detection (inspired by AutoResearchClaw)
//
// Unlike EventTracker which uses simple threshold counts,
// Sentinel detects correlations and anomalies across events:
// - Same-agent failure cluster
// - Same-tool repeated failure
// - Cascading failures (tool fail → empty → escalation in same chat)
// - Sudden rate spike vs baseline
// - Repeated escalation on same topic
// ============================================================================

// SentinelEvent is a rich event with metadata for pattern detection.
type SentinelEvent struct {
	Type      EventType
	ChatID    string
	AgentName string
	ToolName  string
	Topic     string
	Timestamp time.Time
}

// SentinelAlert describes a detected anomaly pattern.
type SentinelAlert struct {
	Pattern     string // pattern name
	Severity    string // low, medium, high, critical
	Description string // human-readable description
	Details     map[string]string
	DetectedAt  time.Time
}

// Sentinel watches for anomaly patterns in event streams.
type Sentinel struct {
	mu     sync.Mutex
	events []SentinelEvent // recent events (rolling window)
	alerts []SentinelAlert // recent alerts (for dedup)

	// Baseline rates (updated hourly) for spike detection
	baselineEscRate   float64 // escalations per hour (rolling avg)
	baselineEmptyRate float64 // empty responses per hour
	baselineSamples   int     // how many hours of baseline data
}

// NewSentinel creates a new pattern detector.
func NewSentinel() *Sentinel {
	return &Sentinel{
		events: make([]SentinelEvent, 0, 500),
	}
}

// RecordEvent adds a new event and checks for patterns.
// Returns an alert if a pattern is detected, nil otherwise.
func (s *Sentinel) RecordEvent(event SentinelEvent) *SentinelAlert {
	s.mu.Lock()
	defer s.mu.Unlock()

	event.Timestamp = time.Now()
	s.events = append(s.events, event)

	// Run all pattern checks
	if alert := s.checkSameAgentCluster(event); alert != nil {
		return s.dedup(alert)
	}
	if alert := s.checkSameToolRepeated(event); alert != nil {
		return s.dedup(alert)
	}
	if alert := s.checkCascadingFailure(event); alert != nil {
		return s.dedup(alert)
	}
	if alert := s.checkRateSpike(event); alert != nil {
		return s.dedup(alert)
	}
	if alert := s.checkSameTopicEscalations(event); alert != nil {
		return s.dedup(alert)
	}

	return nil
}

// ============================================================================
// Pattern 1: Same-agent failure cluster
// If one agent has 3+ escalations/empty in 30 min, its prompt is likely broken
// ============================================================================

func (s *Sentinel) checkSameAgentCluster(latest SentinelEvent) *SentinelAlert {
	if latest.AgentName == "" {
		return nil
	}
	if latest.Type != EventEscalation && latest.Type != EventEmptyResponse {
		return nil
	}

	window := 30 * time.Minute
	cutoff := latest.Timestamp.Add(-window)
	count := 0

	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Timestamp.Before(cutoff) {
			break
		}
		if e.AgentName == latest.AgentName &&
			(e.Type == EventEscalation || e.Type == EventEmptyResponse) {
			count++
		}
	}

	if count >= 3 {
		return &SentinelAlert{
			Pattern:     "agent_cluster",
			Severity:    "high",
			Description: fmt.Sprintf("Agent '%s' has %d failures in %v — possible prompt issue", latest.AgentName, count, window),
			Details: map[string]string{
				"agent": latest.AgentName,
				"count": fmt.Sprintf("%d", count),
			},
			DetectedAt: latest.Timestamp,
		}
	}
	return nil
}

// ============================================================================
// Pattern 2: Same-tool repeated failure
// If one tool fails 3+ times in 15 min, the tool/API is likely down
// ============================================================================

func (s *Sentinel) checkSameToolRepeated(latest SentinelEvent) *SentinelAlert {
	if latest.Type != EventToolFailure || latest.ToolName == "" {
		return nil
	}

	window := 15 * time.Minute
	cutoff := latest.Timestamp.Add(-window)
	count := 0

	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Timestamp.Before(cutoff) {
			break
		}
		if e.Type == EventToolFailure && e.ToolName == latest.ToolName {
			count++
		}
	}

	if count >= 3 {
		return &SentinelAlert{
			Pattern:     "tool_repeated_failure",
			Severity:    "critical",
			Description: fmt.Sprintf("Tool '%s' failed %d times in %v — likely down or broken", latest.ToolName, count, window),
			Details: map[string]string{
				"tool":  latest.ToolName,
				"count": fmt.Sprintf("%d", count),
			},
			DetectedAt: latest.Timestamp,
		}
	}
	return nil
}

// ============================================================================
// Pattern 3: Cascading failure
// tool_failure → empty_response → escalation in the same chat within 10 min
// ============================================================================

func (s *Sentinel) checkCascadingFailure(latest SentinelEvent) *SentinelAlert {
	if latest.Type != EventEscalation || latest.ChatID == "" {
		return nil
	}

	window := 10 * time.Minute
	cutoff := latest.Timestamp.Add(-window)

	hasToolFail := false
	hasEmpty := false
	toolName := ""

	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Timestamp.Before(cutoff) {
			break
		}
		if e.ChatID != latest.ChatID {
			continue
		}
		if e.Type == EventToolFailure {
			hasToolFail = true
			toolName = e.ToolName
		}
		if e.Type == EventEmptyResponse {
			hasEmpty = true
		}
	}

	if hasToolFail && hasEmpty {
		return &SentinelAlert{
			Pattern:     "cascading_failure",
			Severity:    "high",
			Description: fmt.Sprintf("Cascade in chat %s: tool '%s' fail → empty response → escalation", truncateID(latest.ChatID, 8), toolName),
			Details: map[string]string{
				"chat_id": latest.ChatID,
				"tool":    toolName,
			},
			DetectedAt: latest.Timestamp,
		}
	}
	return nil
}

// ============================================================================
// Pattern 4: Rate spike vs baseline
// If current 30-min rate is 3x the hourly baseline, something changed
// ============================================================================

func (s *Sentinel) checkRateSpike(latest SentinelEvent) *SentinelAlert {
	if latest.Type != EventEscalation && latest.Type != EventEmptyResponse {
		return nil
	}

	// Need at least some baseline data
	if s.baselineSamples < 3 {
		return nil
	}

	// Count events of this type in last 30 min
	window := 30 * time.Minute
	cutoff := latest.Timestamp.Add(-window)
	count := 0
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Timestamp.Before(cutoff) {
			break
		}
		if e.Type == latest.Type {
			count++
		}
	}

	// Extrapolate to hourly rate
	currentRate := float64(count) * 2.0 // 30min window → hourly rate

	var baseline float64
	if latest.Type == EventEscalation {
		baseline = s.baselineEscRate
	} else {
		baseline = s.baselineEmptyRate
	}

	// Spike = 3x baseline (with minimum absolute threshold)
	if baseline > 0 && currentRate > baseline*3 && currentRate > 4 {
		return &SentinelAlert{
			Pattern:  "rate_spike",
			Severity: "high",
			Description: fmt.Sprintf("%s rate spike: %.0f/hr (baseline: %.0f/hr, %.0fx)",
				latest.Type, currentRate, baseline, currentRate/baseline),
			Details: map[string]string{
				"event_type":   string(latest.Type),
				"current_rate": fmt.Sprintf("%.1f", currentRate),
				"baseline":     fmt.Sprintf("%.1f", baseline),
				"multiplier":   fmt.Sprintf("%.1fx", currentRate/baseline),
			},
			DetectedAt: latest.Timestamp,
		}
	}
	return nil
}

// ============================================================================
// Pattern 5: Same-topic escalation burst
// 3+ escalations on the same topic in 1 hour → FAQ gap or specific issue
// ============================================================================

func (s *Sentinel) checkSameTopicEscalations(latest SentinelEvent) *SentinelAlert {
	if latest.Type != EventEscalation || latest.Topic == "" {
		return nil
	}

	window := 1 * time.Hour
	cutoff := latest.Timestamp.Add(-window)
	count := 0

	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if e.Timestamp.Before(cutoff) {
			break
		}
		if e.Type == EventEscalation && e.Topic == latest.Topic {
			count++
		}
	}

	if count >= 3 {
		return &SentinelAlert{
			Pattern:     "topic_escalation_burst",
			Severity:    "medium",
			Description: fmt.Sprintf("Topic '%s' caused %d escalations in 1hr — possible FAQ gap", latest.Topic, count),
			Details: map[string]string{
				"topic": latest.Topic,
				"count": fmt.Sprintf("%d", count),
			},
			DetectedAt: latest.Timestamp,
		}
	}
	return nil
}

// ============================================================================
// Deduplication — don't fire the same alert twice in a row
// ============================================================================

func (s *Sentinel) dedup(alert *SentinelAlert) *SentinelAlert {
	// Check if same pattern was alerted in last 15 min
	cooldown := 15 * time.Minute
	for i := len(s.alerts) - 1; i >= 0; i-- {
		a := s.alerts[i]
		if alert.DetectedAt.Sub(a.DetectedAt) > cooldown {
			break
		}
		if a.Pattern == alert.Pattern {
			// Same pattern within cooldown — check if same target
			if a.Details["agent"] == alert.Details["agent"] &&
				a.Details["tool"] == alert.Details["tool"] &&
				a.Details["topic"] == alert.Details["topic"] &&
				a.Details["chat_id"] == alert.Details["chat_id"] &&
				a.Details["event_type"] == alert.Details["event_type"] {
				return nil // duplicate
			}
		}
	}

	s.alerts = append(s.alerts, *alert)

	log.Printf("[SENTINEL] Alert: [%s/%s] %s", alert.Pattern, alert.Severity, alert.Description)

	// Extract lesson from the alert
	ExtractLesson(LessonSystemError, lessonSeverity(alert.Severity), "sentinel",
		fmt.Sprintf("Pattern detected: %s", alert.Description), alert.Details)

	return alert
}

func lessonSeverity(s string) LessonSeverity {
	switch s {
	case "critical":
		return SeverityCritical
	case "high":
		return SeverityHigh
	case "medium":
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// ============================================================================
// Baseline maintenance — called periodically to update rolling averages
// ============================================================================

// UpdateBaseline recalculates hourly event rates from the last 6 hours.
// Called from periodicCleanup().
func (s *Sentinel) UpdateBaseline() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	window := 6 * time.Hour
	cutoff := now.Add(-window)

	var escCount, emptyCount int
	for _, e := range s.events {
		if e.Timestamp.Before(cutoff) {
			continue
		}
		switch e.Type {
		case EventEscalation:
			escCount++
		case EventEmptyResponse:
			emptyCount++
		}
	}

	hours := window.Hours()
	s.baselineEscRate = float64(escCount) / hours
	s.baselineEmptyRate = float64(emptyCount) / hours
	s.baselineSamples++

	if s.baselineSamples <= 10 {
		log.Printf("[SENTINEL] Baseline updated (sample %d): esc=%.1f/hr, empty=%.1f/hr",
			s.baselineSamples, s.baselineEscRate, s.baselineEmptyRate)
	}
}

// Cleanup removes old events and alerts to prevent memory growth.
func (s *Sentinel) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Keep last 6 hours of events
	eventCutoff := time.Now().Add(-6 * time.Hour)
	var kept []SentinelEvent
	for _, e := range s.events {
		if e.Timestamp.After(eventCutoff) {
			kept = append(kept, e)
		}
	}
	s.events = kept

	// Keep last 1 hour of alerts (for dedup)
	alertCutoff := time.Now().Add(-1 * time.Hour)
	var keptAlerts []SentinelAlert
	for _, a := range s.alerts {
		if a.DetectedAt.After(alertCutoff) {
			keptAlerts = append(keptAlerts, a)
		}
	}
	s.alerts = keptAlerts
}

// GetRecentAlerts returns alerts from the last N minutes.
func (s *Sentinel) GetRecentAlerts(window time.Duration) []SentinelAlert {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-window)
	var result []SentinelAlert
	for _, a := range s.alerts {
		if a.DetectedAt.After(cutoff) {
			result = append(result, a)
		}
	}
	return result
}
