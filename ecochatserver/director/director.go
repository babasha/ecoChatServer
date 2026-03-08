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

func (d *Director) periodicCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		d.tracker.Cleanup()
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
	return `You are a Director of Customer Support for Zefir IoT (plant moisture sensors).
You analyze conversation summaries and support metrics to improve the AI assistant's performance.

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
- Write in the same language as the summaries`
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
