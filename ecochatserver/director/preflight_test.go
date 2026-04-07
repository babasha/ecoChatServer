package director

import (
	"testing"

	"github.com/egor/ecochatserver/models"
)

// ============================================================================
// TestExtractPlaceholders — finds {{param}} patterns in templates
// ============================================================================

func TestExtractPlaceholders(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "single placeholder",
			input: "Hello {{name}}, welcome!",
			want:  []string{"name"},
		},
		{
			name:  "multiple placeholders",
			input: "Device {{device_id}} owned by {{user_name}} reported {{issue}}",
			want:  []string{"device_id", "user_name", "issue"},
		},
		{
			name:  "duplicate placeholders — deduplicated",
			input: "{{x}} plus {{x}} equals two {{x}}",
			want:  []string{"x"},
		},
		{
			name:  "no placeholders",
			input: "This is a plain prompt with no params",
			want:  nil,
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "unclosed placeholder — stops at open {{",
			input: "Hello {{name}} and {{broken",
			want:  []string{"name"},
		},
		{
			name:  "empty placeholder — skipped",
			input: "Hello {{}} world",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPlaceholders(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("len = %d, want %d (got: %v, want: %v)", len(got), len(tt.want), got, tt.want)
				return
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

// ============================================================================
// TestValidateOutput — quality gate for skill execution results
// ============================================================================

func TestValidateOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{
			name:    "valid text output",
			output:  "Zefir Pro version 1.2.3 firmware released",
			wantErr: false,
		},
		{
			name:    "valid SQL output with rows",
			output:  "columns: id, name, status\n1 | Zefir Pro | active\nrows_read: 1",
			wantErr: false,
		},
		{
			name:    "valid SQL output with zero rows — acceptable",
			output:  "columns: id, name\nrows_read: 0",
			wantErr: false,
		},
		{
			name:    "empty output — rejected",
			output:  "",
			wantErr: true,
		},
		{
			name:    "whitespace only — rejected",
			output:  "   \n\t  ",
			wantErr: true,
		},
		{
			name:    "output contains error: pattern",
			output:  "error: column does not exist",
			wantErr: true,
		},
		{
			name:    "output contains panic:",
			output:  "panic: runtime error: index out of range",
			wantErr: true,
		},
		{
			name:    "output contains 'unknown tool'",
			output:  "unknown tool: get_device_status",
			wantErr: true,
		},
		{
			name:    "error pattern in uppercase — case insensitive",
			output:  "ERROR: relation does not exist",
			wantErr: true,
		},
		{
			name:    "error word in middle of normal text — no false positive",
			output:  "The water level sensor reported no errors today",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOutput(tt.output)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ============================================================================
// TestCleanRepairResponse — strips markdown from LLM repair output
// ============================================================================

func TestCleanRepairResponse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text — returned as-is",
			input: "SELECT * FROM devices WHERE id = $1",
			want:  "SELECT * FROM devices WHERE id = $1",
		},
		{
			name: "sql code block",
			input: "```sql\nSELECT * FROM devices WHERE id = $1\n```",
			want:  "SELECT * FROM devices WHERE id = $1",
		},
		{
			name: "json code block",
			input: "```json\n{\"url\": \"https://api.example.com\", \"method\": \"GET\"}\n```",
			want:  "{\"url\": \"https://api.example.com\", \"method\": \"GET\"}",
		},
		{
			name: "code block with no language tag",
			input: "```\nSELECT id FROM users\n```",
			want:  "SELECT id FROM users",
		},
		{
			name: "prose before code block — prose discarded",
			input: "Here is the fixed SQL query:\n\n```sql\nSELECT id FROM users WHERE active = true\n```",
			want:  "SELECT id FROM users WHERE active = true",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   \n  ",
			want:  "",
		},
		{
			name: "multiple code blocks — first one used",
			input: "```sql\nSELECT 1\n```\n\nOr alternatively:\n```sql\nSELECT 2\n```",
			want:  "SELECT 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanRepairResponse(tt.input)
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// ============================================================================
// TestStaticValidateHTTP — HTTP skill config validation
// ============================================================================

func TestStaticValidateHTTP(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{
			name:    "valid GET config",
			code:    `{"url": "https://api.example.com/data", "method": "GET"}`,
			wantErr: false,
		},
		{
			name:    "valid POST config",
			code:    `{"url": "https://api.example.com/send", "method": "POST"}`,
			wantErr: false,
		},
		{
			name:    "method lowercase — normalized",
			code:    `{"url": "https://api.example.com/data", "method": "get"}`,
			wantErr: false,
		},
		{
			name:    "missing method — defaults to GET",
			code:    `{"url": "https://api.example.com/data"}`,
			wantErr: false,
		},
		{
			name:    "missing URL — rejected",
			code:    `{"method": "GET"}`,
			wantErr: true,
		},
		{
			name:    "DELETE method — not allowed",
			code:    `{"url": "https://api.example.com/data", "method": "DELETE"}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON — rejected",
			code:    `not json at all`,
			wantErr: true,
		},
		{
			name:    "empty string — rejected",
			code:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := staticValidateHTTP(tt.code)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ============================================================================
// TestStaticValidatePromptChain — prompt template validation
// ============================================================================

func TestStaticValidatePromptChain(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		params  string
		wantErr bool
	}{
		{
			name:    "valid template no params",
			code:    "Tell me about the plant care for this device",
			params:  "",
			wantErr: false,
		},
		{
			name:   "valid template with matching params",
			code:   "Diagnose the issue with device {{device_id}} for user {{user_name}}",
			params: `{"type":"object","properties":{"device_id":{"type":"string"},"user_name":{"type":"string"}}}`,
			wantErr: false,
		},
		{
			name:    "too short — rejected",
			code:    "Hi",
			params:  "",
			wantErr: true,
		},
		{
			name:    "empty — rejected",
			code:    "",
			params:  "",
			wantErr: true,
		},
		{
			name:   "placeholder not in schema — rejected",
			code:   "Hello {{unknown_param}}, your device is ready",
			params: `{"type":"object","properties":{"device_id":{"type":"string"}}}`,
			wantErr: true,
		},
		{
			name:    "invalid params JSON — rejected",
			code:    "Hello {{name}}, welcome!",
			params:  `not valid json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := staticValidatePromptChain(tt.code, tt.params)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ============================================================================
// TestAggregateAgentStats — metrics aggregation
// ============================================================================

func TestAggregateAgentStats(t *testing.T) {
	t.Run("empty slice — all zeros, no division by zero", func(t *testing.T) {
		got := AggregateAgentStats(nil)
		if got.TotalCalls != 0 || got.EscRate != 0 || got.EmptyRate != 0 || got.AvgMs != 0 {
			t.Errorf("expected all zeros, got %+v", got)
		}
	})

	t.Run("single agent", func(t *testing.T) {
		stats := []models.AgentStats{
			{TotalCalls: 100, Escalations: 10, EmptyResponses: 5, AvgResponseMs: 200},
		}
		got := AggregateAgentStats(stats)
		if got.TotalCalls != 100 {
			t.Errorf("TotalCalls = %d, want 100", got.TotalCalls)
		}
		if got.EscRate != 0.1 {
			t.Errorf("EscRate = %f, want 0.1", got.EscRate)
		}
		if got.EmptyRate != 0.05 {
			t.Errorf("EmptyRate = %f, want 0.05", got.EmptyRate)
		}
		if got.AvgMs != 200 {
			t.Errorf("AvgMs = %f, want 200", got.AvgMs)
		}
	})

	t.Run("two agents — weighted average response time", func(t *testing.T) {
		stats := []models.AgentStats{
			{TotalCalls: 100, Escalations: 0, EmptyResponses: 0, AvgResponseMs: 100},
			{TotalCalls: 100, Escalations: 0, EmptyResponses: 0, AvgResponseMs: 300},
		}
		got := AggregateAgentStats(stats)
		if got.TotalCalls != 200 {
			t.Errorf("TotalCalls = %d, want 200", got.TotalCalls)
		}
		if got.AvgMs != 200 {
			t.Errorf("AvgMs = %f, want 200 (weighted avg)", got.AvgMs)
		}
	})

	t.Run("agents with zero calls — skip in rate calculation", func(t *testing.T) {
		stats := []models.AgentStats{
			{TotalCalls: 0, Escalations: 0, EmptyResponses: 0, AvgResponseMs: 0},
			{TotalCalls: 50, Escalations: 5, EmptyResponses: 2, AvgResponseMs: 150},
		}
		got := AggregateAgentStats(stats)
		if got.TotalCalls != 50 {
			t.Errorf("TotalCalls = %d, want 50", got.TotalCalls)
		}
		if got.EscRate != 0.1 {
			t.Errorf("EscRate = %f, want 0.1", got.EscRate)
		}
	})
}
