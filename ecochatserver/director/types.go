package director

import (
	"time"

	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// Directive is an alias for models.DirectorDirective
type Directive = models.DirectorDirective

// PromptChangeRecord alias
type PromptChangeRecord = models.PromptChangeRecord

// Report represents a director's analysis of chat activity
type Report struct {
	ID           uuid.UUID   `json:"id"`
	ReportDate   time.Time   `json:"reportDate"`
	ReportType   string      `json:"reportType"`
	TriggerEvent string      `json:"triggerEvent,omitempty"`
	SummaryCount int         `json:"summaryCount"`
	Analysis     string      `json:"analysis"`
	Directives   []Directive `json:"directives"`
	Stats        *DailyStats `json:"stats,omitempty"`
	Applied      bool        `json:"applied"`
	CreatedAt    time.Time   `json:"createdAt"`

	// Detailed reasoning
	CustomerComplaints []string             `json:"customerComplaints,omitempty"`
	KeyObservations    []string             `json:"keyObservations,omitempty"`
	PromptChanges      []PromptChangeRecord `json:"promptChanges,omitempty"`
	Expectations       string               `json:"expectations,omitempty"`
}

// ============================================================================
// Critic types
// ============================================================================

// CriticAction represents the Critic's verdict on a proposed decision.
type CriticAction string

const (
	CriticApprove CriticAction = "APPROVE"
	CriticReject  CriticAction = "REJECT"
	CriticRevise  CriticAction = "REVISE"
)

// RiskLevel classifies the risk of a proposed change.
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// DecisionType identifies what kind of decision the Critic is reviewing.
type DecisionType string

const (
	DecisionDirectives   DecisionType = "directives"
	DecisionPromptChange DecisionType = "prompt_change"
	DecisionSkillCreate  DecisionType = "skill_create"
	DecisionIdentity     DecisionType = "identity_update"
)

// CriticVerdict is the structured output of the Critic's review.
type CriticVerdict struct {
	Action       CriticAction `json:"action"`
	RiskLevel    RiskLevel    `json:"risk_level"`
	Concerns     []string     `json:"concerns"`
	Reasoning    string       `json:"reasoning"`
	Feedback     string       `json:"feedback"`       // for REVISE: specific guidance for revision
	DecisionType DecisionType `json:"decision_type"`
	Attempt      int          `json:"attempt"`         // 1 = first review, 2 = post-revision review
}
