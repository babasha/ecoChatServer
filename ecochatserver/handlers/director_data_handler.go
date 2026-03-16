package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/egor/ecochatserver/director"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DirectorData returns combined director data: reports, agent stats, tool stats, prompt history
func DirectorData(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	reportType := c.Query("report_type")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Parse time range
	var since, until time.Time
	if startDate != "" {
		if t, err := time.Parse("2006-01-02", startDate); err == nil {
			since = t
		}
	}
	if since.IsZero() {
		since = time.Now().AddDate(0, -1, 0)
	}

	if endDate != "" {
		if t, err := time.Parse("2006-01-02", endDate); err == nil {
			// End of day
			until = t.Add(24*time.Hour - time.Second)
		}
	}
	if until.IsZero() {
		until = time.Now()
	}

	// Get reports with optional type filter
	var reports interface{}
	var total int
	if reportType != "" {
		reps, err := database.GetReportsByDateRangeAndType(since, until, reportType, limit)
		if err == nil {
			reports = reps
			total = len(reps)
		} else {
			reports = []interface{}{}
		}
	} else {
		reps, err := database.GetReportsByDateRange(since, until, limit)
		if err == nil {
			reports = reps
			total = len(reps)
		} else {
			reports = []interface{}{}
		}
	}

	// Get agent stats
	agentStats, _ := database.GetAgentStatsSince(since)

	// Get tool stats
	toolStats, _ := database.GetToolStatsSince(since)

	// Get prompt history for all agents
	agents := director.AgentNames
	type promptInfo struct {
		AgentName string `json:"agentName"`
		Version   int    `json:"version"`
		CreatedBy string `json:"createdBy"`
		IsActive  bool   `json:"isActive"`
		Notes     string `json:"notes"`
		CreatedAt string `json:"createdAt"`
	}
	var promptHistory []promptInfo
	for _, name := range agents {
		prompts, err := database.GetPromptHistory(name, 5)
		if err == nil {
			for _, p := range prompts {
				promptHistory = append(promptHistory, promptInfo{
					AgentName: p.AgentName,
					Version:   p.Version,
					CreatedBy: p.CreatedBy,
					IsActive:  p.IsActive,
					Notes:     p.Notes,
					CreatedAt: p.CreatedAt.Format(time.RFC3339),
				})
			}
		}
	}

	// Build agent stats response
	type agentStatsResp struct {
		AgentName      string  `json:"agentName"`
		TotalCalls     int     `json:"totalCalls"`
		Escalations    int     `json:"escalations"`
		EmptyResponses int     `json:"emptyResponses"`
		EscalationRate float64 `json:"escalationRate"`
		EmptyRate      float64 `json:"emptyRate"`
		AvgResponseLen float64 `json:"avgResponseLen"`
		AvgResponseMs  float64 `json:"avgResponseMs"`
	}
	var statsResp []agentStatsResp
	for _, s := range agentStats {
		statsResp = append(statsResp, agentStatsResp{
			AgentName:      s.AgentName,
			TotalCalls:     s.TotalCalls,
			Escalations:    s.Escalations,
			EmptyResponses: s.EmptyResponses,
			EscalationRate: s.EscalationRate,
			EmptyRate:      s.EmptyRate,
			AvgResponseLen: s.AvgResponseLen,
			AvgResponseMs:  s.AvgResponseMs,
		})
	}

	// Build tool stats response
	type toolStatsResp struct {
		ToolName     string  `json:"toolName"`
		TotalCalls   int     `json:"totalCalls"`
		SuccessCount int     `json:"successCount"`
		EmptyCount   int     `json:"emptyCount"`
		ErrorCount   int     `json:"errorCount"`
		SuccessRate  float64 `json:"successRate"`
	}
	var toolsResp []toolStatsResp
	for _, t := range toolStats {
		toolsResp = append(toolsResp, toolStatsResp{
			ToolName:     t.ToolName,
			TotalCalls:   t.TotalCalls,
			SuccessCount: t.SuccessCount,
			EmptyCount:   t.EmptyCount,
			ErrorCount:   t.ErrorCount,
			SuccessRate:  t.SuccessRate,
		})
	}

	// Build tool catalog: director built-in tools + custom skills
	type toolDef struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Category    string `json:"category"` // "director", "agent", "custom"
		SkillType   string `json:"skillType,omitempty"`
	}
	var toolCatalog []toolDef

	// Director built-in tools
	for _, t := range directorTools {
		toolCatalog = append(toolCatalog, toolDef{
			Name:        t.Name,
			Description: t.Description,
			Category:    "director",
		})
	}

	// Custom skills from DB
	customSkills, _ := database.GetAllSkills()
	for _, s := range customSkills {
		desc := s.Description
		if !s.Enabled {
			desc = "[DISABLED] " + desc
		}
		toolCatalog = append(toolCatalog, toolDef{
			Name:        s.Name,
			Description: desc,
			Category:    "custom",
			SkillType:   s.SkillType,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"reports":       reports,
		"total":         total,
		"agentStats":    statsResp,
		"toolStats":     toolsResp,
		"promptHistory": promptHistory,
		"toolCatalog":   toolCatalog,
	})
}

// DirectorAgentConversations returns the Director↔Agent conversation log.
func DirectorAgentConversations(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	convs, err := database.GetDirectorAgentConversations(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"conversations": convs,
		"total":         len(convs),
	})
}

// DirectorTasks returns tasks for the UI.
func DirectorTasks(c *gin.Context) {
	status := c.Query("status")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)

	tasks, err := database.GetTasks(status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tasks == nil {
		tasks = []queries.DirectorTask{}
	}

	c.JSON(http.StatusOK, gin.H{"tasks": tasks, "total": len(tasks)})
}

// DirectorTaskAddComment allows admin to comment on a task from UI.
func DirectorTaskAddComment(c *gin.Context) {
	var req struct {
		TaskID  string `json:"task_id"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}
	if req.Comment == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "comment is required"})
		return
	}
	taskID, err := uuid.Parse(req.TaskID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task_id"})
		return
	}
	comment, err := database.AddTaskComment(taskID, "admin", req.Comment)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"comment": comment})
}

// DirectorAgentConversationSend allows the admin to send a message to the Agent or Director
// through the same inter-agent communication channel.
func DirectorAgentConversationSend(c *gin.Context) {
	var req struct {
		ChatID  string `json:"chat_id"`
		Message string `json:"message"`
		Target  string `json:"target"` // "agent" or "director"
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
		return
	}
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}
	if req.Target == "" {
		req.Target = "agent"
	}

	bus := getAgentBus()
	if bus == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AgentBus not available"})
		return
	}

	var response string
	var tokIn, tokOut int
	var direction, sender string

	if req.Target == "director" {
		// Admin asks Director
		result, err := bus.QueryDirector(c.Request.Context(), req.Message, "")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		response = result.Response
		if result.Tokens != nil {
			tokIn = result.Tokens.PromptTokens
			tokOut = result.Tokens.CompletionTokens
		}
		direction = "admin_to_director"
		sender = "admin"
	} else {
		// Admin asks Agent about a chat
		if req.ChatID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "chat_id is required when target=agent"})
			return
		}
		result, err := bus.QueryAgent(c.Request.Context(), req.ChatID, req.Message)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		response = result.Response
		if result.Tokens != nil {
			tokIn = result.Tokens.PromptTokens
			tokOut = result.Tokens.CompletionTokens
		}
		direction = "admin_to_agent"
		sender = "admin"
	}

	// Log to conversation history
	if err := database.InsertDirectorAgentConversation(
		req.ChatID, direction, sender, req.Message, response, tokIn, tokOut,
	); err != nil {
		log.Printf("[DIRECTOR_AGENT_CONV] Log error: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"response":  response,
		"direction": direction,
		"tokens_in": tokIn,
		"tokens_out": tokOut,
	})
}
