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
