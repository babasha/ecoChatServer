package models

import (
	"time"

	"github.com/google/uuid"
)

// DirectorSkill represents a custom tool/skill created by the Director agent.
// Skills are dynamically loaded and made available as LLM tools at runtime.
type DirectorSkill struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`        // unique tool name (e.g. "check_firmware_issues")
	Description string     `json:"description"` // what the tool does (shown to LLM)
	Parameters  string     `json:"parameters"`  // JSON Schema for parameters
	SkillType   string     `json:"skill_type"`  // sql_query, prompt_chain, http_api, composite
	Code        string     `json:"code"`        // implementation body
	Version     int        `json:"version"`
	Enabled     bool       `json:"enabled"`
	CreatedBy   string     `json:"created_by"`
	UsageCount  int        `json:"usage_count"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	Tags        []string   `json:"tags"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// CompositeConfig defines the pipeline for a composite skill.
type CompositeConfig struct {
	Steps  []CompositeStep `json:"steps"`
	Output string          `json:"output"` // which variable to return: "{{var_name}}"
}

// CompositeStep is a single step in a composite skill pipeline.
type CompositeStep struct {
	Skill     string            `json:"skill"`      // name of the skill to call
	ArgsMap   map[string]string `json:"args_map"`   // param → value or "{{prev_output}}"
	OutputVar string            `json:"output_var"` // store result as this variable
}

// DirectorToolLog records a Director-level tool call for pattern analysis.
type DirectorToolLog struct {
	ID           uuid.UUID `json:"id"`
	ToolName     string    `json:"tool_name"`
	Arguments    string    `json:"arguments"` // JSON
	ResultLength int       `json:"result_length"`
	Success      bool      `json:"success"`
	CreatedAt    time.Time `json:"created_at"`
}

// ToolPattern represents a detected repeating pattern in tool usage.
type ToolPattern struct {
	ToolName  string `json:"tool_name"`
	CallCount int    `json:"call_count"`
	SampleArgs string `json:"sample_args"` // example arguments JSON
}
