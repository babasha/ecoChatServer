package models

import (
	"time"

	"github.com/google/uuid"
)

// DirectorDirective is a specific instruction from Director to РОП
type DirectorDirective struct {
	Type        string     `json:"type"`
	Priority    string     `json:"priority"`
	Description string     `json:"description"`
	Instruction string     `json:"instruction"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

// PromptChangeRecord documents a specific prompt change made by the Director
type PromptChangeRecord struct {
	Agent       string `json:"agent"`
	Description string `json:"description"`
	Rationale   string `json:"rationale"`
}

// DirectorReport represents a Level 2 Director's analysis
type DirectorReport struct {
	ID           uuid.UUID           `json:"id"`
	ReportDate   time.Time           `json:"reportDate"`
	ReportType   string              `json:"reportType"`
	TriggerEvent string              `json:"triggerEvent,omitempty"`
	SummaryCount int                 `json:"summaryCount"`
	Analysis     string              `json:"analysis"`
	Directives   []DirectorDirective `json:"directives"`
	Applied      bool                `json:"applied"`
	CreatedAt    time.Time           `json:"createdAt"`

	// Detailed reasoning (separate columns for flexible querying)
	CustomerComplaints []string             `json:"customerComplaints,omitempty"`
	KeyObservations    []string             `json:"keyObservations,omitempty"`
	PromptChanges      []PromptChangeRecord `json:"promptChanges,omitempty"`
	Expectations       string               `json:"expectations,omitempty"`
}
