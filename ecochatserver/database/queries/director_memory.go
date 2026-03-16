package queries

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ============================================================================
// Memory CRUD
// ============================================================================

// UpsertMemory inserts or updates a memory entry (by category+key).
// search_vector is auto-populated by DB trigger.
// If m.Embedding is set, it is stored; on conflict, existing embedding is preserved if new one is nil.
func UpsertMemory(db *sql.DB, m *models.DirectorMemory) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	var embeddingArg interface{}
	if len(m.Embedding) > 0 {
		embeddingArg = pq.Float64Array(m.Embedding)
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO director_memories
			(id, category, key, content, importance, access_count, source, tags, pinned, embedding, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $8, $9, $10, NOW(), NOW())
		ON CONFLICT (category, key) DO UPDATE SET
			content = EXCLUDED.content,
			importance = EXCLUDED.importance,
			source = EXCLUDED.source,
			tags = EXCLUDED.tags,
			pinned = EXCLUDED.pinned,
			embedding = COALESCE(EXCLUDED.embedding, director_memories.embedding),
			expires_at = EXCLUDED.expires_at,
			updated_at = NOW()`,
		m.ID, m.Category, m.Key, m.Content, m.Importance, m.Source,
		pq.Array(m.Tags), m.Pinned, embeddingArg, m.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("upsert memory: %w", err)
	}
	return nil
}

// DeleteMemory removes a memory by category+key.
func DeleteMemory(db *sql.DB, category, key string) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	result, err := db.ExecContext(ctx, `
		DELETE FROM director_memories WHERE category = $1 AND key = $2`,
		category, key,
	)
	if err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("memory not found: %s/%s", category, key)
	}
	return nil
}

// ============================================================================
// Memory Search — FTS (full-text) + LIKE fallback
// ============================================================================

// RecallMemories searches memories using PostgreSQL FTS (russian config).
// Falls back to LIKE if FTS returns no results (handles mixed-language queries).
// Bumps access_count for returned results.
func RecallMemories(db *sql.DB, query string, category string, limit int) ([]models.DirectorMemory, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	// Try FTS first
	memories, err := recallWithFTS(db, query, category, limit)
	if err != nil {
		return nil, err
	}

	// Fallback to LIKE if FTS found nothing (handles partial words, english, mixed queries)
	if len(memories) == 0 && query != "" {
		memories, err = recallWithLike(db, query, category, limit)
		if err != nil {
			return nil, err
		}
	}

	// Bump access_count for recalled memories
	if len(memories) > 0 {
		ids := make([]uuid.UUID, len(memories))
		for i, m := range memories {
			ids[i] = m.ID
		}
		go bumpAccessCount(db, ids)
	}

	return memories, nil
}

func recallWithFTS(db *sql.DB, query string, category string, limit int) ([]models.DirectorMemory, error) {
	if query == "" {
		// No query text — just list by category/importance
		return ListMemories(db, category, limit)
	}

	ctx, cancel := WithDBContext()
	defer cancel()

	conditions := []string{
		"(expires_at IS NULL OR expires_at > NOW())",
		"search_vector @@ plainto_tsquery('russian', $1)",
	}
	args := []interface{}{query}
	argIdx := 2

	if category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argIdx))
		args = append(args, category)
		argIdx++
	}

	args = append(args, limit)

	sqlQuery := fmt.Sprintf(`
		SELECT id, category, key, content, importance, access_count, source, tags, pinned, expires_at, created_at, updated_at,
		       ts_rank(search_vector, plainto_tsquery('russian', $1)) AS rank
		FROM director_memories
		WHERE %s
		ORDER BY rank DESC, importance DESC, updated_at DESC
		LIMIT $%d`,
		strings.Join(conditions, " AND "), argIdx,
	)

	return scanMemoriesWithRank(db, ctx, sqlQuery, args)
}

func recallWithLike(db *sql.DB, query string, category string, limit int) ([]models.DirectorMemory, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	searchPattern := "%" + strings.ToLower(query) + "%"

	conditions := []string{
		"(expires_at IS NULL OR expires_at > NOW())",
		"(LOWER(content) LIKE $1 OR LOWER(key) LIKE $1 OR EXISTS (SELECT 1 FROM unnest(tags) t WHERE LOWER(t) LIKE $1))",
	}
	args := []interface{}{searchPattern}
	argIdx := 2

	if category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argIdx))
		args = append(args, category)
		argIdx++
	}

	args = append(args, limit)

	sqlQuery := fmt.Sprintf(`
		SELECT id, category, key, content, importance, access_count, source, tags, pinned, expires_at, created_at, updated_at,
		       0::float AS rank
		FROM director_memories
		WHERE %s
		ORDER BY importance DESC, updated_at DESC
		LIMIT $%d`,
		strings.Join(conditions, " AND "), argIdx,
	)

	return scanMemoriesWithRank(db, ctx, sqlQuery, args)
}

func scanMemoriesWithRank(db *sql.DB, ctx context.Context, query string, args []interface{}) ([]models.DirectorMemory, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("recall memories: %w", err)
	}
	defer rows.Close()

	var memories []models.DirectorMemory
	for rows.Next() {
		var m models.DirectorMemory
		if err := rows.Scan(&m.ID, &m.Category, &m.Key, &m.Content, &m.Importance,
			&m.AccessCount, &m.Source, pq.Array(&m.Tags), &m.Pinned, &m.ExpiresAt,
			&m.CreatedAt, &m.UpdatedAt, &m.Rank); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// bumpAccessCount increments access_count for given IDs (fire-and-forget).
func bumpAccessCount(db *sql.DB, ids []uuid.UUID) {
	ctx, cancel := WithDBContext()
	defer cancel()

	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	db.ExecContext(ctx, fmt.Sprintf(`
		UPDATE director_memories SET access_count = access_count + 1
		WHERE id IN (%s)`, strings.Join(placeholders, ",")), args...)
}

// ============================================================================
// Memory Listing & Stats
// ============================================================================

// ListMemories returns memories for a category, sorted by importance.
func ListMemories(db *sql.DB, category string, limit int) ([]models.DirectorMemory, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 50 {
		limit = 10
	}

	var query string
	var args []interface{}

	if category != "" {
		query = `
			SELECT id, category, key, content, importance, access_count, source, tags, pinned, expires_at, created_at, updated_at
			FROM director_memories
			WHERE category = $1 AND (expires_at IS NULL OR expires_at > NOW())
			ORDER BY importance DESC, updated_at DESC
			LIMIT $2`
		args = []interface{}{category, limit}
	} else {
		query = `
			SELECT id, category, key, content, importance, access_count, source, tags, pinned, expires_at, created_at, updated_at
			FROM director_memories
			WHERE expires_at IS NULL OR expires_at > NOW()
			ORDER BY importance DESC, updated_at DESC
			LIMIT $1`
		args = []interface{}{limit}
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()

	var memories []models.DirectorMemory
	for rows.Next() {
		var m models.DirectorMemory
		if err := rows.Scan(&m.ID, &m.Category, &m.Key, &m.Content, &m.Importance,
			&m.AccessCount, &m.Source, pq.Array(&m.Tags), &m.Pinned, &m.ExpiresAt,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetHotMemories returns top memories for context injection (compact).
// Returns: top N by importance from decisions+patterns+insights (+ all pinned).
func GetHotMemories(db *sql.DB, limit int) ([]models.DirectorMemory, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 10 {
		limit = 5
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, category, key, content, importance, access_count, source, tags, pinned, expires_at, created_at, updated_at
		FROM director_memories
		WHERE (category IN ('decision', 'pattern', 'insight') OR pinned = TRUE)
		  AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY pinned DESC, importance DESC, updated_at DESC
		LIMIT $1`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get hot memories: %w", err)
	}
	defer rows.Close()

	var memories []models.DirectorMemory
	for rows.Next() {
		var m models.DirectorMemory
		if err := rows.Scan(&m.ID, &m.Category, &m.Key, &m.Content, &m.Importance,
			&m.AccessCount, &m.Source, pq.Array(&m.Tags), &m.Pinned, &m.ExpiresAt,
			&m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan hot memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetMemoryStats returns counts per category (for Level 0 metadata injection).
func GetMemoryStats(db *sql.DB) (map[string]int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT category, COUNT(*)
		FROM director_memories
		WHERE expires_at IS NULL OR expires_at > NOW()
		GROUP BY category
		ORDER BY category`)
	if err != nil {
		return nil, fmt.Errorf("get memory stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			return nil, err
		}
		stats[cat] = count
	}
	return stats, rows.Err()
}

// ============================================================================
// Decay & Maintenance
// ============================================================================

// DecayMemories applies exponential decay to old unused memories.
// Formula inspired by OpenClaw: importance *= e^(-ln2/30 * ageDays)
// Pinned memories are never decayed. Called periodically (every 24h).
func DecayMemories(db *sql.DB) (int64, int64, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	// Exponential decay: half-life 30 days, only for non-pinned, non-accessed, older than 7 days
	decayResult, err := db.ExecContext(ctx, `
		UPDATE director_memories
		SET importance = GREATEST(1, ROUND(
			importance * EXP(-0.693 / 30.0 * EXTRACT(EPOCH FROM NOW() - updated_at) / 86400.0)
		)::int),
		updated_at = NOW()
		WHERE pinned = FALSE
		  AND access_count = 0
		  AND updated_at < NOW() - INTERVAL '7 days'
		  AND importance > 1`)
	if err != nil {
		return 0, 0, fmt.Errorf("decay memories: %w", err)
	}
	decayed, _ := decayResult.RowsAffected()

	// Purge: delete non-pinned memories with importance <= 0 or expired
	purgeResult, err := db.ExecContext(ctx, `
		DELETE FROM director_memories
		WHERE (importance <= 0 AND pinned = FALSE)
		   OR (expires_at IS NOT NULL AND expires_at <= NOW())`)
	if err != nil {
		return decayed, 0, fmt.Errorf("purge memories: %w", err)
	}
	purged, _ := purgeResult.RowsAffected()

	return decayed, purged, nil
}

// ============================================================================
// Helpers
// ============================================================================

// CountMemories returns total active memory count.
func CountMemories(db *sql.DB) (int, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var count int
	err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM director_memories
		WHERE expires_at IS NULL OR expires_at > NOW()`).Scan(&count)
	return count, err
}

// SaveAutoMemory is a convenience for programmatic memory saves (from analysis).
func SaveAutoMemory(db *sql.DB, category, key, content string, tags []string, expiresAt *time.Time) error {
	m := &models.DirectorMemory{
		ID:         uuid.New(),
		Category:   category,
		Key:        key,
		Content:    content,
		Importance: 7,
		Source:     "analysis",
		Tags:       tags,
		ExpiresAt:  expiresAt,
	}
	return UpsertMemory(db, m)
}

// ============================================================================
// Hybrid Search — FTS + Vector similarity + MMR diversity
// ============================================================================

// RecallMemoriesHybrid performs hybrid search combining FTS and vector similarity.
// If queryEmbedding is nil/empty, falls back to pure FTS search.
// Results are re-ranked using MMR (Maximal Marginal Relevance) for diversity.
func RecallMemoriesHybrid(db *sql.DB, query string, queryEmbedding []float64, category string, limit int) ([]models.DirectorMemory, error) {
	// Fallback to pure FTS if no embedding available
	if len(queryEmbedding) == 0 {
		return RecallMemories(db, query, category, limit)
	}

	if limit <= 0 || limit > 20 {
		limit = 5
	}

	// 1. Fetch candidate memories with FTS rank and embeddings
	candidates, err := fetchCandidatesForHybrid(db, query, category)
	if err != nil {
		log.Printf("[HYBRID_SEARCH] Fetch candidates error: %v, falling back to FTS", err)
		return RecallMemories(db, query, category, limit)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// 2. Compute hybrid scores
	for i := range candidates {
		// BM25-style FTS normalization: r/(1+r) maps ts_rank to 0..1 independently
		// (unlike max-based normalization, this doesn't inflate scores when few results match)
		ftsNorm := candidates[i].Rank / (1.0 + candidates[i].Rank)

		// Cosine similarity between query and memory embedding
		vecScore := cosineSim(queryEmbedding, candidates[i].Embedding)

		// Hybrid: 70% vector + 30% FTS
		candidates[i].HybridScore = 0.7*vecScore + 0.3*ftsNorm

		// Temporal decay at search time: half-life 30 days, exempt pinned memories
		// Score multiplier only — does NOT modify stored importance
		if !candidates[i].Pinned {
			ageDays := time.Since(candidates[i].UpdatedAt).Hours() / 24.0
			if ageDays > 1 {
				candidates[i].HybridScore *= math.Exp(-math.Ln2 / 30.0 * ageDays)
			}
		}

		// Boost pinned memories
		if candidates[i].Pinned {
			candidates[i].HybridScore *= 1.2
		}

		// Small boost for importance
		candidates[i].HybridScore += float64(candidates[i].Importance) * 0.01
	}

	// 3. Sort by hybrid score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].HybridScore > candidates[j].HybridScore
	})

	// 4. Apply MMR for diversity (lambda=0.7 — balanced relevance/diversity)
	selected := applyMMR(candidates, limit, 0.7)

	// 5. Bump access count
	if len(selected) > 0 {
		ids := make([]uuid.UUID, len(selected))
		for i, m := range selected {
			ids[i] = m.ID
		}
		go bumpAccessCount(db, ids)
	}

	return selected, nil
}

// fetchCandidatesForHybrid returns all non-expired memories with embeddings and FTS scores.
// For small tables (<10K rows) this is efficient enough.
func fetchCandidatesForHybrid(db *sql.DB, query string, category string) ([]models.DirectorMemory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conditions := []string{
		"(expires_at IS NULL OR expires_at > NOW())",
	}
	args := []interface{}{query}
	argIdx := 2

	if category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argIdx))
		args = append(args, category)
		argIdx++
	}

	// Fetch memories with FTS rank (0 if no FTS match) and embedding
	sqlQuery := fmt.Sprintf(`
		SELECT id, category, key, content, importance, access_count, source, tags,
		       pinned, embedding, expires_at, created_at, updated_at,
		       CASE WHEN search_vector @@ plainto_tsquery('russian', $1)
		            THEN ts_rank(search_vector, plainto_tsquery('russian', $1))
		            ELSE 0
		       END AS fts_rank
		FROM director_memories
		WHERE %s
		ORDER BY importance DESC
		LIMIT 1000`,
		strings.Join(conditions, " AND "),
	)

	rows, err := db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch hybrid candidates: %w", err)
	}
	defer rows.Close()

	var candidates []models.DirectorMemory
	for rows.Next() {
		var m models.DirectorMemory
		var embedding pq.Float64Array

		if err := rows.Scan(&m.ID, &m.Category, &m.Key, &m.Content, &m.Importance,
			&m.AccessCount, &m.Source, pq.Array(&m.Tags), &m.Pinned,
			&embedding, &m.ExpiresAt, &m.CreatedAt, &m.UpdatedAt, &m.Rank); err != nil {
			return nil, fmt.Errorf("scan hybrid candidate: %w", err)
		}

		if len(embedding) > 0 {
			m.Embedding = []float64(embedding)
		}
		candidates = append(candidates, m)
	}

	return candidates, rows.Err()
}

// applyMMR applies Maximal Marginal Relevance re-ranking for diversity.
// lambda controls relevance vs diversity tradeoff (1.0 = pure relevance, 0.0 = pure diversity).
func applyMMR(candidates []models.DirectorMemory, k int, lambda float64) []models.DirectorMemory {
	if len(candidates) <= k {
		return candidates
	}

	selected := make([]models.DirectorMemory, 0, k)
	remaining := make([]int, len(candidates))
	for i := range remaining {
		remaining[i] = i
	}

	for len(selected) < k && len(remaining) > 0 {
		bestScore := math.Inf(-1)
		bestIdx := -1

		for ri, ci := range remaining {
			relevance := candidates[ci].HybridScore

			// Max similarity to already selected items
			maxSim := 0.0
			for _, s := range selected {
				sim := cosineSim(candidates[ci].Embedding, s.Embedding)
				if sim > maxSim {
					maxSim = sim
				}
			}

			// MMR score: balance relevance and diversity
			mmrScore := lambda*relevance - (1-lambda)*maxSim

			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = ri
			}
		}

		if bestIdx < 0 {
			break
		}

		selected = append(selected, candidates[remaining[bestIdx]])
		// Remove selected from remaining
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}

	return selected
}

// cosineSim computes cosine similarity between two vectors.
func cosineSim(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ============================================================================
// Director Reports Search — FTS across all historical reports
// ============================================================================

// SearchReports searches director reports by text query using FTS.
// Returns matching reports sorted by relevance, with LIKE fallback.
func SearchReports(db *sql.DB, query string, limit int) ([]models.DirectorReport, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	// Try FTS first
	reports, err := searchReportsFTS(db, query, limit)
	if err != nil {
		return nil, err
	}

	// Fallback to LIKE if FTS found nothing
	if len(reports) == 0 && query != "" {
		reports, err = searchReportsLike(db, query, limit)
		if err != nil {
			return nil, err
		}
	}

	return reports, nil
}

func searchReportsFTS(db *sql.DB, query string, limit int) ([]models.DirectorReport, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, report_date, report_type, trigger_event, summary_count, analysis,
		       directives, applied, created_at,
		       COALESCE(customer_complaints, '[]'), COALESCE(key_observations, '[]'),
		       COALESCE(prompt_changes, '[]'), COALESCE(expectations, ''),
		       ts_rank(search_vector, plainto_tsquery('russian', $1)) AS rank
		FROM director_reports
		WHERE search_vector @@ plainto_tsquery('russian', $1)
		ORDER BY rank DESC, created_at DESC
		LIMIT $2`, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search reports FTS: %w", err)
	}
	defer rows.Close()

	return scanReportRows(rows)
}

func searchReportsLike(db *sql.DB, query string, limit int) ([]models.DirectorReport, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	pattern := "%" + strings.ToLower(query) + "%"
	rows, err := db.QueryContext(ctx, `
		SELECT id, report_date, report_type, trigger_event, summary_count, analysis,
		       directives, applied, created_at,
		       COALESCE(customer_complaints, '[]'), COALESCE(key_observations, '[]'),
		       COALESCE(prompt_changes, '[]'), COALESCE(expectations, ''),
		       0::float AS rank
		FROM director_reports
		WHERE LOWER(analysis) LIKE $1 OR LOWER(expectations) LIKE $1 OR LOWER(trigger_event) LIKE $1
		ORDER BY created_at DESC
		LIMIT $2`, pattern, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("search reports LIKE: %w", err)
	}
	defer rows.Close()

	return scanReportRows(rows)
}

// GetReportsByDateRange returns reports within a date range.
func GetReportsByDateRange(db *sql.DB, from, to time.Time, limit int) ([]models.DirectorReport, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, report_date, report_type, trigger_event, summary_count, analysis,
		       directives, applied, created_at,
		       COALESCE(customer_complaints, '[]'), COALESCE(key_observations, '[]'),
		       COALESCE(prompt_changes, '[]'), COALESCE(expectations, ''),
		       0::float AS rank
		FROM director_reports
		WHERE created_at BETWEEN $1 AND $2
		ORDER BY created_at DESC
		LIMIT $3`, from, to, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get reports by date range: %w", err)
	}
	defer rows.Close()

	return scanReportRows(rows)
}

// GetReportsByDateRangeAndType returns reports within a date range filtered by report_type.
func GetReportsByDateRangeAndType(db *sql.DB, from, to time.Time, reportType string, limit int) ([]models.DirectorReport, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, report_date, report_type, trigger_event, summary_count, analysis,
		       directives, applied, created_at,
		       COALESCE(customer_complaints, '[]'), COALESCE(key_observations, '[]'),
		       COALESCE(prompt_changes, '[]'), COALESCE(expectations, ''),
		       0::float AS rank
		FROM director_reports
		WHERE created_at BETWEEN $1 AND $2
		  AND report_type = $3
		ORDER BY created_at DESC
		LIMIT $4`, from, to, reportType, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get reports by date range and type: %w", err)
	}
	defer rows.Close()

	return scanReportRows(rows)
}

func scanReportRows(rows *sql.Rows) ([]models.DirectorReport, error) {
	var reports []models.DirectorReport
	for rows.Next() {
		var r models.DirectorReport
		var directivesJSON, complaintsJSON, observationsJSON, promptChangesJSON []byte
		var rank float64

		if err := rows.Scan(&r.ID, &r.ReportDate, &r.ReportType, &r.TriggerEvent,
			&r.SummaryCount, &r.Analysis, &directivesJSON, &r.Applied, &r.CreatedAt,
			&complaintsJSON, &observationsJSON, &promptChangesJSON, &r.Expectations,
			&rank); err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}

		if directivesJSON != nil {
			json.Unmarshal(directivesJSON, &r.Directives)
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

		reports = append(reports, r)
	}
	return reports, rows.Err()
}
