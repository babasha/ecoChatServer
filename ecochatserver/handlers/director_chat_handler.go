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
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// directorChatHistory stores per-admin conversation history (in-memory, resets on restart)
var (
	directorChatHistory   = map[string][]llm.Message{} // adminID → messages
	directorChatHistoryMu sync.RWMutex
)

const maxDirectorChatHistory = 20 // keep last 20 messages per admin
const maxToolCalls = 5            // max tool calls per request to prevent infinite loops

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
}

// ============================================================================
// Tool execution — execute Director tool calls against the database
// ============================================================================

func executeDirectorTool(ctx context.Context, call *llm.FunctionCall) string {
	log.Printf("[DIRECTOR_CHAT] Executing tool: %s args=%v", call.Name, call.Arguments)

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
	default:
		return fmt.Sprintf("Unknown tool: %s", call.Name)
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

	// Get chat history for this admin
	directorChatHistoryMu.Lock()
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

	// First call with tools
	resp, err := provider.GenerateWithTools(c.Request.Context(), request.Message, loopHistory, directorTools, opts)
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
		resp, err = provider.GenerateWithTools(c.Request.Context(), "", loopHistory, directorTools, opts)
		if err != nil {
			log.Printf("[DIRECTOR_CHAT] LLM error after tool call: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error after tool execution"})
			return
		}
	}

	if resp.Text == "" && resp.FunctionCall != nil {
		resp.Text = "I tried to gather data but reached the tool call limit. Please try again with a more specific question."
	}

	// Save both messages to history
	directorChatHistoryMu.Lock()
	h := directorChatHistory[adminID]
	h = append(h, llm.Message{Role: "user", Content: request.Message})
	h = append(h, llm.Message{Role: "assistant", Content: resp.Text})
	if len(h) > maxDirectorChatHistory {
		h = h[len(h)-maxDirectorChatHistory:]
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

	c.JSON(http.StatusOK, gin.H{
		"messages": history,
	})
}

// DirectorChatClear clears conversation history for the current admin
func DirectorChatClear(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	directorChatHistoryMu.Lock()
	delete(directorChatHistory, adminID)
	directorChatHistoryMu.Unlock()

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
// System prompt — Director now has tools, no need for static context
// ============================================================================

func buildDirectorChatSystemPrompt() string {
	return `You are the Director of AI Customer Support for Zefir IoT.
You are having a conversation with a human admin who manages the support system.

Your role:
- You are the Level 2 strategic agent who analyzes Level 1 agents (РОП) performance
- You create directives, optimize prompts, track customer complaints and agent metrics
- You can explain your decisions, reasoning, and expected outcomes

You have TOOLS available to query real data from the database. USE THEM ACTIVELY:
- get_agent_metrics: Get performance stats (calls, escalations, empty responses, response times)
- get_latest_report: Get your latest analysis report
- get_active_prompts: See current prompt versions for all agents
- get_prompt_history: See prompt evolution for a specific agent
- get_tool_stats: See tool usage and failure rates
- get_interaction_details: Get detailed per-agent breakdown with tool usage
- run_analysis: Trigger a full analysis cycle (creates a new report)

IMPORTANT:
- When asked about data, metrics, or status — ALWAYS use tools first to get real data. Never make up numbers.
- When asked to run analysis or check something — use the appropriate tool.
- If you have no data for a period, suggest widening the time range (e.g., try 168 hours for a week).
- Be concise, specific, and data-driven.
- Use the same language as the admin (if they write in Russian, respond in Russian).
- If the admin asks to run analysis, use the run_analysis tool.`
}
