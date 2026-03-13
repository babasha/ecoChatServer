package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// Tool result truncation
// ============================================================================

const softTrimHead = 500
const softTrimTail = 200
const softTrimThreshold = softTrimHead + softTrimTail + 100 // ~800 chars

// softTrimToolResult keeps first ~500 + "..." + last ~200 chars when result is too long.
// Seeks newline boundaries for clean cuts.
func softTrimToolResult(s string) string {
	if len(s) <= softTrimThreshold {
		return s
	}
	// Find newline near head boundary for clean cut
	headEnd := softTrimHead
	if idx := strings.LastIndex(s[:softTrimHead+80], "\n"); idx > softTrimHead-100 {
		headEnd = idx + 1
	}
	// Find newline near tail boundary for clean cut
	tailStart := len(s) - softTrimTail
	if idx := strings.Index(s[tailStart-80:], "\n"); idx >= 0 {
		tailStart = tailStart - 80 + idx + 1
	}
	trimmed := len(s) - headEnd - (len(s) - tailStart)
	return s[:headEnd] + fmt.Sprintf("\n...[trimmed %d chars]...\n", trimmed) + s[tailStart:]
}

// ============================================================================
// Built-in tool definitions
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

	argsJSON, _ := json.Marshal(call.Arguments)
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
