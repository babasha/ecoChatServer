package director

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/egor/ecochatserver/database"
)

// selfReflect evaluates the effectiveness of previous directives.
// Compares metrics before/after each directive and saves learnings to memory.
func (d *Director) selfReflect(ctx context.Context) {
	// Get pending directive outcomes (created >24h ago, not yet evaluated)
	outcomes, err := database.GetPendingDirectiveOutcomes()
	if err != nil {
		log.Printf("[DIRECTOR] Self-reflection: failed to get pending outcomes: %v", err)
		return
	}

	if len(outcomes) == 0 {
		return
	}

	log.Printf("[DIRECTOR] Self-reflection: evaluating %d pending directives", len(outcomes))

	// Get current aggregate metrics
	since := time.Now().Add(-24 * time.Hour)
	currentStats, err := database.GetAgentStatsSince(since)
	if err != nil {
		log.Printf("[DIRECTOR] Self-reflection: failed to get current stats: %v", err)
		return
	}

	agg := AggregateAgentStats(currentStats)

	for _, o := range outcomes {
		effectiveness := "neutral"
		var notes string

		// Compare before vs after
		escDelta := agg.EscRate - o.EscalationRateBefore
		emptyDelta := agg.EmptyRate - o.EmptyRateBefore

		if escDelta < -0.05 || emptyDelta < -0.05 {
			effectiveness = "positive"
			notes = fmt.Sprintf("Улучшение: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, agg.EscRate*100,
				o.EmptyRateBefore*100, agg.EmptyRate*100)
		} else if escDelta > 0.1 || emptyDelta > 0.1 {
			effectiveness = "negative"
			notes = fmt.Sprintf("Ухудшение: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, agg.EscRate*100,
				o.EmptyRateBefore*100, agg.EmptyRate*100)
		} else {
			notes = fmt.Sprintf("Без изменений: esc %.0f%%→%.0f%%, empty %.0f%%→%.0f%%",
				o.EscalationRateBefore*100, agg.EscRate*100,
				o.EmptyRateBefore*100, agg.EmptyRate*100)
		}

		// Update outcome in DB
		if err := database.EvaluateDirectiveOutcome(o.ID, effectiveness, notes,
			agg.EscRate, agg.EmptyRate, agg.AvgMs); err != nil {
			log.Printf("[DIRECTOR] Self-reflection: failed to evaluate outcome: %v", err)
			continue
		}

		// Save learning to memory
		content := fmt.Sprintf("Директива '%s' — %s. %s",
			truncate(o.DirectiveInstruction, 100), effectiveness, notes)

		database.SaveAutoMemory("insight",
			fmt.Sprintf("directive_result:%s:%s", o.CreatedAt.Format("20060102"), o.DirectiveType),
			content,
			[]string{"self_reflection", "directive", effectiveness},
			nil)

		// Extract lesson from negative outcomes
		if effectiveness == "negative" {
			ExtractDirectiveNegative(o.DirectiveType, o.DirectiveInstruction, notes)
		}

		log.Printf("[DIRECTOR] Self-reflection: directive [%s] → %s", o.DirectiveType, effectiveness)
	}

	// After evaluating directives, check if gaps warrant auto-skill creation
	d.suggestSkillsFromReflection(ctx)
}
