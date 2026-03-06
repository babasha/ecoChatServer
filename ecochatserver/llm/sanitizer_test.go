package llm

import "testing"

func TestStripThinkTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no tags", "Hello world", "Hello world"},
		{"closed tag", "<think>reasoning here</think>\n\nActual answer", "Actual answer"},
		{"multiline think", "<think>\nline1\nline2\n</think>\n\nAnswer", "Answer"},
		{"unclosed tag (truncated)", "<think>reasoning without close", ""},
		{"text before think", "Before <think>thinking</think> After", "Before After"},
		{"only think block", "<think>all thinking</think>", ""},
		{"empty string", "", ""},
		{"think keyword not a tag", "I think this is fine", "I think this is fine"},
		{"orphaned close tag", "</think>\n\nActual answer after tool call", "Actual answer after tool call"},
		{"orphaned close tag with whitespace", "  </think>  \n\nAnswer", "Answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripThinkTags(tt.input)
			if got != tt.want {
				t.Errorf("stripThinkTags() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitize_ThinkTagsStripped(t *testing.T) {
	// Reasoning content like "I don't know if..." inside <think> should NOT trigger escalation
	input := "<think>I don't know what the user means exactly, let me figure it out</think>\n\nHere is your answer about sensors."
	clean, escalate := sanitize(input)
	if escalate {
		t.Error("sanitize() escalated due to think block content, should not")
	}
	if clean != "Here is your answer about sensors." {
		t.Errorf("sanitize() = %q, want clean answer without think block", clean)
	}
}

func TestSanitize_EscalationWithoutThinkTags(t *testing.T) {
	// Real escalation trigger (not inside think tags) should still work
	input := "I can't help you with this request."
	_, escalate := sanitize(input)
	if !escalate {
		t.Error("sanitize() should escalate on 'i can't help'")
	}
}
