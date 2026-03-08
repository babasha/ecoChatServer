package queries

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// InsertDirectorReport saves a director analysis report
func InsertDirectorReport(db *sql.DB, id uuid.UUID, reportDate time.Time, reportType, triggerEvent string, summaryCount int, analysis string, directives, stats, customerComplaints, keyObservations, promptChanges json.RawMessage, expectations string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO director_reports
			(id, report_date, report_type, trigger_event, summary_count, analysis,
			 directives, stats, customer_complaints, key_observations, prompt_changes, expectations, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())`,
		id, reportDate, reportType, triggerEvent, summaryCount, analysis,
		directives, stats, customerComplaints, keyObservations, promptChanges, expectations,
	)
	if err != nil {
		return fmt.Errorf("insert director report: %w", err)
	}
	return nil
}

// GetLatestDirectorReport returns the most recent director report
func GetLatestDirectorReport(db *sql.DB) (*models.DirectorReport, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var r models.DirectorReport
	var directivesJSON, complaintsJSON, observationsJSON, promptChangesJSON []byte

	err := db.QueryRowContext(ctx, `
		SELECT id, report_date, report_type, trigger_event, summary_count, analysis,
		       directives, applied, created_at,
		       COALESCE(customer_complaints, '[]'), COALESCE(key_observations, '[]'),
		       COALESCE(prompt_changes, '[]'), COALESCE(expectations, '')
		FROM director_reports
		ORDER BY created_at DESC
		LIMIT 1`,
	).Scan(&r.ID, &r.ReportDate, &r.ReportType, &r.TriggerEvent, &r.SummaryCount, &r.Analysis,
		&directivesJSON, &r.Applied, &r.CreatedAt,
		&complaintsJSON, &observationsJSON, &promptChangesJSON, &r.Expectations)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest director report: %w", err)
	}

	if directivesJSON != nil {
		if err := json.Unmarshal(directivesJSON, &r.Directives); err != nil {
			return nil, fmt.Errorf("unmarshal directives: %w", err)
		}
	}
	if complaintsJSON != nil {
		json.Unmarshal(complaintsJSON, &r.CustomerComplaints)
	}
	if observationsJSON != nil {
		json.Unmarshal(observationsJSON, &r.KeyObservations)
	}
	if promptChangesJSON != nil {
		json.Unmarshal(promptChangesJSON, &r.PromptChanges)
	}

	return &r, nil
}

// UpdateDirectorReportPromptChanges updates the prompt_changes column for an existing report
func UpdateDirectorReportPromptChanges(db *sql.DB, id uuid.UUID, promptChanges json.RawMessage) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		UPDATE director_reports SET prompt_changes = $2 WHERE id = $1`,
		id, promptChanges,
	)
	if err != nil {
		return fmt.Errorf("update director report prompt changes: %w", err)
	}
	return nil
}

// GetChatSummariesSince returns all chat summaries created after a given time
func GetChatSummariesSince(db *sql.DB, since time.Time) ([]models.ChatSummary, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, chat_id, summary, messages_from, messages_to, message_count, created_at
		FROM chat_summaries
		WHERE created_at > $1
		ORDER BY created_at ASC`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("get chat summaries since: %w", err)
	}
	defer rows.Close()

	var summaries []models.ChatSummary
	for rows.Next() {
		var s models.ChatSummary
		if err := rows.Scan(&s.ID, &s.ChatID, &s.Summary, &s.MessagesFrom, &s.MessagesTo, &s.MessageCount, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chat summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}
