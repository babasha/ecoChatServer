package queries

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// GetActivePrompt returns the currently active prompt for an agent
func GetActivePrompt(db *sql.DB, agentName string) (*models.AgentPrompt, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var p models.AgentPrompt
	var metricsJSON []byte
	var parentVersion sql.NullInt64
	var notes sql.NullString

	err := db.QueryRowContext(ctx, `
		SELECT id, agent_name, version, prompt, created_by, parent_version, is_active, metrics, notes, created_at
		FROM agent_prompts
		WHERE agent_name = $1 AND is_active = true
		LIMIT 1`, agentName,
	).Scan(&p.ID, &p.AgentName, &p.Version, &p.Prompt, &p.CreatedBy, &parentVersion, &p.IsActive, &metricsJSON, &notes, &p.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get active prompt: %w", err)
	}

	if parentVersion.Valid {
		v := int(parentVersion.Int64)
		p.ParentVersion = &v
	}
	if notes.Valid {
		p.Notes = notes.String
	}
	if len(metricsJSON) > 0 && string(metricsJSON) != "{}" {
		var m models.PromptMetrics
		if err := json.Unmarshal(metricsJSON, &m); err == nil {
			p.Metrics = &m
		}
	}

	return &p, nil
}

// GetPromptHistory returns all prompt versions for an agent, newest first
func GetPromptHistory(db *sql.DB, agentName string, limit int) ([]models.AgentPrompt, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, agent_name, version, prompt, created_by, parent_version, is_active, metrics, notes, created_at
		FROM agent_prompts
		WHERE agent_name = $1
		ORDER BY version DESC
		LIMIT $2`, agentName, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get prompt history: %w", err)
	}
	defer rows.Close()

	return scanPrompts(rows)
}

// InsertPrompt creates a new prompt version
func InsertPrompt(db *sql.DB, agentName string, version int, prompt, createdBy string, parentVersion *int, notes string) (*models.AgentPrompt, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	p := &models.AgentPrompt{
		ID:            uuid.New(),
		AgentName:     agentName,
		Version:       version,
		Prompt:        prompt,
		CreatedBy:     createdBy,
		ParentVersion: parentVersion,
		IsActive:      false,
		Notes:         notes,
		CreatedAt:     time.Now(),
	}

	var pv sql.NullInt64
	if parentVersion != nil {
		pv = sql.NullInt64{Int64: int64(*parentVersion), Valid: true}
	}

	var notesNull sql.NullString
	if notes != "" {
		notesNull = sql.NullString{String: notes, Valid: true}
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO agent_prompts (id, agent_name, version, prompt, created_by, parent_version, is_active, notes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, false, $7, $8)`,
		p.ID, p.AgentName, p.Version, p.Prompt, p.CreatedBy, pv, notesNull, p.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert prompt: %w", err)
	}

	return p, nil
}

// ActivatePrompt sets a specific version as active (deactivates all others for the same agent)
func ActivatePrompt(db *sql.DB, agentName string, version int) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Deactivate all
	_, err = tx.ExecContext(ctx, `
		UPDATE agent_prompts SET is_active = false WHERE agent_name = $1`, agentName)
	if err != nil {
		return fmt.Errorf("deactivate prompts: %w", err)
	}

	// Activate target
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_prompts SET is_active = true WHERE agent_name = $1 AND version = $2`, agentName, version)
	if err != nil {
		return fmt.Errorf("activate prompt: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("prompt version %d not found for agent %s", version, agentName)
	}

	return tx.Commit()
}

// UpdatePromptMetrics updates the metrics for a specific prompt version
func UpdatePromptMetrics(db *sql.DB, agentName string, version int, metrics *models.PromptMetrics) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}

	_, err = db.ExecContext(ctx, `
		UPDATE agent_prompts SET metrics = $1 WHERE agent_name = $2 AND version = $3`,
		metricsJSON, agentName, version)
	if err != nil {
		return fmt.Errorf("update metrics: %w", err)
	}

	return nil
}

// GetNextVersion returns the next version number for an agent
func GetNextVersion(db *sql.DB, agentName string) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var maxVersion sql.NullInt64
	err := db.QueryRowContext(ctx, `
		SELECT MAX(version) FROM agent_prompts WHERE agent_name = $1`, agentName,
	).Scan(&maxVersion)
	if err != nil {
		return 0, fmt.Errorf("get max version: %w", err)
	}

	if !maxVersion.Valid {
		return 1, nil
	}
	return int(maxVersion.Int64) + 1, nil
}

func scanPrompts(rows *sql.Rows) ([]models.AgentPrompt, error) {
	var prompts []models.AgentPrompt
	for rows.Next() {
		var p models.AgentPrompt
		var metricsJSON []byte
		var parentVersion sql.NullInt64
		var notes sql.NullString

		if err := rows.Scan(&p.ID, &p.AgentName, &p.Version, &p.Prompt, &p.CreatedBy,
			&parentVersion, &p.IsActive, &metricsJSON, &notes, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}

		if parentVersion.Valid {
			v := int(parentVersion.Int64)
			p.ParentVersion = &v
		}
		if notes.Valid {
			p.Notes = notes.String
		}
		if len(metricsJSON) > 0 && string(metricsJSON) != "{}" {
			var m models.PromptMetrics
			if err := json.Unmarshal(metricsJSON, &m); err == nil {
				p.Metrics = &m
			}
		}

		prompts = append(prompts, p)
	}
	return prompts, rows.Err()
}
