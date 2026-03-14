package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/agentbus"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Data tool implementations
// ============================================================================

func toolGetAgentMetrics(args map[string]interface{}) string {
	hours := argFloat(args, "hours", 24)

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	stats, err := database.GetAgentStatsSince(since)
	if err != nil {
		return fmt.Sprintf("Error querying agent metrics: %v", err)
	}

	if len(stats) == 0 {
		return fmt.Sprintf("No agent metrics found in the last %.0f hours.", hours)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Agent metrics (last %.0f hours):\n", hours))
	for _, a := range stats {
		escRate := 0.0
		emptyRate := 0.0
		if a.TotalCalls > 0 {
			escRate = float64(a.Escalations) / float64(a.TotalCalls) * 100
			emptyRate = float64(a.EmptyResponses) / float64(a.TotalCalls) * 100
		}
		sb.WriteString(fmt.Sprintf("  %s: %d calls, escalation=%.1f%%, empty=%.1f%%, avg_response=%dms, avg_length=%d chars\n",
			a.AgentName, a.TotalCalls, escRate, emptyRate, int(a.AvgResponseMs), int(a.AvgResponseLen)))
	}
	return sb.String()
}

func toolGetLatestReport() string {
	report, err := database.GetLatestDirectorReport()
	if err != nil {
		return fmt.Sprintf("Error querying report: %v", err)
	}
	if report == nil {
		return "No director reports found. Analysis has never been run. Use run_analysis tool to generate the first report."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Latest report (created %s, type: %s):\n", report.CreatedAt.Format("2006-01-02 15:04"), report.ReportType))
	sb.WriteString(fmt.Sprintf("Summaries analyzed: %d\n", report.SummaryCount))
	sb.WriteString(fmt.Sprintf("Analysis: %s\n", report.Analysis))
	if report.Expectations != "" {
		sb.WriteString(fmt.Sprintf("Expectations: %s\n", report.Expectations))
	}
	if len(report.Directives) > 0 {
		sb.WriteString("Directives:\n")
		for _, d := range report.Directives {
			sb.WriteString(fmt.Sprintf("  [%s/%s] %s: %s\n", d.Type, d.Priority, d.Description, d.Instruction))
		}
	}
	if len(report.CustomerComplaints) > 0 {
		sb.WriteString("Customer complaints:\n")
		for _, c := range report.CustomerComplaints {
			sb.WriteString(fmt.Sprintf("  - %s\n", c))
		}
	}
	if len(report.KeyObservations) > 0 {
		sb.WriteString("Key observations:\n")
		for _, o := range report.KeyObservations {
			sb.WriteString(fmt.Sprintf("  - %s\n", o))
		}
	}
	if len(report.PromptChanges) > 0 {
		sb.WriteString("Prompt changes:\n")
		for _, p := range report.PromptChanges {
			sb.WriteString(fmt.Sprintf("  - [%s] %s (rationale: %s)\n", p.Agent, p.Description, p.Rationale))
		}
	}
	sb.WriteString(fmt.Sprintf("Applied: %v\n", report.Applied))
	return sb.String()
}

func toolGetActivePrompts() string {
	agents := director.AgentNames
	var sb strings.Builder
	sb.WriteString("Active prompts:\n")

	found := false
	for _, name := range agents {
		p, err := database.GetActivePrompt(name)
		if err != nil {
			sb.WriteString(fmt.Sprintf("  %s: error - %v\n", name, err))
			continue
		}
		if p == nil {
			sb.WriteString(fmt.Sprintf("  %s: no active prompt\n", name))
			continue
		}
		found = true
		preview := p.Prompt
		if len(preview) > 150 {
			preview = preview[:150] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %s: v%d by %s (%s)\n    Preview: %s\n",
			name, p.Version, p.CreatedBy, p.CreatedAt.Format("2006-01-02"), preview))
		if p.Metrics != nil {
			sb.WriteString(fmt.Sprintf("    Metrics: %d chats, esc=%.1f%%, empty=%.1f%%\n",
				p.Metrics.ChatsHandled, p.Metrics.EscalationRate*100, p.Metrics.EmptyResponseRate*100))
		}
	}
	if !found {
		sb.WriteString("  No active prompts found for any agent.\n")
	}
	return sb.String()
}

func toolGetPromptHistory(args map[string]interface{}) string {
	agentName := argStr(args, "agent_name")
	if agentName == "" {
		return "Error: agent_name is required. Valid values: zefir_support, plant_expert, device_specialist, support_specialist, orchestrator"
	}

	limit := argInt(args, "limit", 5)

	prompts, err := database.GetPromptHistory(agentName, limit)
	if err != nil {
		return fmt.Sprintf("Error querying prompt history: %v", err)
	}
	if len(prompts) == 0 {
		return fmt.Sprintf("No prompt history found for agent '%s'.", agentName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Prompt history for %s (%d versions):\n", agentName, len(prompts)))
	for _, p := range prompts {
		active := ""
		if p.IsActive {
			active = " [ACTIVE]"
		}
		sb.WriteString(fmt.Sprintf("  v%d by %s (%s)%s\n", p.Version, p.CreatedBy, p.CreatedAt.Format("2006-01-02 15:04"), active))
		if p.Notes != "" {
			sb.WriteString(fmt.Sprintf("    Notes: %s\n", p.Notes))
		}
		if p.Metrics != nil {
			sb.WriteString(fmt.Sprintf("    Metrics: %d chats, esc=%.1f%%, empty=%.1f%%\n",
				p.Metrics.ChatsHandled, p.Metrics.EscalationRate*100, p.Metrics.EmptyResponseRate*100))
		}
	}
	return sb.String()
}

func toolGetToolStats(args map[string]interface{}) string {
	hours := argFloat(args, "hours", 24)

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	stats, err := database.GetToolStatsSince(since)
	if err != nil {
		return fmt.Sprintf("Error querying tool stats: %v", err)
	}
	if len(stats) == 0 {
		return fmt.Sprintf("No tool usage found in the last %.0f hours.", hours)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tool usage stats (last %.0f hours):\n", hours))
	for _, t := range stats {
		sb.WriteString(fmt.Sprintf("  %s: %d calls, success=%d, empty=%d, error=%d (success rate=%.1f%%)\n",
			t.ToolName, t.TotalCalls, t.SuccessCount, t.EmptyCount, t.ErrorCount, t.SuccessRate*100))
	}
	return sb.String()
}

func toolRunAnalysis(ctx context.Context) string {
	adkAR := getAutoResponder()
	if adkAR == nil {
		return "Error: AutoResponder not initialized, cannot run analysis."
	}

	log.Printf("[DIRECTOR_CHAT] Admin requested full analysis cycle")
	err := adkAR.TriggerDirectorAnalysis(ctx)
	if err != nil {
		return fmt.Sprintf("Analysis failed: %v", err)
	}

	return "Analysis completed successfully. A new report has been generated. Use get_latest_report to see the results."
}

func toolGetInteractionDetails(args map[string]interface{}) string {
	hours := argFloat(args, "hours", 24)

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	stats, err := database.GetAgentStatsSince(since)
	if err != nil {
		return fmt.Sprintf("Error querying interactions: %v", err)
	}
	if len(stats) == 0 {
		return fmt.Sprintf("No interactions found in the last %.0f hours.", hours)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Interaction details (last %.0f hours):\n\n", hours))

	for _, a := range stats {
		escRate := 0.0
		emptyRate := 0.0
		if a.TotalCalls > 0 {
			escRate = float64(a.Escalations) / float64(a.TotalCalls) * 100
			emptyRate = float64(a.EmptyResponses) / float64(a.TotalCalls) * 100
		}
		sb.WriteString(fmt.Sprintf("Agent: %s\n", a.AgentName))
		sb.WriteString(fmt.Sprintf("  Total calls: %d\n", a.TotalCalls))
		sb.WriteString(fmt.Sprintf("  Escalations: %d (%.1f%%)\n", a.Escalations, escRate))
		sb.WriteString(fmt.Sprintf("  Empty responses: %d (%.1f%%)\n", a.EmptyResponses, emptyRate))
		sb.WriteString(fmt.Sprintf("  Avg response length: %d chars\n", int(a.AvgResponseLen)))
		sb.WriteString(fmt.Sprintf("  Avg response time: %dms\n", int(a.AvgResponseMs)))

		toolStats, err := database.GetToolStatsByAgentSince(a.AgentName, since)
		if err == nil && len(toolStats) > 0 {
			sb.WriteString("  Tools used:\n")
			for _, t := range toolStats {
				sb.WriteString(fmt.Sprintf("    %s: %d calls (success=%d, error=%d)\n",
					t.ToolName, t.TotalCalls, t.SuccessCount, t.ErrorCount))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func toolGetRecentChats(args map[string]interface{}) string {
	limit := argInt(args, "limit", 10)
	if limit > 50 {
		limit = 50
	}

	chats, total, err := database.GetRecentChatsForDirector(limit)
	if err != nil {
		return fmt.Sprintf("Error querying chats: %v", err)
	}
	if len(chats) == 0 {
		return "No active chats found."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recent chats (%d shown, %d total active):\n\n", len(chats), total))

	for i, ch := range chats {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s", i+1, ch.Source, ch.User.Name))
		if ch.User.Email != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", ch.User.Email))
		}
		sb.WriteString(fmt.Sprintf("\n   Status: %s | Unread: %d | Updated: %s\n",
			ch.Status, ch.UnreadCount, ch.UpdatedAt.Format("2006-01-02 15:04")))
		if ch.LastMessage != nil {
			preview := ch.LastMessage.Content
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("   Last [%s]: %s\n", ch.LastMessage.Sender, preview))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ============================================================================
// Memory tool implementations
// ============================================================================

func toolRemember(args map[string]interface{}) string {
	category := argStr(args, "category")
	key := argStr(args, "key")
	content := argStr(args, "content")

	if category == "" || key == "" || content == "" {
		return "Error: category, key, and content are required."
	}

	if !director.ValidMemoryCategories[category] {
		return "Error: invalid category. Use: fact, decision, pattern, insight, preference."
	}

	if len(content) > 500 {
		content = content[:500]
	}

	importance := argInt(args, "importance", 5)
	if importance < 1 {
		importance = 1
	}
	if importance > 10 {
		importance = 10
	}

	tags := argTags(args, "tags")
	pinned := argBool(args, "pinned", false)

	var expiresAt *time.Time
	if hours := argFloat(args, "expires_in_hours", 0); hours > 0 {
		t := time.Now().Add(time.Duration(hours) * time.Hour)
		expiresAt = &t
	}

	m := &models.DirectorMemory{
		ID:         uuid.New(),
		Category:   category,
		Key:        key,
		Content:    content,
		Importance: importance,
		Source:     "chat",
		Tags:       tags,
		Pinned:     pinned,
		ExpiresAt:  expiresAt,
	}

	if err := database.UpsertMemory(m); err != nil {
		return fmt.Sprintf("Error saving memory: %v", err)
	}

	expiresStr := "permanent"
	if expiresAt != nil {
		expiresStr = fmt.Sprintf("expires %s", expiresAt.Format("2006-01-02 15:04"))
	}
	pinnedStr := ""
	if pinned {
		pinnedStr = " [PINNED]"
	}
	return fmt.Sprintf("Remembered [%s/%s] (importance=%d, %s%s): %s", category, key, importance, expiresStr, pinnedStr, content)
}

func toolRecall(args map[string]interface{}) string {
	query := argStr(args, "query")
	category := argStr(args, "category")
	limit := argInt(args, "limit", 5)

	if query == "" && category == "" {
		return "Error: provide at least a query or category to search."
	}

	memories, err := database.RecallMemoriesHybrid(query, category, limit)
	if err != nil {
		return fmt.Sprintf("Error recalling memories: %v", err)
	}
	if len(memories) == 0 {
		return "No memories found matching your query."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d memories (hybrid search):\n", len(memories)))
	for i, m := range memories {
		scoreInfo := ""
		if m.HybridScore > 0 {
			scoreInfo = fmt.Sprintf(", score=%.2f", m.HybridScore)
		}
		sb.WriteString(fmt.Sprintf("%d. [%s/%s] (imp=%d, accessed=%d%s) %s\n",
			i+1, m.Category, m.Key, m.Importance, m.AccessCount, scoreInfo, m.Content))
		if len(m.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   tags: %s\n", strings.Join(m.Tags, ", ")))
		}
	}
	return sb.String()
}

func toolForget(args map[string]interface{}) string {
	category := argStr(args, "category")
	key := argStr(args, "key")

	if category == "" || key == "" {
		return "Error: category and key are required."
	}

	if err := database.DeleteMemory(category, key); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf("Forgotten [%s/%s].", category, key)
}

func toolListMemories(args map[string]interface{}) string {
	category := argStr(args, "category")
	limit := argInt(args, "limit", 10)

	memories, err := database.ListMemories(category, limit)
	if err != nil {
		return fmt.Sprintf("Error listing memories: %v", err)
	}
	if len(memories) == 0 {
		if category != "" {
			return fmt.Sprintf("No memories in category '%s'.", category)
		}
		return "No memories stored yet."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Memories (%d entries):\n", len(memories)))
	for i, m := range memories {
		preview := m.Content
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		sb.WriteString(fmt.Sprintf("%d. [%s/%s] imp=%d: %s\n", i+1, m.Category, m.Key, m.Importance, preview))
	}
	return sb.String()
}

func toolSearchReports(args map[string]interface{}) string {
	query := argStr(args, "query")
	limit := argInt(args, "limit", 5)

	// Date range mode
	if daysBack := argFloat(args, "days_back", 0); daysBack > 0 {
		to := time.Now()
		from := to.Add(-time.Duration(daysBack) * 24 * time.Hour)
		reports, err := database.GetReportsByDateRange(from, to, limit)
		if err != nil {
			return fmt.Sprintf("Error searching reports: %v", err)
		}
		return formatReportResults(reports, fmt.Sprintf("last %.0f days", daysBack))
	}

	// Text search mode
	if query == "" {
		return "Error: provide either 'query' or 'days_back' parameter."
	}

	reports, err := database.SearchReports(query, limit)
	if err != nil {
		return fmt.Sprintf("Error searching reports: %v", err)
	}
	return formatReportResults(reports, fmt.Sprintf("query '%s'", query))
}

func formatReportResults(reports []models.DirectorReport, searchDesc string) string {
	if len(reports) == 0 {
		return fmt.Sprintf("No reports found for %s.", searchDesc)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d reports (%s):\n\n", len(reports), searchDesc))

	for i, r := range reports {
		sb.WriteString(fmt.Sprintf("── Report #%d (%s, %s) ──\n",
			i+1, r.CreatedAt.Format("2006-01-02 15:04"), r.ReportType))
		sb.WriteString(fmt.Sprintf("Trigger: %s | Summaries: %d | Applied: %v\n", r.TriggerEvent, r.SummaryCount, r.Applied))

		analysis := r.Analysis
		if len(analysis) > 300 {
			analysis = analysis[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("Analysis: %s\n", analysis))

		if len(r.Directives) > 0 {
			sb.WriteString(fmt.Sprintf("Directives (%d):\n", len(r.Directives)))
			for _, d := range r.Directives {
				sb.WriteString(fmt.Sprintf("  - [%s/%s] %s\n", d.Type, d.Priority, d.Instruction))
			}
		}

		if len(r.CustomerComplaints) > 0 {
			sb.WriteString(fmt.Sprintf("Complaints (%d): %s\n", len(r.CustomerComplaints), strings.Join(r.CustomerComplaints, "; ")))
		}

		if r.Expectations != "" {
			expectations := r.Expectations
			if len(expectations) > 200 {
				expectations = expectations[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("Expectations: %s\n", expectations))
		}

		sb.WriteString("\n")
	}
	return sb.String()
}

// ============================================================================
// Advanced search & timeline tool implementations
// ============================================================================

func toolDeepSearch(args map[string]interface{}) string {
	query := argStr(args, "query")
	sourceType := argStr(args, "source_type")
	timeRange := argStr(args, "time_range")
	limit := argInt(args, "limit", 10)

	if query == "" && sourceType == "" {
		return "Error: provide at least a 'query' to search for."
	}
	if sourceType == "" {
		sourceType = "all"
	}

	results, err := database.DeepSearch(query, sourceType, timeRange, limit)
	if err != nil {
		return fmt.Sprintf("Error in deep search: %v", err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results found for '%s' (source=%s, range=%s).", query, sourceType, timeRange)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results for '%s':\n", len(results), query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s | %s | rank=%.2f\n   %s\n",
			i+1, r.Type, r.Meta, r.Date.Format("2006-01-02"), r.Rank, r.Snippet))
	}
	return sb.String()
}

func toolTimeline(args map[string]interface{}) string {
	period := argStr(args, "period")
	detail := argStr(args, "detail")
	if period == "" {
		return "Error: 'period' is required. Use: last_week, last_month, last_quarter, last_year, YYYY-MM, YYYY."
	}
	if detail == "" {
		detail = "summary"
	}

	from, to, err := database.ParsePeriod(period)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	data, err := database.GetTimelineData(from, to, detail)
	if err != nil {
		return fmt.Sprintf("Error getting timeline: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Timeline: %s → %s\n",
		from.Format("2006-01-02"), to.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Interactions: %d | Escalation: %.1f%% | Empty: %.1f%% | Avg response: %dms\n",
		data.TotalInteractions, data.AvgEscalationRate*100, data.AvgEmptyRate*100, int(data.AvgResponseMs)))
	sb.WriteString(fmt.Sprintf("Reports generated: %d\n", data.ReportCount))

	if len(data.DirectiveStats) > 0 {
		sb.WriteString("Directive effectiveness: ")
		parts := []string{}
		for eff, count := range data.DirectiveStats {
			parts = append(parts, fmt.Sprintf("%s=%d", eff, count))
		}
		sb.WriteString(strings.Join(parts, ", "))
		sb.WriteString("\n")
	}

	if len(data.TopAgents) > 0 {
		sb.WriteString("\nPer-agent breakdown:\n")
		for _, a := range data.TopAgents {
			sb.WriteString(fmt.Sprintf("  %s: %d calls, esc=%.1f%%, empty=%.1f%%\n",
				a.Name, a.Calls, a.EscalationRate*100, a.EmptyRate*100))
		}
	}

	if len(data.ReportSummaries) > 0 {
		sb.WriteString("\nReports:\n")
		for _, s := range data.ReportSummaries {
			sb.WriteString(fmt.Sprintf("  %s\n", s))
		}
	}

	return sb.String()
}

// ============================================================================
// Inter-agent communication tool implementations
// ============================================================================

func getAgentBus() *agentbus.AgentBus {
	if ar := getAutoResponder(); ar != nil {
		return ar.GetAgentBus()
	}
	return nil
}

func toolAgentSend(ctx context.Context, args map[string]interface{}) string {
	chatID := argStr(args, "chat_id")
	message := argStr(args, "message")

	if chatID == "" || message == "" {
		return "Error: chat_id and message are required."
	}

	bus := getAgentBus()
	if bus == nil {
		return "Error: AgentBus not available. AutoResponder may not be initialized."
	}

	result, err := bus.QueryAgent(ctx, chatID, message)
	if err != nil {
		return fmt.Sprintf("Error querying agent: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Agent response for chat %s:\n\n%s", chatID, result.Response))
	if result.Tokens != nil {
		sb.WriteString(fmt.Sprintf("\n\n[tokens: %d in + %d out]", result.Tokens.PromptTokens, result.Tokens.CompletionTokens))
	}
	return sb.String()
}

func toolAgentList() string {
	bus := getAgentBus()

	var sb strings.Builder

	// Active sessions from AgentBus
	if bus != nil {
		sessions := bus.ListSessions()
		if len(sessions) > 0 {
			sb.WriteString(fmt.Sprintf("Active agent sessions (%d):\n", len(sessions)))
			for i, s := range sessions {
				duration := time.Since(s.StartedAt).Round(time.Second)
				sb.WriteString(fmt.Sprintf("  %d. [%s] chat=%s (running %s, msgs=%d)\n",
					i+1, s.AgentType, s.ChatID, duration, s.MessageCount))
			}
		} else {
			sb.WriteString("No active agent sessions right now.\n")
		}
	} else {
		sb.WriteString("AgentBus not available.\n")
	}

	// Fallback: cached agent stats
	if adkAR := getAutoResponder(); adkAR != nil {
		total, chatIDs := adkAR.GetAgentCacheStats()
		if total > 0 {
			sb.WriteString(fmt.Sprintf("\nCached agents (%d total):\n", total))
			for i, id := range chatIDs {
				if i >= 10 {
					sb.WriteString(fmt.Sprintf("  ... and %d more\n", total-10))
					break
				}
				sb.WriteString(fmt.Sprintf("  - %s\n", id))
			}
		}
	}

	return sb.String()
}

func toolAgentContext(ctx context.Context, args map[string]interface{}) string {
	chatID := argStr(args, "chat_id")
	if chatID == "" {
		return "Error: chat_id is required."
	}

	limit := argInt(args, "limit", 20)

	bus := getAgentBus()
	if bus == nil {
		return "Error: AgentBus not available."
	}

	agentCtx, err := bus.GetAgentContext(ctx, chatID, limit)
	if err != nil {
		return fmt.Sprintf("Error getting agent context: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Agent context for chat %s:\n\n", chatID))

	if agentCtx.Summary != "" {
		summary := agentCtx.Summary
		if len(summary) > 500 {
			summary = summary[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("Summary: %s\n\n", summary))
	}

	if len(agentCtx.Messages) > 0 {
		sb.WriteString(fmt.Sprintf("Recent messages (%d):\n", len(agentCtx.Messages)))
		for _, m := range agentCtx.Messages {
			content := m.Content
			if len(content) > 200 {
				content = content[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", m.Timestamp.Format("15:04"), m.Sender, content))
		}
	} else {
		sb.WriteString("No messages found.\n")
	}

	return sb.String()
}

// ============================================================================
// Identity tool implementations
// ============================================================================

func toolGetIdentity(args map[string]interface{}) string {
	aspect := argStr(args, "aspect")

	if aspect != "" {
		id, err := database.GetIdentity(aspect)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		if id == nil {
			return fmt.Sprintf("Aspect '%s' not found.", aspect)
		}
		return fmt.Sprintf("[%s] (v%d, updated %s by %s)\n%s",
			id.Key, id.Version, id.UpdatedAt.Format("2006-01-02 15:04"), id.UpdatedBy, id.Content)
	}

	// Return all aspects
	items, err := database.GetAllIdentity()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(items) == 0 {
		return "No identity aspects found. Identity has not been bootstrapped yet."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Your identity (%d aspects):\n\n", len(items)))
	for _, id := range items {
		sb.WriteString(fmt.Sprintf("=== %s (v%d, %s by %s) ===\n%s\n\n",
			id.Key, id.Version, id.UpdatedAt.Format("2006-01-02"), id.UpdatedBy, id.Content))
	}
	return sb.String()
}

func toolUpdateIdentity(args map[string]interface{}) string {
	aspect := argStr(args, "aspect")
	content := argStr(args, "content")
	reason := argStr(args, "reason")

	if aspect == "" || content == "" || reason == "" {
		return "Error: aspect, content, and reason are all required."
	}

	result, err := director.UpdateIdentityChecked(aspect, content, "director", reason)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return result
}

func toolIntrospect(ctx context.Context) string {
	dir := getDirectorInstance()
	if dir == nil {
		return "Error: Director instance not available."
	}

	result, err := dir.Introspect(ctx)
	if err != nil {
		return fmt.Sprintf("Introspection error: %v", err)
	}
	return result
}

func toolIdentityHistory(args map[string]interface{}) string {
	aspect := argStr(args, "aspect")
	if aspect == "" {
		return "Error: aspect is required."
	}

	limit := argInt(args, "limit", 5)

	history, err := database.GetIdentityHistory(aspect, limit)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if len(history) == 0 {
		return fmt.Sprintf("No history found for '%s'. It may have never been updated.", aspect)
	}

	current, _ := database.GetIdentity(aspect)

	var sb strings.Builder
	if current != nil {
		sb.WriteString(fmt.Sprintf("Current: v%d (by %s, %s)\n\n",
			current.Version, current.UpdatedBy, current.UpdatedAt.Format("2006-01-02 15:04")))
	}
	sb.WriteString(fmt.Sprintf("History for '%s' (%d entries):\n\n", aspect, len(history)))
	for _, h := range history {
		preview := h.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("--- v%d (%s by %s) ---\nReason: %s\n%s\n\n",
			h.Version, h.CreatedAt.Format("2006-01-02 15:04"), h.ChangedBy,
			h.ChangeReason, preview))
	}
	return sb.String()
}

func toolRollbackIdentity(args map[string]interface{}) string {
	aspect := argStr(args, "aspect")
	version := argFloat(args, "version", 0)

	if aspect == "" || version <= 0 {
		return "Error: aspect and version are required."
	}

	if err := database.RollbackIdentity(aspect, int(version)); err != nil {
		return fmt.Sprintf("Error rolling back: %v", err)
	}

	// Invalidate cache after rollback
	director.InvalidateIdentityCache()

	log.Printf("[DIRECTOR_IDENTITY] Rolled back %s to v%d", aspect, int(version))
	return fmt.Sprintf("✓ Откат '%s' к версии %d. Создана новая версия с содержимым старой.", aspect, int(version))
}

// ============================================================================
// Webhook tool implementations
// ============================================================================

func toolGetWebhookEvents(args map[string]interface{}) string {
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
	}

	events, err := database.GetRecentWebhookEvents(limit)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if len(events) == 0 {
		return "Нет webhook событий. Внешние системы ещё не отправляли триггеры."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Webhook события (последние %d):\n\n", len(events)))
	for _, evt := range events {
		sb.WriteString(fmt.Sprintf("• [%s] %s/%s — %s\n  Статус: %s | Приоритет: %s | Action: %s\n  Время: %s\n",
			evt.ID.String()[:8],
			evt.Source, evt.EventType,
			truncateStr(evt.Message, 80),
			evt.Status, evt.Priority, evt.Action,
			evt.CreatedAt.Format("2006-01-02 15:04:05"),
		))
		if evt.Error != "" {
			sb.WriteString(fmt.Sprintf("  Ошибка: %s\n", evt.Error))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ============================================================================
// Cron tool implementations
// ============================================================================

func toolCreateCronJob(args map[string]interface{}) string {
	if CronService == nil {
		return "Error: CronService не инициализирован"
	}

	name := argStr(args, "name")
	if name == "" {
		return "Error: name обязательно"
	}

	schedType := argStr(args, "schedule_type")
	schedule := argStr(args, "schedule")
	if schedType == "" || schedule == "" {
		return "Error: schedule_type и schedule обязательны"
	}

	req := &models.CronJobRequest{
		Name:         name,
		Description:  argStr(args, "description"),
		ScheduleType: schedType,
		Schedule:     schedule,
		Timezone:     argStr(args, "timezone"),
		Action:       argStr(args, "action"),
		ActionConfig: argStr(args, "action_config"),
		MaxRuns:      argInt(args, "max_runs", 0),
	}

	job, err := CronService.AddJob(req, "director")
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	nextRunStr := "не определено"
	if job.NextRunAt != nil {
		nextRunStr = job.NextRunAt.Format("2006-01-02 15:04:05 MST")
	}

	return fmt.Sprintf("✓ Задача '%s' создана.\nТип: %s | Расписание: %s | Action: %s\nСледующий запуск: %s",
		job.Name, job.ScheduleType, job.Schedule, job.Action, nextRunStr)
}

func toolListCronJobs() string {
	jobs, err := database.ListCronJobs()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if len(jobs) == 0 {
		return "Нет запланированных задач."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Запланированные задачи (%d):\n\n", len(jobs)))
	for _, job := range jobs {
		status := "✅"
		if !job.Enabled {
			status = "⏸️"
		}

		nextRun := "—"
		if job.NextRunAt != nil {
			nextRun = job.NextRunAt.Format("2006-01-02 15:04")
		}
		lastRun := "никогда"
		if job.LastRunAt != nil {
			lastRun = job.LastRunAt.Format("2006-01-02 15:04")
		}

		sb.WriteString(fmt.Sprintf("%s %s\n", status, job.Name))
		if job.Description != "" {
			sb.WriteString(fmt.Sprintf("  Описание: %s\n", job.Description))
		}
		sb.WriteString(fmt.Sprintf("  Тип: %s | Расписание: %s", job.ScheduleType, job.Schedule))
		if job.Timezone != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", job.Timezone))
		}
		sb.WriteString(fmt.Sprintf("\n  Action: %s | Запусков: %d", job.Action, job.RunCount))
		if job.MaxRuns > 0 {
			sb.WriteString(fmt.Sprintf("/%d", job.MaxRuns))
		}
		sb.WriteString(fmt.Sprintf("\n  Последний: %s | Следующий: %s\n", lastRun, nextRun))
		if job.LastError != "" {
			sb.WriteString(fmt.Sprintf("  Ошибка: %s\n", job.LastError))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func toolDeleteCronJob(args map[string]interface{}) string {
	if CronService == nil {
		return "Error: CronService не инициализирован"
	}

	name := argStr(args, "name")
	if name == "" {
		return "Error: name обязательно"
	}

	job, err := database.GetCronJobByName(name)
	if err != nil || job == nil {
		return fmt.Sprintf("Error: задача '%s' не найдена", name)
	}

	if err := CronService.RemoveJob(job.ID); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("✓ Задача '%s' удалена.", name)
}

func toolToggleCronJob(args map[string]interface{}) string {
	if CronService == nil {
		return "Error: CronService не инициализирован"
	}

	name := argStr(args, "name")
	if name == "" {
		return "Error: name обязательно"
	}

	enabled, ok := args["enabled"].(bool)
	if !ok {
		return "Error: enabled обязательно (true/false)"
	}

	job, err := database.GetCronJobByName(name)
	if err != nil || job == nil {
		return fmt.Sprintf("Error: задача '%s' не найдена", name)
	}

	if err := CronService.ToggleJob(job.ID, enabled); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	action := "включена"
	if !enabled {
		action = "отключена"
	}
	return fmt.Sprintf("✓ Задача '%s' %s.", name, action)
}

func toolRunCronJob(args map[string]interface{}) string {
	if CronService == nil {
		return "Error: CronService не инициализирован"
	}

	name := argStr(args, "name")
	if name == "" {
		return "Error: name обязательно"
	}

	job, err := database.GetCronJobByName(name)
	if err != nil || job == nil {
		return fmt.Sprintf("Error: задача '%s' не найдена", name)
	}

	if err := CronService.RunNow(job.ID); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("✓ Задача '%s' запущена вручную. Результат будет в логах.", name)
}

// ============================================================================
// Browser tool implementations
// ============================================================================

func toolBrowserScreenshot(ctx context.Context, args map[string]interface{}) string {
	if BrowserService == nil {
		return "Error: Browser service не запущен. Установите DIRECTOR_BROWSER_ENABLED=true в настройках."
	}

	targetURL := argStr(args, "url")
	if targetURL == "" {
		return "Error: url обязательно"
	}

	result, err := BrowserService.Screenshot(ctx, targetURL)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	// Return metadata + truncated base64 (full image would be too large for tool result)
	imgPreview := result.ImageB64
	if len(imgPreview) > 500 {
		imgPreview = imgPreview[:500] + "...[truncated]"
	}

	return fmt.Sprintf("✓ Screenshot сделан\nURL: %s\nTitle: %s\nSize: %dx%d\nTimestamp: %s\nImage (base64, %d bytes): %s",
		result.URL, result.Title, result.Width, result.Height,
		result.Timestamp, len(result.ImageB64), imgPreview)
}

func toolBrowserGetText(ctx context.Context, args map[string]interface{}) string {
	if BrowserService == nil {
		return "Error: Browser service не запущен. Установите DIRECTOR_BROWSER_ENABLED=true в настройках."
	}

	targetURL := argStr(args, "url")
	if targetURL == "" {
		return "Error: url обязательно"
	}

	maxLen := argInt(args, "max_length", 5000)

	result, err := BrowserService.GetText(ctx, targetURL, maxLen)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("URL: %s\nTitle: %s\n\n%s", result.URL, result.Title, result.Text)
}

func toolBrowserEvalJS(ctx context.Context, args map[string]interface{}) string {
	if BrowserService == nil {
		return "Error: Browser service не запущен. Установите DIRECTOR_BROWSER_ENABLED=true в настройках."
	}

	targetURL := argStr(args, "url")
	script := argStr(args, "script")
	if targetURL == "" || script == "" {
		return "Error: url и script обязательны"
	}

	result, err := BrowserService.EvalJS(ctx, targetURL, script)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("URL: %s\nResult: %s", targetURL, result)
}

func toolGetWebhookStats() string {
	stats, err := database.GetWebhookStats()
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	var sb strings.Builder
	sb.WriteString("📊 Статистика webhook:\n\n")
	sb.WriteString(fmt.Sprintf("Всего получено: %d\n", stats.TotalReceived))
	sb.WriteString(fmt.Sprintf("Обработано: %d\n", stats.TotalProcessed))
	sb.WriteString(fmt.Sprintf("Дубликаты: %d\n", stats.TotalDuplicate))
	sb.WriteString(fmt.Sprintf("Ошибки: %d\n", stats.TotalFailed))

	if len(stats.BySources) > 0 {
		sb.WriteString("\nПо источникам:\n")
		for source, count := range stats.BySources {
			sb.WriteString(fmt.Sprintf("  %s: %d\n", source, count))
		}
	}

	if len(stats.ByEventType) > 0 {
		sb.WriteString("\nПо типам событий:\n")
		for eventType, count := range stats.ByEventType {
			sb.WriteString(fmt.Sprintf("  %s: %d\n", eventType, count))
		}
	}

	if stats.LastEventAt != nil {
		sb.WriteString(fmt.Sprintf("\nПоследнее событие: %s", stats.LastEventAt.Format("2006-01-02 15:04:05")))
	}

	return sb.String()
}
