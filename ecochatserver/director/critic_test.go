package director

import (
	"testing"
)

// ============================================================================
// TestParseCriticResponse — verifies structured LLM response parsing
// ============================================================================
//
// parseCriticResponse is the critical gate that decides APPROVE/REJECT/REVISE.
// It must be fail-closed: any malformed input defaults to REJECT + RiskHigh.

func TestParseCriticResponse(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		decisionType   DecisionType
		attempt        int
		wantAction     CriticAction
		wantRisk       RiskLevel
		wantConcerns   int // expected number of concerns
		wantFeedback   bool
	}{
		{
			name: "valid APPROVE low risk no concerns",
			input: `ACTION: APPROVE
RISK_LEVEL: low
CONCERNS:
- none
REASONING: The proposed directive is clear, actionable, and low risk.
FEEDBACK:`,
			decisionType: DecisionDirectives,
			attempt:      1,
			wantAction:   CriticApprove,
			wantRisk:     RiskLow,
			wantConcerns: 0, // "none" must be filtered out
			wantFeedback: false,
		},
		{
			name: "valid REJECT high risk with concerns",
			input: `ACTION: REJECT
RISK_LEVEL: high
CONCERNS:
- Instruction is too vague to be actionable
- Contradicts existing directive #3
REASONING: The proposed change introduces contradictions and lacks specificity.
FEEDBACK:`,
			decisionType: DecisionDirectives,
			attempt:      1,
			wantAction:   CriticReject,
			wantRisk:     RiskHigh,
			wantConcerns: 2,
			wantFeedback: false,
		},
		{
			name: "valid REVISE medium risk with feedback",
			input: `ACTION: REVISE
RISK_LEVEL: medium
CONCERNS:
- Missing parameter validation
REASONING: The skill is useful but needs input validation before approval.
FEEDBACK: Add validation for the device_id parameter to prevent SQL injection.`,
			decisionType: DecisionSkillCreate,
			attempt:      1,
			wantAction:   CriticRevise,
			wantRisk:     RiskMedium,
			wantConcerns: 1,
			wantFeedback: true,
		},
		{
			name:         "empty input — fail-closed to REJECT",
			input:        "",
			decisionType: DecisionDirectives,
			attempt:      1,
			wantAction:   CriticReject,
			wantRisk:     RiskHigh,
			wantConcerns: 0,
			wantFeedback: false,
		},
		{
			name:         "garbage input — fail-closed to REJECT",
			input:        "lgtm, ship it",
			decisionType: DecisionPromptChange,
			attempt:      1,
			wantAction:   CriticReject,
			wantRisk:     RiskHigh,
			wantConcerns: 0,
			wantFeedback: false,
		},
		{
			name: "action with trailing text — prefix match still works",
			input: `ACTION: APPROVE - looks good
RISK_LEVEL: low
CONCERNS:
- none
REASONING: Fine.
FEEDBACK:`,
			decisionType: DecisionIdentity,
			attempt:      1,
			wantAction:   CriticApprove,
			wantRisk:     RiskLow,
			wantConcerns: 0,
			wantFeedback: false,
		},
		{
			name: "lowercase action — case insensitive",
			input: `ACTION: approve
RISK_LEVEL: low
CONCERNS:
- none
REASONING: Approved.
FEEDBACK:`,
			decisionType: DecisionDirectives,
			attempt:      1,
			wantAction:   CriticApprove,
			wantRisk:     RiskLow,
			wantConcerns: 0,
			wantFeedback: false,
		},
		{
			name: "missing RISK_LEVEL — fail-closed to high",
			input: `ACTION: APPROVE
CONCERNS:
- none
REASONING: Fine.
FEEDBACK:`,
			decisionType: DecisionDirectives,
			attempt:      1,
			wantAction:   CriticApprove,
			wantRisk:     RiskHigh, // unknown risk = high risk (fail-closed)
			wantConcerns: 0,
			wantFeedback: false,
		},
		{
			name: "multiple concerns including none mixed in",
			input: `ACTION: REJECT
RISK_LEVEL: high
CONCERNS:
- none
- Missing validation
- none
REASONING: Issues found.
FEEDBACK:`,
			decisionType: DecisionSkillCreate,
			attempt:      1,
			wantAction:   CriticReject,
			wantRisk:     RiskHigh,
			wantConcerns: 1, // only "Missing validation" survives
			wantFeedback: false,
		},
		{
			name: "second attempt round 2",
			input: `ACTION: APPROVE
RISK_LEVEL: low
CONCERNS:
- none
REASONING: Revised version addresses all concerns.
FEEDBACK:`,
			decisionType: DecisionPromptChange,
			attempt:      2,
			wantAction:   CriticApprove,
			wantRisk:     RiskLow,
			wantConcerns: 0,
			wantFeedback: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCriticResponse(tt.input, tt.decisionType, tt.attempt)

			if got.Action != tt.wantAction {
				t.Errorf("Action = %q, want %q", got.Action, tt.wantAction)
			}
			if got.RiskLevel != tt.wantRisk {
				t.Errorf("RiskLevel = %q, want %q", got.RiskLevel, tt.wantRisk)
			}
			if len(got.Concerns) != tt.wantConcerns {
				t.Errorf("Concerns count = %d, want %d (got: %v)", len(got.Concerns), tt.wantConcerns, got.Concerns)
			}
			if tt.wantFeedback && got.Feedback == "" {
				t.Errorf("expected non-empty Feedback, got empty")
			}
			if !tt.wantFeedback && got.Feedback != "" {
				t.Errorf("expected empty Feedback, got %q", got.Feedback)
			}
			if got.DecisionType != tt.decisionType {
				t.Errorf("DecisionType = %q, want %q", got.DecisionType, tt.decisionType)
			}
			if got.Attempt != tt.attempt {
				t.Errorf("Attempt = %d, want %d", got.Attempt, tt.attempt)
			}
		})
	}
}
