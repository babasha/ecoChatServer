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

// generateDigestsIfNeeded creates weekly/monthly digests when a period has ended.
func (d *Director) generateDigestsIfNeeded() {
	now := time.Now()

	// Weekly digest: generate for last week if we're past Monday 02:00
	if now.Weekday() == time.Monday || now.Weekday() == time.Tuesday {
		lastWeekStart := now.AddDate(0, 0, -7)
		weekStart := time.Date(lastWeekStart.Year(), lastWeekStart.Month(), lastWeekStart.Day(), 0, 0, 0, 0, now.Location())
		// Align to Monday
		for weekStart.Weekday() != time.Monday {
			weekStart = weekStart.AddDate(0, 0, -1)
		}
		weekEnd := weekStart.AddDate(0, 0, 7)
		d.generateDigest("weekly", weekStart, weekEnd)
	}

	// Monthly digest: generate for last month on the 1st-2nd day
	if now.Day() <= 2 {
		monthStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
		monthEnd := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		d.generateDigest("monthly", monthStart, monthEnd)
	}
}

func (d *Director) generateDigest(periodType string, from, to time.Time) {
	// Check if digest already exists
	existing, err := database.GetDigestForPeriod(periodType, from)
	if err != nil {
		log.Printf("[DIRECTOR] Error checking digest: %v", err)
		return
	}
	if existing != nil {
		return // already generated
	}

	log.Printf("[DIRECTOR] Generating %s digest: %s → %s", periodType, from.Format("2006-01-02"), to.Format("2006-01-02"))

	// Get timeline data
	data, err := database.GetTimelineData(from, to, "full")
	if err != nil {
		log.Printf("[DIRECTOR] Error getting timeline data for digest: %v", err)
		return
	}

	// Count chats
	chatCount, _ := database.CountChatsInPeriod(from, to)

	// Build summary prompt for LLM
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Generate a brief %s digest (%s to %s) for the support system.\n\n",
		periodType, from.Format("2006-01-02"), to.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Stats: %d interactions, %d chats, escalation %.1f%%, empty %.1f%%, avg response %dms\n",
		data.TotalInteractions, chatCount, data.AvgEscalationRate*100, data.AvgEmptyRate*100, int(data.AvgResponseMs)))
	sb.WriteString(fmt.Sprintf("Reports generated: %d\n", data.ReportCount))

	if len(data.DirectiveStats) > 0 {
		sb.WriteString("Directive outcomes: ")
		for eff, count := range data.DirectiveStats {
			sb.WriteString(fmt.Sprintf("%s=%d ", eff, count))
		}
		sb.WriteString("\n")
	}

	for _, s := range data.ReportSummaries {
		sb.WriteString(s + "\n")
	}

	sb.WriteString("\nRespond in this exact format:\nSUMMARY: <2-3 sentences overview>\nKEY_EVENTS:\n- <event 1>\n- <event 2>\nTRENDS:\n- <trend 1>\nLESSONS:\n- <lesson 1>")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := d.provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   500,
	})
	if err != nil {
		log.Printf("[DIRECTOR] Error generating digest summary: %v", err)
		// Save with raw stats even without LLM summary
		resp = &llm.Response{
			Text: fmt.Sprintf("Автоматический дайджест: %d взаимодействий, эскалация %.1f%%, пустые %.1f%%",
				data.TotalInteractions, data.AvgEscalationRate*100, data.AvgEmptyRate*100),
		}
	}

	// Parse LLM response
	summary, keyEvents, trends, lessons := parseDigestResponse(resp.Text)

	digest := &models.DirectorDigest{
		ID:                uuid.New(),
		PeriodType:        periodType,
		PeriodStart:       from,
		PeriodEnd:         to,
		TotalChats:        chatCount,
		TotalInteractions: data.TotalInteractions,
		AvgEscalationRate: data.AvgEscalationRate,
		AvgEmptyRate:      data.AvgEmptyRate,
		AvgResponseMs:     data.AvgResponseMs,
		Summary:           summary,
		KeyEvents:         keyEvents,
		Trends:            trends,
		Lessons:           lessons,
	}

	if err := database.InsertDigest(digest); err != nil {
		log.Printf("[DIRECTOR] Error saving digest: %v", err)
		return
	}

	log.Printf("[DIRECTOR] %s digest saved: %d interactions, %d chats", periodType, data.TotalInteractions, chatCount)
}

// parseDigestResponse extracts structured sections from the LLM digest response.
func parseDigestResponse(text string) (summary string, keyEvents, trends, lessons []string) {
	sections := map[string]*[]string{
		"KEY_EVENTS:": &keyEvents,
		"TRENDS:":     &trends,
		"LESSONS:":    &lessons,
	}

	// Extract SUMMARY
	if idx := strings.Index(text, "SUMMARY:"); idx >= 0 {
		rest := text[idx+8:]
		endIdx := len(rest)
		for section := range sections {
			if si := strings.Index(rest, section); si >= 0 && si < endIdx {
				endIdx = si
			}
		}
		summary = strings.TrimSpace(rest[:endIdx])
	} else {
		summary = text
		if len(summary) > 500 {
			summary = summary[:500]
		}
		return
	}

	// Extract bullet lists
	for section, target := range sections {
		idx := strings.Index(text, section)
		if idx < 0 {
			continue
		}
		rest := text[idx+len(section):]
		endIdx := len(rest)
		for other := range sections {
			if other == section {
				continue
			}
			if si := strings.Index(rest, other); si >= 0 && si < endIdx {
				endIdx = si
			}
		}
		for _, line := range strings.Split(rest[:endIdx], "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
				*target = append(*target, strings.TrimSpace(line[2:]))
			}
		}
	}
	return
}
