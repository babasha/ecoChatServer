package director

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// Director — Level 2 agent that analyzes РОП's work and produces directives
// Uses a powerful cloud model (Claude/GPT-4) for strategic analysis
type Director struct {
	tracker   *EventTracker
	provider  llm.Provider       // separate provider — can be Claude while РОП uses Qwen
	optimizer *PromptOptimizer   // prompt versioning and optimization
}

// Config for Director initialization
type Config struct {
	// Provider config for the Director's own LLM (e.g., Claude)
	// If nil, falls back to global provider
	ProviderConfig *llm.ProviderConfig
}

// New creates a Director with its own LLM provider
// Priority: explicit config → DB settings (DIRECTOR_*) → global provider fallback
func New(cfg Config) (*Director, error) {
	var provider llm.Provider

	if cfg.ProviderConfig != nil {
		// Explicit config passed
		var err error
		provider, err = llm.NewProvider(cfg.ProviderConfig)
		if err != nil {
			return nil, fmt.Errorf("create director provider: %w", err)
		}
		log.Printf("[DIRECTOR] Using dedicated provider: %s", provider.GetName())
	} else {
		// Try loading from DB settings: DIRECTOR_PROVIDER, DIRECTOR_API_KEY, DIRECTOR_MODEL
		dbProvider := loadDirectorProviderFromDB()
		if dbProvider != nil {
			provider = dbProvider
			log.Printf("[DIRECTOR] Using DB-configured provider: %s", provider.GetName())
		} else {
			provider = llm.GetGlobalProvider()
			log.Printf("[DIRECTOR] Using global provider (fallback)")
		}
	}

	tracker := NewEventTracker(DefaultTrackerConfig())

	d := &Director{
		tracker:   tracker,
		provider:  provider,
		optimizer: NewPromptOptimizer(provider),
	}

	// Bootstrap identity (seed defaults if first run)
	d.BootstrapIdentity()

	// Start background cleanup goroutine
	go d.periodicCleanup()

	log.Printf("[DIRECTOR] Initialized — Level 2 agent ready")
	return d, nil
}

// Tracker returns the event tracker for external event recording
func (d *Director) Tracker() *EventTracker {
	return d.tracker
}

// Provider returns the Director's LLM provider (for chat API)
func (d *Director) Provider() llm.Provider {
	return d.provider
}

// Optimizer returns the prompt optimizer for external use (API handlers)
func (d *Director) Optimizer() *PromptOptimizer {
	return d.optimizer
}

// CheckAndAnalyze checks if analysis should be triggered, runs it if needed
// Called asynchronously after each message processing
func (d *Director) CheckAndAnalyze(ctx context.Context) {
	shouldTrigger, reason := d.tracker.ShouldTriggerDirector()
	if !shouldTrigger {
		return
	}

	log.Printf("[DIRECTOR] Triggered: %s (count=%d, window=%v)",
		reason.Description, reason.Count, reason.Window)

	if err := d.AnalyzeEvent(ctx, reason); err != nil {
		log.Printf("[DIRECTOR] Event analysis failed: %v", err)
	}
}

// AnalyzeEvent runs analysis triggered by a specific event
func (d *Director) AnalyzeEvent(ctx context.Context, reason *TriggerReason) error {
	return d.analyze(ctx, "event_triggered", reason.Description)
}

// AnalyzeDaily runs the daily analysis (called by cron or manually)
func (d *Director) AnalyzeDaily(ctx context.Context) error {
	return d.analyze(ctx, "daily", "scheduled daily analysis")
}

func (d *Director) analyze(ctx context.Context, reportType, triggerEvent string) error {
	log.Printf("[DIRECTOR] Starting %s analysis: %s", reportType, triggerEvent)

	// 0. Self-reflection: evaluate previous directives before creating new ones
	d.selfReflect(ctx)

	// 1. Collect all summaries since last report
	summaries, err := d.collectSummaries(ctx)
	if err != nil {
		return fmt.Errorf("collect summaries: %w", err)
	}

	if len(summaries) == 0 {
		log.Printf("[DIRECTOR] No new summaries to analyze")
		return nil
	}

	// 2. Get daily stats from tracker
	stats := d.tracker.GetDailyStats()

	// 3. Build analysis prompt
	prompt := d.buildAnalysisPrompt(summaries, stats, triggerEvent)

	// 4. Call LLM (Claude/GPT-4)
	resp, err := d.provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature:  0.3,
		MaxTokens:    2000,
		SystemPrompt: getDirectorSystemPrompt(),
	})
	if err != nil {
		return fmt.Errorf("LLM analysis: %w", err)
	}

	// 5. Parse full structured response
	parsed := parseDirectorResponse(resp.Text)

	// 6. Save report to DB
	report := &Report{
		ID:                 uuid.New(),
		ReportDate:         time.Now(),
		ReportType:         reportType,
		TriggerEvent:       triggerEvent,
		SummaryCount:       len(summaries),
		Analysis:           parsed.Analysis,
		Directives:         parsed.Directives,
		Stats:              &stats,
		Applied:            false,
		CreatedAt:          time.Now(),
		CustomerComplaints: parsed.CustomerComplaints,
		KeyObservations:    parsed.KeyObservations,
		Expectations:       parsed.Expectations,
	}

	if err := d.saveReport(report); err != nil {
		return fmt.Errorf("save report: %w", err)
	}

	log.Printf("[DIRECTOR] Report saved: %d summaries, %d directives, %d complaints, %d observations",
		len(summaries), len(parsed.Directives), len(parsed.CustomerComplaints), len(parsed.KeyObservations))

	// Auto-save key findings to persistent memory
	d.saveAnalysisToMemory(parsed, reportType, triggerEvent)

	// Save directive baselines for future self-reflection
	d.saveDirectiveBaselines(report)

	// Prompt optimization: only for daily reports (not event-triggered)
	if reportType == "daily" && d.optimizer != nil {
		agents := []string{"zefir_support", "plant_expert", "device_specialist", "support_specialist", "orchestrator"}
		for _, agentName := range agents {
			// First: evaluate current vs parent, rollback if worse
			if rollbackResult, err := d.optimizer.EvaluateAndRollback(ctx, agentName); err != nil {
				log.Printf("[DIRECTOR] Rollback check failed for %s: %v", agentName, err)
			} else if rollbackResult != "" {
				report.PromptChanges = append(report.PromptChanges, PromptChangeRecord{
					Agent:       agentName,
					Description: "Rolled back to previous version",
					Rationale:   rollbackResult,
				})
			}

			// Then: try to improve (only main agent, not all — to limit API calls)
			if agentName == "zefir_support" {
				if changeNotes, err := d.optimizer.OptimizePrompt(ctx, agentName, parsed.Analysis, summaries); err != nil {
					log.Printf("[DIRECTOR] Prompt optimization failed for %s: %v", agentName, err)
				} else if changeNotes != "" {
					report.PromptChanges = append(report.PromptChanges, PromptChangeRecord{
						Agent:       agentName,
						Description: changeNotes,
						Rationale:   parsed.Expectations,
					})
				}
			}
		}

		// Update report with prompt changes if any were made
		if len(report.PromptChanges) > 0 {
			if err := d.updateReportPromptChanges(report); err != nil {
				log.Printf("[DIRECTOR] Failed to update report prompt changes: %v", err)
			}
		}
	}

	return nil
}

// GetActiveDirectives returns current directives that the РОП should follow
func (d *Director) GetActiveDirectives(ctx context.Context) ([]Directive, error) {
	report, err := database.GetLatestDirectorReport()
	if err != nil {
		return nil, err
	}
	if report == nil {
		return nil, nil
	}

	// Filter out expired directives
	var active []Directive
	now := time.Now()
	for _, dir := range report.Directives {
		if dir.ExpiresAt != nil && now.After(*dir.ExpiresAt) {
			continue
		}
		active = append(active, dir)
	}

	return active, nil
}

// BuildDirectivesContext formats active directives for injection into agent context
func (d *Director) BuildDirectivesContext(ctx context.Context) string {
	directives, err := d.GetActiveDirectives(ctx)
	if err != nil {
		log.Printf("[DIRECTOR] Failed to get directives: %v", err)
		return ""
	}

	if len(directives) == 0 {
		return ""
	}

	var parts []string
	for _, dir := range directives {
		prefix := ""
		if dir.Priority == "high" {
			prefix = "IMPORTANT: "
		}
		parts = append(parts, fmt.Sprintf("- %s%s", prefix, dir.Instruction))
	}

	return "[DIRECTOR INSTRUCTIONS]\n" + strings.Join(parts, "\n") + "\n[END INSTRUCTIONS]"
}

// collectSummaries gets chat summaries since the last director report
func (d *Director) collectSummaries(ctx context.Context) ([]string, error) {
	lastReport, err := database.GetLatestDirectorReport()
	if err != nil {
		return nil, err
	}

	var since time.Time
	if lastReport != nil {
		since = lastReport.CreatedAt
	} else {
		since = time.Now().Add(-24 * time.Hour) // first run: last 24h
	}

	summaries, err := database.GetChatSummariesSince(since)
	if err != nil {
		return nil, err
	}

	var texts []string
	for _, s := range summaries {
		texts = append(texts, fmt.Sprintf("[Chat %s, %s]: %s",
			s.ChatID.String()[:8], s.CreatedAt.Format("15:04"), s.Summary))
	}

	return texts, nil
}

func (d *Director) buildAnalysisPrompt(summaries []string, stats DailyStats, trigger string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Trigger: %s\n\n", trigger))

	sb.WriteString(fmt.Sprintf("Event tracker stats:\n- Escalations: %d\n- Empty responses: %d\n- Tool failures: %d\n",
		stats.Escalations, stats.EmptyResponses, stats.ToolFailures))

	if len(stats.TopTopics) > 0 {
		sb.WriteString("- Top topics: ")
		topicParts := make([]string, 0, len(stats.TopTopics))
		for topic, count := range stats.TopTopics {
			topicParts = append(topicParts, fmt.Sprintf("%s(%d)", topic, count))
		}
		sb.WriteString(strings.Join(topicParts, ", "))
		sb.WriteString("\n")
	}

	// Add granular per-agent and per-tool metrics from DB
	since := time.Now().Add(-24 * time.Hour)

	agentStats, err := database.GetAgentStatsSince(since)
	if err == nil && len(agentStats) > 0 {
		sb.WriteString("\nPer-agent metrics (last 24h):\n")
		for _, a := range agentStats {
			sb.WriteString(fmt.Sprintf("  %s: %d calls, escalation %.0f%%, empty %.0f%%, avg response %dms\n",
				a.AgentName, a.TotalCalls, a.EscalationRate*100, a.EmptyRate*100, int(a.AvgResponseMs)))

			// Per-tool breakdown for this agent
			toolStats, tErr := database.GetToolStatsByAgentSince(a.AgentName, since)
			if tErr == nil && len(toolStats) > 0 {
				for _, t := range toolStats {
					sb.WriteString(fmt.Sprintf("    tool %s: %d calls, success %.0f%%",
						t.ToolName, t.TotalCalls, t.SuccessRate*100))
					if t.ErrorCount > 0 {
						sb.WriteString(fmt.Sprintf(" ⚠ %d errors", t.ErrorCount))
					}
					if t.EmptyCount > 0 {
						sb.WriteString(fmt.Sprintf(" (%d empty)", t.EmptyCount))
					}
					sb.WriteString("\n")
				}
			}
		}
	}

	sb.WriteString(fmt.Sprintf("\nChat summaries (%d conversations):\n", len(summaries)))
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (d *Director) saveReport(report *Report) error {
	directivesJSON, err := json.Marshal(report.Directives)
	if err != nil {
		return fmt.Errorf("marshal directives: %w", err)
	}

	statsJSON, err := json.Marshal(report.Stats)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}

	complaintsJSON, err := json.Marshal(report.CustomerComplaints)
	if err != nil {
		return fmt.Errorf("marshal complaints: %w", err)
	}

	observationsJSON, err := json.Marshal(report.KeyObservations)
	if err != nil {
		return fmt.Errorf("marshal observations: %w", err)
	}

	promptChangesJSON, err := json.Marshal(report.PromptChanges)
	if err != nil {
		return fmt.Errorf("marshal prompt changes: %w", err)
	}

	return database.InsertDirectorReport(
		report.ID, report.ReportDate, report.ReportType, report.TriggerEvent,
		report.SummaryCount, report.Analysis, directivesJSON, statsJSON,
		complaintsJSON, observationsJSON, promptChangesJSON, report.Expectations,
	)
}

// updateReportPromptChanges updates only the prompt_changes column for an existing report
func (d *Director) updateReportPromptChanges(report *Report) error {
	promptChangesJSON, err := json.Marshal(report.PromptChanges)
	if err != nil {
		return fmt.Errorf("marshal prompt changes: %w", err)
	}

	return database.UpdateDirectorReportPromptChanges(report.ID, promptChangesJSON)
}

// selfReflect evaluates the effectiveness of previous directives.
// Compares metrics before/after each directive and saves learnings to memory.
func (d *Director) selfReflect(ctx context.Context) {
	// Get pending directive outcomes (created >24h ago, not yet evaluated)
	outcomes, err := database.GetPendingDirectiveOutcomes()
	if err != nil {
		log.Printf("[DIRECTOR] Self-reflection: failed to get pending outcomes: %v", err)
		return
	}

	if len(outcomes) == 0 {
		return
	}

	log.Printf("[DIRECTOR] Self-reflection: evaluating %d pending directives", len(outcomes))

	// Get current aggregate metrics
	since := time.Now().Add(-24 * time.Hour)
	currentStats, err := database.GetAgentStatsSince(since)
	if err != nil {
		log.Printf("[DIRECTOR] Self-reflection: failed to get current stats: %v", err)
		return
	}

	// Aggregate current rates
	var totalCalls, totalEscalations, totalEmpty int
	var totalResponseMs float64
	for _, s := range currentStats {
		totalCalls += s.TotalCalls
		totalEscalations += s.Escalations
		totalEmpty += s.EmptyResponses
		totalResponseMs += s.AvgResponseMs * float64(s.TotalCalls)
	}

	var currentEscRate, currentEmptyRate, currentAvgMs float64
	if totalCalls > 0 {
		currentEscRate = float64(totalEscalations) / float64(totalCalls)
		currentEmptyRate = float64(totalEmpty) / float64(totalCalls)
		currentAvgMs = totalResponseMs / float64(totalCalls)
	}

	for _, o := range outcomes {
		effectiveness := "neutral"
		var notes string

		// Compare before vs after
		escDelta := currentEscRate - o.EscalationRateBefore
		emptyDelta := currentEmptyRate - o.EmptyRateBefore

		if escDelta < -0.05 || emptyDelta < -0.05 {
			effectiveness = "positive"
			notes = fmt.Sprintf("Улучшение: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, currentEscRate*100,
				o.EmptyRateBefore*100, currentEmptyRate*100)
		} else if escDelta > 0.1 || emptyDelta > 0.1 {
			effectiveness = "negative"
			notes = fmt.Sprintf("Ухудшение: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, currentEscRate*100,
				o.EmptyRateBefore*100, currentEmptyRate*100)
		} else {
			notes = fmt.Sprintf("Без изменений: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, currentEscRate*100,
				o.EmptyRateBefore*100, currentEmptyRate*100)
		}

		// Update outcome in DB
		if err := database.EvaluateDirectiveOutcome(o.ID, effectiveness, notes,
			currentEscRate, currentEmptyRate, currentAvgMs); err != nil {
			log.Printf("[DIRECTOR] Self-reflection: failed to evaluate outcome: %v", err)
			continue
		}

		// Save learning to memory
		content := fmt.Sprintf("Директива '%s' — %s. %s",
			truncateStr(o.DirectiveInstruction, 100), effectiveness, notes)

		database.SaveAutoMemory("insight",
			fmt.Sprintf("directive_result:%s:%s", o.CreatedAt.Format("20060102"), o.DirectiveType),
			content,
			[]string{"self_reflection", "directive", effectiveness},
			nil)

		log.Printf("[DIRECTOR] Self-reflection: directive [%s] → %s", o.DirectiveType, effectiveness)
	}

	// After evaluating directives, check if gaps warrant auto-skill creation
	d.suggestSkillsFromReflection(ctx)
}

// saveDirectiveBaselines records current metrics as baselines for new directives.
func (d *Director) saveDirectiveBaselines(report *Report) {
	if len(report.Directives) == 0 {
		return
	}

	// Get current aggregate metrics as baseline
	since := time.Now().Add(-24 * time.Hour)
	stats, err := database.GetAgentStatsSince(since)
	if err != nil {
		log.Printf("[DIRECTOR] Failed to get baseline metrics: %v", err)
		return
	}

	var totalCalls, totalEscalations, totalEmpty int
	var totalResponseMs float64
	for _, s := range stats {
		totalCalls += s.TotalCalls
		totalEscalations += s.Escalations
		totalEmpty += s.EmptyResponses
		totalResponseMs += s.AvgResponseMs * float64(s.TotalCalls)
	}

	var escRate, emptyRate, avgMs float64
	if totalCalls > 0 {
		escRate = float64(totalEscalations) / float64(totalCalls)
		emptyRate = float64(totalEmpty) / float64(totalCalls)
		avgMs = totalResponseMs / float64(totalCalls)
	}

	for _, dir := range report.Directives {
		if err := database.InsertDirectiveOutcome(
			report.ID, dir.Type, dir.Instruction,
			escRate, emptyRate, avgMs,
		); err != nil {
			log.Printf("[DIRECTOR] Failed to save directive baseline: %v", err)
		}
	}

	log.Printf("[DIRECTOR] Saved baselines for %d directives", len(report.Directives))
}

func (d *Director) periodicCleanup() {
	trackerTicker := time.NewTicker(1 * time.Hour)
	decayTicker := time.NewTicker(24 * time.Hour)
	digestTicker := time.NewTicker(6 * time.Hour)          // check for digest generation
	introspectTicker := time.NewTicker(7 * 24 * time.Hour) // weekly introspection
	skillBuilderTicker := time.NewTicker(24 * time.Hour)   // daily skill auto-creation check
	defer trackerTicker.Stop()
	defer decayTicker.Stop()
	defer digestTicker.Stop()
	defer introspectTicker.Stop()
	defer skillBuilderTicker.Stop()

	for {
		select {
		case <-trackerTicker.C:
			d.tracker.Cleanup()
		case <-decayTicker.C:
			decayed, purged, err := database.DecayMemories()
			if err != nil {
				log.Printf("[DIRECTOR] Memory decay error: %v", err)
			} else if decayed > 0 || purged > 0 {
				log.Printf("[DIRECTOR] Memory maintenance: decayed=%d, purged=%d", decayed, purged)
			}
		case <-digestTicker.C:
			d.generateDigestsIfNeeded()
		case <-introspectTicker.C:
			d.PeriodicIntrospect()
		case <-skillBuilderTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			d.analyzeToolPatterns(ctx)
			cancel()
		}
	}
}

// generateDigestsIfNeeded creates weekly/monthly digests when a period has ended.
func (d *Director) generateDigestsIfNeeded() {
	now := time.Now()

	// Weekly digest: generate for last week if we're past Monday 02:00
	if now.Weekday() == time.Monday || now.Weekday() == time.Tuesday {
		lastWeekStart := now.AddDate(0, 0, -7)
		weekStart := time.Date(lastWeekStart.Year(), lastWeekStart.Month(), lastWeekStart.Day(), 0, 0, 0, 0, now.Location())
		// Align to Monday
		for weekStart.Weekday() != time.Monday {
			weekStart = weekStart.AddDate(0, 0, -1)
		}
		weekEnd := weekStart.AddDate(0, 0, 7)

		d.generateDigest("weekly", weekStart, weekEnd)
	}

	// Monthly digest: generate for last month on the 1st-2nd day
	if now.Day() <= 2 {
		monthStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
		monthEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

		d.generateDigest("monthly", monthStart, monthEnd)
	}
}

func (d *Director) generateDigest(periodType string, from, to time.Time) {
	// Check if digest already exists
	existing, err := database.GetDigestForPeriod(periodType, from)
	if err != nil {
		log.Printf("[DIRECTOR] Error checking digest: %v", err)
		return
	}
	if existing != nil {
		return // already generated
	}

	log.Printf("[DIRECTOR] Generating %s digest: %s → %s", periodType, from.Format("2006-01-02"), to.Format("2006-01-02"))

	// Get timeline data
	data, err := database.GetTimelineData(from, to, "full")
	if err != nil {
		log.Printf("[DIRECTOR] Error getting timeline data for digest: %v", err)
		return
	}

	// Count chats
	chatCount, _ := database.CountChatsInPeriod(from, to)

	// Build summary prompt for LLM
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Generate a brief %s digest (%s to %s) for the support system.\n\n",
		periodType, from.Format("2006-01-02"), to.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Stats: %d interactions, %d chats, escalation %.1f%%, empty %.1f%%, avg response %dms\n",
		data.TotalInteractions, chatCount, data.AvgEscalationRate*100, data.AvgEmptyRate*100, int(data.AvgResponseMs)))
	sb.WriteString(fmt.Sprintf("Reports generated: %d\n", data.ReportCount))

	if len(data.DirectiveStats) > 0 {
		sb.WriteString("Directive outcomes: ")
		for eff, count := range data.DirectiveStats {
			sb.WriteString(fmt.Sprintf("%s=%d ", eff, count))
		}
		sb.WriteString("\n")
	}

	for _, s := range data.ReportSummaries {
		sb.WriteString(s + "\n")
	}

	sb.WriteString("\nRespond in this exact format:\nSUMMARY: <2-3 sentences overview>\nKEY_EVENTS:\n- <event 1>\n- <event 2>\nTRENDS:\n- <trend 1>\nLESSONS:\n- <lesson 1>")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   500,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Error generating digest summary: %v", err)
		// Save with raw stats even without LLM summary
		resp = &llm.Response{
			Text: fmt.Sprintf("Автоматический дайджест: %d взаимодействий, эскалация %.1f%%, пустые %.1f%%",
				data.TotalInteractions, data.AvgEscalationRate*100, data.AvgEmptyRate*100),
		}
	}

	// Parse LLM response
	summary, keyEvents, trends, lessons := parseDigestResponse(resp.Text)

	digest := &models.DirectorDigest{
		ID:                uuid.New(),
		PeriodType:        periodType,
		PeriodStart:       from,
		PeriodEnd:         to,
		TotalChats:        chatCount,
		TotalInteractions: data.TotalInteractions,
		AvgEscalationRate: data.AvgEscalationRate,
		AvgEmptyRate:      data.AvgEmptyRate,
		AvgResponseMs:     data.AvgResponseMs,
		Summary:           summary,
		KeyEvents:         keyEvents,
		Trends:            trends,
		Lessons:           lessons,
	}

	if err := database.InsertDigest(digest); err != nil {
		log.Printf("[DIRECTOR] Error saving digest: %v", err)
		return
	}

	log.Printf("[DIRECTOR] %s digest saved: %d interactions, %d chats", periodType, data.TotalInteractions, chatCount)
}

// parseDigestResponse extracts structured sections from the LLM digest response.
func parseDigestResponse(text string) (summary string, keyEvents, trends, lessons []string) {
	sections := map[string]*[]string{
		"KEY_EVENTS:": &keyEvents,
		"TRENDS:":     &trends,
		"LESSONS:":    &lessons,
	}

	// Extract SUMMARY
	if idx := strings.Index(text, "SUMMARY:"); idx >= 0 {
		rest := text[idx+8:]
		endIdx := len(rest)
		for section := range sections {
			if si := strings.Index(rest, section); si >= 0 && si < endIdx {
				endIdx = si
			}
		}
		summary = strings.TrimSpace(rest[:endIdx])
	} else {
		summary = text
		if len(summary) > 500 {
			summary = summary[:500]
		}
		return
	}

	// Extract bullet lists
	for section, target := range sections {
		idx := strings.Index(text, section)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(section):]
		endIdx := len(rest)
		for other := range sections {
			if other == section {
				continue
			}
			if si := strings.Index(rest, other); si >= 0 && si < endIdx {
				endIdx = si
			}
		}
		for _, line := range strings.Split(rest[:endIdx], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
				*target = append(*target, strings.TrimSpace(line[2:]))
			}
		}
	}
	return
}

// saveAnalysisToMemory persists key findings from analysis to long-term memory.
// Runs programmatically — no extra LLM calls.
func (d *Director) saveAnalysisToMemory(parsed *ParsedDirectorResponse, reportType, trigger string) {
	dateKey := time.Now().Format("2006-01-02")

	// Save directives as decisions
	for i, dir := range parsed.Directives {
		if i >= 3 {
			break // max 3 decisions per analysis
		}
		key := fmt.Sprintf("directive:%s:%s:%d", dateKey, dir.Type, i)
		content := dir.Instruction
		if len(content) > 300 {
			content = content[:300]
		}
		tags := []string{"directive", dir.Type, dir.Priority}

		// Directives expire in 7 days
		expires := time.Now().Add(7 * 24 * time.Hour)

		if err := database.SaveAutoMemory("decision", key, content, tags, &expires); err != nil {
			log.Printf("[DIRECTOR] Failed to save directive memory: %v", err)
		}
	}

	// Save key observations as patterns (top 3)
	for i, obs := range parsed.KeyObservations {
		if i >= 3 {
			break
		}
		key := fmt.Sprintf("observation:%s:%d", dateKey, i)
		content := obs
		if len(content) > 300 {
			content = content[:300]
		}

		if err := database.SaveAutoMemory("pattern", key, content, []string{"observation", reportType}, nil); err != nil {
			log.Printf("[DIRECTOR] Failed to save observation memory: %v", err)
		}
	}

	// Save analysis summary as insight
	if parsed.Analysis != "" {
		summary := parsed.Analysis
		if len(summary) > 400 {
			summary = summary[:400]
		}
		key := fmt.Sprintf("analysis_summary:%s", dateKey)
		if err := database.SaveAutoMemory("insight", key, summary, []string{"analysis", reportType, trigger}, nil); err != nil {
			log.Printf("[DIRECTOR] Failed to save analysis memory: %v", err)
		}
	}

	log.Printf("[DIRECTOR] Auto-saved findings to memory")
}

// ============================================================================
// Self-building: auto-create skills from repeating tool patterns
// ============================================================================

// analyzeToolPatterns detects repeating tool usage and proposes new skills.
func (d *Director) analyzeToolPatterns(ctx context.Context) {
	since := time.Now().Add(-7 * 24 * time.Hour) // last 7 days
	patterns, err := database.GetRepeatingToolPatterns(since, 5) // tools called 5+ times
	if err != nil {
		log.Printf("[DIRECTOR] Pattern analysis error: %v", err)
		return
	}

	if len(patterns) == 0 {
		return
	}

	// Check auto-skill limit (max 10)
	autoCount, err := database.CountAutoCreatedSkills()
	if err != nil || autoCount >= 10 {
		return
	}

	// Check cooldown: max 1 auto-skill per 24h
	cooldownKey := fmt.Sprintf("auto_skill_created:%s", time.Now().Format("2006-01-02"))
	existing, _ := database.RecallMemories(cooldownKey, "", 1)
	if len(existing) > 0 {
		return // already created one today
	}

	// Filter out tools that already have skills
	existingSkills, _ := database.GetAllSkills()
	skillNames := make(map[string]bool)
	for _, s := range existingSkills {
		skillNames[s.Name] = true
	}

	// Filter patterns: skip built-in tools and already-skilled tools
	var newPatterns []models.ToolPattern
	builtinTools := map[string]bool{
		"get_agent_metrics": true, "get_latest_report": true, "get_active_prompts": true,
		"get_prompt_history": true, "get_tool_stats": true, "run_analysis": true,
		"get_interaction_details": true, "get_recent_chats": true,
		"remember": true, "recall": true, "forget": true, "list_memories": true,
		"search_reports": true, "deep_search": true, "timeline": true,
		"create_skill": true, "edit_skill": true, "list_skills": true,
		"delete_skill": true, "test_skill": true,
		"get_identity": true, "update_identity": true, "introspect": true,
		"identity_history": true, "rollback_identity": true,
	}
	for _, p := range patterns {
		if !builtinTools[p.ToolName] && !skillNames[p.ToolName] {
			newPatterns = append(newPatterns, p)
		}
	}

	if len(newPatterns) == 0 {
		return
	}

	// Build prompt for LLM to propose a skill
	var sb strings.Builder
	sb.WriteString("Analyzing repeating tool usage patterns to suggest a reusable custom skill.\n\n")
	sb.WriteString("Frequently called tools (not yet formalized as skills):\n")
	for _, p := range newPatterns {
		sb.WriteString(fmt.Sprintf("- %s: called %d times, sample args: %s\n",
			p.ToolName, p.CallCount, p.SampleArgs))
	}
	sb.WriteString(`
Based on these patterns, propose ONE custom skill that would be most useful.

Respond in EXACTLY this format (no extra text):
SKILL_NAME: <lowercase_underscore_name>
SKILL_TYPE: sql_query|prompt_chain
DESCRIPTION: <what the skill does, 1 sentence>
CODE: <implementation - SQL query or prompt template>
PARAMETERS: <JSON Schema for parameters>
REASON: <why this skill would help>

If no useful skill can be created from these patterns, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Pattern→skill LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	// Parse LLM response
	skill := parseSkillProposal(resp.Text)
	if skill == nil {
		log.Printf("[DIRECTOR] Could not parse skill proposal from LLM response")
		return
	}

	skill.CreatedBy = "auto_pattern"
	skill.Tags = append(skill.Tags, "auto_created", "pattern_based")

	if err := database.CreateSkill(skill); err != nil {
		log.Printf("[DIRECTOR] Auto-skill creation failed: %v", err)
		return
	}

	// Save cooldown and memory
	database.SaveAutoMemory("decision", cooldownKey,
		fmt.Sprintf("Auto-created skill '%s' (%s) from repeating patterns", skill.Name, skill.SkillType),
		[]string{"auto_skill", "pattern"}, nil)

	log.Printf("[DIRECTOR] Auto-created skill '%s' (type=%s) from tool patterns", skill.Name, skill.SkillType)
}

// suggestSkillsFromReflection creates skills based on negative directive outcomes.
func (d *Director) suggestSkillsFromReflection(ctx context.Context) {
	since := time.Now().Add(-30 * 24 * time.Hour) // last 30 days

	// Check if there are enough negative outcomes to warrant a skill
	negByType, err := database.GetNegativeOutcomesByType(since)
	if err != nil || len(negByType) == 0 {
		return
	}

	// Need at least 3 negative outcomes in some category
	hasSignificantGap := false
	for _, count := range negByType {
		if count >= 3 {
			hasSignificantGap = true
			break
		}
	}
	if !hasSignificantGap {
		return
	}

	// Check cooldown and limits
	autoCount, err := database.CountAutoCreatedSkills()
	if err != nil || autoCount >= 10 {
		return
	}
	cooldownKey := fmt.Sprintf("auto_skill_reflection:%s", time.Now().Format("2006-01-02"))
	existing, _ := database.RecallMemories(cooldownKey, "", 1)
	if len(existing) > 0 {
		return
	}

	// Get the actual negative outcomes for context
	negOutcomes, err := database.GetNegativeOutcomesSince(since)
	if err != nil || len(negOutcomes) == 0 {
		return
	}

	// Also check memory for gap patterns
	gapMemories, _ := database.RecallMemories("tool_failure", "pattern", 5)

	// Build LLM prompt
	var sb strings.Builder
	sb.WriteString("Analyzing negative directive outcomes and system gaps to propose a useful custom skill.\n\n")
	sb.WriteString("Negative outcomes by type:\n")
	for dtype, count := range negByType {
		sb.WriteString(fmt.Sprintf("- %s: %d negative outcomes\n", dtype, count))
	}
	sb.WriteString("\nRecent negative outcomes:\n")
	for i, o := range negOutcomes {
		if i >= 5 {
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s — %s\n",
			o.DirectiveType, truncateStr(o.DirectiveInstruction, 100), o.EvaluationNotes))
	}
	if len(gapMemories) > 0 {
		sb.WriteString("\nKnown gaps from memory:\n")
		for _, m := range gapMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", truncateStr(m.Content, 120)))
		}
	}

	sb.WriteString(`
Based on these gaps and failures, propose ONE custom skill that would help the Director be more effective.

Respond in EXACTLY this format:
SKILL_NAME: <lowercase_underscore_name>
SKILL_TYPE: sql_query|prompt_chain
DESCRIPTION: <what the skill does>
CODE: <implementation>
PARAMETERS: <JSON Schema>
REASON: <why this fills the gap>

If no skill would help, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Reflection→skill LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	skill := parseSkillProposal(resp.Text)
	if skill == nil {
		log.Printf("[DIRECTOR] Could not parse reflection skill proposal")
		return
	}

	skill.CreatedBy = "auto_reflection"
	skill.Tags = append(skill.Tags, "auto_created", "reflection_based")

	if err := database.CreateSkill(skill); err != nil {
		log.Printf("[DIRECTOR] Auto-skill from reflection failed: %v", err)
		return
	}

	// Save cooldown and memory
	database.SaveAutoMemory("decision", cooldownKey,
		fmt.Sprintf("Auto-created skill '%s' (%s) from self-reflection on negative outcomes",
			skill.Name, skill.SkillType),
		[]string{"auto_skill", "reflection"}, nil)

	log.Printf("[DIRECTOR] Auto-created skill '%s' from self-reflection", skill.Name)
}

// parseSkillProposal extracts a skill definition from LLM response text.
func parseSkillProposal(text string) *models.DirectorSkill {
	extract := func(label string) string {
		idx := strings.Index(text, label+":")
		if idx < 0 {
			return ""
		}
		rest := text[idx+len(label)+1:]
		endIdx := strings.Index(rest, "\n")
		if endIdx < 0 {
			endIdx = len(rest)
		}
		return strings.TrimSpace(rest[:endIdx])
	}

	name := extract("SKILL_NAME")
	skillType := extract("SKILL_TYPE")
	description := extract("DESCRIPTION")
	reason := extract("REASON")

	// CODE might be multiline — extract until next label
	codeStart := strings.Index(text, "CODE:")
	paramsStart := strings.Index(text, "PARAMETERS:")
	var code string
	if codeStart >= 0 && paramsStart >= 0 && paramsStart > codeStart {
		code = strings.TrimSpace(text[codeStart+5 : paramsStart])
	} else if codeStart >= 0 {
		code = extract("CODE")
	}

	params := extract("PARAMETERS")

	if name == "" || skillType == "" || code == "" {
		return nil
	}

	// Validate skill type
	if skillType != "sql_query" && skillType != "prompt_chain" {
		return nil // auto-skills limited to safe types
	}

	// Validate name format
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return nil
		}
	}

	// Default params if empty
	if params == "" || params == "{}" {
		params = `{"type":"object","properties":{}}`
	}

	// Add reason to description
	if reason != "" && len(description) < 200 {
		description = description + " (auto: " + reason + ")"
	}

	return &models.DirectorSkill{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Parameters:  params,
		SkillType:   skillType,
		Code:        code,
		Enabled:     true,
		Tags:        []string{},
	}
}

// loadDirectorProviderFromDB tries to create a dedicated LLM provider from DB settings.
// Settings: DIRECTOR_PROVIDER (claude/openai/gemini), DIRECTOR_API_KEY, DIRECTOR_MODEL
func loadDirectorProviderFromDB() llm.Provider {
	providerType := database.GetSetting("DIRECTOR_PROVIDER", "")
	if providerType == "" {
		return nil
	}

	apiKey := database.GetSetting("DIRECTOR_API_KEY", "")
	if apiKey == "" {
		log.Printf("[DIRECTOR] DIRECTOR_PROVIDER=%s but no DIRECTOR_API_KEY set", providerType)
		return nil
	}

	model := database.GetSetting("DIRECTOR_MODEL", "")

	provider, err := llm.NewProvider(&llm.ProviderConfig{
		Type:   llm.ProviderType(providerType),
		APIKey: apiKey,
		Model:  model,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Failed to create DB-configured provider: %v", err)
		return nil
	}

	return provider
}

func getDirectorSystemPrompt() string {
	// Load identity for analysis context
	identityPrompt := BuildIdentitySystemPrompt()

	var sb strings.Builder
	if identityPrompt != "" {
		sb.WriteString(identityPrompt)
		sb.WriteString("\n")
	}

	sb.WriteString(`You analyze conversation summaries and support metrics to improve the AI assistant's performance.

Your output MUST follow this exact format (all sections required):

ANALYSIS:
<3-7 sentences analyzing the overall situation, patterns, quality of responses, and areas for improvement>

CUSTOMER_COMPLAINTS:
- <specific complaint or pain point extracted from conversations>
- <another complaint...>
(list every distinct complaint/frustration you found in the summaries, even minor ones)

KEY_OBSERVATIONS:
- <pattern, trend, or notable observation across conversations>
- <another observation...>
(what's working well, what's failing, recurring topics, behavioral patterns)

DIRECTIVES:
- [type:prompt_update] [priority:high/medium/low] <instruction for the support agent>
- [type:faq_gap] [priority:high/medium/low] <missing FAQ topic to address>
- [type:pattern] [priority:high/medium/low] <pattern observation and recommendation>
- [type:alert] [priority:high/medium/low] <urgent issue requiring attention>

EXPECTATIONS:
<2-4 sentences: what specific improvements you expect from these directives. What metrics should improve? What customer experience changes should occur? How will you measure success?>

Rules:
- Be specific and actionable — no vague advice
- Quote actual customer phrases when describing complaints
- Identify root causes, not just symptoms
- Focus on improving customer experience
- Flag recurring issues that need FAQ updates
- Max 5 directives per report
- Write in the same language as the summaries`)

	return sb.String()
}

// ParsedDirectorResponse holds all sections from the Director's LLM response
type ParsedDirectorResponse struct {
	Analysis           string
	Directives         []Directive
	CustomerComplaints []string
	KeyObservations    []string
	Expectations       string
}

// parseDirectorResponse splits LLM response into structured sections
func parseDirectorResponse(text string) *ParsedDirectorResponse {
	result := &ParsedDirectorResponse{}

	// Section markers in expected order
	sections := []string{"ANALYSIS:", "CUSTOMER_COMPLAINTS:", "KEY_OBSERVATIONS:", "DIRECTIVES:", "EXPECTATIONS:"}

	// Find positions of all sections
	positions := map[string]int{}
	for _, s := range sections {
		positions[s] = strings.Index(text, s)
	}

	// Helper: extract text between two sections
	extractSection := func(sectionName string) string {
		start := positions[sectionName]
		if start < 0 {
			return ""
		}
		start += len(sectionName)

		// Find the next section that comes after this one
		end := len(text)
		for _, other := range sections {
			if other == sectionName {
				continue
			}
			otherPos := positions[other]
			if otherPos > positions[sectionName] && otherPos < end {
				end = otherPos
			}
		}
		return strings.TrimSpace(text[start:end])
	}

	// Helper: parse bulleted list from section text
	parseBulletList := func(sectionText string) []string {
		var items []string
		for _, line := range strings.Split(sectionText, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				items = append(items, strings.TrimSpace(line[2:]))
			} else if strings.HasPrefix(line, "* ") {
				items = append(items, strings.TrimSpace(line[2:]))
			}
		}
		return items
	}

	// 1. Analysis
	result.Analysis = extractSection("ANALYSIS:")
	if result.Analysis == "" {
		result.Analysis = text // fallback: entire text
	}

	// 2. Customer complaints
	result.CustomerComplaints = parseBulletList(extractSection("CUSTOMER_COMPLAINTS:"))

	// 3. Key observations
	result.KeyObservations = parseBulletList(extractSection("KEY_OBSERVATIONS:"))

	// 4. Directives
	directivesText := extractSection("DIRECTIVES:")
	for _, line := range strings.Split(directivesText, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")

		dir := parseDirectiveLine(line)
		if dir.Instruction != "" {
			result.Directives = append(result.Directives, dir)
		}
	}

	// 5. Expectations
	result.Expectations = extractSection("EXPECTATIONS:")

	return result
}

func parseDirectiveLine(line string) Directive {
	dir := Directive{
		Type:     "prompt_update",
		Priority: "medium",
	}

	// Extract [type:...] tag
	if idx := strings.Index(line, "[type:"); idx >= 0 {
		end := strings.Index(line[idx:], "]")
		if end > 0 {
			dir.Type = line[idx+6 : idx+end]
			line = line[:idx] + line[idx+end+1:]
		}
	}

	// Extract [priority:...] tag
	if idx := strings.Index(line, "[priority:"); idx >= 0 {
		end := strings.Index(line[idx:], "]")
		if end > 0 {
			dir.Priority = line[idx+10 : idx+end]
			line = line[:idx] + line[idx+end+1:]
		}
	}

	dir.Instruction = strings.TrimSpace(line)
	dir.Description = dir.Instruction

	return dir
}
