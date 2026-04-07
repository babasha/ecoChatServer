package director

import (
	"testing"
)

// ============================================================================
// TestParseDirectiveLine — tag extraction from directive strings
// ============================================================================

func TestParseDirectiveLine(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantType     string
		wantPriority string
		wantInstr    string
	}{
		{
			name:         "full tags with instruction",
			input:        "[type:prompt_update] [priority:high] Always greet the user by name",
			wantType:     "prompt_update",
			wantPriority: "high",
			wantInstr:    "Always greet the user by name",
		},
		{
			name:         "only type tag",
			input:        "[type:faq_gap] Add FAQ about watering schedules",
			wantType:     "faq_gap",
			wantPriority: "medium", // default
			wantInstr:    "Add FAQ about watering schedules",
		},
		{
			name:         "only priority tag",
			input:        "[priority:low] Minor wording improvement",
			wantType:     "prompt_update", // default
			wantPriority: "low",
			wantInstr:    "Minor wording improvement",
		},
		{
			name:         "no tags — pure instruction",
			input:        "Be more empathetic in responses",
			wantType:     "prompt_update",
			wantPriority: "medium",
			wantInstr:    "Be more empathetic in responses",
		},
		{
			name:         "empty input",
			input:        "",
			wantType:     "prompt_update",
			wantPriority: "medium",
			wantInstr:    "",
		},
		{
			name:         "tags in reverse order — priority before type",
			input:        "[priority:high] [type:alert] Device offline alert detected",
			wantType:     "alert",
			wantPriority: "high",
			wantInstr:    "Device offline alert detected",
		},
		{
			name:         "type tag with no closing bracket — should not panic",
			input:        "[type:broken Device still has no closing bracket",
			wantType:     "prompt_update", // tag not parsed, default used
			wantPriority: "medium",
			wantInstr:    "[type:broken Device still has no closing bracket",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDirectiveLine(tt.input)
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
			if got.Priority != tt.wantPriority {
				t.Errorf("Priority = %q, want %q", got.Priority, tt.wantPriority)
			}
			if got.Instruction != tt.wantInstr {
				t.Errorf("Instruction = %q, want %q", got.Instruction, tt.wantInstr)
			}
		})
	}
}

// ============================================================================
// TestParseDirectorResponse — full LLM response parsing
// ============================================================================

func TestParseDirectorResponse(t *testing.T) {
	t.Run("full valid response", func(t *testing.T) {
		input := `ANALYSIS:
Support quality was mixed this week. Users struggled with device pairing.

CUSTOMER_COMPLAINTS:
- Cannot pair Zefir Pro to app
- Plant recommendations are too generic

KEY_OBSERVATIONS:
- Pairing issues account for 40% of escalations
- Spanish-language users get slower responses

DIRECTIVES:
- [type:prompt_update] [priority:high] Improve pairing troubleshooting steps
- [type:faq_gap] [priority:medium] Add FAQ on Zefir Pro pairing

EXPECTATIONS:
Escalation rate should drop by 20% next week.`

		got := parseDirectorResponse(input)

		if got.Analysis == "" {
			t.Error("Analysis should not be empty")
		}
		if len(got.CustomerComplaints) != 2 {
			t.Errorf("CustomerComplaints count = %d, want 2", len(got.CustomerComplaints))
		}
		if len(got.KeyObservations) != 2 {
			t.Errorf("KeyObservations count = %d, want 2", len(got.KeyObservations))
		}
		if len(got.Directives) != 2 {
			t.Errorf("Directives count = %d, want 2", len(got.Directives))
		}
		if got.Directives[0].Type != "prompt_update" {
			t.Errorf("Directive[0].Type = %q, want prompt_update", got.Directives[0].Type)
		}
		if got.Directives[0].Priority != "high" {
			t.Errorf("Directive[0].Priority = %q, want high", got.Directives[0].Priority)
		}
		if got.Expectations == "" {
			t.Error("Expectations should not be empty")
		}
	})

	t.Run("empty input — fallback to full text as analysis", func(t *testing.T) {
		got := parseDirectorResponse("just a plain note")
		if got.Analysis != "just a plain note" {
			t.Errorf("Analysis should fall back to full text, got %q", got.Analysis)
		}
		if len(got.Directives) != 0 {
			t.Errorf("expected no directives, got %d", len(got.Directives))
		}
	})
}
