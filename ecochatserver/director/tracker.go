package director

import (
	"log"
	"sync"
	"time"
)

// EventType represents the type of tracked event
type EventType string

const (
	EventEscalation    EventType = "escalation"
	EventEmptyResponse EventType = "empty_response"
	EventToolFailure   EventType = "tool_failure"
)

// TrackerConfig configures event thresholds
type TrackerConfig struct {
	EscalationWindow    time.Duration // sliding window for escalation counting
	EscalationThreshold int           // escalations in window to trigger director
	EmptyRespThreshold  int           // empty responses per day to trigger
	CooldownPeriod      time.Duration // min time between director triggers
}

// DefaultTrackerConfig returns sensible defaults
func DefaultTrackerConfig() TrackerConfig {
	return TrackerConfig{
		EscalationWindow:    1 * time.Hour,
		EscalationThreshold: 3,
		EmptyRespThreshold:  5,
		CooldownPeriod:      30 * time.Minute,
	}
}

// EventTracker tracks events in-memory with sliding windows
type EventTracker struct {
	mu     sync.Mutex
	config TrackerConfig

	escalations    []time.Time
	emptyResponses []time.Time
	toolFailures   []time.Time

	// Topic frequency tracking (reset daily)
	topicCounts  map[string]int
	topicResetAt time.Time

	// Cooldown: prevent spamming director
	lastTrigger time.Time
}

// NewEventTracker creates a new tracker
func NewEventTracker(config TrackerConfig) *EventTracker {
	t := &EventTracker{
		config:       config,
		topicCounts:  make(map[string]int),
		topicResetAt: startOfTomorrow(),
	}

	log.Printf("[EVENT_TRACKER] Initialized: escalation_window=%v, escalation_threshold=%d, cooldown=%v",
		config.EscalationWindow, config.EscalationThreshold, config.CooldownPeriod)

	return t
}

// RecordEscalation records an escalation event
func (t *EventTracker) RecordEscalation(chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	t.escalations = append(t.escalations, now)
	log.Printf("[EVENT_TRACKER] Escalation recorded for chat %s", chatID)
}

// RecordEmptyResponse records when the agent returned an empty/fallback response
func (t *EventTracker) RecordEmptyResponse(chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.emptyResponses = append(t.emptyResponses, time.Now())
	log.Printf("[EVENT_TRACKER] Empty response recorded for chat %s", chatID)
}

// RecordToolFailure records when a tool call failed
func (t *EventTracker) RecordToolFailure(chatID string, toolName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.toolFailures = append(t.toolFailures, time.Now())
	log.Printf("[EVENT_TRACKER] Tool failure recorded: %s in chat %s", toolName, chatID)
}

// RecordTopic records a topic/intent from a conversation
func (t *EventTracker) RecordTopic(topic string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Reset daily
	if time.Now().After(t.topicResetAt) {
		t.topicCounts = make(map[string]int)
		t.topicResetAt = startOfTomorrow()
	}

	t.topicCounts[topic]++
}

// TriggerReason describes why the director should be triggered
type TriggerReason struct {
	Event       EventType
	Description string
	Count       int
	Window      time.Duration
}

// ShouldTriggerDirector checks all thresholds and returns a reason if triggered
func (t *EventTracker) ShouldTriggerDirector() (bool, *TriggerReason) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()

	// Cooldown check
	if !t.lastTrigger.IsZero() && now.Sub(t.lastTrigger) < t.config.CooldownPeriod {
		return false, nil
	}

	// Check escalations in sliding window
	recentEsc := countWithinWindow(t.escalations, now, t.config.EscalationWindow)
	if recentEsc >= t.config.EscalationThreshold {
		t.lastTrigger = now
		return true, &TriggerReason{
			Event:       EventEscalation,
			Description: "escalation spike",
			Count:       recentEsc,
			Window:      t.config.EscalationWindow,
		}
	}

	// Check empty responses (daily)
	recentEmpty := countWithinWindow(t.emptyResponses, now, 24*time.Hour)
	if recentEmpty >= t.config.EmptyRespThreshold {
		t.lastTrigger = now
		return true, &TriggerReason{
			Event:       EventEmptyResponse,
			Description: "too many empty responses",
			Count:       recentEmpty,
			Window:      24 * time.Hour,
		}
	}

	return false, nil
}

// GetDailyStats returns current day stats for the daily report
type DailyStats struct {
	Escalations    int
	EmptyResponses int
	ToolFailures   int
	TopTopics      map[string]int
}

func (t *EventTracker) GetDailyStats() DailyStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	day := 24 * time.Hour

	// Copy topic counts
	topics := make(map[string]int, len(t.topicCounts))
	for k, v := range t.topicCounts {
		topics[k] = v
	}

	return DailyStats{
		Escalations:    countWithinWindow(t.escalations, now, day),
		EmptyResponses: countWithinWindow(t.emptyResponses, now, day),
		ToolFailures:   countWithinWindow(t.toolFailures, now, day),
		TopTopics:      topics,
	}
}

// Cleanup removes old events to prevent memory growth
func (t *EventTracker) Cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoff := time.Now().Add(-48 * time.Hour)
	t.escalations = pruneOlderThan(t.escalations, cutoff)
	t.emptyResponses = pruneOlderThan(t.emptyResponses, cutoff)
	t.toolFailures = pruneOlderThan(t.toolFailures, cutoff)
}

// countWithinWindow counts events within a time window
func countWithinWindow(events []time.Time, now time.Time, window time.Duration) int {
	cutoff := now.Add(-window)
	count := 0
	for _, t := range events {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// pruneOlderThan removes events older than cutoff
func pruneOlderThan(events []time.Time, cutoff time.Time) []time.Time {
	var kept []time.Time
	for _, t := range events {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

func startOfTomorrow() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}
