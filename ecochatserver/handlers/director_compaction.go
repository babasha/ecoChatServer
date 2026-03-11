package handlers

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
	"github.com/google/uuid"
)

// ============================================================================
// Compaction — summarize old messages instead of dropping them
// ============================================================================

// runCompaction summarizes old messages via LLM and saves to DB.
// Runs in a goroutine to not block the chat response.
// Also flushes key facts to persistent memory.
func runCompaction(provider llm.Provider, adminID string, messagesToCompact []llm.Message) {
	if len(messagesToCompact) < 2 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Load existing compaction for merge
	existingCompaction, _ := database.GetLatestCompaction(adminID)
	existingSummary := ""
	var previousID *uuid.UUID
	if existingCompaction != nil {
		existingSummary = existingCompaction.Summary
		previousID = &existingCompaction.ID
	}

	// 2. Build summarization prompt
	var sb strings.Builder
	if existingSummary != "" {
		sb.WriteString("Предыдущая сводка разговора:\n---\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n---\n\n")
	}
	sb.WriteString("Новые сообщения для включения в сводку:\n---\n")
	for _, m := range messagesToCompact {
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}
	sb.WriteString("---\n\n")
	sb.WriteString(`Создай ОБЪЕДИНЁННУЮ сводку разговора:
1. Сохрани все ключевые решения, факты и контекст из предыдущей сводки
2. Добавь новую информацию из сообщений выше
3. Отметь незавершённые темы и открытые вопросы
4. Будь кратким но полным (макс 400 слов)
5. Используй буллеты для структуры
6. Пиши на языке разговора

Выведи ТОЛЬКО сводку, без преамбулы.`)

	// 3. Call LLM for summarization
	resp, err := provider.GenerateResponse(ctx, sb.String(), nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[COMPACTION] LLM summarization failed: %v", err)
		// Fallback: still flush to memory
		go flushMemoryBeforeCompaction(provider, messagesToCompact)
		return
	}

	summary := resp.Text
	if len(summary) > 3000 {
		summary = summary[:3000]
	}

	// 4. Save compaction to DB
	now := time.Now()
	messagesFrom := now.Add(-time.Duration(len(messagesToCompact)) * time.Minute) // approximate
	_, err = database.SaveCompaction(adminID, summary, messagesFrom, now,
		len(messagesToCompact), nil, previousID)
	if err != nil {
		log.Printf("[COMPACTION] DB save failed: %v", err)
	} else {
		log.Printf("[COMPACTION] Saved compaction: %d messages → %d chars summary (admin=%s)",
			len(messagesToCompact), len(summary), adminID[:min(8, len(adminID))])
	}

	// 5. Also flush key facts to persistent memory
	go flushMemoryBeforeCompaction(provider, messagesToCompact)
}

// ============================================================================
// Memory Flush — extract key facts from compacted messages into persistent memory
// ============================================================================

// flushMemoryBeforeCompaction extracts important information from messages
// about to be compacted and saves it to persistent memory.
// Runs in a goroutine to not block the chat response.
func flushMemoryBeforeCompaction(provider llm.Provider, dropped []llm.Message) {
	if len(dropped) < 2 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Build a compact representation of dropped messages
	var sb strings.Builder
	for _, m := range dropped {
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}

	// Fetch existing hot memories for deduplication awareness
	existingContext := ""
	if hotMems, err := database.GetHotMemories(5); err == nil && len(hotMems) > 0 {
		var existingSB strings.Builder
		existingSB.WriteString("\nAlready stored in memory (DO NOT duplicate):\n")
		for _, m := range hotMems {
			content := m.Content
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			existingSB.WriteString(fmt.Sprintf("- [%s/%s] %s\n", m.Category, m.Key, content))
		}
		existingContext = existingSB.String()
	}

	prompt := fmt.Sprintf(`The following conversation messages are about to be removed from context.
Extract ONLY truly important information that should be remembered long-term.

Messages being dropped:
%s
%s
If there is important information worth saving, respond in this exact format (one per line):
REMEMBER: category=<fact|decision|pattern|insight|preference> key=<unique_key> importance=<1-10> content=<brief text>

Rules:
- Only extract genuinely important facts, decisions, or patterns
- Skip greetings, routine questions, tool outputs, and ephemeral context
- DO NOT save anything already stored in memory (see above)
- Max 3 items. If nothing NEW is worth remembering, respond with: NOTHING
- Keep content under 200 chars
- Use descriptive keys like "admin:preference:language" or "pattern:monday_peak"`, sb.String(), existingContext)

	resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   500,
	})
	if err != nil {
		log.Printf("[MEMORY_FLUSH] LLM error: %v", err)
		return
	}

	if strings.Contains(resp.Text, "NOTHING") || resp.Text == "" {
		return
	}

	// Parse REMEMBER lines
	saved := 0
	for _, line := range strings.Split(resp.Text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "REMEMBER:") {
			continue
		}
		line = strings.TrimPrefix(line, "REMEMBER:")
		line = strings.TrimSpace(line)

		mem := parseFlushMemoryLine(line)
		if mem == nil {
			continue
		}

		if err := database.UpsertMemory(mem); err != nil {
			log.Printf("[MEMORY_FLUSH] Save error: %v", err)
		} else {
			saved++
		}
	}

	if saved > 0 {
		log.Printf("[MEMORY_FLUSH] Saved %d memories from compacted context", saved)
	}

	// Auto-adapt: analyze admin communication patterns
	go autoAdaptStyle(dropped)
}

// autoAdaptStyle analyzes dropped messages for admin communication patterns
// and suggests style updates to the Director's identity.
func autoAdaptStyle(messages []llm.Message) {
	stats := director.AnalyzeAdminPatterns(messages)
	suggestion := director.SuggestStyleUpdate(stats)
	if suggestion == "" {
		return
	}

	// Check if we already have this style note
	current, _ := database.GetIdentity("style")
	if current == nil {
		return
	}

	// Only update if suggestion contains something not already in style
	alreadyHas := true
	for _, line := range strings.Split(suggestion, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			line = strings.TrimPrefix(line, "- ")
		}
		if !strings.Contains(current.Content, line) {
			alreadyHas = false
			break
		}
	}
	if alreadyHas {
		return
	}

	// Append adaptation notes to style
	newStyle := current.Content + "\n\n[Авто-адаптация по паттернам общения]\n" + suggestion
	if _, err := director.UpdateIdentityChecked("style", newStyle, "system",
		fmt.Sprintf("авто-адаптация: %d сообщений проанализировано", stats.TotalMessages)); err != nil {
		log.Printf("[AUTO_ADAPT] Style update failed: %v", err)
	} else {
		log.Printf("[AUTO_ADAPT] Style updated based on %d admin messages", stats.TotalMessages)
	}
}

// parseFlushMemoryLine parses a line like:
// category=fact key=admin:egor:lang importance=8 content=Admin prefers Russian
func parseFlushMemoryLine(line string) *models.DirectorMemory {
	m := &models.DirectorMemory{
		ID:         uuid.New(),
		Importance: 5,
		Source:     "context_flush",
	}

	// Extract content= (last field, may contain spaces and = signs)
	contentIdx := strings.Index(line, "content=")
	if contentIdx < 0 {
		return nil
	}
	m.Content = strings.TrimSpace(line[contentIdx+8:])
	if m.Content == "" {
		return nil
	}
	if len(m.Content) > 500 {
		m.Content = m.Content[:500]
	}

	// Parse key=value pairs from the part before content=
	prefix := line[:contentIdx]
	for _, token := range strings.Fields(prefix) {
		if strings.HasPrefix(token, "category=") {
			m.Category = strings.TrimPrefix(token, "category=")
		} else if strings.HasPrefix(token, "key=") {
			m.Key = strings.TrimPrefix(token, "key=")
		} else if strings.HasPrefix(token, "importance=") {
			fmt.Sscanf(strings.TrimPrefix(token, "importance="), "%d", &m.Importance)
		}
	}

	// Clamp importance to valid range [1, 10]
	if m.Importance < 1 {
		m.Importance = 1
	}
	if m.Importance > 10 {
		m.Importance = 10
	}

	// Validate required fields
	validCategories := map[string]bool{"fact": true, "decision": true, "pattern": true, "insight": true, "preference": true}
	if !validCategories[m.Category] || m.Key == "" {
		return nil
	}

	m.Tags = []string{"context_flush"}
	return m
}
