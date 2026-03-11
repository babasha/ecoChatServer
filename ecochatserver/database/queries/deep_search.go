package queries

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/lib/pq"
)

// ============================================================================
// Deep Search — unified FTS across all Director data sources
// ============================================================================

// DeepSearch performs a unified FTS search across memories, reports, chat summaries,
// directive outcomes, and digests. Returns results weighted by source importance.
func DeepSearch(db *sql.DB, query string, sourceType string, timeRange string, limit int) ([]models.DeepSearchResult, error) {
	if limit <= 0 || limit > 20 {
		limit = 10
	}

	since := parseTimeRange(timeRange)

	var allResults []models.DeepSearchResult

	sources := []struct {
		name   string
		search func(*sql.DB, string, time.Time, int) ([]models.DeepSearchResult, error)
	}{
		{"memories", searchMemoriesDeep},
		{"reports", searchReportsDeep},
		{"chats", searchChatSummariesDeep},
		{"directives", searchDirectivesDeep},
		{"digests", searchDigestsDeep},
	}

	for _, src := range sources {
		if sourceType != "" && sourceType != "all" && sourceType != src.name {
			continue
		}
		results, err := src.search(db, query, since, limit)
		if err != nil {
			continue // partial results are better than none
		}
		allResults = append(allResults, results...)
	}

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Rank > allResults[j].Rank
	})

	if len(allResults) > limit {
		allResults = allResults[:limit]
	}

	return allResults, nil
}

func parseTimeRange(timeRange string) time.Time {
	switch timeRange {
	case "last_week":
		return time.Now().AddDate(0, 0, -7)
	case "last_month":
		return time.Now().AddDate(0, -1, 0)
	case "last_quarter":
		return time.Now().AddDate(0, -3, 0)
	case "last_year":
		return time.Now().AddDate(-1, 0, 0)
	default:
		return time.Time{}
	}
}

// ── Per-source search functions ──────────────────────────────────────────

func searchMemoriesDeep(db *sql.DB, query string, since time.Time, limit int) ([]models.DeepSearchResult, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var conditions []string
	var args []interface{}
	argIdx := 1

	conditions = append(conditions, "(expires_at IS NULL OR expires_at > NOW())")

	hasFTS := query != ""
	if hasFTS {
		conditions = append(conditions, fmt.Sprintf("search_vector @@ plainto_tsquery('russian', $%d)", argIdx))
		args = append(args, query)
		argIdx++
	}
	if !since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("updated_at >= $%d", argIdx))
		args = append(args, since)
		argIdx++
	}
	args = append(args, limit)

	rankExpr := "importance::float / 10.0"
	orderBy := "importance DESC, updated_at DESC"
	if hasFTS {
		rankExpr = "ts_rank(search_vector, plainto_tsquery('russian', $1)) * importance::float / 10.0 * 1.5"
		orderBy = "rank DESC"
	}

	sqlQ := fmt.Sprintf(`
		SELECT id::text, category || '/' || key, LEFT(content, 150), updated_at, %s AS rank
		FROM director_memories
		WHERE %s
		ORDER BY %s
		LIMIT $%d`,
		rankExpr, strings.Join(conditions, " AND "), orderBy, argIdx)

	return scanDeepResults(ctx, db, sqlQ, args, "memory")
}

func searchReportsDeep(db *sql.DB, query string, since time.Time, limit int) ([]models.DeepSearchResult, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var conditions []string
	var args []interface{}
	argIdx := 1

	hasFTS := query != ""
	if hasFTS {
		conditions = append(conditions, fmt.Sprintf(
			"(search_vector IS NOT NULL AND search_vector @@ plainto_tsquery('russian', $%d))", argIdx))
		args = append(args, query)
		argIdx++
	}
	if !since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, since)
		argIdx++
	}
	args = append(args, limit)

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	rankExpr := "0::float"
	orderBy := "created_at DESC"
	if hasFTS {
		rankExpr = "ts_rank(search_vector, plainto_tsquery('russian', $1))"
		orderBy = "rank DESC"
	}

	sqlQ := fmt.Sprintf(`
		SELECT id::text, report_type, LEFT(analysis, 150), created_at, %s AS rank
		FROM director_reports
		%s
		ORDER BY %s
		LIMIT $%d`,
		rankExpr, whereClause, orderBy, argIdx)

	return scanDeepResults(ctx, db, sqlQ, args, "report")
}

func searchChatSummariesDeep(db *sql.DB, query string, since time.Time, limit int) ([]models.DeepSearchResult, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var conditions []string
	var args []interface{}
	argIdx := 1

	hasFTS := query != ""
	if hasFTS {
		conditions = append(conditions, fmt.Sprintf(
			"((search_vector IS NOT NULL AND search_vector @@ plainto_tsquery('russian', $%d)) OR LOWER(summary) LIKE $%d)",
			argIdx, argIdx+1))
		args = append(args, query, "%"+strings.ToLower(query)+"%")
		argIdx += 2
	}
	if !since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, since)
		argIdx++
	}
	args = append(args, limit)

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	rankExpr := "0::float"
	orderBy := "created_at DESC"
	if hasFTS {
		rankExpr = "COALESCE(ts_rank(search_vector, plainto_tsquery('russian', $1)), 0) * 0.8"
		orderBy = "rank DESC"
	}

	sqlQ := fmt.Sprintf(`
		SELECT chat_id::text, 'chat', LEFT(summary, 150), created_at, %s AS rank
		FROM chat_summaries
		%s
		ORDER BY %s
		LIMIT $%d`,
		rankExpr, whereClause, orderBy, argIdx)

	return scanDeepResults(ctx, db, sqlQ, args, "chat_summary")
}

func searchDirectivesDeep(db *sql.DB, query string, since time.Time, limit int) ([]models.DeepSearchResult, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var conditions []string
	var args []interface{}
	argIdx := 1

	hasFTS := query != ""
	if hasFTS {
		conditions = append(conditions, fmt.Sprintf(
			"((search_vector IS NOT NULL AND search_vector @@ plainto_tsquery('russian', $%d)) OR LOWER(directive_instruction) LIKE $%d)",
			argIdx, argIdx+1))
		args = append(args, query, "%"+strings.ToLower(query)+"%")
		argIdx += 2
	}
	if !since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", argIdx))
		args = append(args, since)
		argIdx++
	}
	args = append(args, limit)

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	rankExpr := "0::float"
	orderBy := "created_at DESC"
	if hasFTS {
		rankExpr = "COALESCE(ts_rank(search_vector, plainto_tsquery('russian', $1)), 0) * 0.7"
		orderBy = "rank DESC"
	}

	sqlQ := fmt.Sprintf(`
		SELECT id::text, directive_type || ':' || effectiveness, LEFT(directive_instruction, 150), created_at, %s AS rank
		FROM directive_outcomes
		%s
		ORDER BY %s
		LIMIT $%d`,
		rankExpr, whereClause, orderBy, argIdx)

	return scanDeepResults(ctx, db, sqlQ, args, "directive")
}

func searchDigestsDeep(db *sql.DB, query string, since time.Time, limit int) ([]models.DeepSearchResult, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var conditions []string
	var args []interface{}
	argIdx := 1

	hasFTS := query != ""
	if hasFTS {
		conditions = append(conditions, fmt.Sprintf(
			"search_vector IS NOT NULL AND search_vector @@ plainto_tsquery('russian', $%d)", argIdx))
		args = append(args, query)
		argIdx++
	}
	if !since.IsZero() {
		conditions = append(conditions, fmt.Sprintf("period_start >= $%d", argIdx))
		args = append(args, since)
		argIdx++
	}
	args = append(args, limit)

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	rankExpr := "0::float"
	orderBy := "period_start DESC"
	if hasFTS {
		rankExpr = "ts_rank(search_vector, plainto_tsquery('russian', $1)) * 1.2"
		orderBy = "rank DESC"
	}

	sqlQ := fmt.Sprintf(`
		SELECT id::text, period_type, LEFT(summary, 150), period_start, %s AS rank
		FROM director_digests
		%s
		ORDER BY %s
		LIMIT $%d`,
		rankExpr, whereClause, orderBy, argIdx)

	return scanDeepResults(ctx, db, sqlQ, args, "digest")
}

// scanDeepResults is a generic scanner for deep search results.
func scanDeepResults(ctx context.Context, db *sql.DB, query string, args []interface{}, resultType string) ([]models.DeepSearchResult, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []models.DeepSearchResult
	for rows.Next() {
		var r models.DeepSearchResult
		r.Type = resultType
		if err := rows.Scan(&r.ID, &r.Meta, &r.Snippet, &r.Date, &r.Rank); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ============================================================================
// Timeline — aggregate metrics for historical periods
// ============================================================================

// GetTimelineData aggregates all metrics for a given time period.
func GetTimelineData(db *sql.DB, from, to time.Time, detail string) (*models.TimelineData, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	data := &models.TimelineData{
		PeriodStart: from,
		PeriodEnd:   to,
	}

	// 1. Aggregate interaction metrics
	err := db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN was_escalated THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0), 0),
			COALESCE(SUM(CASE WHEN was_empty THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0), 0),
			COALESCE(AVG(response_time_ms), 0)
		FROM interaction_metrics
		WHERE created_at BETWEEN $1 AND $2`,
		from, to,
	).Scan(&data.TotalInteractions, &data.AvgEscalationRate, &data.AvgEmptyRate, &data.AvgResponseMs)
	if err != nil {
		return nil, fmt.Errorf("timeline interaction metrics: %w", err)
	}

	// 2. Reports
	reportRows, err := db.QueryContext(ctx, `
		SELECT report_type, LEFT(analysis, 200), created_at
		FROM director_reports
		WHERE created_at BETWEEN $1 AND $2
		ORDER BY created_at DESC
		LIMIT 20`,
		from, to,
	)
	if err == nil {
		defer reportRows.Close()
		for reportRows.Next() {
			var reportType, analysis string
			var createdAt time.Time
			if reportRows.Scan(&reportType, &analysis, &createdAt) == nil {
				data.ReportCount++
				if detail == "full" {
					data.ReportSummaries = append(data.ReportSummaries,
						fmt.Sprintf("[%s %s] %s", createdAt.Format("2006-01-02"), reportType, analysis))
				}
			}
		}
	}

	// 3. Directive effectiveness
	dirRows, err := db.QueryContext(ctx, `
		SELECT effectiveness, COUNT(*)
		FROM directive_outcomes
		WHERE created_at BETWEEN $1 AND $2 AND effectiveness != 'pending'
		GROUP BY effectiveness`,
		from, to,
	)
	if err == nil {
		data.DirectiveStats = make(map[string]int)
		for dirRows.Next() {
			var eff string
			var count int
			if dirRows.Scan(&eff, &count) == nil {
				data.DirectiveStats[eff] = count
			}
		}
		dirRows.Close()
	}

	// 4. Top agents (full detail only)
	if detail == "full" {
		agentRows, err := db.QueryContext(ctx, `
			SELECT agent_name,
				   COUNT(*),
				   COALESCE(SUM(CASE WHEN was_escalated THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0), 0),
				   COALESCE(SUM(CASE WHEN was_empty THEN 1 ELSE 0 END)::float / NULLIF(COUNT(*), 0), 0)
			FROM interaction_metrics
			WHERE created_at BETWEEN $1 AND $2
			GROUP BY agent_name
			ORDER BY COUNT(*) DESC
			LIMIT 10`,
			from, to,
		)
		if err == nil {
			for agentRows.Next() {
				var a models.AgentStatsBrief
				if agentRows.Scan(&a.Name, &a.Calls, &a.EscalationRate, &a.EmptyRate) == nil {
					data.TopAgents = append(data.TopAgents, a)
				}
			}
			agentRows.Close()
		}
	}

	return data, nil
}

// ============================================================================
// Director Digests — consolidated period summaries
// ============================================================================

// InsertDigest creates or updates a consolidated digest.
func InsertDigest(db *sql.DB, d *models.DirectorDigest) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	_, err := db.ExecContext(ctx, `
		INSERT INTO director_digests
			(id, period_type, period_start, period_end,
			 total_chats, total_interactions,
			 avg_escalation_rate, avg_empty_rate, avg_response_ms,
			 summary, key_events, trends, lessons, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (period_type, period_start) DO UPDATE SET
			total_chats = EXCLUDED.total_chats,
			total_interactions = EXCLUDED.total_interactions,
			avg_escalation_rate = EXCLUDED.avg_escalation_rate,
			avg_empty_rate = EXCLUDED.avg_empty_rate,
			avg_response_ms = EXCLUDED.avg_response_ms,
			summary = EXCLUDED.summary,
			key_events = EXCLUDED.key_events,
			trends = EXCLUDED.trends,
			lessons = EXCLUDED.lessons`,
		d.ID, d.PeriodType, d.PeriodStart, d.PeriodEnd,
		d.TotalChats, d.TotalInteractions,
		d.AvgEscalationRate, d.AvgEmptyRate, d.AvgResponseMs,
		d.Summary, pq.Array(d.KeyEvents), pq.Array(d.Trends), pq.Array(d.Lessons),
	)
	if err != nil {
		return fmt.Errorf("insert digest: %w", err)
	}
	return nil
}

// GetDigestForPeriod returns a specific digest.
func GetDigestForPeriod(db *sql.DB, periodType string, periodStart time.Time) (*models.DirectorDigest, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var d models.DirectorDigest
	err := db.QueryRowContext(ctx, `
		SELECT id, period_type, period_start, period_end,
		       total_chats, total_interactions,
		       avg_escalation_rate, avg_empty_rate, avg_response_ms,
		       summary, key_events, trends, lessons, created_at
		FROM director_digests
		WHERE period_type = $1 AND period_start = $2`,
		periodType, periodStart,
	).Scan(&d.ID, &d.PeriodType, &d.PeriodStart, &d.PeriodEnd,
		&d.TotalChats, &d.TotalInteractions,
		&d.AvgEscalationRate, &d.AvgEmptyRate, &d.AvgResponseMs,
		&d.Summary, pq.Array(&d.KeyEvents), pq.Array(&d.Trends), pq.Array(&d.Lessons), &d.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get digest: %w", err)
	}
	return &d, nil
}

// ListDigests returns recent digests, optionally filtered by period type.
func ListDigests(db *sql.DB, periodType string, limit int) ([]models.DirectorDigest, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 50 {
		limit = 10
	}

	var rows *sql.Rows
	var err error

	if periodType != "" {
		rows, err = db.QueryContext(ctx, `
			SELECT id, period_type, period_start, period_end,
			       total_chats, total_interactions,
			       avg_escalation_rate, avg_empty_rate, avg_response_ms,
			       summary, key_events, trends, lessons, created_at
			FROM director_digests
			WHERE period_type = $1
			ORDER BY period_start DESC
			LIMIT $2`, periodType, limit)
	} else {
		rows, err = db.QueryContext(ctx, `
			SELECT id, period_type, period_start, period_end,
			       total_chats, total_interactions,
			       avg_escalation_rate, avg_empty_rate, avg_response_ms,
			       summary, key_events, trends, lessons, created_at
			FROM director_digests
			ORDER BY period_start DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list digests: %w", err)
	}
	defer rows.Close()

	var digests []models.DirectorDigest
	for rows.Next() {
		var d models.DirectorDigest
		if err := rows.Scan(&d.ID, &d.PeriodType, &d.PeriodStart, &d.PeriodEnd,
			&d.TotalChats, &d.TotalInteractions,
			&d.AvgEscalationRate, &d.AvgEmptyRate, &d.AvgResponseMs,
			&d.Summary, pq.Array(&d.KeyEvents), pq.Array(&d.Trends), pq.Array(&d.Lessons), &d.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan digest: %w", err)
		}
		digests = append(digests, d)
	}
	return digests, rows.Err()
}

// ParsePeriod parses a period string into from/to times.
// Supports: "last_week", "last_month", "last_quarter", "last_year", "YYYY-MM", "YYYY".
func ParsePeriod(period string) (from, to time.Time, err error) {
	now := time.Now()

	switch period {
	case "last_week":
		to = now
		from = now.AddDate(0, 0, -7)
	case "last_month":
		to = now
		from = now.AddDate(0, -1, 0)
	case "last_quarter":
		to = now
		from = now.AddDate(0, -3, 0)
	case "last_year":
		to = now
		from = now.AddDate(-1, 0, 0)
	default:
		if t, e := time.Parse("2006-01", period); e == nil {
			from = t
			to = t.AddDate(0, 1, 0)
			return
		}
		if t, e := time.Parse("2006", period); e == nil {
			from = t
			to = t.AddDate(1, 0, 0)
			return
		}
		err = fmt.Errorf("unknown period: %s (use: last_week, last_month, last_quarter, last_year, YYYY-MM, YYYY)", period)
	}
	return
}

// CountChatsInPeriod counts distinct chats with interactions in a period.
func CountChatsInPeriod(db *sql.DB, from, to time.Time) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT chat_id)
		FROM interaction_metrics
		WHERE created_at BETWEEN $1 AND $2`,
		from, to,
	).Scan(&count)
	return count, err
}

// GetWeekStart returns the Monday of the week containing t.
func GetWeekStart(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	return time.Date(t.Year(), t.Month(), t.Day()-(weekday-1), 0, 0, 0, 0, t.Location())
}

// GetMonthStart returns the first day of the month.
func GetMonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}
