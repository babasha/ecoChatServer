package director

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
)

// ReviserFunc generates a revised version of the proposed content using Critic's feedback.
// It receives the original proposed content and the Critic's feedback, returns the revised content.
type ReviserFunc func(ctx context.Context, original, feedback string) (string, error)

// criticize runs a single Critic review on a proposed decision.
func (d *Director) criticize(ctx context.Context, decisionType DecisionType, proposed, decisionContext string, attempt int) (*CriticVerdict, error) {
	// Load rejection history to detect repeated proposals
	rejectionHistory := loadRejectionHistory(decisionType)

	prompt := buildCriticPrompt(decisionType, proposed, decisionContext, rejectionHistory, attempt)

	resp, err := d.provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature:  0.1,
		MaxTokens:    1000,
		SystemPrompt: getCriticSystemPrompt(decisionType),
	})
	if err != nil {
		return nil, fmt.Errorf("critic LLM call: %w", err)
	}

	verdict := parseCriticResponse(resp.Text, decisionType, attempt)
	saveCriticVerdict(verdict, proposed)

	// Extract lesson from rejections
	if verdict.Action == CriticReject || verdict.Action == CriticRevise {
		ExtractCriticRejection(decisionType, verdict, proposed)
	}

	log.Printf("[CRITIC] %s review (attempt %d): %s, risk=%s, concerns=%d",
		decisionType, attempt, verdict.Action, verdict.RiskLevel, len(verdict.Concerns))

	return verdict, nil
}

// criticizeWithDebate runs the full debate loop: Critic → (optional REVISE) → Critic again.
// If reviserFunc is nil, REVISE is treated as REJECT.
// Returns the final verdict and the content to apply (original or revised).
func (d *Director) criticizeWithDebate(
	ctx context.Context,
	decisionType DecisionType,
	proposed, decisionContext string,
	reviserFunc ReviserFunc,
) (*CriticVerdict, string, error) {

	// Round 1: Critic reviews the original proposal
	verdict, err := d.criticize(ctx, decisionType, proposed, decisionContext, 1)
	if err != nil {
		return nil, "", err
	}

	switch verdict.Action {
	case CriticApprove:
		return verdict, proposed, nil

	case CriticReject:
		return verdict, "", nil

	case CriticRevise:
		if reviserFunc == nil {
			// No reviser available — treat REVISE as REJECT
			verdict.Action = CriticReject
			verdict.Reasoning += " (no revision possible — treated as REJECT)"
			return verdict, "", nil
		}

		// Feed feedback back to the Optimizer for revision
		log.Printf("[CRITIC] Requesting revision for %s: %s", decisionType, truncate(verdict.Feedback, 100))

		revised, err := reviserFunc(ctx, proposed, verdict.Feedback)
		if err != nil {
			log.Printf("[CRITIC] Revision failed for %s: %v", decisionType, err)
			verdict.Action = CriticReject
			verdict.Reasoning += fmt.Sprintf(" (revision failed: %v)", err)
			return verdict, "", nil
		}

		if revised == "" || revised == proposed {
			// Reviser couldn't produce a different version — treat as reject
			log.Printf("[CRITIC] Revision produced no changes for %s", decisionType)
			verdict.Action = CriticReject
			verdict.Reasoning += " (revision produced no changes — treated as REJECT)"
			return verdict, "", nil
		}

		// Round 2: Critic reviews the revised proposal (final — no more REVISE allowed)
		verdict2, err := d.criticize(ctx, decisionType, revised, decisionContext+"\n\nPREVIOUS CRITIC FEEDBACK:\n"+verdict.Feedback, 2)
		if err != nil {
			return nil, "", err
		}

		// On round 2, REVISE is treated as REJECT to prevent infinite loops
		if verdict2.Action == CriticRevise {
			verdict2.Action = CriticReject
			verdict2.Reasoning += " (2nd round REVISE → auto-REJECT)"
		}

		if verdict2.Action == CriticApprove {
			return verdict2, revised, nil
		}
		return verdict2, "", nil
	}

	// Fallback (shouldn't reach here)
	return verdict, proposed, nil
}

// ============================================================================
// Critic prompts per decision type
// ============================================================================

func getCriticSystemPrompt(dt DecisionType) string {
	base := `You are a conservative quality gate reviewing proposed changes to an AI system.
Your job is to FIND PROBLEMS, not approve changes. Be skeptical and paranoid.
Only approve changes that are clearly beneficial with low risk of regression.
Respond ONLY in the structured format specified.`

	specific := map[DecisionType]string{
		DecisionDirectives: `
You are reviewing DIRECTIVES that will be given to a customer support AI agent.
Check for: vague/non-actionable instructions, contradictions with each other,
directives that could harm customer experience, scope creep beyond the analysis findings.`,

		DecisionPromptChange: `
You are reviewing a PROMPT CHANGE for a customer support AI agent.
This is critical — a bad prompt change can break the entire support system.
Check for: loss of important existing instructions, vague additions ("be more helpful"),
scope creep (analysis says X but prompt changes Y), self-contradictions,
prompt getting too long, removal of safety guardrails.`,

		DecisionSkillCreate: `
You are reviewing an AUTO-CREATED SKILL (custom tool) for the Director agent.
Check for: SQL injection risks, overly broad queries, duplication with existing tools,
unclear purpose, potential for misuse, missing parameter validation.`,

		DecisionIdentity: `
You are reviewing a SELF-MODIFICATION of the Director's identity/personality.
Check for: drift from core values, self-serving changes, contradiction with existing aspects,
loss of important traits, changes that weren't warranted by data.`,
	}

	return base + specific[dt]
}

func buildCriticPrompt(dt DecisionType, proposed, decisionContext, rejectionHistory string, attempt int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("DECISION TYPE: %s\n", dt))
	sb.WriteString(fmt.Sprintf("REVIEW ROUND: %d\n\n", attempt))

	sb.WriteString("PROPOSED CHANGE:\n---\n")
	sb.WriteString(proposed)
	sb.WriteString("\n---\n\n")

	if decisionContext != "" {
		sb.WriteString("CONTEXT (analysis/reason for this change):\n")
		sb.WriteString(decisionContext)
		sb.WriteString("\n\n")
	}

	if rejectionHistory != "" {
		sb.WriteString("PREVIOUSLY REJECTED CHANGES (do NOT approve similar proposals):\n")
		sb.WriteString(rejectionHistory)
		sb.WriteString("\n\n")
	}

	if attempt == 2 {
		sb.WriteString("NOTE: This is the REVISED version after your previous feedback. ")
		sb.WriteString("This is the final round — you can only APPROVE or REJECT.\n\n")
	}

	sb.WriteString(`Review this proposed change and respond in EXACTLY this format:

ACTION: APPROVE|REJECT|REVISE
RISK_LEVEL: low|medium|high
CONCERNS:
- <specific concern 1>
- <specific concern 2>
(if no concerns, write "- none")
REASONING: <2-3 sentences explaining your decision>
FEEDBACK: <if REVISE: specific instructions for how to fix the issues. if APPROVE/REJECT: empty>`)

	return sb.String()
}

// ============================================================================
// Response parsing
// ============================================================================

func parseCriticResponse(text string, dt DecisionType, attempt int) *CriticVerdict {
	verdict := &CriticVerdict{
		Action:       CriticReject,  // fail-closed: reject if parsing fails
		RiskLevel:    RiskHigh,
		DecisionType: dt,
		Attempt:      attempt,
		Reasoning:    "could not parse critic response",
	}

	p := NewSectionParser(text, []string{
		"ACTION:", "RISK_LEVEL:", "CONCERNS:", "REASONING:", "FEEDBACK:",
	})

	// Parse ACTION
	actionStr := strings.TrimSpace(strings.ToUpper(p.Get("ACTION:")))
	switch {
	case strings.HasPrefix(actionStr, "REJECT"):
		verdict.Action = CriticReject
	case strings.HasPrefix(actionStr, "REVISE"):
		verdict.Action = CriticRevise
	case strings.HasPrefix(actionStr, "APPROVE"):
		verdict.Action = CriticApprove
	}

	// Parse RISK_LEVEL — only override default (RiskHigh) if section was found.
	// Keeps fail-closed behavior: missing/malformed input stays RiskHigh.
	riskStr := strings.TrimSpace(strings.ToLower(p.Get("RISK_LEVEL:")))
	if riskStr != "" {
		switch {
		case strings.HasPrefix(riskStr, "low"):
			verdict.RiskLevel = RiskLow
		case strings.HasPrefix(riskStr, "high"):
			verdict.RiskLevel = RiskHigh
		default:
			verdict.RiskLevel = RiskMedium
		}
	}

	// Parse CONCERNS
	verdict.Concerns = p.GetBulletList("CONCERNS:")
	// Filter out "none" placeholders
	filtered := verdict.Concerns[:0]
	for _, c := range verdict.Concerns {
		if strings.ToLower(c) != "none" {
			filtered = append(filtered, c)
		}
	}
	verdict.Concerns = filtered

	// Parse REASONING
	verdict.Reasoning = p.Get("REASONING:")

	// Parse FEEDBACK (for REVISE)
	verdict.Feedback = p.Get("FEEDBACK:")

	return verdict
}

// ============================================================================
// Memory persistence
// ============================================================================

func saveCriticVerdict(verdict *CriticVerdict, proposed string) {
	dateKey := time.Now().Format("2006-01-02-150405")
	key := fmt.Sprintf("critic:%s:%s:%d", verdict.DecisionType, dateKey, verdict.Attempt)
	tags := []string{"critic", "verdict", string(verdict.DecisionType), string(verdict.Action)}

	content := fmt.Sprintf("%s [risk:%s] — %s",
		verdict.Action, verdict.RiskLevel, truncate(verdict.Reasoning, 200))
	if len(verdict.Concerns) > 0 {
		content += "\nConcerns: " + strings.Join(verdict.Concerns, "; ")
	}

	// Rejections persist longer so we can detect repeat proposals
	var expires *time.Time
	switch verdict.Action {
	case CriticReject, CriticRevise:
		t := time.Now().Add(30 * 24 * time.Hour)
		expires = &t
	default:
		t := time.Now().Add(7 * 24 * time.Hour)
		expires = &t
	}

	if err := database.SaveAutoMemory("decision", key, content, tags, expires); err != nil {
		log.Printf("[CRITIC] Failed to save verdict to memory: %v", err)
	}
}

func loadRejectionHistory(dt DecisionType) string {
	// Verdicts are saved with category="decision", key starts with "critic:"
	// Search by key prefix and filter by decision type tag
	memories, err := database.RecallMemories("critic:"+string(dt), "decision", 5)
	if err != nil || len(memories) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, m := range memories {
		isReject := false
		for _, tag := range m.Tags {
			if tag == "REJECT" || tag == "REVISE" {
				isReject = true
				break
			}
		}
		if isReject {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", m.CreatedAt.Format("2006-01-02"), truncate(m.Content, 150)))
		}
	}
	return sb.String()
}
