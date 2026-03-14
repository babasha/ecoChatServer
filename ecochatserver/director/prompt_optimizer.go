package director

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

// PromptOptimizer handles prompt versioning, evaluation, and optimization
type PromptOptimizer struct {
	provider llm.Provider
}

// NewPromptOptimizer creates optimizer using the Director's LLM provider
func NewPromptOptimizer(provider llm.Provider) *PromptOptimizer {
	return &PromptOptimizer{provider: provider}
}

// OptimizePrompt analyzes current prompt performance and creates an improved version.
// Returns change notes (empty string if no changes were made).
func (po *PromptOptimizer) OptimizePrompt(ctx context.Context, agentName string, analysis string, summaries []string) (string, error) {
	// 1. Get current active prompt
	current, err := database.GetActivePrompt(agentName)
	if err != nil {
		return "", fmt.Errorf("get active prompt: %w", err)
	}
	if current == nil {
		log.Printf("[PROMPT_OPT] No active prompt for %s, skipping optimization", agentName)
		return "", nil
	}

	// 2. Get prompt history for context (last 3 versions)
	history, err := database.GetPromptHistory(agentName, 3)
	if err != nil {
		return "", fmt.Errorf("get prompt history: %w", err)
	}

	// 3. Ask Director's LLM to improve the prompt
	improvedPrompt, notes, err := po.generateImprovedPrompt(ctx, current, history, analysis, summaries)
	if err != nil {
		return "", fmt.Errorf("generate improved prompt: %w", err)
	}

	if improvedPrompt == "" || improvedPrompt == current.Prompt {
		log.Printf("[PROMPT_OPT] No improvement needed for %s", agentName)
		return "", nil
	}

	// 4. Save new version
	nextVersion, err := database.GetNextPromptVersion(agentName)
	if err != nil {
		return "", fmt.Errorf("get next version: %w", err)
	}

	parentV := current.Version
	_, err = database.InsertPrompt(agentName, nextVersion, improvedPrompt, "director", &parentV, notes)
	if err != nil {
		return "", fmt.Errorf("insert prompt: %w", err)
	}

	// 5. Activate new version
	if err := database.ActivatePrompt(agentName, nextVersion); err != nil {
		return "", fmt.Errorf("activate prompt: %w", err)
	}

	log.Printf("[PROMPT_OPT] Created and activated %s v%d (parent: v%d): %s",
		agentName, nextVersion, current.Version, notes)

	return notes, nil
}

// EvaluateAndRollback compares current version with parent, rolls back if worse.
// Returns rollback reason (empty string if no rollback occurred).
func (po *PromptOptimizer) EvaluateAndRollback(ctx context.Context, agentName string) (string, error) {
	current, err := database.GetActivePrompt(agentName)
	if err != nil || current == nil {
		return "", err
	}

	// Only evaluate director-created prompts with metrics
	if current.CreatedBy != "director" || current.Metrics == nil || current.ParentVersion == nil {
		return "", nil
	}

	// Get parent metrics
	history, err := database.GetPromptHistory(agentName, 10)
	if err != nil {
		return "", err
	}

	var parent *models.AgentPrompt
	for i := range history {
		if history[i].Version == *current.ParentVersion {
			parent = &history[i]
			break
		}
	}

	if parent == nil || parent.Metrics == nil {
		return "", nil // no parent to compare with
	}

	// Compare: if current is worse on key metrics, rollback
	if isWorse(current.Metrics, parent.Metrics) {
		reason := fmt.Sprintf("v%d regression vs v%d: esc %.1f%%→%.1f%%, empty %.1f%%→%.1f%%",
			current.Version, parent.Version,
			parent.Metrics.EscalationRate*100, current.Metrics.EscalationRate*100,
			parent.Metrics.EmptyResponseRate*100, current.Metrics.EmptyResponseRate*100)

		log.Printf("[PROMPT_OPT] %s for %s — rolling back", reason, agentName)

		if err := database.ActivatePrompt(agentName, parent.Version); err != nil {
			return "", fmt.Errorf("rollback activate: %w", err)
		}

		log.Printf("[PROMPT_OPT] Rolled back %s from v%d to v%d", agentName, current.Version, parent.Version)
		return reason, nil
	}

	log.Printf("[PROMPT_OPT] v%d performing well for %s (esc: %.1f%%, empty: %.1f%%)",
		current.Version, agentName,
		current.Metrics.EscalationRate*100, current.Metrics.EmptyResponseRate*100)

	return "", nil
}

// isWorse returns true if current metrics are worse than parent
func isWorse(current, parent *models.PromptMetrics) bool {
	if current.ChatsHandled < 5 {
		return false // not enough data to judge
	}

	// Escalation rate increased significantly (>50% relative increase)
	if parent.EscalationRate > 0 && current.EscalationRate > parent.EscalationRate*1.5 {
		return true
	}

	// Empty response rate doubled
	if parent.EmptyResponseRate > 0 && current.EmptyResponseRate > parent.EmptyResponseRate*2 {
		return true
	}

	// Conversations taking much longer (>50% more messages)
	if parent.AvgMessagesToResolve > 0 && current.AvgMessagesToResolve > parent.AvgMessagesToResolve*1.5 {
		return true
	}

	return false
}

func (po *PromptOptimizer) generateImprovedPrompt(
	ctx context.Context,
	current *models.AgentPrompt,
	history []models.AgentPrompt,
	analysis string,
	summaries []string,
) (string, string, error) {

	// Build context about previous versions
	var historyCtx string
	for _, h := range history {
		status := ""
		if h.IsActive {
			status = " [ACTIVE]"
		}
		metricsStr := "no metrics"
		if h.Metrics != nil {
			metricsStr = fmt.Sprintf("esc:%.0f%% empty:%.0f%% avg_msgs:%.1f",
				h.Metrics.EscalationRate*100, h.Metrics.EmptyResponseRate*100, h.Metrics.AvgMessagesToResolve)
		}
		historyCtx += fmt.Sprintf("v%d (%s, %s)%s: %s\n",
			h.Version, h.CreatedBy, metricsStr, status, truncate(h.Notes, 100))
	}

	prompt := fmt.Sprintf(`You are optimizing a system prompt for an AI customer support agent.

CURRENT PROMPT (v%d):
---
%s
---

VERSION HISTORY:
%s

TODAY'S ANALYSIS:
%s

SAMPLE CONVERSATIONS:
%s

TASK:
Based on the analysis and conversations, improve the current prompt.
- Fix specific issues identified in the analysis
- Keep the same structure and scope
- Be concrete — add specific instructions, not vague advice
- Do NOT make the prompt significantly longer (max 20%% increase)
- If the current prompt is already good, respond with "NO_CHANGES_NEEDED"

Output format:
IMPROVED_PROMPT:
<the improved prompt text>

CHANGE_NOTES:
<1-2 sentences explaining what changed and why>`,
		current.Version, current.Prompt, historyCtx, analysis,
		strings.Join(summaries, "\n"))

	resp, err := po.provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature:  0.3,
		MaxTokens:    3000,
		SystemPrompt: "You are an expert prompt engineer. Output only the improved prompt and change notes in the specified format.",
	})
	if err != nil {
		return "", "", err
	}

	return parseOptimizationResponse(resp.Text)
}

func parseOptimizationResponse(text string) (string, string, error) {
	if strings.Contains(text, "NO_CHANGES_NEEDED") {
		return "", "", nil
	}

	var prompt, notes string

	// Extract IMPROVED_PROMPT
	promptIdx := strings.Index(text, "IMPROVED_PROMPT:")
	notesIdx := strings.Index(text, "CHANGE_NOTES:")

	if promptIdx >= 0 && notesIdx > promptIdx {
		prompt = strings.TrimSpace(text[promptIdx+len("IMPROVED_PROMPT:") : notesIdx])
	} else if promptIdx >= 0 {
		prompt = strings.TrimSpace(text[promptIdx+len("IMPROVED_PROMPT:"):])
	}

	if notesIdx >= 0 {
		notes = strings.TrimSpace(text[notesIdx+len("CHANGE_NOTES:"):])
	}

	// Clean up markdown artifacts
	prompt = strings.TrimPrefix(prompt, "---")
	prompt = strings.TrimSuffix(prompt, "---")
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return "", "", fmt.Errorf("could not parse improved prompt from response")
	}

	return prompt, notes, nil
}

