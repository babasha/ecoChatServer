package queries

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/lib/pq"
)

// ============================================================================
// Director Skills CRUD — custom tools created by Director at runtime
// ============================================================================

// CreateSkill inserts or upserts a custom skill.
func CreateSkill(db *sql.DB, skill *models.DirectorSkill) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	return db.QueryRowContext(ctx, `
		INSERT INTO director_skills (id, name, description, parameters, skill_type, code, version, enabled, created_by, tags)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6, 1, $7, $8, $9)
		ON CONFLICT (name) DO UPDATE SET
			description = EXCLUDED.description,
			parameters = EXCLUDED.parameters,
			skill_type = EXCLUDED.skill_type,
			code = EXCLUDED.code,
			version = director_skills.version + 1,
			enabled = EXCLUDED.enabled,
			tags = EXCLUDED.tags,
			updated_at = NOW()
		RETURNING version`,
		skill.ID, skill.Name, skill.Description, skill.Parameters,
		skill.SkillType, skill.Code, skill.Enabled,
		skill.CreatedBy, pq.Array(skill.Tags),
	).Scan(&skill.Version)
}

// GetEnabledSkills returns all enabled custom skills.
func GetEnabledSkills(db *sql.DB) ([]models.DirectorSkill, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, parameters, skill_type, code, version, enabled,
			   created_by, usage_count, last_used_at, last_error, tags, created_at, updated_at
		FROM director_skills
		WHERE enabled = true
		ORDER BY usage_count DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("query enabled skills: %w", err)
	}
	defer rows.Close()

	return scanSkills(rows)
}

// GetAllSkills returns all skills (including disabled).
func GetAllSkills(db *sql.DB) ([]models.DirectorSkill, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, description, parameters, skill_type, code, version, enabled,
			   created_by, usage_count, last_used_at, last_error, tags, created_at, updated_at
		FROM director_skills
		ORDER BY enabled DESC, usage_count DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("query all skills: %w", err)
	}
	defer rows.Close()

	return scanSkills(rows)
}

// GetSkillByName returns a skill by its unique name.
func GetSkillByName(db *sql.DB, name string) (*models.DirectorSkill, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var s models.DirectorSkill
	err := db.QueryRowContext(ctx, `
		SELECT id, name, description, parameters, skill_type, code, version, enabled,
			   created_by, usage_count, last_used_at, last_error, tags, created_at, updated_at
		FROM director_skills
		WHERE name = $1`, name,
	).Scan(
		&s.ID, &s.Name, &s.Description, &s.Parameters, &s.SkillType,
		&s.Code, &s.Version, &s.Enabled, &s.CreatedBy, &s.UsageCount,
		&s.LastUsedAt, &s.LastError, pq.Array(&s.Tags), &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSkill updates specific fields of an existing skill.
func UpdateSkill(db *sql.DB, name string, description, parameters, code *string, enabled *bool, tags []string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	setClauses := []string{"updated_at = NOW()", "version = version + 1"}
	args := []interface{}{name}
	argIdx := 2

	if description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *description)
		argIdx++
	}
	if parameters != nil {
		setClauses = append(setClauses, fmt.Sprintf("parameters = $%d::jsonb", argIdx))
		args = append(args, *parameters)
		argIdx++
	}
	if code != nil {
		setClauses = append(setClauses, fmt.Sprintf("code = $%d", argIdx))
		args = append(args, *code)
		argIdx++
	}
	if enabled != nil {
		setClauses = append(setClauses, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, *enabled)
		argIdx++
	}
	if tags != nil {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", argIdx))
		args = append(args, pq.Array(tags))
		argIdx++
	}

	query := fmt.Sprintf("UPDATE director_skills SET %s WHERE name = $1",
		strings.Join(setClauses, ", "))

	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update skill: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("skill not found: %s", name)
	}
	return nil
}

// DeleteSkill removes a skill by name.
func DeleteSkill(db *sql.DB, name string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	result, err := db.ExecContext(ctx, "DELETE FROM director_skills WHERE name = $1", name)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("skill not found: %s", name)
	}
	return nil
}

// RecordSkillUsage increments usage count and records last error.
func RecordSkillUsage(db *sql.DB, name string, lastError string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		UPDATE director_skills
		SET usage_count = usage_count + 1,
			last_used_at = $2,
			last_error = $3,
			updated_at = NOW()
		WHERE name = $1`,
		name, time.Now(), lastError)
	return err
}

// ToggleSkill enables or disables a skill.
func ToggleSkill(db *sql.DB, name string, enabled bool) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx,
		"UPDATE director_skills SET enabled = $2, updated_at = NOW() WHERE name = $1",
		name, enabled)
	return err
}

// LogDirectorToolCall records a Director-level tool call for pattern analysis.
func LogDirectorToolCall(db *sql.DB, toolName, argsJSON string, resultLen int, success bool) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO director_tool_log (tool_name, arguments, result_length, success)
		VALUES ($1, $2::jsonb, $3, $4)`,
		toolName, argsJSON, resultLen, success)
	return err
}

// GetRepeatingToolPatterns finds tools called 3+ times in a period with similar patterns.
func GetRepeatingToolPatterns(db *sql.DB, since time.Time, minCount int) ([]models.ToolPattern, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT tool_name, COUNT(*) as call_count,
		       (SELECT arguments FROM director_tool_log t2
		        WHERE t2.tool_name = t1.tool_name ORDER BY created_at DESC LIMIT 1) as sample_args
		FROM director_tool_log t1
		WHERE created_at > $1 AND success = true
		GROUP BY tool_name
		HAVING COUNT(*) >= $2
		ORDER BY call_count DESC
		LIMIT 10`,
		since, minCount)
	if err != nil {
		return nil, fmt.Errorf("get repeating patterns: %w", err)
	}
	defer rows.Close()

	var patterns []models.ToolPattern
	for rows.Next() {
		var p models.ToolPattern
		if err := rows.Scan(&p.ToolName, &p.CallCount, &p.SampleArgs); err != nil {
			return nil, fmt.Errorf("scan pattern: %w", err)
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// CountAutoCreatedSkills returns how many skills were auto-created (for limiting).
func CountAutoCreatedSkills(db *sql.DB) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM director_skills WHERE created_by = 'auto_reflection' OR created_by = 'auto_pattern'",
	).Scan(&count)
	return count, err
}

// scanSkills helper to scan skill rows.
func scanSkills(rows *sql.Rows) ([]models.DirectorSkill, error) {
	var skills []models.DirectorSkill
	for rows.Next() {
		var s models.DirectorSkill
		err := rows.Scan(
			&s.ID, &s.Name, &s.Description, &s.Parameters, &s.SkillType,
			&s.Code, &s.Version, &s.Enabled, &s.CreatedBy, &s.UsageCount,
			&s.LastUsedAt, &s.LastError, pq.Array(&s.Tags), &s.CreatedAt, &s.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		skills = append(skills, s)
	}
	return skills, nil
}

