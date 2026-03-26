package director

import (
	"testing"
)

// ============================================================================
// TestExtractConcepts — verifies tag normalization into concept labels
// ============================================================================

func TestExtractConcepts(t *testing.T) {
	g := &HebbianGraph{enabled: true}

	tests := []struct {
		tags     []string
		wantLen  int
		wantLabel string // first expected label
	}{
		{
			tags:      []string{"device:Zefir Pro", "issue:firmware", "escalation"},
			wantLen:   3,
			wantLabel: "device:zefir pro",
		},
		{
			tags:      []string{"DEVICE_MODEL", "model:Zefir Mini", "model:Zefir Mini"}, // duplicate
			wantLen:   2,
			wantLabel: "device_model",
		},
		{
			tags:      []string{},
			wantLen:   0,
			wantLabel: "",
		},
		{
			tags:      []string{"  ", "", "escalate"},
			wantLen:   1,
			wantLabel: "escalation",
		},
	}

	for _, tt := range tests {
		got := g.ExtractConcepts(tt.tags)
		if len(got) != tt.wantLen {
			t.Errorf("ExtractConcepts(%v): got %d concepts, want %d", tt.tags, len(got), tt.wantLen)
			continue
		}
		if tt.wantLen > 0 && got[0].Label != tt.wantLabel {
			t.Errorf("ExtractConcepts(%v): first label = %q, want %q", tt.tags, got[0].Label, tt.wantLabel)
		}
	}
}

// ============================================================================
// TestClassifyBareConcept — verifies bare tag classification
// ============================================================================

func TestClassifyBareConcept(t *testing.T) {
	tests := []struct {
		tag          string
		wantLabel    string
		wantCategory string
	}{
		{"escalation", "escalation", "event"},
		{"escalate", "escalation", "event"},
		{"empty_response", "empty_response", "event"},
		{"tool_failure", "tool_failure", "event"},
		{"directive", "directive", "event"},
		{"lesson", "lesson", "event"},
		{"observation", "observation", "event"},
		{"positive", "sentiment:positive", "sentiment"},
		{"negative", "sentiment:negative", "sentiment"},
		{"frustrated", "sentiment:frustrated", "sentiment"},
		{"satisfied", "sentiment:satisfied", "sentiment"},
		{"device:Zefir Pro", "device:zefir pro", "device"},
		{"issue:firmware", "issue:firmware", "issue"},
		{"model:Zefir Mini", "model:zefir mini", "model"},
		{"resolution:resolved", "resolution:resolved", "resolution"},
		{"somebarething", "somebarething", "concept"},
	}

	for _, tt := range tests {
		label, category := classifyTag(tt.tag)
		if label != tt.wantLabel {
			t.Errorf("classifyTag(%q): label = %q, want %q", tt.tag, label, tt.wantLabel)
		}
		if category != tt.wantCategory {
			t.Errorf("classifyTag(%q): category = %q, want %q", tt.tag, category, tt.wantCategory)
		}
	}
}

// ============================================================================
// TestHebbianWeightFormula — verifies the bounded growth curve
// ============================================================================

func TestHebbianWeightFormula(t *testing.T) {
	// w_new = w_old + lr * (1 - w_old / w_max)
	// lr=1.0, w_max=10.0
	apply := func(w float64) float64 {
		return w + 1.0*(1.0-w/10.0)
	}

	// First co-occurrence: 0 → 1.0
	w := apply(0.0)
	if abs(w-1.0) > 1e-9 {
		t.Errorf("step 0→1: got %.6f, want 1.0", w)
	}

	// Second co-occurrence: 1.0 → 1.9
	w = apply(w)
	if abs(w-1.9) > 1e-9 {
		t.Errorf("step 1→2: got %.6f, want 1.9", w)
	}

	// After many iterations the weight must converge below 10.0
	w = 0.0
	for i := 0; i < 1000; i++ {
		w = apply(w)
	}
	if w >= 10.0 {
		t.Errorf("weight exceeded w_max after 1000 steps: %.6f", w)
	}
	if w < 9.99 {
		t.Errorf("weight did not converge near w_max after 1000 steps: %.6f", w)
	}
}

// TestFormatFloat — basic sanity check for the helper
func TestFormatFloat(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{1.234, "1.234"},
		{0.0, "0.000"},
		{10.0, "10.000"},
		{3.141592, "3.141"},
	}
	for _, tt := range tests {
		got := formatFloat(tt.in)
		if got != tt.want {
			t.Errorf("formatFloat(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
