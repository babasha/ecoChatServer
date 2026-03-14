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

// analyze runs the full analysis pipeline: self-reflect → collect → LLM → save → optimize.
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
		for _, agentName := range AgentNames {
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

// collectSummaries gathers chat summaries since the last director report.
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

// buildAnalysisPrompt constructs the LLM prompt with event stats and chat summaries.
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

// saveReport persists the analysis report to DB.
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

// updateReportPromptChanges updates only the prompt_changes column for an existing report.
func (d *Director) updateReportPromptChanges(report *Report) error {
	promptChangesJSON, err := json.Marshal(report.PromptChanges)
	if err != nil {
		return fmt.Errorf("marshal prompt changes: %w", err)
	}
	return database.UpdateDirectorReportPromptChanges(report.ID, promptChangesJSON)
}

// saveDirectiveBaselines records current metrics as baselines for new directives.
func (d *Director) saveDirectiveBaselines(report *Report) {
	if len(report.Directives) == 0 {
		return
	}

	since := time.Now().Add(-24 * time.Hour)
	stats, err := database.GetAgentStatsSince(since)
	if err != nil {
		log.Printf("[DIRECTOR] Failed to get baseline metrics: %v", err)
		return
	}

	agg := AggregateAgentStats(stats)

	for _, dir := range report.Directives {
		if err := database.InsertDirectiveOutcome(
			report.ID, dir.Type, dir.Instruction,
			agg.EscRate, agg.EmptyRate, agg.AvgMs,
		); err != nil {
			log.Printf("[DIRECTOR] Failed to save directive baseline: %v", err)
		}
	}

	log.Printf("[DIRECTOR] Saved baselines for %d directives", len(report.Directives))
}

// saveAnalysisToMemory persists key findings from analysis to long-term memory.
// Runs programmatically — no extra LLM calls.
func (d *Director) saveAnalysisToMemory(parsed *ParsedDirectorResponse, reportType, trigger string) {
	dateKey := time.Now().Format("2006-01-02")

	// Save directives as decisions (max 3)
	for i, dir := range parsed.Directives {
		if i >= 3 {
			break
		}
		key := fmt.Sprintf("directive:%s:%s:%d", dateKey, dir.Type, i)
		tags := []string{"directive", dir.Type, dir.Priority}
		expires := time.Now().Add(7 * 24 * time.Hour)

		if err := database.SaveAutoMemory("decision", key, truncate(dir.Instruction, 300), tags, &expires); err != nil {
			log.Printf("[DIRECTOR] Failed to save directive memory: %v", err)
		}
	}

	// Save key observations as patterns (max 3)
	for i, obs := range parsed.KeyObservations {
		if i >= 3 {
			break
		}
		key := fmt.Sprintf("observation:%s:%d", dateKey, i)
		if err := database.SaveAutoMemory("pattern", key, truncate(obs, 300), []string{"observation", reportType}, nil); err != nil {
			log.Printf("[DIRECTOR] Failed to save observation memory: %v", err)
		}
	}

	// Save analysis summary as insight
	if parsed.Analysis != "" {
		key := fmt.Sprintf("analysis_summary:%s", dateKey)
		if err := database.SaveAutoMemory("insight", key, truncate(parsed.Analysis, 400), []string{"analysis", reportType, trigger}, nil); err != nil {
			log.Printf("[DIRECTOR] Failed to save analysis memory: %v", err)
		}
	}

	log.Printf("[DIRECTOR] Auto-saved findings to memory")
}
