package models

import (
	"time"

	"github.com/google/uuid"
)

// DirectorMemory represents a persistent memory entry for the Director agent.
// Memories are categorized and ranked by importance for efficient context injection.
type DirectorMemory struct {
	ID          uuid.UUID  `json:"id"`
	Category    string     `json:"category"`     // fact, decision, pattern, insight, preference
	Key         string     `json:"key"`          // unique within category for upsert
	Content     string     `json:"content"`      // compact text (<500 chars)
	Importance  int        `json:"importance"`   // 1-10
	AccessCount int        `json:"access_count"` // how many times recalled
	Source      string     `json:"source"`       // chat, analysis, observation, admin
	Tags        []string   `json:"tags"`
	Pinned      bool       `json:"pinned"`       // pinned memories never decay
	Embedding   []float64  `json:"embedding,omitempty"` // vector embedding for semantic search
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Rank        float64    `json:"rank,omitempty"`        // FTS rank (only in search results)
	HybridScore float64   `json:"hybrid_score,omitempty"` // combined FTS+vector score
}
