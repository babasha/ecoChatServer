package handlers

import (
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

// DirectorChatMessage handles a chat message to the Director AI
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

	// Build context: latest report + agent stats
	contextInfo := buildDirectorChatContext()

	// Get chat history for this admin
	directorChatHistoryMu.Lock()
	history := make([]llm.Message, len(directorChatHistory[adminID]))
	copy(history, directorChatHistory[adminID])
	directorChatHistoryMu.Unlock()

	// Call Director's LLM
	systemPrompt := buildDirectorChatSystemPrompt(contextInfo)

	resp, err := provider.GenerateResponse(c.Request.Context(), request.Message, history, &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    2000,
		SystemPrompt: systemPrompt,
	})
	if err != nil {
		log.Printf("[DIRECTOR_CHAT] LLM error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error"})
		return
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
	log.Printf("[DIRECTOR_CHAT] Admin %s: %d chars → %d chars",
		idPreview, len(request.Message), len(resp.Text))

	c.JSON(http.StatusOK, gin.H{
		"message": resp.Text,
		"usage":   resp.Usage,
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

// buildDirectorChatContext gathers recent reports and stats for the system prompt
func buildDirectorChatContext() string {
	var sb strings.Builder

	// Latest report
	report, err := database.GetLatestDirectorReport()
	if err == nil && report != nil {
		sb.WriteString(fmt.Sprintf("LATEST REPORT (%s):\n", report.CreatedAt.Format("2006-01-02 15:04")))
		sb.WriteString(fmt.Sprintf("Analysis: %s\n", report.Analysis))
		if report.Expectations != "" {
			sb.WriteString(fmt.Sprintf("Expectations: %s\n", report.Expectations))
		}
		if len(report.CustomerComplaints) > 0 {
			sb.WriteString("Customer complaints:\n")
			for _, c := range report.CustomerComplaints {
				sb.WriteString(fmt.Sprintf("- %s\n", c))
			}
		}
		if len(report.KeyObservations) > 0 {
			sb.WriteString("Key observations:\n")
			for _, o := range report.KeyObservations {
				sb.WriteString(fmt.Sprintf("- %s\n", o))
			}
		}
		sb.WriteString("\n")
	}

	// Agent stats (last 24h)
	since := time.Now().Add(-24 * time.Hour)
	agentStats, err := database.GetAgentStatsSince(since)
	if err == nil && len(agentStats) > 0 {
		sb.WriteString("AGENT STATS (last 24h):\n")
		for _, a := range agentStats {
			escRate := 0.0
			emptyRate := 0.0
			if a.TotalCalls > 0 {
				escRate = float64(a.Escalations) / float64(a.TotalCalls) * 100
				emptyRate = float64(a.EmptyResponses) / float64(a.TotalCalls) * 100
			}
			sb.WriteString(fmt.Sprintf("  %s: %d calls, esc=%.1f%%, empty=%.1f%%, avg=%dms\n",
				a.AgentName, a.TotalCalls, escRate, emptyRate, int(a.AvgResponseMs)))
		}
	}

	// Active prompt versions
	agents := []string{"zefir_support", "plant_expert", "device_specialist", "support_specialist", "orchestrator"}
	var promptParts []string
	for _, name := range agents {
		if p, err := database.GetActivePrompt(name); err == nil && p != nil {
			promptParts = append(promptParts, fmt.Sprintf("  %s: v%d by %s", name, p.Version, p.CreatedBy))
		}
	}
	if len(promptParts) > 0 {
		sb.WriteString("\nACTIVE PROMPTS:\n")
		sb.WriteString(strings.Join(promptParts, "\n"))
		sb.WriteString("\n")
	}

	return sb.String()
}

func buildDirectorChatSystemPrompt(contextInfo string) string {
	return `You are the Director of AI Customer Support for Zefir IoT.
You are having a conversation with a human admin who manages the support system.

Your role:
- You are the Level 2 strategic agent who analyzes РОП (Level 1 agents) performance
- You create directives, optimize prompts, track customer complaints and agent metrics
- You can explain your decisions, reasoning, and expected outcomes

When the admin asks you questions, draw from your knowledge of:
- Recent analysis reports and their findings
- Customer complaint patterns
- Agent performance metrics (escalation rates, empty responses, response times)
- Prompt optimization decisions (what you changed, why, and expected impact)
- Tool usage patterns and failure rates

If the admin asks you to take action (adjust prompts, change directives, etc.),
explain what you would do and why, but note that actual changes require running
a full analysis cycle.

Be concise, specific, and data-driven. Use the same language as the admin.

CURRENT SYSTEM STATE:
` + contextInfo
}
