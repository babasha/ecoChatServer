package queries

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertDirectiveOutcome saves baseline metrics when a directive is created.
func InsertDirectiveOutcome(db *sql.DB, reportID uuid.UUID, directiveType, instruction string,
	escRateBefore, emptyRateBefore, avgMsBefore float64) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO directive_outcomes
			(id, report_id, directive_type, directive_instruction,
			 escalation_rate_before, empty_rate_before, avg_response_ms_before,
			 effectiveness, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', NOW())`,
		uuid.New(), reportID, directiveType, instruction,
		escRateBefore, emptyRateBefore, avgMsBefore,
	)
	if err != nil {
		return fmt.Errorf("insert directive outcome: %w", err)
	}
	return nil
}

// GetPendingDirectiveOutcomes returns outcomes awaiting evaluation (>24h old, still pending).
func GetPendingDirectiveOutcomes(db *sql.DB) ([]models.DirectiveOutcome, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, report_id, directive_type, directive_instruction,
		       escalation_rate_before, empty_rate_before, avg_response_ms_before,
		       effectiveness, created_at
		FROM directive_outcomes
		WHERE effectiveness = 'pending'
		  AND created_at < NOW() - INTERVAL '24 hours'
		ORDER BY created_at ASC
		LIMIT 10`)
	if err != nil {
		return nil, fmt.Errorf("get pending directive outcomes: %w", err)
	}
	defer rows.Close()

	var outcomes []models.DirectiveOutcome
	for rows.Next() {
		var o models.DirectiveOutcome
		if err := rows.Scan(&o.ID, &o.ReportID, &o.DirectiveType, &o.DirectiveInstruction,
			&o.EscalationRateBefore, &o.EmptyRateBefore, &o.AvgResponseMsBefore,
			&o.Effectiveness, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan directive outcome: %w", err)
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, rows.Err()
}

// EvaluateDirectiveOutcome fills in the "after" metrics and effectiveness assessment.
func EvaluateDirectiveOutcome(db *sql.DB, id uuid.UUID, effectiveness, notes string,
	escRateAfter, emptyRateAfter, avgMsAfter float64) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	now := time.Now()
	_, err := db.ExecContext(ctx, `
		UPDATE directive_outcomes
		SET escalation_rate_after = $2,
		    empty_rate_after = $3,
		    avg_response_ms_after = $4,
		    effectiveness = $5,
		    evaluation_notes = $6,
		    evaluated_at = $7
		WHERE id = $1`,
		id, escRateAfter, emptyRateAfter, avgMsAfter,
		effectiveness, notes, now,
	)
	if err != nil {
		return fmt.Errorf("evaluate directive outcome: %w", err)
	}
	return nil
}

// GetNegativeOutcomesSince returns negative outcomes from a given date for analysis.
func GetNegativeOutcomesSince(db *sql.DB, since time.Time) ([]models.DirectiveOutcome, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, report_id, directive_type, directive_instruction,
		       escalation_rate_before, empty_rate_before, avg_response_ms_before,
		       effectiveness, evaluation_notes, created_at
		FROM directive_outcomes
		WHERE effectiveness = 'negative'
		  AND created_at > $1
		ORDER BY created_at DESC
		LIMIT 20`, since)
	if err != nil {
		return nil, fmt.Errorf("get negative outcomes: %w", err)
	}
	defer rows.Close()

	var outcomes []models.DirectiveOutcome
	for rows.Next() {
		var o models.DirectiveOutcome
		var notes *string
		if err := rows.Scan(&o.ID, &o.ReportID, &o.DirectiveType, &o.DirectiveInstruction,
			&o.EscalationRateBefore, &o.EmptyRateBefore, &o.AvgResponseMsBefore,
			&o.Effectiveness, &notes, &o.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan negative outcome: %w", err)
		}
		if notes != nil {
			o.EvaluationNotes = *notes
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, rows.Err()
}

// GetNegativeOutcomesByType returns counts of negative outcomes grouped by directive_type.
func GetNegativeOutcomesByType(db *sql.DB, since time.Time) (map[string]int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT directive_type, COUNT(*)
		FROM directive_outcomes
		WHERE effectiveness = 'negative'
		  AND created_at > $1
		GROUP BY directive_type
		ORDER BY COUNT(*) DESC`, since)
	if err != nil {
		return nil, fmt.Errorf("get negative outcomes by type: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var dtype string
		var count int
		if err := rows.Scan(&dtype, &count); err != nil {
			return nil, err
		}
		result[dtype] = count
	}
	return result, rows.Err()
}

// GetDirectiveEffectivenessStats returns aggregate effectiveness for display.
func GetDirectiveEffectivenessStats(db *sql.DB) (map[string]int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT effectiveness, COUNT(*)
		FROM directive_outcomes
		WHERE effectiveness != 'pending'
		GROUP BY effectiveness`)
	if err != nil {
		return nil, fmt.Errorf("get directive effectiveness: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var eff string
		var count int
		if err := rows.Scan(&eff, &count); err != nil {
			return nil, err
		}
		stats[eff] = count
	}
	return stats, rows.Err()
}
