package queries

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ============================================================================
// Hebbian Associative Graph — data access layer
//
// concept_nodes: normalized concepts extracted from memory tags
// concept_edges: weighted associations between co-occurring concepts
// ============================================================================

// ConceptNode represents a node in the Hebbian associative graph.
type ConceptNode struct {
	ID         uuid.UUID
	Label      string    // canonical concept: "device:Zefir Pro", "issue:firmware", "escalation"
	Category   string    // namespace: "device", "issue", "event", "sentiment", "tool", ...
	Activation float64   // current activation level (1.0 = fresh, decays over time)
	HitCount   int       // total number of times this concept was observed
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ConceptEdge represents a weighted association between two concept nodes.
type ConceptEdge struct {
	ID        uuid.UUID
	SourceID  uuid.UUID
	TargetID  uuid.UUID
	Weight    float64   // Hebbian weight, bounded [0, 10], grows on co-occurrence
	CoOccur   int       // raw co-occurrence count
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NeighborResult is a concept node with its association score to the query.
type NeighborResult struct {
	NodeID     uuid.UUID
	Label      string
	Category   string
	Activation float64
	EdgeWeight float64
	Score      float64 // EdgeWeight * Activation
}

// ============================================================================
// Node operations
// ============================================================================

// UpsertConceptNode inserts a concept or increments hit_count + refreshes activation.
// Returns the node's UUID.
func UpsertConceptNode(db *sql.DB, label, category string) (uuid.UUID, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	var id uuid.UUID
	err := db.QueryRowContext(ctx, `
		INSERT INTO concept_nodes (id, label, category, activation, hit_count, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 1.0, 1, NOW(), NOW())
		ON CONFLICT (label) DO UPDATE SET
			hit_count  = concept_nodes.hit_count + 1,
			activation = LEAST(concept_nodes.activation + 0.1, 1.0),
			updated_at = NOW()
		RETURNING id`,
		label, category,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upsert concept node %q: %w", label, err)
	}
	return id, nil
}

// GetConceptNodesByLabels batch-fetches nodes by their labels.
func GetConceptNodesByLabels(db *sql.DB, labels []string) ([]ConceptNode, error) {
	if len(labels) == 0 {
		return nil, nil
	}

	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, label, category, activation, hit_count, created_at, updated_at
		FROM concept_nodes
		WHERE label = ANY($1)`,
		pq.Array(labels),
	)
	if err != nil {
		return nil, fmt.Errorf("get concept nodes by labels: %w", err)
	}
	defer rows.Close()

	var nodes []ConceptNode
	for rows.Next() {
		var n ConceptNode
		if err := rows.Scan(&n.ID, &n.Label, &n.Category, &n.Activation, &n.HitCount, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan concept node: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// ============================================================================
// Edge operations
// ============================================================================

// UpsertConceptEdge inserts or reinforces an edge using the bounded Hebbian rule:
//
//	w_new = w_old + lr * (1 - w_old / w_max)
//
// where lr=learningRate and w_max=10.0.
// Edges are stored with the lexicographically smaller UUID as source to enforce uniqueness.
func UpsertConceptEdge(db *sql.DB, idA, idB uuid.UUID, learningRate float64) error {
	ctx, cancel := WithDBContext()
	defer cancel()

	// Canonical ordering: smaller UUID is always source
	sourceID, targetID := idA, idB
	if strings.Compare(idA.String(), idB.String()) > 0 {
		sourceID, targetID = idB, idA
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO concept_edges (id, source_id, target_id, weight, co_occur, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, NOW(), NOW())
		ON CONFLICT (source_id, target_id) DO UPDATE SET
			weight   = concept_edges.weight + $3 * (1.0 - concept_edges.weight / 10.0),
			co_occur = concept_edges.co_occur + 1,
			updated_at = NOW()`,
		sourceID, targetID, learningRate,
	)
	if err != nil {
		return fmt.Errorf("upsert concept edge %s↔%s: %w", sourceID, targetID, err)
	}
	return nil
}

// ============================================================================
// Graph traversal
// ============================================================================

// GetNeighbors returns the top-N concept nodes most strongly associated with
// the given seed nodes (1-hop spreading activation), excluding seed nodes themselves.
// Results are ordered by EdgeWeight * Activation DESC.
func GetNeighbors(db *sql.DB, nodeIDs []uuid.UUID, excludeIDs []uuid.UUID, limit int) ([]NeighborResult, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	ctx, cancel := WithDBContext()
	defer cancel()

	// Build exclusion list: seed nodes + explicitly excluded
	excludeSet := make(map[uuid.UUID]struct{}, len(nodeIDs)+len(excludeIDs))
	for _, id := range nodeIDs {
		excludeSet[id] = struct{}{}
	}
	for _, id := range excludeIDs {
		excludeSet[id] = struct{}{}
	}
	exclude := make([]uuid.UUID, 0, len(excludeSet))
	for id := range excludeSet {
		exclude = append(exclude, id)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT
			cn.id,
			cn.label,
			cn.category,
			cn.activation,
			MAX(ce.weight) AS edge_weight
		FROM concept_edges ce
		JOIN concept_nodes cn ON cn.id = CASE
			WHEN ce.source_id = ANY($1::uuid[]) THEN ce.target_id
			ELSE ce.source_id
		END
		WHERE (ce.source_id = ANY($1::uuid[]) OR ce.target_id = ANY($1::uuid[]))
		  AND cn.id != ALL($2::uuid[])
		GROUP BY cn.id, cn.label, cn.category, cn.activation
		ORDER BY MAX(ce.weight) * cn.activation DESC
		LIMIT $3`,
		pq.Array(nodeIDs), pq.Array(exclude), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get neighbors: %w", err)
	}
	defer rows.Close()

	var results []NeighborResult
	for rows.Next() {
		var r NeighborResult
		if err := rows.Scan(&r.NodeID, &r.Label, &r.Category, &r.Activation, &r.EdgeWeight); err != nil {
			return nil, fmt.Errorf("scan neighbor: %w", err)
		}
		r.Score = r.EdgeWeight * r.Activation
		results = append(results, r)
	}
	return results, rows.Err()
}

// ============================================================================
// Decay & maintenance
// ============================================================================

// DecayConceptEdges applies exponential decay to edges not reinforced recently.
// Half-life is halfLifeDays. Edges below pruneThreshold are deleted.
// Returns (decayed count, pruned count).
func DecayConceptEdges(db *sql.DB, halfLifeDays, pruneThreshold float64) (int64, int64, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	// Decay: w *= exp(-ln2/halfLife * ageDays)
	result, err := db.ExecContext(ctx, `
		UPDATE concept_edges
		SET
			weight     = weight * EXP(-0.693147 / $1 * EXTRACT(EPOCH FROM NOW() - updated_at) / 86400.0),
			updated_at = NOW()
		WHERE updated_at < NOW() - INTERVAL '1 day'
		  AND weight >= $2`,
		halfLifeDays, pruneThreshold,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("decay concept edges: %w", err)
	}
	decayed, _ := result.RowsAffected()

	// Prune weak edges
	pruneResult, err := db.ExecContext(ctx, `
		DELETE FROM concept_edges WHERE weight < $1`,
		pruneThreshold,
	)
	if err != nil {
		return decayed, 0, fmt.Errorf("prune concept edges: %w", err)
	}
	pruned, _ := pruneResult.RowsAffected()

	return decayed, pruned, nil
}

// DecayConceptNodes applies activation decay to nodes not seen recently.
// Orphan nodes (no remaining edges) below pruneThreshold are deleted.
// Returns (decayed count, pruned count).
func DecayConceptNodes(db *sql.DB, halfLifeDays, pruneThreshold float64) (int64, int64, error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	result, err := db.ExecContext(ctx, `
		UPDATE concept_nodes
		SET
			activation = activation * EXP(-0.693147 / $1 * EXTRACT(EPOCH FROM NOW() - updated_at) / 86400.0),
			updated_at = NOW()
		WHERE updated_at < NOW() - INTERVAL '1 day'
		  AND activation >= $2`,
		halfLifeDays, pruneThreshold,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("decay concept nodes: %w", err)
	}
	decayed, _ := result.RowsAffected()

	// Prune orphan nodes with low activation
	pruneResult, err := db.ExecContext(ctx, `
		DELETE FROM concept_nodes
		WHERE activation < $1
		  AND NOT EXISTS (
			SELECT 1 FROM concept_edges
			WHERE source_id = concept_nodes.id OR target_id = concept_nodes.id
		  )`,
		pruneThreshold,
	)
	if err != nil {
		return decayed, 0, fmt.Errorf("prune concept nodes: %w", err)
	}
	pruned, _ := pruneResult.RowsAffected()

	return decayed, pruned, nil
}

// ============================================================================
// Stats
// ============================================================================

// GetConceptGraphStats returns summary statistics for the graph.
func GetConceptGraphStats(db *sql.DB) (nodes int, edges int, avgWeight float64, err error) {
	ctx, cancel := WithDBContext()
	defer cancel()

	row := db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM concept_nodes),
			(SELECT COUNT(*) FROM concept_edges),
			(SELECT COALESCE(AVG(weight), 0) FROM concept_edges)
	`)
	if scanErr := row.Scan(&nodes, &edges, &avgWeight); scanErr != nil {
		err = fmt.Errorf("get concept graph stats: %w", scanErr)
	}
	return
}

// GetTopConceptsByHitCount returns the most frequently observed concepts (for debugging).
func GetTopConceptsByHitCount(db *sql.DB, limit int) ([]ConceptNode, error) {
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, label, category, activation, hit_count, created_at, updated_at
		FROM concept_nodes
		ORDER BY hit_count DESC, activation DESC
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get top concepts: %w", err)
	}
	defer rows.Close()

	var nodes []ConceptNode
	for rows.Next() {
		var n ConceptNode
		if err := rows.Scan(&n.ID, &n.Label, &n.Category, &n.Activation, &n.HitCount, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan top concept: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// GetStrongestEdges returns the edges with the highest weight (for debugging/Director inspection).
func GetStrongestEdges(db *sql.DB, limit int) ([]ConceptEdge, error) {
	if limit <= 0 {
		limit = 20
	}
	ctx, cancel := WithDBContext()
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		SELECT id, source_id, target_id, weight, co_occur, created_at, updated_at
		FROM concept_edges
		ORDER BY weight DESC
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get strongest edges: %w", err)
	}
	defer rows.Close()

	var edges []ConceptEdge
	for rows.Next() {
		var e ConceptEdge
		if err := rows.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Weight, &e.CoOccur, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

// ReinforceConcepts is a convenience function that upserts all concept nodes
// and reinforces edges between all pairs. Called from the Hebbian graph layer.
// learningRate=1.0 for standard co-occurrence.
func ReinforceConcepts(db *sql.DB, concepts []ConceptNodeInput) error {
	if len(concepts) < 2 {
		// Need at least 2 concepts to form an association
		if len(concepts) == 1 {
			_, err := UpsertConceptNode(db, concepts[0].Label, concepts[0].Category)
			return err
		}
		return nil
	}

	// Upsert all nodes and collect their IDs
	ids := make([]uuid.UUID, 0, len(concepts))
	for _, c := range concepts {
		id, err := UpsertConceptNode(db, c.Label, c.Category)
		if err != nil {
			log.Printf("[HEBBIAN] Failed to upsert node %q: %v", c.Label, err)
			continue
		}
		ids = append(ids, id)
	}

	// Reinforce all pairs (N*(N-1)/2 edges)
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if err := UpsertConceptEdge(db, ids[i], ids[j], 1.0); err != nil {
				log.Printf("[HEBBIAN] Failed to upsert edge %s↔%s: %v", ids[i], ids[j], err)
			}
		}
	}

	return nil
}

// ConceptNodeInput is a lightweight input struct for batch concept reinforcement.
type ConceptNodeInput struct {
	Label    string
	Category string
}
