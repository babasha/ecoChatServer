package director

import (
	"log"
	"strings"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/database/queries"
	"github.com/google/uuid"
)

// HebbianGraph manages the associative concept graph for Director's memory.
// Concepts that co-occur in memory tags strengthen their edge weight over time,
// enabling discovery of operational correlations (e.g., "firmware_1.3" ↔ "sensor_offline").
type HebbianGraph struct {
	enabled bool
}

// ScoredConcept is a concept node with its association score to query seeds.
type ScoredConcept struct {
	Label      string
	Category   string
	Score      float64 // EdgeWeight * NodeActivation
	EdgeWeight float64
}

// NewHebbianGraph creates a HebbianGraph, enabled by DIRECTOR_HEBBIAN_ENABLED setting.
func NewHebbianGraph() *HebbianGraph {
	enabled := database.GetSetting("DIRECTOR_HEBBIAN_ENABLED", "true") == "true"
	return &HebbianGraph{enabled: enabled}
}

// Enabled returns whether the Hebbian graph is active.
func (g *HebbianGraph) Enabled() bool {
	return g.enabled
}

// ============================================================================
// Concept extraction
// ============================================================================

// knownPrefixes are the prefixes used when constructing concept labels from tags.
var knownPrefixes = []string{
	"device:", "issue:", "model:", "resolution:", "sentiment:",
	"tool:", "event:", "category:", "source:",
}

// ExtractConcepts normalizes raw memory tags into canonical concept labels.
// Tags already containing a known prefix are passed through; bare words get classified.
func (g *HebbianGraph) ExtractConcepts(tags []string) []queries.ConceptNodeInput {
	seen := make(map[string]struct{}, len(tags))
	result := make([]queries.ConceptNodeInput, 0, len(tags))

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}

		label, category := classifyTag(tag)
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		result = append(result, queries.ConceptNodeInput{Label: label, Category: category})
	}
	return result
}

// classifyTag returns (label, category) for a raw tag string.
func classifyTag(tag string) (label, category string) {
	lower := strings.ToLower(tag)

	// Already prefixed — extract category from prefix
	for _, prefix := range knownPrefixes {
		if strings.HasPrefix(lower, prefix) {
			cat := strings.TrimSuffix(prefix, ":")
			return lower, cat
		}
	}

	// Bare escalation / event keywords
	switch lower {
	case "escalation", "escalate":
		return "escalation", "event"
	case "empty_response", "empty-response":
		return "empty_response", "event"
	case "tool_failure", "tool-failure":
		return "tool_failure", "event"
	case "directive", "lesson", "observation":
		return lower, "event"
	case "positive", "negative", "neutral", "frustrated", "satisfied":
		return "sentiment:" + lower, "sentiment"
	}

	// Default: bare concept
	return lower, "concept"
}

// ============================================================================
// Core Hebbian operations
// ============================================================================

// ReinforceConcepts upserts all concept nodes from the given tags and reinforces
// edges between all co-occurring pairs. Called asynchronously from SaveAutoMemory.
func (g *HebbianGraph) ReinforceConcepts(tags []string) {
	if !g.enabled || len(tags) < 2 {
		return
	}

	concepts := g.ExtractConcepts(tags)
	if len(concepts) < 2 {
		return
	}

	if err := database.HebbianReinforceConcepts(concepts); err != nil {
		log.Printf("[HEBBIAN] ReinforceConcepts error: %v", err)
		return
	}
	log.Printf("[HEBBIAN] Reinforced %d concepts, %d edges", len(concepts), len(concepts)*(len(concepts)-1)/2)
}

// GetAssociatedConcepts returns the top-N concept nodes most strongly associated
// with the given seed labels via 1-hop spreading activation.
func (g *HebbianGraph) GetAssociatedConcepts(seeds []string, topN int) []ScoredConcept {
	if !g.enabled || len(seeds) == 0 {
		return nil
	}

	// Resolve seed labels to node IDs
	nodes, err := database.HebbianGetConceptNodesByLabels(seeds)
	if err != nil || len(nodes) == 0 {
		return nil
	}

	seedIDs := make([]uuid.UUID, len(nodes))
	for i, n := range nodes {
		seedIDs[i] = n.ID
	}

	neighbors, err := database.HebbianGetNeighbors(seedIDs, seedIDs, topN)
	if err != nil {
		log.Printf("[HEBBIAN] GetNeighbors error: %v", err)
		return nil
	}

	result := make([]ScoredConcept, len(neighbors))
	for i, nb := range neighbors {
		result[i] = ScoredConcept{
			Label:      nb.Label,
			Category:   nb.Category,
			Score:      nb.Score,
			EdgeWeight: nb.EdgeWeight,
		}
	}
	return result
}

// BoostRecallQuery returns a map[conceptLabel]score for all concepts associated with
// the given query tokens. Used by RecallMemoriesHybrid to boost relevant memories.
func (g *HebbianGraph) BoostRecallQuery(queryTags []string, maxExpansion int) map[string]float64 {
	if !g.enabled || len(queryTags) == 0 {
		return nil
	}

	// Normalize query tokens into concept labels
	normalized := make([]string, 0, len(queryTags))
	for _, t := range queryTags {
		label, _ := classifyTag(t)
		normalized = append(normalized, label)
	}

	associated := g.GetAssociatedConcepts(normalized, maxExpansion)
	if len(associated) == 0 {
		return nil
	}

	boosts := make(map[string]float64, len(associated))
	for _, sc := range associated {
		boosts[sc.Label] = sc.Score
	}
	return boosts
}

// ============================================================================
// Decay & maintenance
// ============================================================================

// DecayEdges applies exponential decay to concept edges (half-life 14 days).
// Returns (decayed, pruned, error).
func (g *HebbianGraph) DecayEdges() (int64, int64, error) {
	if !g.enabled {
		return 0, 0, nil
	}
	return database.HebbianDecayEdges(14.0, 0.1)
}

// DecayNodes applies activation decay to concept nodes (half-life 30 days).
// Orphan nodes below threshold are deleted.
// Returns (decayed, pruned, error).
func (g *HebbianGraph) DecayNodes() (int64, int64, error) {
	if !g.enabled {
		return 0, 0, nil
	}
	return database.HebbianDecayNodes(30.0, 0.05)
}

// ============================================================================
// Stats & diagnostics
// ============================================================================

// GetGraphStats returns (nodes, edges, avgWeight, error).
func (g *HebbianGraph) GetGraphStats() (int, int, float64, error) {
	return database.HebbianGetGraphStats()
}

// QueryGraph returns a human-readable summary of concepts associated with a label.
// depth=1 for immediate neighbors, depth=2 for 2-hop (expensive, use sparingly).
func (g *HebbianGraph) QueryGraph(concept string, depth int) string {
	if !g.enabled {
		return "[HEBBIAN] graph disabled"
	}

	associated := g.GetAssociatedConcepts([]string{concept}, 20)
	if len(associated) == 0 {
		return "[HEBBIAN] no associations found for: " + concept
	}

	var sb strings.Builder
	sb.WriteString("Associations for \"")
	sb.WriteString(concept)
	sb.WriteString("\":\n")
	for _, sc := range associated {
		sb.WriteString("  → ")
		sb.WriteString(sc.Label)
		sb.WriteString(" (")
		sb.WriteString(sc.Category)
		sb.WriteString(") score=")
		sb.WriteString(formatFloat(sc.Score))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// formatFloat formats a float64 to 3 decimal places without importing fmt.
func formatFloat(f float64) string {
	// Use strconv via a simple helper to avoid fmt dependency in hot paths
	i := int(f * 1000)
	neg := ""
	if i < 0 {
		neg = "-"
		i = -i
	}
	whole := i / 1000
	frac := i % 1000
	return neg + itoa(whole) + "." + pad3(frac)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := 19
	for n > 0 {
		buf[pos] = byte('0' + n%10)
		n /= 10
		pos--
	}
	return string(buf[pos+1:])
}

func pad3(n int) string {
	s := itoa(n)
	for len(s) < 3 {
		s = "0" + s
	}
	return s
}
