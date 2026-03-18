package director

import (
	"context"
	"encoding/json"
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

	// Inject lessons from past skill creation failures
	if lessonOverlay := BuildSkillCreationLessonOverlay(); lessonOverlay != "" {
		sb.WriteString(lessonOverlay)
		sb.WriteString("\n")
	}

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
TEST_ARGS: <JSON object with sample values for testing, e.g. {"hours": 24, "name": "test"}>
REASON: <why this skill would help>

If no useful skill can be created from these patterns, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Pattern→skill LLM error: %v", err)
		ExtractLLMError("skill_pattern", err, truncate(sb.String(), 200))
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	proposal := parseSkillProposal(resp.Text)
	if proposal == nil {
		log.Printf("[DIRECTOR] Could not parse skill proposal from LLM response")
		ExtractParsingError("skill_pattern", resp.Text)
		return
	}
	d.createAutoSkill(ctx, proposal, "auto_pattern", cooldownKey,
		fmt.Sprintf("Source: tool pattern analysis\nPatterns: %s", sb.String()),
		[]string{"pattern_based"})
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
			o.DirectiveType, truncate(o.DirectiveInstruction, 100), o.EvaluationNotes))
	}
	if len(gapMemories) > 0 {
		sb.WriteString("\nKnown gaps from memory:\n")
		for _, m := range gapMemories {
			sb.WriteString(fmt.Sprintf("- %s\n", truncate(m.Content, 120)))
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
TEST_ARGS: <JSON object with sample values for testing, e.g. {"hours": 24}>
REASON: <why this fills the gap>

If no skill would help, respond with: NO_SKILL_NEEDED`)

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Reflection→skill LLM error: %v", err)
		ExtractLLMError("skill_reflection", err, truncate(sb.String(), 200))
		return
	}

	if strings.Contains(resp.Text, "NO_SKILL_NEEDED") {
		return
	}

	proposal := parseSkillProposal(resp.Text)
	if proposal == nil {
		log.Printf("[DIRECTOR] Could not parse reflection skill proposal")
		ExtractParsingError("skill_reflection", resp.Text)
		return
	}
	d.createAutoSkill(ctx, proposal, "auto_reflection", cooldownKey,
		fmt.Sprintf("Source: self-reflection on negative outcomes\n%s", sb.String()),
		[]string{"reflection_based"})
}

// createAutoSkill is the shared pipeline for both pattern-based and reflection-based skill creation.
// Handles: Critic review → Pre-flight validation → DB create → cooldown memory.
func (d *Director) createAutoSkill(ctx context.Context, proposal *SkillProposal, source, cooldownKey, criticContext string, extraTags []string) {
	skill := proposal.Skill

	// 1. Critic review
	skillDescription := fmt.Sprintf("Name: %s\nType: %s\nDescription: %s\nCode: %s\nParameters: %s",
		skill.Name, skill.SkillType, skill.Description, skill.Code, skill.Parameters)

	verdict, err := d.criticize(ctx, DecisionSkillCreate, skillDescription, criticContext, 1)
	if err != nil {
		log.Printf("[CRITIC] Skill review error for '%s', skipping: %v", skill.Name, err)
		return
	}
	if verdict.Action != CriticApprove {
		log.Printf("[CRITIC] Skill '%s' %s: %s", skill.Name, verdict.Action, verdict.Reasoning)
		return
	}
	if verdict.RiskLevel == RiskHigh {
		log.Printf("[CRITIC] Skill '%s' approved with HIGH risk — saving as pending", skill.Name)
		savePendingDecision("skill_create", skill.Name, skill.Description, verdict,
			append([]string{"skill_create"}, extraTags...))
		return
	}

	// 2. Pre-flight validation
	pfResult := d.PreflightValidate(ctx, proposal)
	if !pfResult.Passed {
		log.Printf("[PREFLIGHT] Skill '%s' failed: %s", skill.Name, strings.Join(pfResult.Errors, "; "))
		SavePreflightLesson(skill.Name, skill.SkillType, pfResult)
		return
	}
	skill.Code = pfResult.SkillCode

	// 3. Create in DB
	skill.CreatedBy = source
	skill.Tags = append(skill.Tags, "auto_created", "critic_approved", "preflight_passed")
	skill.Tags = append(skill.Tags, extraTags...)
	if pfResult.Repaired {
		skill.Tags = append(skill.Tags, "auto_repaired")
	}

	if err := database.CreateSkill(skill); err != nil {
		log.Printf("[DIRECTOR] Auto-skill '%s' creation failed: %v", skill.Name, err)
		SavePartialSkill(skill)
		ExtractSystemError("create_skill_"+source, err)
		return
	}

	// 4. Save cooldown
	database.SaveAutoMemory("decision", cooldownKey,
		fmt.Sprintf("Auto-created skill '%s' (%s) [source: %s, risk: %s, repaired: %v]",
			skill.Name, skill.SkillType, source, verdict.RiskLevel, pfResult.Repaired),
		[]string{"auto_skill", source}, nil)

	log.Printf("[DIRECTOR] Auto-created skill '%s' (%s) [source: %s, preflight: passed]",
		skill.Name, skill.SkillType, source)
}

// parseSkillProposal extracts a skill definition and test args from LLM response text.
func parseSkillProposal(text string) *SkillProposal {
	// Use SectionParser for consistent parsing
	sections := []string{"SKILL_NAME:", "SKILL_TYPE:", "DESCRIPTION:", "CODE:", "PARAMETERS:"}
	hasTestArgs := strings.Contains(text, "TEST_ARGS:")
	if hasTestArgs {
		sections = append(sections, "TEST_ARGS:")
	}
	sections = append(sections, "REASON:")

	p := NewSectionParser(text, sections)

	name := strings.TrimSpace(strings.Split(p.Get("SKILL_NAME:"), "\n")[0])
	skillType := strings.TrimSpace(strings.Split(p.Get("SKILL_TYPE:"), "\n")[0])
	description := strings.TrimSpace(strings.Split(p.Get("DESCRIPTION:"), "\n")[0])
	reason := strings.TrimSpace(strings.Split(p.Get("REASON:"), "\n")[0])

	// CODE and PARAMETERS are multiline — strip code block markers
	code := stripCodeBlock(p.Get("CODE:"))
	params := stripCodeBlock(p.Get("PARAMETERS:"))
	var testArgsRaw string
	if hasTestArgs {
		testArgsRaw = stripCodeBlock(p.Get("TEST_ARGS:"))
	}

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

	// Parse test args
	var testArgs map[string]interface{}
	if testArgsRaw != "" {
		if err := json.Unmarshal([]byte(testArgsRaw), &testArgs); err != nil {
			log.Printf("[PREFLIGHT] Could not parse TEST_ARGS: %v", err)
			testArgs = map[string]interface{}{}
		}
	}
	if testArgs == nil {
		testArgs = map[string]interface{}{}
	}

	// Add reason to description
	if reason != "" && len(description) < 200 {
		description = description + " (auto: " + reason + ")"
	}

	return &SkillProposal{
		Skill: &models.DirectorSkill{
			ID:          uuid.New(),
			Name:        name,
			Description: description,
			Parameters:  params,
			SkillType:   skillType,
			Code:        code,
			Enabled:     true,
			Tags:        []string{},
		},
		TestArgs: testArgs,
	}
}
