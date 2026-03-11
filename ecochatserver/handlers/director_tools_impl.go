package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Data tool implementations
// ============================================================================

func toolGetAgentMetrics(args map[string]interface{}) string {
	hours := 24.0
	if h, ok := args["hours"].(float64); ok && h > 0 {
		hours = h
	}

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
	agents := []string{"zefir_support", "plant_expert", "device_specialist", "support_specialist", "orchestrator"}
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
	agentName, _ := args["agent_name"].(string)
	if agentName == "" {
		return "Error: agent_name is required. Valid values: zefir_support, plant_expert, device_specialist, support_specialist, orchestrator"
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

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
	hours := 24.0
	if h, ok := args["hours"].(float64); ok && h > 0 {
		hours = h
	}

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
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
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
	hours := 24.0
	if h, ok := args["hours"].(float64); ok && h > 0 {
		hours = h
	}

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
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
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
	category, _ := args["category"].(string)
	key, _ := args["key"].(string)
	content, _ := args["content"].(string)

	if category == "" || key == "" || content == "" {
		return "Error: category, key, and content are required."
	}

	validCategories := map[string]bool{"fact": true, "decision": true, "pattern": true, "insight": true, "preference": true}
	if !validCategories[category] {
		return "Error: invalid category. Use: fact, decision, pattern, insight, preference."
	}

	if len(content) > 500 {
		content = content[:500]
	}

	importance := 5
	if imp, ok := args["importance"].(float64); ok && imp >= 1 && imp <= 10 {
		importance = int(imp)
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]interface{}); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	pinned := false
	if p, ok := args["pinned"].(bool); ok {
		pinned = p
	}

	var expiresAt *time.Time
	if hours, ok := args["expires_in_hours"].(float64); ok && hours > 0 {
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
	query, _ := args["query"].(string)
	category, _ := args["category"].(string)
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

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
	category, _ := args["category"].(string)
	key, _ := args["key"].(string)

	if category == "" || key == "" {
		return "Error: category and key are required."
	}

	if err := database.DeleteMemory(category, key); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return fmt.Sprintf("Forgotten [%s/%s].", category, key)
}

func toolListMemories(args map[string]interface{}) string {
	category, _ := args["category"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

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
	query, _ := args["query"].(string)
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	// Date range mode
	if daysBack, ok := args["days_back"].(float64); ok && daysBack > 0 {
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
	query, _ := args["query"].(string)
	sourceType, _ := args["source_type"].(string)
	timeRange, _ := args["time_range"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

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
	period, _ := args["period"].(string)
	detail, _ := args["detail"].(string)
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
// Identity tool implementations
// ============================================================================

func toolGetIdentity(args map[string]interface{}) string {
	aspect, _ := args["aspect"].(string)

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
	aspect, _ := args["aspect"].(string)
	content, _ := args["content"].(string)
	reason, _ := args["reason"].(string)

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
	aspect, _ := args["aspect"].(string)
	if aspect == "" {
		return "Error: aspect is required."
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

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
	aspect, _ := args["aspect"].(string)
	version, _ := args["version"].(float64)

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
