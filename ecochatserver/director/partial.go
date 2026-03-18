package director

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/models"
)

// ============================================================================
// Partial result types — what intermediate state we capture
// ============================================================================

// PartialType identifies what kind of partial result is cached.
type PartialType string

const (
	PartialAnalysis     PartialType = "analysis"      // summaries + stats collected, LLM not called yet
	PartialReport       PartialType = "report"         // report built but not saved to DB
	PartialSkill        PartialType = "skill"          // skill ready but DB create failed
	PartialPromptApply  PartialType = "prompt_apply"   // prompt inserted but not activated
)

// partialKeyPrefix returns the memory key prefix for partial results.
func partialKeyPrefix(pt PartialType) string {
	return fmt.Sprintf("partial:%s", pt)
}

// partialKey returns the full memory key for a partial result.
func partialKey(pt PartialType) string {
	return fmt.Sprintf("partial:%s:%s", pt, time.Now().Format("2006-01-02"))
}

// ============================================================================
// Save partial results
// ============================================================================

// PartialAnalysisData captures the intermediate state of an analysis run.
type PartialAnalysisData struct {
	Summaries    []string   `json:"summaries"`
	Stats        DailyStats `json:"stats"`
	TriggerEvent string     `json:"trigger_event"`
	ReportType   string     `json:"report_type"`
	CollectedAt  time.Time  `json:"collected_at"`
}

// SavePartialAnalysis caches summaries + stats when LLM call fails.
// Next analyze() run can pick these up instead of re-collecting.
func SavePartialAnalysis(summaries []string, stats DailyStats, reportType, triggerEvent string) {
	data := PartialAnalysisData{
		Summaries:    summaries,
		Stats:        stats,
		TriggerEvent: triggerEvent,
		ReportType:   reportType,
		CollectedAt:  time.Now(),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[PARTIAL] Failed to marshal analysis data: %v", err)
		return
	}

	// Expires in 24h — only useful for next run
	expires := time.Now().Add(24 * time.Hour)
	key := partialKey(PartialAnalysis)

	if err := database.SaveAutoMemory("decision", key, string(jsonBytes), []string{"partial", "analysis"}, &expires); err != nil {
		log.Printf("[PARTIAL] Failed to save analysis partial: %v", err)
		return
	}

	log.Printf("[PARTIAL] Saved analysis partial: %d summaries, trigger=%s", len(summaries), triggerEvent)
}

// LoadPartialAnalysis retrieves cached summaries + stats from a previous failed run.
// Returns nil if no partial result exists or if it's too old.
func LoadPartialAnalysis() *PartialAnalysisData {
	memories, err := database.RecallMemories(partialKeyPrefix(PartialAnalysis), "", 1)
	if err != nil || len(memories) == 0 {
		return nil
	}

	m := memories[0]

	// Only use if less than 24h old
	if time.Since(m.CreatedAt) > 24*time.Hour {
		return nil
	}

	var data PartialAnalysisData
	if err := json.Unmarshal([]byte(m.Content), &data); err != nil {
		log.Printf("[PARTIAL] Failed to unmarshal analysis partial: %v", err)
		return nil
	}

	log.Printf("[PARTIAL] Loaded analysis partial: %d summaries from %s",
		len(data.Summaries), data.CollectedAt.Format("15:04"))
	return &data
}

// ClearPartialAnalysis removes cached analysis data after successful completion.
func ClearPartialAnalysis() {
	clearPartials(PartialAnalysis)
}

// ============================================================================
// Partial report — full report built but saveReport() failed
// ============================================================================

// PartialReportData captures a complete report that couldn't be saved to DB.
type PartialReportData struct {
	ParsedDirectorResponse        // embed shared fields (Analysis, Directives, etc.)
	SummaryCount           int    `json:"summary_count"`
	ReportType             string `json:"report_type"`
	TriggerEvent           string `json:"trigger_event"`
	SavedAt                time.Time `json:"saved_at"`
}

// SavePartialReport caches a complete report when DB save fails.
func SavePartialReport(report *Report) {
	data := PartialReportData{
		ParsedDirectorResponse: ParsedDirectorResponse{
			Analysis:           report.Analysis,
			Directives:         report.Directives,
			CustomerComplaints: report.CustomerComplaints,
			KeyObservations:    report.KeyObservations,
			Expectations:       report.Expectations,
		},
		SummaryCount: report.SummaryCount,
		ReportType:   report.ReportType,
		TriggerEvent: report.TriggerEvent,
		SavedAt:      time.Now(),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[PARTIAL] Failed to marshal report data: %v", err)
		return
	}

	expires := time.Now().Add(24 * time.Hour)
	key := partialKey(PartialReport)

	if err := database.SaveAutoMemory("decision", key, string(jsonBytes), []string{"partial", "report"}, &expires); err != nil {
		log.Printf("[PARTIAL] Failed to save report partial: %v", err)
		return
	}

	log.Printf("[PARTIAL] Saved report partial: %d directives, %d observations",
		len(report.Directives), len(report.KeyObservations))
}

// LoadPartialReport retrieves a cached report from a previous failed save.
func LoadPartialReport() *PartialReportData {
	memories, err := database.RecallMemories(partialKeyPrefix(PartialReport), "", 1)
	if err != nil || len(memories) == 0 {
		return nil
	}

	m := memories[0]
	if time.Since(m.CreatedAt) > 24*time.Hour {
		return nil
	}

	var data PartialReportData
	if err := json.Unmarshal([]byte(m.Content), &data); err != nil {
		log.Printf("[PARTIAL] Failed to unmarshal report partial: %v", err)
		return nil
	}

	log.Printf("[PARTIAL] Loaded report partial from %s", data.SavedAt.Format("15:04"))
	return &data
}

// ClearPartialReport removes cached report data after successful save.
func ClearPartialReport() {
	clearPartials(PartialReport)
}

// ============================================================================
// Partial skill — fully prepared skill definition when DB create failed
// ============================================================================

// PartialSkillData captures a skill that passed all gates but couldn't be saved to DB.
type PartialSkillData struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	SkillType   string   `json:"skill_type"`
	Code        string   `json:"code"`
	Parameters  string   `json:"parameters"`
	CreatedBy   string   `json:"created_by"`
	Tags        []string `json:"tags"`
	SavedAt     time.Time `json:"saved_at"`
}

// SavePartialSkill caches a fully prepared skill when DB create fails.
func SavePartialSkill(skill *models.DirectorSkill) {
	data := PartialSkillData{
		Name:        skill.Name,
		Description: skill.Description,
		SkillType:   skill.SkillType,
		Code:        skill.Code,
		Parameters:  skill.Parameters,
		CreatedBy:   skill.CreatedBy,
		Tags:        skill.Tags,
		SavedAt:     time.Now(),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		log.Printf("[PARTIAL] Failed to marshal skill data: %v", err)
		return
	}

	expires := time.Now().Add(48 * time.Hour) // skills get 48h — they're expensive to recreate
	key := fmt.Sprintf("partial:skill:%s:%s", skill.Name, time.Now().Format("2006-01-02"))

	if err := database.SaveAutoMemory("decision", key, string(jsonBytes), []string{"partial", "skill", skill.SkillType}, &expires); err != nil {
		log.Printf("[PARTIAL] Failed to save skill partial: %v", err)
		return
	}

	log.Printf("[PARTIAL] Saved skill partial: '%s' (%s) — awaiting DB retry", skill.Name, skill.SkillType)
}

// LoadPendingSkills retrieves skills that passed all gates but couldn't be saved.
func LoadPendingSkills() []PartialSkillData {
	memories, err := database.RecallMemories("partial:skill", "", 5)
	if err != nil || len(memories) == 0 {
		return nil
	}

	var skills []PartialSkillData
	for _, m := range memories {
		if time.Since(m.CreatedAt) > 48*time.Hour {
			continue
		}
		var data PartialSkillData
		if err := json.Unmarshal([]byte(m.Content), &data); err != nil {
			continue
		}
		skills = append(skills, data)
	}

	if len(skills) > 0 {
		log.Printf("[PARTIAL] Found %d pending skills awaiting DB retry", len(skills))
	}
	return skills
}

// RetryPendingSkills attempts to create skills that previously failed DB save.
// Called at the start of analyzeToolPatterns().
func RetryPendingSkills() {
	pending := LoadPendingSkills()
	if len(pending) == 0 {
		return
	}

	for _, data := range pending {
		// Check if skill was already created (maybe by another path)
		existing, _ := database.GetSkillByName(data.Name)
		if existing != nil {
			log.Printf("[PARTIAL] Skill '%s' already exists, clearing partial", data.Name)
			clearPartialSkill(data.Name)
			continue
		}

		skill := &models.DirectorSkill{
			Name:        data.Name,
			Description: data.Description,
			SkillType:   data.SkillType,
			Code:        data.Code,
			Parameters:  data.Parameters,
			CreatedBy:   data.CreatedBy,
			Tags:        append(data.Tags, "retry_from_partial"),
			Enabled:     true,
		}

		if err := database.CreateSkill(skill); err != nil {
			log.Printf("[PARTIAL] Retry failed for skill '%s': %v", data.Name, err)
			continue
		}

		clearPartialSkill(data.Name)
		log.Printf("[PARTIAL] Successfully retried skill '%s' from partial", data.Name)
	}
}

// clearPartialSkill removes a specific skill's partial data.
func clearPartialSkill(name string) {
	// Find and delete by key prefix (key includes date suffix)
	memories, err := database.RecallMemories(fmt.Sprintf("partial:skill:%s", name), "decision", 5)
	if err != nil || len(memories) == 0 {
		return
	}
	for _, m := range memories {
		database.DeleteMemory("decision", m.Key)
	}
}

// ============================================================================
// Partial prompt apply — prompt inserted but activation failed
// ============================================================================

// PartialPromptApplyData captures an orphaned prompt that was inserted but not activated.
type PartialPromptApplyData struct {
	AgentName string `json:"agent_name"`
	Version   int    `json:"version"`
	SavedAt   time.Time `json:"saved_at"`
}

// SavePartialPromptApply records an orphaned prompt version for cleanup/retry.
func SavePartialPromptApply(agentName string, version int) {
	data := PartialPromptApplyData{
		AgentName: agentName,
		Version:   version,
		SavedAt:   time.Now(),
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return
	}

	expires := time.Now().Add(24 * time.Hour)
	key := fmt.Sprintf("partial:prompt_apply:%s:%d", agentName, version)

	database.SaveAutoMemory("decision", key, string(jsonBytes),
		[]string{"partial", "prompt_apply", agentName}, &expires)

	log.Printf("[PARTIAL] Saved orphaned prompt: %s v%d — needs activation or cleanup", agentName, version)
}

// RetryPendingPromptActivations attempts to activate orphaned prompt versions.
func RetryPendingPromptActivations() {
	memories, err := database.RecallMemories("partial:prompt_apply", "", 5)
	if err != nil || len(memories) == 0 {
		return
	}

	for _, m := range memories {
		if time.Since(m.CreatedAt) > 24*time.Hour {
			continue
		}

		var data PartialPromptApplyData
		if err := json.Unmarshal([]byte(m.Content), &data); err != nil {
			continue
		}

		if err := database.ActivatePrompt(data.AgentName, data.Version); err != nil {
			log.Printf("[PARTIAL] Retry activation failed for %s v%d: %v", data.AgentName, data.Version, err)
			continue
		}

		database.DeleteMemory("decision", fmt.Sprintf("partial:prompt_apply:%s:%d", data.AgentName, data.Version))
		log.Printf("[PARTIAL] Successfully activated orphaned prompt %s v%d", data.AgentName, data.Version)
	}
}

// ============================================================================
// Utility
// ============================================================================

// clearPartials removes all partial results of a given type.
func clearPartials(pt PartialType) {
	memories, err := database.RecallMemories(partialKeyPrefix(pt), "", 10)
	if err != nil || len(memories) == 0 {
		return
	}

	for _, m := range memories {
		hasTag := false
		for _, tag := range m.Tags {
			if tag == "partial" {
				hasTag = true
				break
			}
		}
		if hasTag {
			database.DeleteMemory("decision", m.Key)
		}
	}

	log.Printf("[PARTIAL] Cleared %d partial results for type '%s'", len(memories), pt)
}

// InjectPartialContext builds a context section from partial results for LLM prompts.
// If there are cached partial results, include their insights so the next LLM call
// can build on prior work rather than starting from scratch.
func InjectPartialContext() string {
	var parts []string

	// Check for partial report (prior analysis that wasn't saved)
	if pr := LoadPartialReport(); pr != nil {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Prior analysis (from %s, not saved due to error):\n", pr.SavedAt.Format("15:04")))
		sb.WriteString(fmt.Sprintf("Analysis: %s\n", truncate(pr.Analysis, 200)))
		if len(pr.KeyObservations) > 0 {
			sb.WriteString("Key observations: ")
			sb.WriteString(strings.Join(pr.KeyObservations, "; "))
			sb.WriteString("\n")
		}
		parts = append(parts, sb.String())
	}

	if len(parts) == 0 {
		return ""
	}

	return "\n[PARTIAL RESULTS FROM PRIOR FAILED RUN]\n" + strings.Join(parts, "\n") + "[END PARTIAL RESULTS]\n"
}
