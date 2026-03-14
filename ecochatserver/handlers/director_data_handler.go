package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/gin-gonic/gin"
)

// DirectorData returns combined director data: reports, agent stats, tool stats, prompt history
func DirectorData(c *gin.Context) {
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Parse time range for stats
	var since time.Time
	if startDate != "" {
		if t, err := time.Parse("2006-01-02", startDate); err == nil {
			since = t
		}
	}
	if since.IsZero() {
		since = time.Now().AddDate(0, -1, 0) // default: last month
	}

	// Get latest report
	report, _ := database.GetLatestDirectorReport()

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

	// Build reports array (just latest for now, can expand later)
	var reports []interface{}
	if report != nil {
		reports = append(reports, report)
	}

	_ = endDate // used for filtering scope info
	_ = limit

	c.JSON(http.StatusOK, gin.H{
		"reports":       reports,
		"total":         len(reports),
		"agentStats":    statsResp,
		"toolStats":     toolsResp,
		"promptHistory": promptHistory,
	})
}
