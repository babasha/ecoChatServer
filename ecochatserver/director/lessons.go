package director

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
)

// ============================================================================
// Lesson types — classification of failures (like AutoResearchClaw's categories)
// ============================================================================

// LessonType classifies what kind of failure produced the lesson.
type LessonType string

const (
	LessonLLMError          LessonType = "llm_error"          // LLM call failed
	LessonSkillFailure      LessonType = "skill_failure"      // skill creation/execution failed
	LessonPromptRegression  LessonType = "prompt_regression"  // prompt change made things worse
	LessonDirectiveNegative LessonType = "directive_negative" // directive had negative outcome
	LessonCriticRejection   LessonType = "critic_rejection"   // proposal rejected by Critic
	LessonRepairFailure     LessonType = "repair_failure"     // repair attempt failed
	LessonPreflightFailure  LessonType = "preflight_failure"  // preflight validation failed
	LessonSystemError       LessonType = "system_error"       // DB/infrastructure error
	LessonParsingError      LessonType = "parsing_error"      // LLM response could not be parsed
)

// LessonSeverity indicates how critical the lesson is.
type LessonSeverity string

const (
	SeverityLow      LessonSeverity = "low"      // informational
	SeverityMedium   LessonSeverity = "medium"    // should be addressed
	SeverityHigh     LessonSeverity = "high"      // critical, affects system quality
	SeverityCritical LessonSeverity = "critical"  // system-level failure
)

// ============================================================================
// ExtractLesson — universal entry point to record a failure
// ============================================================================

// ExtractLesson records a lesson from a failure for future reference.
// This is called from every failure point in the Director pipeline.
func ExtractLesson(lessonType LessonType, severity LessonSeverity, source, description string, context map[string]string) {
	dateKey := time.Now().Format("2006-01-02-150405")
	key := fmt.Sprintf("lesson:%s:%s:%s", lessonType, source, dateKey)

	// Build structured content
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] %s\n", severity, description))
	if len(context) > 0 {
		sb.WriteString("Context:\n")
		for k, v := range context {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", k, truncate(v, 150)))
		}
	}

	content := truncate(sb.String(), 450)
	tags := []string{"lesson", string(lessonType), string(severity), source}

	// Lessons have 30-day expiry (time-decay like AutoResearchClaw)
	expires := time.Now().Add(30 * 24 * time.Hour)

	if err := database.SaveAutoMemory("lesson", key, content, tags, &expires); err != nil {
		// Don't recurse — just log if saving fails
		log.Printf("[LESSON] Failed to save lesson: %v", err)
		return
	}

	log.Printf("[LESSON] Extracted: [%s/%s] %s — %s", lessonType, severity, source, truncate(description, 80))
}

// ============================================================================
// Convenience extractors for common failure types
// ============================================================================

// ExtractLLMError records a lesson from a failed LLM call.
func ExtractLLMError(source string, err error, promptSnippet string) {
	ExtractLesson(LessonLLMError, SeverityMedium, source, err.Error(), map[string]string{
		"prompt_snippet": truncate(promptSnippet, 200),
	})
}

// ExtractCriticRejection records a lesson from a Critic rejection.
func ExtractCriticRejection(decisionType DecisionType, verdict *CriticVerdict, proposedSnippet string) {
	ExtractLesson(LessonCriticRejection, SeverityMedium, string(decisionType),
		fmt.Sprintf("Critic %s: %s", verdict.Action, verdict.Reasoning),
		map[string]string{
			"concerns":  strings.Join(verdict.Concerns, "; "),
			"risk":      string(verdict.RiskLevel),
			"proposed":  truncate(proposedSnippet, 200),
		})
}

// ExtractPromptRegression records a lesson from a prompt rollback.
func ExtractPromptRegression(agentName, reason string) {
	ExtractLesson(LessonPromptRegression, SeverityHigh, agentName, reason, nil)
}

// ExtractDirectiveNegative records a lesson from a negative directive outcome.
func ExtractDirectiveNegative(directiveType, instruction, notes string) {
	ExtractLesson(LessonDirectiveNegative, SeverityMedium, directiveType,
		fmt.Sprintf("Directive failed: %s", truncate(instruction, 100)),
		map[string]string{
			"evaluation": notes,
		})
}

// ExtractRepairFailure records a lesson from a failed repair attempt.
func ExtractRepairFailure(skillName, skillType, errorMsg string, attempt int) {
	ExtractLesson(LessonRepairFailure, SeverityMedium, "preflight",
		fmt.Sprintf("Repair attempt %d failed for skill '%s'", attempt, skillName),
		map[string]string{
			"skill_type": skillType,
			"error":      errorMsg,
		})
}

// ExtractPreflightFailure records a lesson from a failed preflight validation.
func ExtractPreflightFailure(skillName, skillType string, errors []string) {
	ExtractLesson(LessonPreflightFailure, SeverityMedium, "preflight",
		fmt.Sprintf("Skill '%s' (%s) failed preflight", skillName, skillType),
		map[string]string{
			"errors": strings.Join(errors, "; "),
		})
}

// ExtractParsingError records a lesson when LLM output couldn't be parsed.
func ExtractParsingError(source, rawOutput string) {
	ExtractLesson(LessonParsingError, SeverityLow, source,
		"Could not parse LLM response into expected format",
		map[string]string{
			"raw_output": truncate(rawOutput, 200),
		})
}

// ExtractSystemError records a lesson from infrastructure failures.
func ExtractSystemError(source string, err error) {
	ExtractLesson(LessonSystemError, SeverityCritical, source, err.Error(), nil)
}

// ============================================================================
// QueryLessonsForContext — retrieve relevant lessons for a specific task
// ============================================================================

// QueryLessonsForContext retrieves lessons relevant to the given context.
// Uses tag-based filtering to find lessons of specific types.
func QueryLessonsForContext(lessonTypes []LessonType, maxResults int) []string {
	var allLessons []string

	for _, lt := range lessonTypes {
		// category="lesson", query by lesson type tag in key prefix
		memories, err := database.RecallMemories("lesson:"+string(lt), "lesson", maxResults)
		if err != nil {
			continue
		}
		for _, m := range memories {
			allLessons = append(allLessons, fmt.Sprintf("[%s, %s] %s",
				m.CreatedAt.Format("2006-01-02"), lt, truncate(m.Content, 200)))
		}
	}

	// Limit total results
	if len(allLessons) > maxResults {
		allLessons = allLessons[:maxResults]
	}

	return allLessons
}

// ============================================================================
// BuildLessonOverlay — inject lessons into LLM prompts
// (like AutoResearchClaw's build_overlay())
// ============================================================================

// BuildLessonOverlay returns a formatted section of relevant lessons
// for injection into LLM prompts. Returns empty string if no lessons found.
func BuildLessonOverlay(context string, lessonTypes []LessonType) string {
	lessons := QueryLessonsForContext(lessonTypes, 7)
	if len(lessons) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n[LESSONS FROM PAST FAILURES — avoid repeating these mistakes]\n")
	for _, l := range lessons {
		sb.WriteString(fmt.Sprintf("- %s\n", l))
	}
	sb.WriteString("[END LESSONS]\n")

	return sb.String()
}

// BuildAnalysisLessonOverlay returns lessons relevant for the analysis pipeline.
func BuildAnalysisLessonOverlay() string {
	return BuildLessonOverlay("analysis", []LessonType{
		LessonDirectiveNegative,
		LessonPromptRegression,
		LessonCriticRejection,
	})
}

// BuildSkillCreationLessonOverlay returns lessons relevant for skill creation.
func BuildSkillCreationLessonOverlay() string {
	return BuildLessonOverlay("skill_creation", []LessonType{
		LessonSkillFailure,
		LessonPreflightFailure,
		LessonRepairFailure,
		LessonCriticRejection,
	})
}

// BuildPromptOptLessonOverlay returns lessons relevant for prompt optimization.
func BuildPromptOptLessonOverlay() string {
	return BuildLessonOverlay("prompt_optimization", []LessonType{
		LessonPromptRegression,
		LessonCriticRejection,
		LessonDirectiveNegative,
	})
}
