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
// Compaction — summarize old messages + extract memories
// Supports multi-stage compaction for large conversations (OpenClaw-inspired).
// ============================================================================

const (
	// compactionMaxInputChars is the threshold above which multi-stage compaction is used.
	// ~3000 chars ≈ ~750 tokens — safe for a single LLM call with overhead.
	compactionMaxInputChars = 3000
	// compactionChunkSize is the target size per chunk in multi-stage mode.
	compactionChunkSize = 2000
)

// runCompaction summarizes old messages via LLM, saves summary to DB,
// extracts key facts to persistent memory, and cleans up compacted messages from DB.
// Runs in a goroutine to not block the chat response.
func runCompaction(provider llm.Provider, adminID string, messagesToCompact []llm.Message) {
	if len(messagesToCompact) < 2 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Load existing compaction for merge
	existingCompaction, _ := database.GetLatestCompaction(adminID)
	existingSummary := ""
	var previousID *uuid.UUID
	if existingCompaction != nil {
		existingSummary = existingCompaction.Summary
		previousID = &existingCompaction.ID
	}

	// 2. Fetch existing hot memories for deduplication awareness
	existingMemories := buildExistingMemoriesContext()

	// 3. Prepare messages: strip verbose tool results before sending to LLM
	stripped := stripToolResults(messagesToCompact)

	// 4. Estimate input size and decide single vs multi-stage
	inputChars := 0
	for _, m := range stripped {
		inputChars += len(m.Content)
	}

	var summary string
	var memoryLines []string

	if inputChars > compactionMaxInputChars && len(stripped) > 4 {
		// Multi-stage: split into chunks, summarize each, then merge
		log.Printf("[COMPACTION] Multi-stage mode: %d chars across %d messages", inputChars, len(stripped))
		summary, memoryLines = runMultiStageCompaction(ctx, provider, stripped, existingSummary, existingMemories)
	} else {
		// Single-stage: standard compaction
		summary, memoryLines = runSingleStageCompaction(ctx, provider, stripped, existingSummary, existingMemories)
	}

	if summary == "" {
		log.Printf("[COMPACTION] Empty summary, skipping DB save (admin=%s)", adminID[:min(8, len(adminID))])
		return
	}

	// Check deadline before DB operations
	if ctx.Err() != nil {
		log.Printf("[COMPACTION] Context expired before DB save, discarding result (admin=%s)", adminID[:min(8, len(adminID))])
		return
	}

	if len(summary) > 3000 {
		summary = summary[:3000]
	}

	// 5. Save compaction to DB
	now := time.Now()
	messagesFrom := now.Add(-time.Duration(len(messagesToCompact)) * time.Minute)
	_, err := database.SaveCompaction(adminID, summary, messagesFrom, now,
		len(messagesToCompact), nil, previousID)
	if err != nil {
		log.Printf("[COMPACTION] DB save failed: %v", err)
	} else {
		log.Printf("[COMPACTION] Saved compaction: %d messages → %d chars summary (admin=%s)",
			len(messagesToCompact), len(summary), adminID[:min(8, len(adminID))])

		// Clean up compacted messages from DB (only after successful save)
		if deleted, err := database.DeleteOldDirectorChatMessages(adminID, now); err != nil {
			log.Printf("[COMPACTION] DB cleanup failed: %v", err)
		} else if deleted > 0 {
			log.Printf("[COMPACTION] Cleaned up %d compacted messages from DB", deleted)
		}
	}

	// 6. Save extracted memories
	saveExtractedMemories(memoryLines, "COMPACTION")

	// 7. Auto-adapt style (lightweight, no LLM call)
	go autoAdaptStyle(messagesToCompact)
}

// stripToolResults aggressively trims function-role messages (tool output)
// before sending to compaction LLM — they are verbose and waste tokens.
func stripToolResults(messages []llm.Message) []llm.Message {
	result := make([]llm.Message, len(messages))
	for i, m := range messages {
		if m.Role == "function" {
			content := m.Content
			if len(content) > 200 {
				content = content[:200] + "... [tool output truncated]"
			}
			result[i] = llm.Message{Role: m.Role, Content: content}
		} else {
			result[i] = m
		}
	}
	return result
}

// buildExistingMemoriesContext returns a compact string of hot memories for deduplication.
func buildExistingMemoriesContext() string {
	hotMems, err := database.GetHotMemories(5)
	if err != nil || len(hotMems) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\nУже сохранено в памяти (НЕ дублируй):\n")
	for _, m := range hotMems {
		content := m.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		sb.WriteString(fmt.Sprintf("- [%s/%s] %s\n", m.Category, m.Key, content))
	}
	return sb.String()
}

// saveExtractedMemories parses and saves REMEMBER: lines to persistent memory.
func saveExtractedMemories(memoryLines []string, source string) {
	saved := 0
	for _, line := range memoryLines {
		mem := parseFlushMemoryLine(line)
		if mem == nil {
			continue
		}
		if err := database.UpsertMemory(mem); err != nil {
			log.Printf("[%s] Memory save error: %v", source, err)
		} else {
			saved++
		}
	}
	if saved > 0 {
		log.Printf("[%s] Saved %d memories from compacted context", source, saved)
	}
}

// runSingleStageCompaction handles normal-size compaction in one LLM call.
func runSingleStageCompaction(ctx context.Context, provider llm.Provider, messages []llm.Message, existingSummary, existingMemories string) (string, []string) {
	prompt := buildCompactionPrompt(messages, existingSummary, existingMemories)

	resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   1200,
	})
	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[COMPACTION] Timed out during single-stage LLM call")
		} else {
			log.Printf("[COMPACTION] LLM call failed: %v", err)
		}
		return "", nil
	}

	return parseCompactionResponse(resp.Text)
}

// runMultiStageCompaction splits messages into chunks, summarizes each, then merges.
// Inspired by OpenClaw's progressive summarization with fallback.
func runMultiStageCompaction(ctx context.Context, provider llm.Provider, messages []llm.Message, existingSummary, existingMemories string) (string, []string) {
	// Split messages into chunks by character budget
	chunks := splitMessagesIntoChunks(messages, compactionChunkSize)
	log.Printf("[COMPACTION] Split %d messages into %d chunks", len(messages), len(chunks))

	// Stage 1: Summarize each chunk independently
	var partialSummaries []string
	var allMemoryLines []string

	for i, chunk := range chunks {
		if ctx.Err() != nil {
			log.Printf("[COMPACTION] Context expired during chunk %d/%d", i+1, len(chunks))
			break
		}

		chunkSummary := existingSummary
		if i > 0 {
			// For subsequent chunks, use accumulated partial summaries as context
			chunkSummary = strings.Join(partialSummaries, "\n")
		}

		prompt := buildCompactionPrompt(chunk, chunkSummary, existingMemories)
		resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
			Temperature: 0.1,
			MaxTokens:   800,
		})
		if err != nil {
			log.Printf("[COMPACTION] Chunk %d/%d failed: %v", i+1, len(chunks), err)
			continue
		}

		summary, memories := parseCompactionResponse(resp.Text)
		if summary != "" {
			partialSummaries = append(partialSummaries, summary)
		}
		allMemoryLines = append(allMemoryLines, memories...)
	}

	if len(partialSummaries) == 0 {
		return "", allMemoryLines
	}

	// Stage 2: If multiple partial summaries, merge them into final
	if len(partialSummaries) == 1 {
		return partialSummaries[0], allMemoryLines
	}

	if ctx.Err() != nil {
		// Context expired — return best effort (last partial summary)
		return partialSummaries[len(partialSummaries)-1], allMemoryLines
	}

	mergePrompt := fmt.Sprintf(`Объедини следующие частичные сводки разговора в одну финальную:

%s

Правила:
1. Сохрани ВСЕ ключевые факты, решения и контекст
2. Убери дублирование
3. Макс 400 слов, буллеты
4. Сохраняй все идентификаторы: UUID, URL, IP, имена файлов, версии
5. Отметь незавершённые темы

SUMMARY:`, strings.Join(partialSummaries, "\n---\n"))

	resp, err := provider.GenerateResponse(ctx, mergePrompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   800,
	})
	if err != nil {
		log.Printf("[COMPACTION] Merge stage failed: %v, using last partial summary", err)
		return partialSummaries[len(partialSummaries)-1], allMemoryLines
	}

	finalSummary := strings.TrimSpace(resp.Text)
	// Strip SUMMARY: prefix if present
	if idx := strings.Index(finalSummary, "SUMMARY:"); idx >= 0 {
		finalSummary = strings.TrimSpace(finalSummary[idx+8:])
	}

	return finalSummary, allMemoryLines
}

// splitMessagesIntoChunks divides messages into chunks respecting character budget.
func splitMessagesIntoChunks(messages []llm.Message, maxCharsPerChunk int) [][]llm.Message {
	var chunks [][]llm.Message
	var current []llm.Message
	currentChars := 0

	for _, m := range messages {
		msgChars := len(m.Content)
		if currentChars+msgChars > maxCharsPerChunk && len(current) > 0 {
			chunks = append(chunks, current)
			current = nil
			currentChars = 0
		}
		current = append(current, m)
		currentChars += msgChars
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

// buildCompactionPrompt constructs the LLM prompt for compaction.
func buildCompactionPrompt(messages []llm.Message, existingSummary, existingMemories string) string {
	var sb strings.Builder
	if existingSummary != "" {
		sb.WriteString("Предыдущая сводка разговора:\n---\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n---\n\n")
	}
	sb.WriteString("Новые сообщения для обработки:\n---\n")
	for _, m := range messages {
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}
	sb.WriteString("---\n")
	sb.WriteString(existingMemories)
	sb.WriteString(`
Выполни ДВЕ задачи:

=== ЗАДАЧА 1: СВОДКА ===
Создай ОБЪЕДИНЁННУЮ сводку разговора:
1. Сохрани все ключевые решения, факты и контекст из предыдущей сводки
2. Добавь новую информацию из сообщений выше
3. Отметь незавершённые темы и открытые вопросы
4. Будь кратким но полным (макс 400 слов)
5. Используй буллеты для структуры
6. Пиши на языке разговора
7. ОБЯЗАТЕЛЬНО сохраняй все идентификаторы: UUID, URL, IP-адреса, имена файлов, номера версий, имена хостов — без них контекст теряет ценность

=== ЗАДАЧА 2: ПАМЯТЬ ===
Извлеки важные факты для долгосрочной памяти (макс 3 штуки).
Пропускай приветствия, рутинные вопросы, вывод инструментов.
НЕ дублируй то что уже сохранено (см. выше).

=== ФОРМАТ ОТВЕТА ===
SUMMARY:
<сводка здесь>

MEMORIES:
REMEMBER: category=<fact|decision|pattern|insight|preference> key=<unique_key> importance=<1-10> content=<краткий текст до 200 символов>
(или NOTHING если нечего запоминать)`)
	return sb.String()
}

// parseCompactionResponse splits the combined LLM response into summary and memory lines.
func parseCompactionResponse(text string) (summary string, memoryLines []string) {
	// Find SUMMARY: and MEMORIES: sections
	summaryIdx := strings.Index(text, "SUMMARY:")
	memoriesIdx := strings.Index(text, "MEMORIES:")

	if summaryIdx >= 0 && memoriesIdx > summaryIdx {
		// Both sections found
		summary = strings.TrimSpace(text[summaryIdx+8 : memoriesIdx])
		memoriesSection := text[memoriesIdx+9:]
		for _, line := range strings.Split(memoriesSection, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "REMEMBER:") {
				memoryLines = append(memoryLines, strings.TrimSpace(strings.TrimPrefix(line, "REMEMBER:")))
			}
		}
	} else if summaryIdx >= 0 {
		// Only summary
		summary = strings.TrimSpace(text[summaryIdx+8:])
	} else {
		// No markers — treat entire response as summary
		summary = strings.TrimSpace(text)
	}
	return
}

// ============================================================================
// Standalone Memory Flush — used only by DirectorChatClear (not compaction)
// ============================================================================

// flushMemoryBeforeCompaction extracts important information from messages
// about to be cleared and saves it to persistent memory.
func flushMemoryBeforeCompaction(provider llm.Provider, dropped []llm.Message) {
	if len(dropped) < 2 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var sb strings.Builder
	for _, m := range dropped {
		content := m.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))
	}

	existingContext := buildExistingMemoriesContext()

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
		log.Printf("[MEMORY_FLUSH] Saved %d memories from cleared context", saved)
	}

	go autoAdaptStyle(dropped)
}

// ============================================================================
// Auto-adapt & helpers
// ============================================================================

// autoAdaptStyle analyzes dropped messages for admin communication patterns
// and suggests style updates to the Director's identity.
func autoAdaptStyle(messages []llm.Message) {
	stats := director.AnalyzeAdminPatterns(messages)
	suggestion := director.SuggestStyleUpdate(stats)
	if suggestion == "" {
		return
	}

	current, _ := database.GetIdentity("style")
	if current == nil {
		return
	}

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

	if m.Importance < 1 {
		m.Importance = 1
	}
	if m.Importance > 10 {
		m.Importance = 10
	}

	if !director.ValidMemoryCategories[m.Category] || m.Key == "" {
		return nil
	}

	m.Tags = []string{"context_flush"}
	return m
}
