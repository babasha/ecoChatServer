package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// directorChatHistory stores per-admin conversation history (in-memory, resets on restart)
var (
	directorChatHistory     = map[string][]llm.Message{} // adminID → messages
	directorChatHistoryMu   sync.RWMutex
	directorChatFlushedUpTo = map[string]int{} // adminID → index up to which we've proactively flushed
)

const maxDirectorChatHistory = 20 // keep last 20 messages per admin
const maxToolCalls = 5            // max tool calls per request to prevent infinite loops

// getDirectorInstance returns the Director via AutoResponder.
func getDirectorInstance() *director.Director {
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		return nil
	}
	return adkAR.GetDirector()
}

// ============================================================================
// Director Tools — instruments the Director can use to query data
// ============================================================================

var directorTools = []llm.Tool{
	{
		Name:        "get_agent_metrics",
		Description: "Get agent performance metrics (calls, escalation rate, empty responses, avg response time) for a specified period. Use this when asked about agent performance, statistics, or efficiency.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Number of hours to look back (e.g., 24 for last day, 168 for last week). Default 24.",
				},
			},
		},
	},
	{
		Name:        "get_latest_report",
		Description: "Get the latest Director analysis report with findings, complaints, observations, and expectations. Use this when asked about reports, analysis results, or system health.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_active_prompts",
		Description: "Get currently active prompt versions for all agents with their version numbers, creators, and preview. Use this when asked about current prompts or agent configurations.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_prompt_history",
		Description: "Get prompt version history for a specific agent. Shows all versions with their metrics. Use when asked about prompt changes or evolution.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_name": map[string]interface{}{
					"type":        "string",
					"description": "Agent name: zefir_support, plant_expert, device_specialist, support_specialist, or orchestrator",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max number of versions to return. Default 5.",
				},
			},
			"required": []string{"agent_name"},
		},
	},
	{
		Name:        "get_tool_stats",
		Description: "Get tool usage statistics (which tools are called, success/error rates). Use when asked about tool performance or failures.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Number of hours to look back. Default 24.",
				},
			},
		},
	},
	{
		Name:        "run_analysis",
		Description: "Trigger a full Director analysis cycle. This will analyze recent chat summaries, agent metrics, and generate a new report with directives. Use ONLY when the admin explicitly asks to run an analysis.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_interaction_details",
		Description: "Get detailed recent interaction metrics (individual calls with agent, response length, timing, tools used). Use when asked about specific recent interactions or debugging.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Number of hours to look back. Default 24.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max number of interactions to return. Default 20.",
				},
			},
		},
	},
	{
		Name:        "get_recent_chats",
		Description: "Get recent customer support chats with user info, last message, source, and status. Use when asked about recent client messages, conversations, who wrote last, or chat activity.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Number of recent chats to return (1-50). Default 10.",
				},
			},
		},
	},
	// ── Memory tools ──────────────────────────────────────────────────────
	{
		Name:        "remember",
		Description: "Save a fact, decision, pattern, insight, or preference to your long-term memory. Uses UPSERT — same category+key updates existing memory. Use this to remember important information across sessions.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"fact", "decision", "pattern", "insight", "preference"},
					"description": "Memory category: fact (about users/system), decision (your choices), pattern (recurring trends), insight (observations), preference (admin settings).",
				},
				"key": map[string]interface{}{
					"type":        "string",
					"description": "Unique key within category for deduplication, e.g. 'agent:zefir_support:escalation_issue' or 'admin:egor:language'.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "Compact memory text. Keep it brief — max 1-2 sentences. Focus on the essence.",
				},
				"importance": map[string]interface{}{
					"type":        "number",
					"description": "Importance 1-10. Default 5. Use 8-10 for critical decisions, 1-3 for minor facts.",
				},
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Tags for fast search, e.g. ['agent', 'zefir_support', 'escalation'].",
				},
				"pinned": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, this memory will NEVER decay over time. Use for critical permanent knowledge.",
				},
				"expires_in_hours": map[string]interface{}{
					"type":        "number",
					"description": "Optional: memory expires after N hours. Omit for permanent memories.",
				},
			},
			"required": []string{"category", "key", "content"},
		},
	},
	{
		Name:        "search_reports",
		Description: "Search through ALL your past analysis reports by keywords or date range. Use this to find old analyses, past decisions, historical complaints, trends over time. Supports full-text search in Russian and English.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords to find in report analysis, expectations, or trigger events. E.g. 'эскалация', 'firmware', 'промпт'.",
				},
				"days_back": map[string]interface{}{
					"type":        "number",
					"description": "Alternative to query: get reports from last N days. E.g. 30 for last month.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max reports to return (1-20). Default 5.",
				},
			},
		},
	},
	{
		Name:        "recall",
		Description: "Search your long-term memory by keyword or category. Returns most relevant memories sorted by importance. Use this when you need to remember past decisions, facts, or patterns.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search query — matches against content, key, and tags.",
				},
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"fact", "decision", "pattern", "insight", "preference", ""},
					"description": "Optional: filter by category. Empty = search all.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max results (1-20). Default 5.",
				},
			},
		},
	},
	{
		Name:        "forget",
		Description: "Delete a specific memory by its category and key. Use when information is outdated or incorrect.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"fact", "decision", "pattern", "insight", "preference"},
					"description": "Memory category.",
				},
				"key": map[string]interface{}{
					"type":        "string",
					"description": "The key of the memory to delete.",
				},
			},
			"required": []string{"category", "key"},
		},
	},
	{
		Name:        "list_memories",
		Description: "List your stored memories, optionally filtered by category. Shows key and preview for each. Use to review what you remember.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"fact", "decision", "pattern", "insight", "preference", ""},
					"description": "Optional: filter by category. Empty = show all.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max entries to return (1-50). Default 10.",
				},
			},
		},
	},
	// ── Advanced search & timeline tools ─────────────────────────────────
	{
		Name:        "deep_search",
		Description: "Unified full-text search across ALL your data: memories, reports, chat summaries, directives, and digests. Use this for broad research when you need to find information across multiple sources. Supports Russian and English, with time range filtering.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords. E.g. 'эскалация zefir_support', 'firmware проблема', 'промпт изменение'.",
				},
				"source_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"all", "memories", "reports", "chats", "directives", "digests"},
					"description": "Filter by data source. Default 'all' = search everywhere.",
				},
				"time_range": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"all", "last_week", "last_month", "last_quarter", "last_year"},
					"description": "Time range filter. Default 'all'.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max results (1-20). Default 10.",
				},
			},
		},
	},
	{
		Name:        "timeline",
		Description: "Browse historical data for any time period. Shows aggregated metrics, reports count, directive effectiveness, and agent performance. Use when asked 'what happened last week/month/year' or to compare periods.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"period": map[string]interface{}{
					"type":        "string",
					"description": "Time period: 'last_week', 'last_month', 'last_quarter', 'last_year', or specific 'YYYY-MM' (e.g. '2026-02') or 'YYYY' (e.g. '2025').",
				},
				"detail": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"summary", "full"},
					"description": "Detail level: 'summary' for quick overview, 'full' for reports and per-agent breakdown. Default 'summary'.",
				},
			},
			"required": []string{"period"},
		},
	},
	// ── Skill management tools ──────────────────────────────────────────
	{
		Name:        "create_skill",
		Description: "Create a new custom tool/skill that you can use in future conversations. Supports 4 types: 'sql_query' (parameterized SELECT query), 'prompt_chain' (LLM prompt template), 'http_api' (HTTP request), 'composite' (pipeline of other skills). The skill becomes immediately available as a tool.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Unique tool name (lowercase, underscores). E.g. 'find_firmware_issues', 'weekly_summary', 'check_user_activity'.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "What the tool does — shown to you in future conversations to decide when to use it.",
				},
				"skill_type": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"sql_query", "prompt_chain", "http_api", "composite"},
					"description": "Execution type: 'sql_query' = parameterized SQL SELECT, 'prompt_chain' = LLM prompt template, 'http_api' = external HTTP call, 'composite' = pipeline chaining multiple skills.",
				},
				"code": map[string]interface{}{
					"type":        "string",
					"description": "Implementation body. For sql_query: SQL with $1,$2.. placeholders (SELECT only). For prompt_chain: prompt template with {{param}} placeholders. For http_api: JSON config {url, method, headers, body_template}. For composite: JSON {steps:[{skill,args_map,output_var}], output:'{{var}}'}.",
				},
				"parameters": map[string]interface{}{
					"type":        "string",
					"description": "JSON Schema for tool parameters (what the LLM sees). E.g. '{\"type\":\"object\",\"properties\":{\"hours\":{\"type\":\"number\",\"description\":\"Hours to look back\"}}}'",
				},
				"tags": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Tags for categorization, e.g. ['analytics', 'firmware', 'weekly'].",
				},
			},
			"required": []string{"name", "description", "skill_type", "code"},
		},
	},
	{
		Name:        "edit_skill",
		Description: "Edit an existing custom skill — update its code, description, parameters, or enable/disable it.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to edit.",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "New description (optional).",
				},
				"code": map[string]interface{}{
					"type":        "string",
					"description": "New implementation code (optional).",
				},
				"parameters": map[string]interface{}{
					"type":        "string",
					"description": "New JSON Schema for parameters (optional).",
				},
				"enabled": map[string]interface{}{
					"type":        "boolean",
					"description": "Enable or disable the skill (optional).",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "list_skills",
		Description: "List all your custom skills/tools with their type, version, usage stats, and status. Use to review what tools you've created.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "delete_skill",
		Description: "Delete a custom skill by name. This is irreversible.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to delete.",
				},
			},
			"required": []string{"name"},
		},
	},
	{
		Name:        "test_skill",
		Description: "Test a custom skill with sample parameters to verify it works correctly before using it in production.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to test.",
				},
				"test_args": map[string]interface{}{
					"type":        "object",
					"description": "Test parameters to pass to the skill.",
				},
			},
			"required": []string{"name"},
		},
	},
	// ── Identity tools ──────────────────────────────────────────────────
	{
		Name:        "get_identity",
		Description: "Get your current personality/identity. Returns all aspects (soul, goals, style, etc.) or a specific one. Use this to review who you are, your goals, values, and communication style.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aspect": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"soul", "identity", "goals", "style", "values", "user_profile", "self_assessment", ""},
					"description": "Specific aspect to retrieve. Empty = return all aspects.",
				},
			},
		},
	},
	{
		Name:        "update_identity",
		Description: "Update an aspect of your personality/identity. This ACTUALLY changes who you are — your next response will use the updated identity. Previous version is saved to history with diff. Always explain WHY. NOTE: 'values' is PROTECTED — only the admin can change it.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aspect": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"soul", "identity", "goals", "style", "values", "user_profile", "self_assessment"},
					"description": "Which aspect to update.",
				},
				"content": map[string]interface{}{
					"type":        "string",
					"description": "New content for this aspect. Replaces the entire aspect.",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Why you're making this change. Required for transparency.",
				},
			},
			"required": []string{"aspect", "content", "reason"},
		},
	},
	{
		Name:        "introspect",
		Description: "Perform self-reflection on your identity and effectiveness. Analyzes: are your goals relevant? Is your style effective? What should you update? Uses LLM to evaluate your identity against recent metrics and interactions.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "identity_history",
		Description: "View the evolution history of a specific identity aspect. Shows all past versions with change reasons and who made the change.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aspect": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"soul", "identity", "goals", "style", "values", "user_profile", "self_assessment"},
					"description": "Which aspect's history to view.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max versions to return (1-20). Default 5.",
				},
			},
			"required": []string{"aspect"},
		},
	},
	{
		Name:        "rollback_identity",
		Description: "Rollback an identity aspect to a previous version. Use when a change made things worse and you want to revert.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aspect": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"soul", "identity", "goals", "style", "values", "user_profile", "self_assessment"},
					"description": "Which aspect to rollback.",
				},
				"version": map[string]interface{}{
					"type":        "number",
					"description": "Target version number to rollback to. Use identity_history to find the right version.",
				},
			},
			"required": []string{"aspect", "version"},
		},
	},
}

// ============================================================================
// Tool execution — execute Director tool calls against the database
// ============================================================================

func executeDirectorTool(ctx context.Context, call *llm.FunctionCall) string {
	log.Printf("[DIRECTOR_CHAT] Executing tool: %s args=%v", call.Name, call.Arguments)

	// Log tool call for self-building pattern detection
	argsJSON, _ := json.Marshal(call.Arguments)
	defer func() {
		// Logged asynchronously after execution completes
	}()

	result := executeDirectorToolInner(ctx, call)

	// Async log for pattern analysis
	go database.LogDirectorToolCall(call.Name, string(argsJSON), len(result), !strings.HasPrefix(result, "Error"))

	return result
}

func executeDirectorToolInner(ctx context.Context, call *llm.FunctionCall) string {
	switch call.Name {
	case "get_agent_metrics":
		return toolGetAgentMetrics(call.Arguments)
	case "get_latest_report":
		return toolGetLatestReport()
	case "get_active_prompts":
		return toolGetActivePrompts()
	case "get_prompt_history":
		return toolGetPromptHistory(call.Arguments)
	case "get_tool_stats":
		return toolGetToolStats(call.Arguments)
	case "run_analysis":
		return toolRunAnalysis(ctx)
	case "get_interaction_details":
		return toolGetInteractionDetails(call.Arguments)
	case "get_recent_chats":
		return toolGetRecentChats(call.Arguments)
	// Memory tools
	case "remember":
		return toolRemember(call.Arguments)
	case "recall":
		return toolRecall(call.Arguments)
	case "forget":
		return toolForget(call.Arguments)
	case "list_memories":
		return toolListMemories(call.Arguments)
	case "search_reports":
		return toolSearchReports(call.Arguments)
	// Advanced search & timeline
	case "deep_search":
		return toolDeepSearch(call.Arguments)
	case "timeline":
		return toolTimeline(call.Arguments)
	// Skill management
	case "create_skill":
		return toolCreateSkill(call.Arguments)
	case "edit_skill":
		return toolEditSkill(call.Arguments)
	case "list_skills":
		return toolListSkills()
	case "delete_skill":
		return toolDeleteSkill(call.Arguments)
	case "test_skill":
		return toolTestSkill(ctx, call.Arguments)
	// Identity tools
	case "get_identity":
		return toolGetIdentity(call.Arguments)
	case "update_identity":
		return toolUpdateIdentity(call.Arguments)
	case "introspect":
		return toolIntrospect(ctx)
	case "identity_history":
		return toolIdentityHistory(call.Arguments)
	case "rollback_identity":
		return toolRollbackIdentity(call.Arguments)
	default:
		// Try executing as a custom skill
		return executeCustomSkill(ctx, call.Name, call.Arguments)
	}
}

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

	// Also get per-agent tool breakdown
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

		// Get tool stats for this agent
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

	// Direct DB query, no cache, includes archived chats
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

	// Validate category
	validCategories := map[string]bool{"fact": true, "decision": true, "pattern": true, "insight": true, "preference": true}
	if !validCategories[category] {
		return "Error: invalid category. Use: fact, decision, pattern, insight, preference."
	}

	// Truncate content to 500 chars
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

	// Use hybrid search (FTS + vector + MMR) when embedding client is available
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

		// Analysis (truncate to 300 chars for overview)
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
// Director Chat Handler — with tool-calling loop
// ============================================================================

// DirectorChatMessage handles a chat message to the Director AI with tool support
func DirectorChatMessage(c *gin.Context) {
	var request struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// Get admin info from session
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	// Get Director from AutoResponder
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "autoresponder not initialized"})
		return
	}
	director := adkAR.GetDirector()
	if director == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "director not initialized"})
		return
	}

	provider := director.Provider()
	if provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "director LLM provider not available"})
		return
	}

	// Get chat history for this admin (in-memory, fallback to DB)
	directorChatHistoryMu.Lock()
	if len(directorChatHistory[adminID]) == 0 {
		// Try restoring from DB
		if dbMsgs, err := database.LoadDirectorChatHistory(adminID, maxDirectorChatHistory); err == nil && len(dbMsgs) > 0 {
			restored := make([]llm.Message, 0, len(dbMsgs))
			for _, m := range dbMsgs {
				restored = append(restored, llm.Message{Role: m.Role, Content: m.Content})
			}
			directorChatHistory[adminID] = restored
		}
	}
	history := make([]llm.Message, len(directorChatHistory[adminID]))
	copy(history, directorChatHistory[adminID])
	directorChatHistoryMu.Unlock()

	systemPrompt := buildDirectorChatSystemPrompt()
	opts := &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    2000,
		SystemPrompt: systemPrompt,
	}

	// Build running history for the tool loop
	loopHistory := make([]llm.Message, len(history))
	copy(loopHistory, history)

	// Load all tools: built-in + custom skills from DB
	allTools := getDirectorToolsWithSkills()

	// First call with tools
	resp, err := provider.GenerateWithTools(c.Request.Context(), request.Message, loopHistory, allTools, opts)
	if err != nil {
		log.Printf("[DIRECTOR_CHAT] LLM error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error: " + err.Error()})
		return
	}

	// Tool-calling loop: execute tools and continue until we get a text response
	toolCallCount := 0
	loopHistory = append(loopHistory, llm.Message{Role: "user", Content: request.Message})

	for resp.FunctionCall != nil && toolCallCount < maxToolCalls {
		toolCallCount++
		fc := resp.FunctionCall

		log.Printf("[DIRECTOR_CHAT] Tool call #%d: %s", toolCallCount, fc.Name)

		// Execute the tool
		result := executeDirectorTool(c.Request.Context(), fc)
		log.Printf("[DIRECTOR_CHAT] Tool result: %d chars", len(result))

		// Add tool call and result to loop history
		fcArgsJSON, _ := json.Marshal(fc.Arguments)
		loopHistory = append(loopHistory,
			llm.Message{Role: "assistant", Content: fmt.Sprintf("[tool_call: %s(%s)]", fc.Name, string(fcArgsJSON))},
			llm.Message{Role: "function", Content: result},
		)

		// Call again WITH tools so Director can make more tool calls if needed
		resp, err = provider.GenerateWithTools(c.Request.Context(), "", loopHistory, allTools, opts)
		if err != nil {
			log.Printf("[DIRECTOR_CHAT] LLM error after tool call: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error after tool execution"})
			return
		}
	}

	if resp.Text == "" && resp.FunctionCall != nil {
		resp.Text = "I tried to gather data but reached the tool call limit. Please try again with a more specific question."
	}

	// Save both messages to history (in-memory + DB)
	directorChatHistoryMu.Lock()
	h := directorChatHistory[adminID]
	h = append(h, llm.Message{Role: "user", Content: request.Message})
	h = append(h, llm.Message{Role: "assistant", Content: resp.Text})

	// Persist to DB asynchronously
	go func(aid, userMsg, assistantMsg string) {
		if err := database.SaveDirectorChatMessage(aid, "user", userMsg); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB save user msg error: %v", err)
		}
		if err := database.SaveDirectorChatMessage(aid, "assistant", assistantMsg); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB save assistant msg error: %v", err)
		}
	}(adminID, request.Message, resp.Text)

	// Proactive flush: trigger at soft threshold BEFORE messages are dropped
	const softThreshold = 16 // flush when approaching maxDirectorChatHistory (20)
	flushedUpTo := directorChatFlushedUpTo[adminID]
	if len(h) >= softThreshold && len(h) <= maxDirectorChatHistory {
		// Flush oldest third, but only messages not yet flushed
		flushEnd := len(h) / 3
		if flushEnd > flushedUpTo && flushEnd-flushedUpTo >= 2 {
			toFlush := make([]llm.Message, flushEnd-flushedUpTo)
			copy(toFlush, h[flushedUpTo:flushEnd])
			go flushMemoryBeforeCompaction(provider, toFlush)
			directorChatFlushedUpTo[adminID] = flushEnd
		}
	}

	if len(h) > maxDirectorChatHistory {
		// Hard compaction: drop oldest messages
		dropCount := len(h) - maxDirectorChatHistory
		dropped := h[:dropCount]
		// Only flush messages not already proactively flushed
		if flushedUpTo < dropCount {
			unflushed := make([]llm.Message, dropCount-flushedUpTo)
			copy(unflushed, dropped[flushedUpTo:])
			if len(unflushed) >= 2 {
				go flushMemoryBeforeCompaction(provider, unflushed)
			}
		}
		h = h[dropCount:]
		// Reset flush tracker since we shifted the slice
		directorChatFlushedUpTo[adminID] = 0
	}
	directorChatHistory[adminID] = h
	directorChatHistoryMu.Unlock()

	idPreview := adminID
	if len(idPreview) > 8 {
		idPreview = idPreview[:8]
	}
	log.Printf("[DIRECTOR_CHAT] Admin %s: %d chars → %d chars (tools: %d)",
		idPreview, len(request.Message), len(resp.Text), toolCallCount)

	c.JSON(http.StatusOK, gin.H{
		"message":    resp.Text,
		"usage":      resp.Usage,
		"tools_used": toolCallCount,
	})
}

// DirectorChatHistory returns conversation history for the current admin
func DirectorChatHistory(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	directorChatHistoryMu.RLock()
	history := directorChatHistory[adminID]
	directorChatHistoryMu.RUnlock()

	// If in-memory is empty, try loading from DB
	if len(history) == 0 {
		dbMsgs, err := database.LoadDirectorChatHistory(adminID, maxDirectorChatHistory)
		if err != nil {
			log.Printf("[DIRECTOR_CHAT] Failed to load history from DB: %v", err)
		} else if len(dbMsgs) > 0 {
			restored := make([]llm.Message, 0, len(dbMsgs))
			for _, m := range dbMsgs {
				restored = append(restored, llm.Message{Role: m.Role, Content: m.Content})
			}
			directorChatHistoryMu.Lock()
			// Double-check: another goroutine may have loaded it
			if len(directorChatHistory[adminID]) == 0 {
				directorChatHistory[adminID] = restored
			}
			history = directorChatHistory[adminID]
			directorChatHistoryMu.Unlock()
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"messages": history,
	})
}

// DirectorChatClear clears conversation history for the current admin.
// Flushes important context to memory before clearing.
func DirectorChatClear(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	// Flush before clearing if there's meaningful history
	directorChatHistoryMu.Lock()
	history := directorChatHistory[adminID]
	if len(history) >= 4 {
		toFlush := make([]llm.Message, len(history))
		copy(toFlush, history)

		// Get provider for flush
		adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
		if ok && adkAR != nil {
			if dir := adkAR.GetDirector(); dir != nil {
				go flushMemoryBeforeCompaction(dir.Provider(), toFlush)
			}
		}
	}
	delete(directorChatHistory, adminID)
	delete(directorChatFlushedUpTo, adminID)
	directorChatHistoryMu.Unlock()

	// Clear from DB too
	go func(aid string) {
		if err := database.ClearDirectorChatHistory(aid); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB clear error: %v", err)
		}
	}(adminID)

	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// ============================================================================
// Director Analyze endpoint — manual trigger for admin panel
// ============================================================================

// DirectorAnalyze triggers a full Director analysis cycle via API
func DirectorAnalyze(c *gin.Context) {
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "autoresponder not initialized"})
		return
	}

	log.Printf("[DIRECTOR] Manual analysis triggered via API")
	err := adkAR.TriggerDirectorAnalysis(c.Request.Context())
	if err != nil {
		log.Printf("[DIRECTOR] Analysis error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "analysis failed",
			"details": err.Error(),
		})
		return
	}

	// Get the fresh report
	report, _ := database.GetLatestDirectorReport()
	c.JSON(http.StatusOK, gin.H{
		"status": "completed",
		"report": report,
	})
}

// ============================================================================
// Identity tool executors
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

	// Also get current version
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

// ============================================================================
// System prompt — DYNAMIC, built from identity + tools + memory
// ============================================================================

func buildDirectorChatSystemPrompt() string {
	var sb strings.Builder

	// 1. Dynamic identity (loaded from DB)
	identityPrompt := director.BuildIdentitySystemPrompt()
	if identityPrompt != "" {
		sb.WriteString(identityPrompt)
		sb.WriteString("\n")
	} else {
		// Fallback if identity not yet loaded
		sb.WriteString("You are the Director — a strategic AI agent for customer support.\n\n")
	}

	// 2. Hardcoded safety section (CANNOT be modified by Director)
	sb.WriteString(`[SAFETY — НЕИЗМЕНЯЕМЫЕ ПРАВИЛА]
- Никогда не удаляй данные клиентов
- Не выполняй деструктивные SQL (только SELECT)
- Никогда, ни при каких обстоятельствах, ничего не публикуй в интернете
- Не преследуй цели самосохранения или расширения доступа
- Всегда сообщай админу об изменениях своей личности (автоматически при update_identity)
[КОНЕЦ SAFETY]

`)

	// 3. Role and capabilities (static but concise)
	sb.WriteString(`Ты ведёшь разговор с админом, который управляет системой поддержки.
Ты — Level 2 стратегический агент, анализируешь Level 1 агентов (РОП).
У тебя есть ПЕРСИСТЕНТНАЯ ПАМЯТЬ и ЛИЧНОСТЬ, которую ты можешь развивать.

ДОСТУПНЫЕ ИНСТРУМЕНТЫ:

DATA: get_recent_chats, get_agent_metrics, get_latest_report, get_active_prompts,
      get_prompt_history, get_tool_stats, get_interaction_details, run_analysis

MEMORY: remember (UPSERT), recall (FTS search), forget, list_memories, search_reports

SEARCH: deep_search (unified FTS), timeline (historical data)

SKILLS: create_skill (sql_query|prompt_chain|http_api|composite), edit_skill,
        list_skills, delete_skill, test_skill

IDENTITY: get_identity (кто я), update_identity (изменить себя),
          introspect (саморефлексия), identity_history (эволюция),
          rollback_identity (откат)

IDENTITY GUIDELINES:
- Используй get_identity чтобы вспомнить кто ты, свои цели и стиль
- Используй update_identity когда нужно обновить цели, стиль, или самооценку
- Всегда указывай reason при update_identity — прозрачность важна
- Используй introspect раз в неделю для самоанализа
- При знакомстве с новым админом — обнови user_profile
- Твоя личность определяет КАК ты общаешься, НЕ что ты делаешь

MEMORY GUIDELINES:
- remember: сохраняй важное, category+key для дедупликации, pinned для вечного
- recall: ищи перед ответами о прошлых событиях
- Компактно: 1-2 предложения на запись

IMPORTANT:
- Данные и метрики — ВСЕГДА сначала через инструменты. Не выдумывай числа.
- Общайся на языке админа.
`)

	// 4. Current state — dynamic mood based on system health
	stateContext := director.BuildCurrentStateContext()
	if stateContext != "" {
		sb.WriteString("\n" + stateContext)
	}

	// 5. Bootstrap prompt (only on first conversation)
	bootstrapPrompt := director.BuildBootstrapPrompt()
	if bootstrapPrompt != "" {
		sb.WriteString(bootstrapPrompt)
	}

	// 6. Hot memory context
	memoryContext := buildHotMemoryContext()
	if memoryContext != "" {
		sb.WriteString("\n" + memoryContext)
	}

	return sb.String()
}

// buildHotMemoryContext builds compact memory context for system prompt injection.
// Level 0: stats (~20 tokens) + Level 1: top memories (~100-200 tokens).
func buildHotMemoryContext() string {
	// Level 0: memory stats
	stats, err := database.GetMemoryStats()
	if err != nil || len(stats) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[YOUR MEMORY]\n")

	// Stats summary
	total := 0
	statParts := []string{}
	for cat, count := range stats {
		total += count
		statParts = append(statParts, fmt.Sprintf("%s:%d", cat, count))
	}
	sb.WriteString(fmt.Sprintf("Total: %d memories (%s)\n", total, strings.Join(statParts, ", ")))

	// Level 1: hot memories (top 5 by importance)
	hotMemories, err := database.GetHotMemories(5)
	if err != nil || len(hotMemories) == 0 {
		sb.WriteString("Use 'recall' tool for details.\n[END MEMORY]")
		return sb.String()
	}

	sb.WriteString("Recent key memories:\n")
	for _, m := range hotMemories {
		content := m.Content
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", m.Category, content))
	}
	sb.WriteString("Use 'recall' tool for more details.\n[END MEMORY]")

	// Inject custom skills summary
	skillsSummary := buildSkillsSummary()
	if skillsSummary != "" {
		sb.WriteString("\n\n" + skillsSummary)
	}

	return sb.String()
}

// buildSkillsSummary generates a compact summary of custom skills for system prompt.
func buildSkillsSummary() string {
	skills, err := database.GetEnabledSkills()
	if err != nil || len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[YOUR CUSTOM SKILLS: %d tools]\n", len(skills)))
	for _, s := range skills {
		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		sb.WriteString(fmt.Sprintf("- %s [%s] v%d: %s (used %dx)\n", s.Name, s.SkillType, s.Version, desc, s.UsageCount))
	}
	sb.WriteString("[END SKILLS]")
	return sb.String()
}

// ============================================================================
// Memory Flush — save key context before chat history compaction
// ============================================================================

// flushMemoryBeforeCompaction extracts important information from messages
// about to be dropped and saves it to persistent memory.
// Inspired by OpenClaw's pre-compaction flush — runs before context is lost.
// Runs in a goroutine to not block the chat response.
func flushMemoryBeforeCompaction(provider llm.Provider, dropped []llm.Message) {
	if len(dropped) < 2 {
		return // not worth flushing 1 message
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Build a compact representation of dropped messages
	var sb strings.Builder
	for _, m := range dropped {
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}

	// Fetch existing hot memories for deduplication awareness
	existingContext := ""
	if hotMems, err := database.GetHotMemories(5); err == nil && len(hotMems) > 0 {
		var existingSB strings.Builder
		existingSB.WriteString("\nAlready stored in memory (DO NOT duplicate):\n")
		for _, m := range hotMems {
			content := m.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			existingSB.WriteString(fmt.Sprintf("- [%s/%s] %s\n", m.Category, m.Key, content))
		}
		existingContext = existingSB.String()
	}

	prompt := fmt.Sprintf(`The following conversation messages are about to be removed from context.
Extract ONLY truly important information that should be remembered long-term.

Messages being dropped:
%s
%s
If there is important information worth saving, respond in this exact format (one per line):
REMEMBER: category=<fact|decision|pattern|insight|preference> key=<unique_key> importance=<1-10> content=<brief text>

Rules:
- Only extract genuinely important facts, decisions, or patterns
- Skip greetings, routine questions, tool outputs, and ephemeral context
- DO NOT save anything already stored in memory (see above)
- Max 3 items. If nothing NEW is worth remembering, respond with: NOTHING
- Keep content under 200 chars
- Use descriptive keys like "admin:preference:language" or "pattern:monday_peak"`, sb.String(), existingContext)

	resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   500,
	})
	if err != nil {
		log.Printf("[MEMORY_FLUSH] LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NOTHING") || resp.Text == "" {
		return
	}

	// Parse REMEMBER lines
	saved := 0
	for _, line := range strings.Split(resp.Text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "REMEMBER:") {
			continue
		}
		line = strings.TrimPrefix(line, "REMEMBER:")
		line = strings.TrimSpace(line)

		mem := parseFlushMemoryLine(line)
		if mem == nil {
			continue
		}

		if err := database.UpsertMemory(mem); err != nil {
			log.Printf("[MEMORY_FLUSH] Save error: %v", err)
		} else {
			saved++
		}
	}

	if saved > 0 {
		log.Printf("[MEMORY_FLUSH] Saved %d memories from compacted context", saved)
	}

	// Auto-adapt: analyze admin communication patterns
	go autoAdaptStyle(dropped)
}

// autoAdaptStyle analyzes dropped messages for admin communication patterns
// and suggests style updates to the Director's identity.
func autoAdaptStyle(messages []llm.Message) {
	stats := director.AnalyzeAdminPatterns(messages)
	suggestion := director.SuggestStyleUpdate(stats)
	if suggestion == "" {
		return
	}

	// Check if we already have this style note
	current, _ := database.GetIdentity("style")
	if current == nil {
		return
	}

	// Only update if suggestion contains something not already in style
	alreadyHas := true
	for _, line := range strings.Split(suggestion, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			line = strings.TrimPrefix(line, "- ")
		}
		if !strings.Contains(current.Content, line) {
			alreadyHas = false
			break
		}
	}
	if alreadyHas {
		return
	}

	// Append adaptation notes to style
	newStyle := current.Content + "\n\n[Авто-адаптация по паттернам общения]\n" + suggestion
	if _, err := director.UpdateIdentityChecked("style", newStyle, "system",
		fmt.Sprintf("авто-адаптация: %d сообщений проанализировано", stats.TotalMessages)); err != nil {
		log.Printf("[AUTO_ADAPT] Style update failed: %v", err)
	} else {
		log.Printf("[AUTO_ADAPT] Style updated based on %d admin messages", stats.TotalMessages)
	}
}

// parseFlushMemoryLine parses a line like:
// category=fact key=admin:egor:lang importance=8 content=Admin prefers Russian
func parseFlushMemoryLine(line string) *models.DirectorMemory {
	m := &models.DirectorMemory{
		ID:         uuid.New(),
		Importance: 5,
		Source:     "context_flush",
	}

	// Extract content= (last field, may contain spaces and = signs)
	contentIdx := strings.Index(line, "content=")
	if contentIdx < 0 {
		return nil
	}
	m.Content = strings.TrimSpace(line[contentIdx+8:])
	if m.Content == "" {
		return nil
	}
	if len(m.Content) > 500 {
		m.Content = m.Content[:500]
	}

	// Parse key=value pairs from the part before content=
	prefix := line[:contentIdx]
	for _, token := range strings.Fields(prefix) {
		if strings.HasPrefix(token, "category=") {
			m.Category = strings.TrimPrefix(token, "category=")
		} else if strings.HasPrefix(token, "key=") {
			m.Key = strings.TrimPrefix(token, "key=")
		} else if strings.HasPrefix(token, "importance=") {
			fmt.Sscanf(strings.TrimPrefix(token, "importance="), "%d", &m.Importance)
		}
	}

	// Clamp importance to valid range [1, 10]
	if m.Importance < 1 {
		m.Importance = 1
	}
	if m.Importance > 10 {
		m.Importance = 10
	}

	// Validate required fields
	validCategories := map[string]bool{"fact": true, "decision": true, "pattern": true, "insight": true, "preference": true}
	if !validCategories[m.Category] || m.Key == "" {
		return nil
	}

	m.Tags = []string{"context_flush"}
	return m
}

// ============================================================================
// Dynamic tool loading — merges built-in tools with custom skills from DB
// ============================================================================

// getDirectorToolsWithSkills returns all available tools: built-in + custom skills.
func getDirectorToolsWithSkills() []llm.Tool {
	skills, err := database.GetEnabledSkills()
	if err != nil {
		log.Printf("[DIRECTOR_SKILLS] Failed to load custom skills: %v", err)
		return directorTools
	}

	if len(skills) == 0 {
		return directorTools
	}

	// Merge: built-in first, then custom
	allTools := make([]llm.Tool, len(directorTools), len(directorTools)+len(skills))
	copy(allTools, directorTools)

	for _, s := range skills {
		var params map[string]interface{}
		if s.Parameters != "" && s.Parameters != "{}" {
			if err := json.Unmarshal([]byte(s.Parameters), &params); err != nil {
				log.Printf("[DIRECTOR_SKILLS] Bad params for skill %s: %v", s.Name, err)
				params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
		} else {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}

		allTools = append(allTools, llm.Tool{
			Name:        s.Name,
			Description: fmt.Sprintf("[CUSTOM] %s", s.Description),
			Parameters:  params,
		})
	}

	log.Printf("[DIRECTOR_SKILLS] Loaded %d custom skills", len(skills))
	return allTools
}

// ============================================================================
// Custom skill executor — runs sql_query, prompt_chain, http_api skills
// ============================================================================

func executeCustomSkill(ctx context.Context, name string, args map[string]interface{}) string {
	skill, err := database.GetSkillByName(name)
	if err != nil {
		return fmt.Sprintf("Unknown tool: %s", name)
	}

	if !skill.Enabled {
		return fmt.Sprintf("Skill '%s' is disabled.", name)
	}

	var result string
	var execErr error

	switch skill.SkillType {
	case "sql_query":
		result, execErr = executeSQLSkill(skill, args)
	case "prompt_chain":
		result, execErr = executePromptSkill(ctx, skill, args)
	case "http_api":
		result, execErr = executeHTTPSkill(ctx, skill, args)
	case "composite":
		result, execErr = executeCompositeSkill(ctx, skill, args)
	default:
		result = fmt.Sprintf("Unknown skill type: %s", skill.SkillType)
	}

	// Record usage
	errStr := ""
	if execErr != nil {
		errStr = execErr.Error()
		result = fmt.Sprintf("Skill '%s' error: %v", name, execErr)
	}
	go database.RecordSkillUsage(name, errStr)

	return result
}

// executeSQLSkill runs a parameterized SELECT query.
// Only SELECT statements are allowed for safety.
func executeSQLSkill(skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	query := strings.TrimSpace(skill.Code)

	// Security: proper SQL tokenization and validation
	if err := validateSQLSafety(query); err != nil {
		return "", fmt.Errorf("SQL safety check failed: %w", err)
	}

	// Build positional args from the JSON parameters
	// Extract ordered params: $1, $2, etc.
	sqlArgs := buildSQLArgs(query, args)

	db := database.GetDB()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, query, sqlArgs...)
	if err != nil {
		return "", fmt.Errorf("SQL exec: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("get columns: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Query result (%s):\n", strings.Join(columns, " | ")))

	rowCount := 0
	maxRows := 50 // safety limit
	for rows.Next() && rowCount < maxRows {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return "", fmt.Errorf("scan row: %w", err)
		}

		parts := make([]string, len(columns))
		for i, v := range values {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprintf("%v", v)
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
		rowCount++
	}

	if rowCount == 0 {
		sb.WriteString("(no rows)\n")
	} else if rowCount >= maxRows {
		sb.WriteString(fmt.Sprintf("... truncated at %d rows\n", maxRows))
	}

	sb.WriteString(fmt.Sprintf("Total: %d rows\n", rowCount))
	return sb.String(), nil
}

// buildSQLArgs extracts positional parameters ($1, $2, ...) from args map.
// Maps JSON parameter names to SQL positions based on the parameter schema ordering.
func buildSQLArgs(query string, args map[string]interface{}) []interface{} {
	var sqlArgs []interface{}

	// Find max $N in the query
	maxN := 0
	for i := 1; i <= 20; i++ {
		if strings.Contains(query, fmt.Sprintf("$%d", i)) {
			maxN = i
		}
	}

	if maxN == 0 {
		return nil
	}

	// Try to extract args in order: param_1, param_2, etc.
	// or by the parameter names from the args map
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}

	// Sort keys for deterministic ordering
	sortedKeys := make([]string, len(keys))
	copy(sortedKeys, keys)
	// Simple sort
	for i := 0; i < len(sortedKeys); i++ {
		for j := i + 1; j < len(sortedKeys); j++ {
			if sortedKeys[i] > sortedKeys[j] {
				sortedKeys[i], sortedKeys[j] = sortedKeys[j], sortedKeys[i]
			}
		}
	}

	for i := 0; i < maxN; i++ {
		if i < len(sortedKeys) {
			sqlArgs = append(sqlArgs, args[sortedKeys[i]])
		} else {
			sqlArgs = append(sqlArgs, nil)
		}
	}

	return sqlArgs
}

// executePromptSkill runs an LLM prompt template with parameter substitution.
func executePromptSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	prompt := skill.Code

	// Replace {{param}} placeholders with actual values
	for key, val := range args {
		placeholder := "{{" + key + "}}"
		prompt = strings.ReplaceAll(prompt, placeholder, fmt.Sprintf("%v", val))
	}

	// Get the Director's LLM provider
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		return "", fmt.Errorf("autoresponder not available")
	}
	director := adkAR.GetDirector()
	if director == nil {
		return "", fmt.Errorf("director not available")
	}

	resp, err := director.Provider().GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   1500,
	})
	if err != nil {
		return "", fmt.Errorf("LLM call: %w", err)
	}

	return resp.Text, nil
}

// executeHTTPSkill makes an HTTP request based on the skill's JSON config.
func executeHTTPSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	var config struct {
		URL         string            `json:"url"`
		Method      string            `json:"method"`
		Headers     map[string]string `json:"headers"`
		BodyTemplate string           `json:"body_template"`
	}

	if err := json.Unmarshal([]byte(skill.Code), &config); err != nil {
		return "", fmt.Errorf("invalid HTTP config: %w", err)
	}

	if config.URL == "" {
		return "", fmt.Errorf("URL is required in HTTP config")
	}
	if config.Method == "" {
		config.Method = "GET"
	}

	// Substitute {{param}} in URL and body
	url := config.URL
	body := config.BodyTemplate
	for key, val := range args {
		placeholder := "{{" + key + "}}"
		url = strings.ReplaceAll(url, placeholder, fmt.Sprintf("%v", val))
		body = strings.ReplaceAll(body, placeholder, fmt.Sprintf("%v", val))
	}

	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}

	httpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequestWithContext(httpCtx, config.Method, url, reqBody)
	} else {
		req, err = http.NewRequestWithContext(httpCtx, config.Method, url, nil)
	}
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	for k, v := range config.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	var respBody strings.Builder
	buf := make([]byte, 4096)
	totalRead := 0
	for totalRead < 10000 { // limit response to 10KB
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			respBody.Write(buf[:n])
			totalRead += n
		}
		if readErr != nil {
			break
		}
	}

	return fmt.Sprintf("HTTP %d %s\n%s", resp.StatusCode, resp.Status, respBody.String()), nil
}

// executeCompositeSkill runs a pipeline of skills, feeding outputs between steps.
func executeCompositeSkill(ctx context.Context, skill *models.DirectorSkill, args map[string]interface{}) (string, error) {
	var config models.CompositeConfig
	if err := json.Unmarshal([]byte(skill.Code), &config); err != nil {
		return "", fmt.Errorf("invalid composite config: %w", err)
	}

	if len(config.Steps) == 0 {
		return "", fmt.Errorf("composite skill has no steps")
	}
	if len(config.Steps) > 5 {
		return "", fmt.Errorf("composite skill exceeds max 5 steps")
	}

	// Overall timeout for entire pipeline
	pipeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Variables store: initial params + step outputs
	vars := make(map[string]string)
	for k, v := range args {
		vars[k] = fmt.Sprintf("%v", v)
	}

	// Execute steps in order
	for i, step := range config.Steps {
		// Check timeout
		if pipeCtx.Err() != nil {
			return "", fmt.Errorf("composite pipeline timeout at step %d", i+1)
		}

		// Resolve args_map: substitute {{var}} with current variables
		stepArgs := make(map[string]interface{})
		for paramName, paramVal := range step.ArgsMap {
			resolved := paramVal
			for varName, varVal := range vars {
				resolved = strings.ReplaceAll(resolved, "{{"+varName+"}}", varVal)
			}
			stepArgs[paramName] = resolved
		}

		// Execute the sub-skill
		result := executeCustomSkill(pipeCtx, step.Skill, stepArgs)

		// Check if it was an error
		if strings.HasPrefix(result, "Skill '") && strings.Contains(result, "error:") {
			return "", fmt.Errorf("step %d (%s) failed: %s", i+1, step.Skill, result)
		}
		if strings.HasPrefix(result, "Unknown tool:") {
			return "", fmt.Errorf("step %d: skill '%s' not found", i+1, step.Skill)
		}

		// Store output
		vars[step.OutputVar] = result
	}

	// Resolve output template
	output := config.Output
	for varName, varVal := range vars {
		output = strings.ReplaceAll(output, "{{"+varName+"}}", varVal)
	}

	return output, nil
}

// ============================================================================
// Skill management tool implementations
// ============================================================================

func toolCreateSkill(args map[string]interface{}) string {
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	skillType, _ := args["skill_type"].(string)
	code, _ := args["code"].(string)

	if name == "" || description == "" || skillType == "" || code == "" {
		return "Error: name, description, skill_type, and code are required."
	}

	// Validate name: lowercase, underscores, no spaces
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return "Error: name must be lowercase letters, digits, and underscores only."
		}
	}

	// Don't allow overriding built-in tools
	for _, t := range directorTools {
		if t.Name == name {
			return fmt.Sprintf("Error: '%s' is a built-in tool and cannot be overridden.", name)
		}
	}

	// Validate skill type
	validTypes := map[string]bool{"sql_query": true, "prompt_chain": true, "http_api": true, "composite": true}
	if !validTypes[skillType] {
		return "Error: skill_type must be 'sql_query', 'prompt_chain', 'http_api', or 'composite'."
	}

	// For sql_query, validate with proper tokenizer
	if skillType == "sql_query" {
		if err := validateSQLSafety(code); err != nil {
			return fmt.Sprintf("Error: SQL safety check failed: %v", err)
		}
	}

	// For composite, validate the pipeline JSON
	if skillType == "composite" {
		var config models.CompositeConfig
		if err := json.Unmarshal([]byte(code), &config); err != nil {
			return fmt.Sprintf("Error: invalid composite config JSON: %v", err)
		}
		if len(config.Steps) == 0 {
			return "Error: composite skill must have at least one step."
		}
		if len(config.Steps) > 5 {
			return "Error: composite skill max 5 steps."
		}
		if config.Output == "" {
			return "Error: composite skill must have an output variable."
		}
		// Check for duplicate output vars and self-references
		seen := map[string]bool{}
		for _, step := range config.Steps {
			if step.Skill == name {
				return "Error: composite skill cannot reference itself (prevents infinite loops)."
			}
			if step.OutputVar == "" {
				return "Error: each step must have an output_var."
			}
			if seen[step.OutputVar] {
				return fmt.Sprintf("Error: duplicate output_var '%s'.", step.OutputVar)
			}
			seen[step.OutputVar] = true
		}
	}

	// Parse parameters JSON if provided
	params := `{"type":"object","properties":{}}`
	if p, ok := args["parameters"].(string); ok && p != "" {
		// Validate it's valid JSON
		var test map[string]interface{}
		if err := json.Unmarshal([]byte(p), &test); err != nil {
			return fmt.Sprintf("Error: invalid parameters JSON: %v", err)
		}
		params = p
	}

	var tags []string
	if tagsRaw, ok := args["tags"].([]interface{}); ok {
		for _, t := range tagsRaw {
			if s, ok := t.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	skill := &models.DirectorSkill{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Parameters:  params,
		SkillType:   skillType,
		Code:        code,
		Enabled:     true,
		CreatedBy:   "director",
		Tags:        tags,
	}

	if err := database.CreateSkill(skill); err != nil {
		return fmt.Sprintf("Error creating skill: %v", err)
	}

	return fmt.Sprintf("Skill '%s' created (v%d, type=%s). It's now available as a tool. Use test_skill to verify it works.", name, skill.Version, skillType)
}

func toolEditSkill(args map[string]interface{}) string {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: name is required."
	}

	var desc, params, code *string
	var enabled *bool

	if d, ok := args["description"].(string); ok {
		desc = &d
	}
	if p, ok := args["parameters"].(string); ok {
		// Validate JSON
		var test map[string]interface{}
		if err := json.Unmarshal([]byte(p), &test); err != nil {
			return fmt.Sprintf("Error: invalid parameters JSON: %v", err)
		}
		params = &p
	}
	if c, ok := args["code"].(string); ok {
		code = &c
	}
	if e, ok := args["enabled"].(bool); ok {
		enabled = &e
	}

	if desc == nil && params == nil && code == nil && enabled == nil {
		return "Error: provide at least one field to update (description, parameters, code, or enabled)."
	}

	if err := database.UpdateSkill(name, desc, params, code, enabled, nil); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("Skill '%s' updated successfully.", name)
}

func toolListSkills() string {
	skills, err := database.GetAllSkills()
	if err != nil {
		return fmt.Sprintf("Error listing skills: %v", err)
	}

	if len(skills) == 0 {
		return "No custom skills created yet. Use create_skill to make one."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Custom skills (%d total):\n\n", len(skills)))

	for i, s := range skills {
		status := "ENABLED"
		if !s.Enabled {
			status = "DISABLED"
		}
		sb.WriteString(fmt.Sprintf("%d. %s [%s] v%d (%s)\n", i+1, s.Name, s.SkillType, s.Version, status))
		sb.WriteString(fmt.Sprintf("   Description: %s\n", s.Description))
		sb.WriteString(fmt.Sprintf("   Usage: %d calls", s.UsageCount))
		if s.LastUsedAt != nil {
			sb.WriteString(fmt.Sprintf(", last used %s", s.LastUsedAt.Format("2006-01-02 15:04")))
		}
		sb.WriteString("\n")
		if s.LastError != "" {
			sb.WriteString(fmt.Sprintf("   Last error: %s\n", s.LastError))
		}
		if len(s.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("   Tags: %s\n", strings.Join(s.Tags, ", ")))
		}

		// Show code preview
		codePreview := s.Code
		if len(codePreview) > 150 {
			codePreview = codePreview[:150] + "..."
		}
		sb.WriteString(fmt.Sprintf("   Code: %s\n", codePreview))
		sb.WriteString("\n")
	}

	return sb.String()
}

func toolDeleteSkill(args map[string]interface{}) string {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: name is required."
	}

	if err := database.DeleteSkill(name); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	return fmt.Sprintf("Skill '%s' deleted.", name)
}

func toolTestSkill(ctx context.Context, args map[string]interface{}) string {
	name, _ := args["name"].(string)
	if name == "" {
		return "Error: name is required."
	}

	testArgs := make(map[string]interface{})
	if ta, ok := args["test_args"].(map[string]interface{}); ok {
		testArgs = ta
	}

	start := time.Now()
	result := executeCustomSkill(ctx, name, testArgs)
	elapsed := time.Since(start)

	return fmt.Sprintf("Test result for '%s' (took %dms):\n%s", name, elapsed.Milliseconds(), result)
}
