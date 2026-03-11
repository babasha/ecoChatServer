package models

import (
	"time"

	"github.com/google/uuid"
)

// DirectiveOutcome tracks the effectiveness of a Director directive.
// Created when a directive is issued (with baseline metrics),
// evaluated 24-48h later during self-reflection.
type DirectiveOutcome struct {
	ID                    uuid.UUID  `json:"id"`
	ReportID              uuid.UUID  `json:"report_id"`
	DirectiveType         string     `json:"directive_type"`
	DirectiveInstruction  string     `json:"directive_instruction"`
	EscalationRateBefore  float64    `json:"escalation_rate_before"`
	EmptyRateBefore       float64    `json:"empty_rate_before"`
	AvgResponseMsBefore   float64    `json:"avg_response_ms_before"`
	EscalationRateAfter   *float64   `json:"escalation_rate_after,omitempty"`
	EmptyRateAfter        *float64   `json:"empty_rate_after,omitempty"`
	AvgResponseMsAfter    *float64   `json:"avg_response_ms_after,omitempty"`
	Effectiveness         string     `json:"effectiveness"` // positive, negative, neutral, pending
	EvaluationNotes       string     `json:"evaluation_notes"`
	EvaluatedAt           *time.Time `json:"evaluated_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}
