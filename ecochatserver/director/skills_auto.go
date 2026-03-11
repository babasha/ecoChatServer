package director

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// analyzeToolPatterns detects repeating tool usage and proposes new skills.
func (d *Director) analyzeToolPatterns(ctx context.Context) {
	since := time.Now().Add(-7 * 24 * time.Hour) // last 7 days
	patterns, err := database.GetRepeatingToolPatterns(since, 5)
	if err != nil {
		log.Printf("[DIRECTOR] Pattern analysis error: %v", err)
		return
	}

	if len(patterns) == 0 {
		return
	}

	// Check auto-skill limit (max 10)
	autoCount, err := database.CountAutoCreatedSkills()
	if err != nil || autoCount >= 10 {
		return
	}

	// Check cooldown: max 1 auto-skill per 24h
	cooldownKey := fmt.Sprintf("auto_skill_created:%s", time.Now().Format("2006-01-02"))
	existing, _ := database.RecallMemories(cooldownKey, "", 1)
	if len(existing) > 0 {
		return // already created one today
	}

	// Filter out tools that already have skills
	existingSkills, _ := database.GetAllSkills()
	skillNames := make(map[string]bool)
	for _, s := range existingSkills {
		skillNames[s.Name] = true
	}

	// Filter patterns: skip built-in tools and already-skilled tools
	var newPatterns []models.ToolPattern
	builtinTools := map[string]bool{
		"get_agent_metrics": true, "get_latest_report": true, "get_active_prompts": true,
		"get_prompt_history": true, "get_tool_stats": true, "run_analysis": true,
		"get_interaction_details": true, "get_recent_chats": true,
		"remember": true, "recall": true, "forget": true, "list_memories": true,
		"search_reports": true, "deep_search": true, "timeline": true,
		"create_skill": true, "edit_skill": true, "list_skills": true,
		"delete_skill": true, "test_skill": true,
		"get_identity": true, "update_identity": true, "introspect": true,
		"identity_history": true, "rollback_identity": true,
	}
	for _, p := range patterns {
		if !builtinTools[p.ToolName] && !skillNames[p.ToolName] {
			newPatterns = append(newPatterns, p)
		}
	}

	if len(newPatterns) == 0 {
		return
	}

	// Build prompt for LLM to propose a skill
	var sb strings.Builder
	sb.WriteString("Analyzing repeating tool usage patterns to suggest a reusable custom skill.\n\n")
	sb.WriteString("Frequently called tools (not yet formalized as skills):\n")
	for _, p := range newPatterns {
		sb.WriteString(fmt.Sprintf("- %s: called %d times, sample args: %s\n",
			p.ToolName, p.CallCount, p.SampleArgs))
	}
	sb.WriteString(`
Based on these patterns, propose ONE custom skill that would be most useful.

Respond in EXACTLY this format (no extra text):
SKILL_NAME: <lowercase_underscore_name>
SKILL_TYPE: sql_query|prompt_chain
DESCRIPTION: <what the skill does, 1 sentence>
CODE: <implementation - SQL query or prompt template>
PARAMETERS: <JSON Schema for parameters>
REASON: <why this skill would help>

If no useful skill can be created from these patterns, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Pattern→skill LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	skill := parseSkillProposal(resp.Text)
	if skill == nil {
		log.Printf("[DIRECTOR] Could not parse skill proposal from LLM response")
		return
	}

	skill.CreatedBy = "auto_pattern"
	skill.Tags = append(skill.Tags, "auto_created", "pattern_based")

	if err := database.CreateSkill(skill); err != nil {
		log.Printf("[DIRECTOR] Auto-skill creation failed: %v", err)
		return
	}

	// Save cooldown and memory
	database.SaveAutoMemory("decision", cooldownKey,
		fmt.Sprintf("Auto-created skill '%s' (%s) from repeating patterns", skill.Name, skill.SkillType),
		[]string{"auto_skill", "pattern"}, nil)

	log.Printf("[DIRECTOR] Auto-created skill '%s' (type=%s) from tool patterns", skill.Name, skill.SkillType)
}

// suggestSkillsFromReflection creates skills based on negative directive outcomes.
func (d *Director) suggestSkillsFromReflection(ctx context.Context) {
	since := time.Now().Add(-30 * 24 * time.Hour) // last 30 days

	// Check if there are enough negative outcomes to warrant a skill
	negByType, err := database.GetNegativeOutcomesByType(since)
	if err != nil || len(negByType) == 0 {
		return
	}

	// Need at least 3 negative outcomes in some category
	hasSignificantGap := false
	for _, count := range negByType {
		if count >= 3 {
			hasSignificantGap = true
			break
		}
	}
	if !hasSignificantGap {
		return
	}

	// Check cooldown and limits
	autoCount, err := database.CountAutoCreatedSkills()
	if err != nil || autoCount >= 10 {
		return
	}
	cooldownKey := fmt.Sprintf("auto_skill_reflection:%s", time.Now().Format("2006-01-02"))
	existing, _ := database.RecallMemories(cooldownKey, "", 1)
	if len(existing) > 0 {
		return
	}

	// Get the actual negative outcomes for context
	negOutcomes, err := database.GetNegativeOutcomesSince(since)
	if err != nil || len(negOutcomes) == 0 {
		return
	}

	// Also check memory for gap patterns
	gapMemories, _ := database.RecallMemories("tool_failure", "pattern", 5)

	// Build LLM prompt
	var sb strings.Builder
	sb.WriteString("Analyzing negative directive outcomes and system gaps to propose a useful custom skill.\n\n")
	sb.WriteString("Negative outcomes by type:\n")
	for dtype, count := range negByType {
		sb.WriteString(fmt.Sprintf("- %s: %d negative outcomes\n", dtype, count))
	}
	sb.WriteString("\nRecent negative outcomes:\n")
	for i, o := range negOutcomes {
		if i >= 5 {
			break
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s — %s\n",
			o.DirectiveType, truncateStr(o.DirectiveInstruction, 100), o.EvaluationNotes))
	}
	if len(gapMemories) > 0 {
		sb.WriteString("\nKnown gaps from memory:\n")
		for _, m := range gapMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", truncateStr(m.Content, 120)))
		}
	}

	sb.WriteString(`
Based on these gaps and failures, propose ONE custom skill that would help the Director be more effective.

Respond in EXACTLY this format:
SKILL_NAME: <lowercase_underscore_name>
SKILL_TYPE: sql_query|prompt_chain
DESCRIPTION: <what the skill does>
CODE: <implementation>
PARAMETERS: <JSON Schema>
REASON: <why this fills the gap>

If no skill would help, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Reflection→skill LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	skill := parseSkillProposal(resp.Text)
	if skill == nil {
		log.Printf("[DIRECTOR] Could not parse reflection skill proposal")
		return
	}

	skill.CreatedBy = "auto_reflection"
	skill.Tags = append(skill.Tags, "auto_created", "reflection_based")

	if err := database.CreateSkill(skill); err != nil {
		log.Printf("[DIRECTOR] Auto-skill from reflection failed: %v", err)
		return
	}

	// Save cooldown and memory
	database.SaveAutoMemory("decision", cooldownKey,
		fmt.Sprintf("Auto-created skill '%s' (%s) from self-reflection on negative outcomes",
			skill.Name, skill.SkillType),
		[]string{"auto_skill", "reflection"}, nil)

	log.Printf("[DIRECTOR] Auto-created skill '%s' from self-reflection", skill.Name)
}

// parseSkillProposal extracts a skill definition from LLM response text.
func parseSkillProposal(text string) *models.DirectorSkill {
	extract := func(label string) string {
		idx := strings.Index(text, label+":")
		if idx < 0 {
			return ""
		}
		rest := text[idx+len(label)+1:]
		endIdx := strings.Index(rest, "\n")
		if endIdx < 0 {
			endIdx = len(rest)
		}
		return strings.TrimSpace(rest[:endIdx])
	}

	name := extract("SKILL_NAME")
	skillType := extract("SKILL_TYPE")
	description := extract("DESCRIPTION")
	reason := extract("REASON")

	// CODE might be multiline — extract until next label
	codeStart := strings.Index(text, "CODE:")
	paramsStart := strings.Index(text, "PARAMETERS:")
	var code string
	if codeStart >= 0 && paramsStart >= 0 && paramsStart > codeStart {
		code = strings.TrimSpace(text[codeStart+5 : paramsStart])
	} else if codeStart >= 0 {
		code = extract("CODE")
	}

	params := extract("PARAMETERS")

	if name == "" || skillType == "" || code == "" {
		return nil
	}

	// Validate skill type
	if skillType != "sql_query" && skillType != "prompt_chain" {
		return nil // auto-skills limited to safe types
	}

	// Validate name format
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return nil
		}
	}

	// Default params if empty
	if params == "" || params == "{}" {
		params = `{"type":"object","properties":{}}`
	}

	// Add reason to description
	if reason != "" && len(description) < 200 {
		description = description + " (auto: " + reason + ")"
	}

	return &models.DirectorSkill{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Parameters:  params,
		SkillType:   skillType,
		Code:        code,
		Enabled:     true,
		Tags:        []string{},
	}
}
