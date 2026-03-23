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
// Built-in tool definitions — split into core (always loaded) + lazy categories
// ============================================================================

// directorTools contains ALL tools (used by data handler, skill conflict check, etc.)
var directorTools []llm.Tool

// coreDirectorTools — always loaded, ~8 tools
var coreDirectorTools []llm.Tool

// lazyToolCategories — loaded on demand via load_tools
var lazyToolCategories map[string]lazyCategory

type lazyCategory struct {
	Description string
	Tools       []llm.Tool
}

func init() {
	// Build categories
	lazyToolCategories = map[string]lazyCategory{
		"analytics": {
			Description: "метрики агентов, статистика инструментов, детали взаимодействий, запуск анализа",
			Tools:       analyticsTools,
		},
		"reports": {
			Description: "поиск по отчётам, таймлайн, сохранение ежедневного отчёта",
			Tools:       reportTools,
		},
		"identity": {
			Description: "личность: просмотр, изменение, интроспекция, история, откат",
			Tools:       identityTools,
		},
		"skills": {
			Description: "управление кастомными скиллами: создание, редактирование, удаление, тестирование",
			Tools:       skillTools,
		},
		"memory_extra": {
			Description: "расширенная память: удаление, список всех воспоминаний",
			Tools:       memoryExtraTools,
		},
		"prompts": {
			Description: "активные промпты агентов, история версий промптов",
			Tools:       promptTools,
		},
		"cron": {
			Description: "планировщик задач: создание, список, удаление, запуск крон-задач",
			Tools:       cronTools,
		},
		"agents": {
			Description: "отправка вопросов L1 агентам, список активных сессий",
			Tools:       agentCommTools,
		},
		"tasks": {
			Description: "управление задачами: создание, список, обновление, комментарии",
			Tools:       taskTools,
		},
		"webhooks": {
			Description: "события и статистика вебхуков от внешних систем",
			Tools:       webhookTools,
		},
		"browser": {
			Description: "скриншот, извлечение текста, выполнение JS на веб-страницах",
			Tools:       browserTools,
		},
	}

	// Build core tools list
	coreDirectorTools = append(coreDirectorTools, coreChatTools...)
	coreDirectorTools = append(coreDirectorTools, coreMemoryTools...)
	coreDirectorTools = append(coreDirectorTools, coreSearchTools...)
	coreDirectorTools = append(coreDirectorTools, loadToolsDef)

	// Build full list (for data handler, skill conflict checks)
	directorTools = make([]llm.Tool, len(coreDirectorTools))
	copy(directorTools, coreDirectorTools)
	for _, cat := range lazyToolCategories {
		directorTools = append(directorTools, cat.Tools...)
	}
}

// handleLoadTools processes load_tools call, returns message + new tools to add.
func handleLoadTools(args map[string]interface{}, loaded map[string]bool) (string, []llm.Tool) {
	raw, ok := args["categories"]
	if !ok {
		// List available categories
		var sb strings.Builder
		sb.WriteString("Available categories:\n")
		for name, cat := range lazyToolCategories {
			sb.WriteString(fmt.Sprintf("  %s — %s\n", name, cat.Description))
		}
		return sb.String(), nil
	}

	var categories []string
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				categories = append(categories, s)
			}
		}
	case string:
		categories = []string{v}
	}

	if len(categories) == 0 {
		return "Error: specify categories as array, e.g. [\"analytics\", \"identity\"].", nil
	}

	var newTools []llm.Tool
	var loadedNames, alreadyLoaded, unknown []string

	for _, cat := range categories {
		if loaded[cat] {
			alreadyLoaded = append(alreadyLoaded, cat)
			continue
		}
		tc, exists := lazyToolCategories[cat]
		if !exists {
			unknown = append(unknown, cat)
			continue
		}
		newTools = append(newTools, tc.Tools...)
		loaded[cat] = true
		loadedNames = append(loadedNames, fmt.Sprintf("%s (%d)", cat, len(tc.Tools)))
	}

	var sb strings.Builder
	if len(loadedNames) > 0 {
		sb.WriteString("Loaded: " + strings.Join(loadedNames, ", ") + ". These tools are now available.")
	}
	if len(alreadyLoaded) > 0 {
		sb.WriteString(" Already loaded: " + strings.Join(alreadyLoaded, ", ") + ".")
	}
	if len(unknown) > 0 {
		sb.WriteString(" Unknown: " + strings.Join(unknown, ", ") + ".")
		sb.WriteString(" Available: analytics, reports, identity, skills, memory_extra, prompts, cron, agents, tasks, webhooks, browser.")
	}
	return sb.String(), newTools
}

// ── load_tools meta-tool ─────────────────────────────────────────────
var loadToolsDef = llm.Tool{
	Name:        "load_tools",
	Description: "Load extra tool categories before using them: analytics, reports, identity, skills, memory_extra, prompts, cron, agents, tasks, webhooks, browser.",
	Parameters: map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"categories": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"categories"},
	},
}

// ── Core tools (always loaded) ───────────────────────────────────────

var coreChatTools = []llm.Tool{
	{
		Name:        "get_recent_chats",
		Description: "Recent chats with chat_id, user, last message, source, status. Supports name search.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit":  map[string]interface{}{"type": "number", "description": "1-50, default 10."},
				"search": map[string]interface{}{"type": "string", "description": "Filter by user name (ILIKE)."},
			},
		},
	},
	{
		Name:        "agent_context",
		Description: "Recent messages + summary for a chat (no LLM call).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"chat_id": map[string]interface{}{"type": "string"},
				"limit":   map[string]interface{}{"type": "number", "description": "Messages to return, default 20."},
			},
			"required": []string{"chat_id"},
		},
	},
	{
		Name:        "describe_schema",
		Description: "DB schema: tables, columns, types, relationships. Use before creating sql_query skills.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"table": map[string]interface{}{"type": "string", "description": "Specific table, or omit for overview."},
			},
		},
	},
}

var coreMemoryTools = []llm.Tool{
	{
		Name:        "remember",
		Description: "Save to long-term memory (UPSERT by category+key).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"category":         map[string]interface{}{"type": "string", "enum": []string{"fact", "decision", "pattern", "insight", "preference"}},
				"key":              map[string]interface{}{"type": "string", "description": "Unique dedup key."},
				"content":          map[string]interface{}{"type": "string", "description": "1-2 sentences."},
				"importance":       map[string]interface{}{"type": "number", "description": "1-10, default 5."},
				"tags":             map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
				"pinned":           map[string]interface{}{"type": "boolean", "description": "Never decay."},
				"expires_in_hours": map[string]interface{}{"type": "number"},
			},
			"required": []string{"category", "key", "content"},
		},
	},
	{
		Name:        "recall",
		Description: "Search long-term memory by keyword or category.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":    map[string]interface{}{"type": "string"},
				"category": map[string]interface{}{"type": "string", "enum": []string{"fact", "decision", "pattern", "insight", "preference", ""}},
				"limit":    map[string]interface{}{"type": "number", "description": "Default 5."},
			},
		},
	},
}

var coreSearchTools = []llm.Tool{
	{
		Name:        "deep_search",
		Description: "Full-text search across memories, reports, chats, directives, digests.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query":       map[string]interface{}{"type": "string"},
				"source_type": map[string]interface{}{"type": "string", "enum": []string{"all", "memories", "reports", "chats", "directives", "digests"}},
				"time_range":  map[string]interface{}{"type": "string", "enum": []string{"all", "last_week", "last_month", "last_quarter", "last_year"}},
				"limit":       map[string]interface{}{"type": "number", "description": "Default 10."},
			},
		},
	},
}

// ── Lazy-loaded categories ───────────────────────────────────────────

var analyticsTools = []llm.Tool{
	{
		Name:        "get_agent_metrics",
		Description: "Get agent performance metrics (calls, escalation rate, empty responses, avg response time) for a specified period.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Hours to look back. Default 24.",
				},
			},
		},
	},
	{
		Name:        "get_latest_report",
		Description: "Get the latest Director analysis report with findings, complaints, observations, and expectations.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_tool_stats",
		Description: "Get tool usage statistics (which tools are called, success/error rates).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Hours to look back. Default 24.",
				},
			},
		},
	},
	{
		Name:        "run_analysis",
		Description: "Trigger a full Director analysis cycle. Use ONLY when the admin explicitly asks.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		Name:        "get_interaction_details",
		Description: "Get detailed recent interaction metrics (individual calls with agent, response length, timing, tools used).",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"hours": map[string]interface{}{
					"type":        "number",
					"description": "Hours to look back. Default 24.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max interactions to return. Default 20.",
				},
			},
		},
	},
}

var reportTools = []llm.Tool{
	{
		Name:        "search_reports",
		Description: "Search past analysis reports by keywords or date range. Full-text search in Russian and English.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Search keywords.",
				},
				"days_back": map[string]interface{}{
					"type":        "number",
					"description": "Get reports from last N days.",
				},
				"limit": map[string]interface{}{
					"type":        "number",
					"description": "Max reports (1-20). Default 5.",
				},
			},
		},
	},
	{
		Name:        "timeline",
		Description: "Historical metrics, reports, directives by time period.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"period": map[string]interface{}{"type": "string", "description": "last_week/last_month/last_quarter/last_year/YYYY-MM/YYYY."},
				"detail": map[string]interface{}{"type": "string", "enum": []string{"summary", "full"}},
			},
			"required": []string{"period"},
		},
	},
}

var skillTools = []llm.Tool{
	{
		Name:        "create_skill",
		Description: "Create a new custom tool/skill. Types: sql_query, prompt_chain, http_api, composite. Becomes immediately available.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Unique tool name (lowercase, underscores)."},
				"description": map[string]interface{}{"type": "string", "description": "What the tool does."},
				"skill_type":  map[string]interface{}{"type": "string", "enum": []string{"sql_query", "prompt_chain", "http_api", "composite"}, "description": "Execution type."},
				"code":        map[string]interface{}{"type": "string", "description": "Implementation body (SQL/prompt template/JSON config)."},
				"parameters":  map[string]interface{}{"type": "string", "description": "JSON Schema for parameters."},
				"tags":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Tags."},
			},
			"required": []string{"name", "description", "skill_type", "code"},
		},
	},
	{
		Name:        "edit_skill",
		Description: "Edit an existing custom skill — update code, description, parameters, or enable/disable.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name":        map[string]interface{}{"type": "string", "description": "Skill name."},
				"description": map[string]interface{}{"type": "string", "description": "New description."},
				"code":        map[string]interface{}{"type": "string", "description": "New code."},
				"parameters":  map[string]interface{}{"type": "string", "description": "New JSON Schema."},
				"enabled":     map[string]interface{}{"type": "boolean", "description": "Enable/disable."},
			},
			"required": []string{"name"},
		},
	},
	{Name: "list_skills", Description: "List all custom skills with type, version, usage stats.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{Name: "delete_skill", Description: "Delete a custom skill by name (irreversible).", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Skill name."}}, "required": []string{"name"}}},
	{Name: "test_skill", Description: "Test a custom skill with sample parameters.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "description": "Skill name."}, "test_args": map[string]interface{}{"type": "object", "description": "Test parameters."}}, "required": []string{"name"}}},
}

var memoryExtraTools = []llm.Tool{
	{Name: "forget", Description: "Delete a specific memory by category and key.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"category": map[string]interface{}{"type": "string", "enum": []string{"fact", "decision", "pattern", "insight", "preference"}}, "key": map[string]interface{}{"type": "string", "description": "Memory key to delete."}}, "required": []string{"category", "key"}}},
	{Name: "list_memories", Description: "List stored memories, optionally filtered by category.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"category": map[string]interface{}{"type": "string", "enum": []string{"fact", "decision", "pattern", "insight", "preference", ""}}, "limit": map[string]interface{}{"type": "number", "description": "Max entries (1-50). Default 10."}}}},
}

var promptTools = []llm.Tool{
	{Name: "get_active_prompts", Description: "Get currently active prompt versions for all agents.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{Name: "get_prompt_history", Description: "Get prompt version history for a specific agent.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"agent_name": map[string]interface{}{"type": "string", "description": "Agent name."}, "limit": map[string]interface{}{"type": "number", "description": "Max versions. Default 5."}}, "required": []string{"agent_name"}}},
	{
		Name:        "update_agent_prompt",
		Description: "Update the system prompt for an L1 agent (e.g. zefir_support). Creates a new version and activates it immediately. Previous version is preserved in history for rollback.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_name": map[string]interface{}{"type": "string", "description": "Agent name, e.g. 'zefir_support'."},
				"prompt":     map[string]interface{}{"type": "string", "description": "Full new system prompt text."},
				"notes":      map[string]interface{}{"type": "string", "description": "Reason for the change (saved to history)."},
			},
			"required": []string{"agent_name", "prompt"},
		},
	},
}

var identityTools = []llm.Tool{
	{Name: "get_identity", Description: "Get your current personality/identity aspects (soul, goals, style, etc.).", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"aspect": map[string]interface{}{"type": "string", "enum": []string{"soul", "identity", "goals", "style", "values", "capabilities", "user_profile", "self_assessment", ""}, "description": "Specific aspect or empty for all."}}}},
	{Name: "update_identity", Description: "Update an aspect of your identity. Previous version saved to history.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"aspect": map[string]interface{}{"type": "string", "enum": []string{"soul", "identity", "goals", "style", "values", "capabilities", "user_profile", "self_assessment"}}, "content": map[string]interface{}{"type": "string", "description": "New content."}, "reason": map[string]interface{}{"type": "string", "description": "Why."}}, "required": []string{"aspect", "content", "reason"}}},
	{Name: "introspect", Description: "Self-reflection on identity and effectiveness using LLM.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{Name: "identity_history", Description: "View evolution history of an identity aspect.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"aspect": map[string]interface{}{"type": "string", "enum": []string{"soul", "identity", "goals", "style", "values", "capabilities", "user_profile", "self_assessment"}}, "limit": map[string]interface{}{"type": "number", "description": "Max versions. Default 5."}}, "required": []string{"aspect"}}},
	{Name: "rollback_identity", Description: "Rollback identity aspect to a previous version.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"aspect": map[string]interface{}{"type": "string", "enum": []string{"soul", "identity", "goals", "style", "values", "capabilities", "user_profile", "self_assessment"}}, "version": map[string]interface{}{"type": "number", "description": "Target version."}}, "required": []string{"aspect", "version"}}},
}

var agentCommTools = []llm.Tool{
	{Name: "agent_send", Description: "Send a question to an L1 support agent about a specific chat.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"chat_id": map[string]interface{}{"type": "string", "description": "Chat UUID."}, "message": map[string]interface{}{"type": "string", "description": "Your question."}}, "required": []string{"chat_id", "message"}}},
	{Name: "agent_list", Description: "List all currently active L1 agent sessions.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
}

var webhookTools = []llm.Tool{
	{Name: "get_webhook_events", Description: "Get recent webhook events from external systems.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"limit": map[string]interface{}{"type": "number", "description": "Max events (default 10, max 50)."}}}},
	{Name: "get_webhook_stats", Description: "Get aggregated webhook usage statistics.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
}

var cronTools = []llm.Tool{
	{Name: "create_cron_job", Description: "Create a scheduled task. Types: at (one-shot), every (interval), cron (expression).", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}, "schedule_type": map[string]interface{}{"type": "string", "enum": []string{"at", "every", "cron"}}, "schedule": map[string]interface{}{"type": "string"}, "timezone": map[string]interface{}{"type": "string"}, "action": map[string]interface{}{"type": "string", "enum": []string{"analyze", "send_message"}}, "action_config": map[string]interface{}{"type": "string"}, "max_runs": map[string]interface{}{"type": "number"}}, "required": []string{"name", "schedule_type", "schedule"}}},
	{Name: "list_cron_jobs", Description: "List all scheduled cron jobs with status and next run time.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}},
	{Name: "delete_cron_job", Description: "Delete a cron job by name.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}, "required": []string{"name"}}},
	{Name: "toggle_cron_job", Description: "Enable or disable a cron job.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "enabled": map[string]interface{}{"type": "boolean"}}, "required": []string{"name", "enabled"}}},
	{Name: "run_cron_job", Description: "Manually trigger a cron job immediately.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}}, "required": []string{"name"}}},
}

var taskTools = []llm.Tool{
	{Name: "create_task", Description: "Create a task for yourself or assign to someone.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"title": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}, "priority": map[string]interface{}{"type": "string", "enum": []string{"high", "medium", "low"}}, "category": map[string]interface{}{"type": "string"}, "assigned_to": map[string]interface{}{"type": "string"}, "tags": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}}, "required": []string{"title"}}},
	{Name: "list_tasks", Description: "List tasks, filter by status.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"status": map[string]interface{}{"type": "string", "enum": []string{"pending", "in_progress", "completed", "failed", "cancelled", ""}}, "limit": map[string]interface{}{"type": "number"}}}},
	{Name: "update_task", Description: "Update task status with comment.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"task_id": map[string]interface{}{"type": "string"}, "status": map[string]interface{}{"type": "string", "enum": []string{"pending", "in_progress", "completed", "failed", "cancelled"}}, "comment": map[string]interface{}{"type": "string"}, "failure_reason": map[string]interface{}{"type": "string"}}, "required": []string{"task_id", "status"}}},
	{Name: "comment_task", Description: "Add a comment to a task.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"task_id": map[string]interface{}{"type": "string"}, "comment": map[string]interface{}{"type": "string"}}, "required": []string{"task_id", "comment"}}},
	{Name: "get_task", Description: "Get full task details with comments and history.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"task_id": map[string]interface{}{"type": "string"}}, "required": []string{"task_id"}}},
	{Name: "save_daily_report", Description: "Save a daily report with per-chat analysis to database.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"analysis": map[string]interface{}{"type": "string"}, "customer_complaints": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "key_observations": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}, "directives": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object"}}, "expectations": map[string]interface{}{"type": "string"}, "report_type": map[string]interface{}{"type": "string", "enum": []string{"daily", "weekly", "incident", "custom"}}}, "required": []string{"analysis"}}},
}

var browserTools = []llm.Tool{
	{Name: "browser_screenshot", Description: "Navigate to URL and take a screenshot (base64 PNG).", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string"}}, "required": []string{"url"}}},
	{Name: "browser_get_text", Description: "Navigate to URL and extract visible text.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string"}, "max_length": map[string]interface{}{"type": "number"}}, "required": []string{"url"}}},
	{Name: "browser_eval_js", Description: "Navigate to URL and evaluate JavaScript.", Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"url": map[string]interface{}{"type": "string"}, "script": map[string]interface{}{"type": "string"}}, "required": []string{"url", "script"}}},
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
	case "load_tools":
		// Handled in chat handler loop, but just in case:
		return "Use load_tools from the chat interface. Categories: analytics, reports, identity, skills, memory_extra, prompts, cron, agents, tasks, webhooks, browser."
	case "get_agent_metrics":
		return toolGetAgentMetrics(call.Arguments)
	case "get_latest_report":
		return toolGetLatestReport()
	case "get_active_prompts":
		return toolGetActivePrompts()
	case "get_prompt_history":
		return toolGetPromptHistory(call.Arguments)
	case "update_agent_prompt":
		return toolUpdateAgentPrompt(call.Arguments)
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
	// Database schema
	case "describe_schema":
		return toolDescribeSchema(call.Arguments)
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
	// Inter-agent communication
	case "agent_send":
		return toolAgentSend(ctx, call.Arguments)
	case "agent_list":
		return toolAgentList()
	case "agent_context":
		return toolAgentContext(ctx, call.Arguments)
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
	case "get_webhook_events":
		return toolGetWebhookEvents(call.Arguments)
	case "get_webhook_stats":
		return toolGetWebhookStats()
	case "create_cron_job":
		return toolCreateCronJob(call.Arguments)
	case "list_cron_jobs":
		return toolListCronJobs()
	case "delete_cron_job":
		return toolDeleteCronJob(call.Arguments)
	case "toggle_cron_job":
		return toolToggleCronJob(call.Arguments)
	case "run_cron_job":
		return toolRunCronJob(call.Arguments)
	// Task management
	case "create_task":
		return toolCreateTask(call.Arguments)
	case "list_tasks":
		return toolListTasks(call.Arguments)
	case "update_task":
		return toolUpdateTask(call.Arguments)
	case "comment_task":
		return toolCommentTask(call.Arguments)
	case "get_task":
		return toolGetTask(call.Arguments)
	case "save_daily_report":
		return toolSaveDailyReport(call.Arguments)
	case "browser_screenshot":
		return toolBrowserScreenshot(ctx, call.Arguments)
	case "browser_get_text":
		return toolBrowserGetText(ctx, call.Arguments)
	case "browser_eval_js":
		return toolBrowserEvalJS(ctx, call.Arguments)
	default:
		// Try executing as a custom skill
		return executeCustomSkill(ctx, call.Name, call.Arguments)
	}
}
