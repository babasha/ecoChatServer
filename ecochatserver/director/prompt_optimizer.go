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

// PromptProposal holds a generated prompt improvement before it is applied.
type PromptProposal struct {
	AgentName     string
	CurrentPrompt *models.AgentPrompt
	NewPrompt     string
	Notes         string
	Analysis      string
	Summaries     []string
}

// GenerateOptimizedPrompt generates an improved prompt but does NOT save or activate it.
// Returns a PromptProposal for review by the Critic, or nil if no improvement is needed.
func (po *PromptOptimizer) GenerateOptimizedPrompt(ctx context.Context, agentName string, analysis string, summaries []string) (*PromptProposal, error) {
	current, err := database.GetActivePrompt(agentName)
	if err != nil {
		return nil, fmt.Errorf("get active prompt: %w", err)
	}
	if current == nil {
		log.Printf("[PROMPT_OPT] No active prompt for %s, skipping optimization", agentName)
		return nil, nil
	}

	history, err := database.GetPromptHistory(agentName, 3)
	if err != nil {
		return nil, fmt.Errorf("get prompt history: %w", err)
	}

	improvedPrompt, notes, err := po.generateImprovedPrompt(ctx, current, history, analysis, summaries)
	if err != nil {
		return nil, fmt.Errorf("generate improved prompt: %w", err)
	}

	if improvedPrompt == "" || improvedPrompt == current.Prompt {
		log.Printf("[PROMPT_OPT] No improvement needed for %s", agentName)
		return nil, nil
	}

	return &PromptProposal{
		AgentName:     agentName,
		CurrentPrompt: current,
		NewPrompt:     improvedPrompt,
		Notes:         notes,
		Analysis:      analysis,
		Summaries:     summaries,
	}, nil
}

// ApplyPromptProposal saves and activates a prompt proposal (after Critic approval).
func (po *PromptOptimizer) ApplyPromptProposal(ctx context.Context, proposal *PromptProposal, promptText string) (string, error) {
	nextVersion, err := database.GetNextPromptVersion(proposal.AgentName)
	if err != nil {
		return "", fmt.Errorf("get next version: %w", err)
	}

	parentV := proposal.CurrentPrompt.Version
	_, err = database.InsertPrompt(proposal.AgentName, nextVersion, promptText, "director", &parentV, proposal.Notes)
	if err != nil {
		return "", fmt.Errorf("insert prompt: %w", err)
	}

	if err := database.ActivatePrompt(proposal.AgentName, nextVersion); err != nil {
		// Prompt was inserted but not activated — save partial for retry
		SavePartialPromptApply(proposal.AgentName, nextVersion)
		return "", fmt.Errorf("activate prompt: %w", err)
	}

	log.Printf("[PROMPT_OPT] Created and activated %s v%d (parent: v%d): %s",
		proposal.AgentName, nextVersion, proposal.CurrentPrompt.Version, proposal.Notes)

	return proposal.Notes, nil
}

// OptimizePrompt is a convenience wrapper that generates + applies without Critic review.
// Kept for backward compatibility. The analysis.go pipeline uses Generate + Critic + Apply instead.
func (po *PromptOptimizer) OptimizePrompt(ctx context.Context, agentName string, analysis string, summaries []string) (string, error) {
	proposal, err := po.GenerateOptimizedPrompt(ctx, agentName, analysis, summaries)
	if err != nil {
		return "", err
	}
	if proposal == nil {
		return "", nil
	}
	return po.ApplyPromptProposal(ctx, proposal, proposal.NewPrompt)
}

// RevisePrompt generates a revised version of a prompt using Critic feedback.
func (po *PromptOptimizer) RevisePrompt(ctx context.Context, proposal *PromptProposal, criticFeedback string) (string, error) {
	current := proposal.CurrentPrompt
	history, _ := database.GetPromptHistory(proposal.AgentName, 3)
	historyCtx := formatPromptHistory(history)

	prompt := fmt.Sprintf(`You previously proposed an improved prompt, but it was flagged by a quality reviewer.

CURRENT PROMPT (v%d):
---
%s
---

YOUR PREVIOUS PROPOSAL:
---
%s
---

REVIEWER FEEDBACK:
%s

VERSION HISTORY:
%s

TODAY'S ANALYSIS:
%s

TASK:
Revise your proposed prompt to address the reviewer's concerns.
Keep the improvements that are valid, fix the issues that were flagged.
If the reviewer is right that no changes are needed, respond with "NO_CHANGES_NEEDED".

Output format:
IMPROVED_PROMPT:
<the revised prompt text>

CHANGE_NOTES:
<1-2 sentences explaining what changed and why>`,
		current.Version, current.Prompt, proposal.NewPrompt, criticFeedback,
		historyCtx, proposal.Analysis)

	resp, err := po.provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature:  0.3,
		MaxTokens:    3000,
		SystemPrompt: "You are an expert prompt engineer. A quality reviewer has flagged issues with your previous proposal. Address their concerns. Output only the revised prompt and change notes in the specified format.",
	})
	if err != nil {
		return "", err
	}

	revised, _, err := parseOptimizationResponse(resp.Text)
	return revised, err
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

		// Extract lesson from the regression
		ExtractPromptRegression(agentName, reason)

		log.Printf("[PROMPT_OPT] %s for %s — rolling back", reason, agentName)

		if err := database.ActivatePrompt(agentName, parent.Version); err != nil {
			// Rollback decision made but activation failed — save partial for retry
			SavePartialPromptApply(agentName, parent.Version)
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

// formatPromptHistory builds a human-readable history string from prompt versions.
func formatPromptHistory(history []models.AgentPrompt) string {
	var sb strings.Builder
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
		sb.WriteString(fmt.Sprintf("v%d (%s, %s)%s: %s\n",
			h.Version, h.CreatedBy, metricsStr, status, truncate(h.Notes, 100)))
	}
	return sb.String()
}

func (po *PromptOptimizer) generateImprovedPrompt(
	ctx context.Context,
	current *models.AgentPrompt,
	history []models.AgentPrompt,
	analysis string,
	summaries []string,
) (string, string, error) {

	historyCtx := formatPromptHistory(history)

	// Inject lessons from past prompt regressions
	lessonOverlay := BuildPromptOptLessonOverlay()

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
		strings.Join(summaries, "\n"), lessonOverlay)

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

	p := NewSectionParser(text, []string{"IMPROVED_PROMPT:", "CHANGE_NOTES:"})

	prompt := p.Get("IMPROVED_PROMPT:")
	notes := p.Get("CHANGE_NOTES:")

	// Clean up markdown artifacts
	prompt = strings.TrimPrefix(prompt, "---")
	prompt = strings.TrimSuffix(prompt, "---")
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return "", "", fmt.Errorf("could not parse improved prompt from response")
	}

	return prompt, notes, nil
}

