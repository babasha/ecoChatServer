package adkagent

import (
	"log"

	"github.com/egor/ecochatserver/database"
)

// loadPrompt loads the active prompt from DB for the given agent.
// Falls back to the hardcoded default if no DB prompt exists.
// This allows the Director (Level 2) to optimize prompts without code changes.
func loadPrompt(agentName string, fallback func() string) string {
	dbPrompt, err := database.GetActivePrompt(agentName)
	if err != nil {
		log.Printf("[PROMPT] Error loading prompt for %s: %v (using hardcoded fallback)", agentName, err)
		return fallback()
	}

	if dbPrompt != nil {
		log.Printf("[PROMPT] Loaded DB prompt for %s: version=%d, created_by=%s",
			agentName, dbPrompt.Version, dbPrompt.CreatedBy)
		return dbPrompt.Prompt
	}

	log.Printf("[PROMPT] No DB prompt for %s, using hardcoded default", agentName)
	return fallback()
}
