package queries

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertInteractionMetric saves a single interaction metric
func InsertInteractionMetric(db *sql.DB, m *models.InteractionMetric) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	toolsJSON, err := json.Marshal(m.ToolsCalled)
	if err != nil {
		return fmt.Errorf("marshal tools: %w", err)
	}

	var msgID *uuid.UUID
	if m.MessageID != nil {
		msgID = m.MessageID
	}

	var pv sql.NullInt64
	if m.PromptVersion != nil {
		pv = sql.NullInt64{Int64: int64(*m.PromptVersion), Valid: true}
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO interaction_metrics
			(id, chat_id, message_id, agent_mode, agent_name, prompt_version,
			 tools_called, tool_count, was_escalated, was_empty, response_length, response_time_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		m.ID, m.ChatID, msgID, m.AgentMode, m.AgentName, pv,
		toolsJSON, m.ToolCount, m.WasEscalated, m.WasEmpty, m.ResponseLength, m.ResponseTimeMs, m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert interaction metric: %w", err)
	}
	return nil
}

// GetAgentStatsSince returns aggregated stats per agent since a given time
func GetAgentStatsSince(db *sql.DB, since time.Time) ([]models.AgentStats, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT
			agent_name,
			COUNT(*) as total_calls,
			SUM(CASE WHEN was_escalated THEN 1 ELSE 0 END) as escalations,
			SUM(CASE WHEN was_empty THEN 1 ELSE 0 END) as empty_responses,
			COALESCE(AVG(response_length), 0) as avg_response_len,
			COALESCE(AVG(response_time_ms), 0) as avg_response_ms
		FROM interaction_metrics
		WHERE created_at > $1
		GROUP BY agent_name
		ORDER BY total_calls DESC`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent stats: %w", err)
	}
	defer rows.Close()

	var stats []models.AgentStats
	for rows.Next() {
		var s models.AgentStats
		if err := rows.Scan(&s.AgentName, &s.TotalCalls, &s.Escalations, &s.EmptyResponses,
			&s.AvgResponseLen, &s.AvgResponseMs); err != nil {
			return nil, fmt.Errorf("scan agent stats: %w", err)
		}
		if s.TotalCalls > 0 {
			s.EscalationRate = float64(s.Escalations) / float64(s.TotalCalls)
			s.EmptyRate = float64(s.EmptyResponses) / float64(s.TotalCalls)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetToolStatsSince returns aggregated tool usage stats since a given time
func GetToolStatsSince(db *sql.DB, since time.Time) ([]models.ToolStats, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	// Unnest the JSONB array of tool calls and aggregate
	rows, err := db.QueryContext(ctx, `
		SELECT
			tool->>'name' as tool_name,
			COUNT(*) as total_calls,
			SUM(CASE WHEN tool->>'result' = 'success' THEN 1 ELSE 0 END) as success_count,
			SUM(CASE WHEN tool->>'result' = 'empty' THEN 1 ELSE 0 END) as empty_count,
			SUM(CASE WHEN tool->>'result' = 'error' THEN 1 ELSE 0 END) as error_count
		FROM interaction_metrics,
			 jsonb_array_elements(tools_called) as tool
		WHERE created_at > $1
		  AND jsonb_typeof(tools_called) = 'array'
		GROUP BY tool->>'name'
		ORDER BY total_calls DESC`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("get tool stats: %w", err)
	}
	defer rows.Close()

	var stats []models.ToolStats
	for rows.Next() {
		var s models.ToolStats
		if err := rows.Scan(&s.ToolName, &s.TotalCalls, &s.SuccessCount, &s.EmptyCount, &s.ErrorCount); err != nil {
			return nil, fmt.Errorf("scan tool stats: %w", err)
		}
		if s.TotalCalls > 0 {
			s.SuccessRate = float64(s.SuccessCount) / float64(s.TotalCalls)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetToolStatsByAgentSince returns tool stats broken down by agent
func GetToolStatsByAgentSince(db *sql.DB, agentName string, since time.Time) ([]models.ToolStats, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT
			tool->>'name' as tool_name,
			COUNT(*) as total_calls,
			SUM(CASE WHEN tool->>'result' = 'success' THEN 1 ELSE 0 END) as success_count,
			SUM(CASE WHEN tool->>'result' = 'empty' THEN 1 ELSE 0 END) as empty_count,
			SUM(CASE WHEN tool->>'result' = 'error' THEN 1 ELSE 0 END) as error_count
		FROM interaction_metrics,
			 jsonb_array_elements(tools_called) as tool
		WHERE created_at > $1 AND agent_name = $2
		  AND jsonb_typeof(tools_called) = 'array'
		GROUP BY tool->>'name'
		ORDER BY total_calls DESC`, since, agentName,
	)
	if err != nil {
		return nil, fmt.Errorf("get tool stats by agent: %w", err)
	}
	defer rows.Close()

	var stats []models.ToolStats
	for rows.Next() {
		var s models.ToolStats
		if err := rows.Scan(&s.ToolName, &s.TotalCalls, &s.SuccessCount, &s.EmptyCount, &s.ErrorCount); err != nil {
			return nil, fmt.Errorf("scan tool stats: %w", err)
		}
		if s.TotalCalls > 0 {
			s.SuccessRate = float64(s.SuccessCount) / float64(s.TotalCalls)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetPromptVersionMetrics calculates metrics for a specific prompt version
func GetPromptVersionMetrics(db *sql.DB, agentName string, version int) (*models.PromptMetrics, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var m models.PromptMetrics
	var totalCalls int

	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) as total,
			COALESCE(AVG(CASE WHEN was_escalated THEN 1.0 ELSE 0.0 END), 0) as esc_rate,
			COALESCE(AVG(CASE WHEN was_empty THEN 1.0 ELSE 0.0 END), 0) as empty_rate,
			COALESCE(AVG(response_length), 0) as avg_len,
			COALESCE(AVG(tool_count), 0) as avg_tools
		FROM interaction_metrics
		WHERE agent_name = $1 AND prompt_version = $2`,
		agentName, version,
	).Scan(&totalCalls, &m.EscalationRate, &m.EmptyResponseRate, &m.AvgResponseLength, &m.ToolUsageRate)

	if err != nil {
		return nil, fmt.Errorf("get prompt version metrics: %w", err)
	}

	m.ChatsHandled = totalCalls
	return &m, nil
}
