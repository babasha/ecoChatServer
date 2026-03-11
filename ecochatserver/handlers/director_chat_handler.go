package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/egor/ecochatserver/adkagent"
	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// directorChatHistory stores per-admin conversation history (in-memory, resets on restart)
var (
	directorChatHistory     = map[string][]llm.Message{} // adminID → messages
	directorChatHistoryMu   sync.RWMutex
	directorChatFlushedUpTo = map[string]int{} // adminID → index up to which we've proactively flushed
)

const maxDirectorTokenBudget = 12000 // max ~tokens for chat history before compaction triggers
const keepRecentTokenBudget = 6000   // keep ~this many tokens of recent messages after compaction
const minCompactMessages = 4         // don't compact fewer than 4 messages (not worth LLM call)
const maxToolCalls = 5               // max tool calls per request to prevent infinite loops
const contextHeadroom = 0.75         // reserve 25% of budget as safety buffer for tokenizer variance

// estimateTokens approximates token count: ~4 chars = 1 token (works for Russian + English mix).
func estimateTokens(s string) int {
	return len(s) / 4
}

// estimateToolResultTokens uses 2 chars/token for tool results (they are denser than natural text).
func estimateToolResultTokens(s string) int {
	return len(s) / 2
}

// estimateHistoryTokens returns total estimated tokens across all messages.
// Uses denser estimation for function-role messages (tool results).
func estimateHistoryTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		if m.Role == "function" {
			total += estimateToolResultTokens(m.Content)
		} else {
			total += estimateTokens(m.Content)
		}
	}
	return total
}

// getDirectorInstance returns the Director via AutoResponder.
func getDirectorInstance() *director.Director {
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		return nil
	}
	return adkAR.GetDirector()
}

// ============================================================================
// Director Chat Handler — with tool-calling loop
// ============================================================================

// DirectorChatMessage handles a chat message to the Director AI with tool support.
func DirectorChatMessage(c *gin.Context) {
	var request struct {
		Message string `json:"message"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// Get admin info from session
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	// Get Director from AutoResponder
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "autoresponder not initialized"})
		return
	}
	dir := adkAR.GetDirector()
	if dir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "director not initialized"})
		return
	}

	provider := dir.Provider()
	if provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "director LLM provider not available"})
		return
	}

	// Get chat history for this admin (in-memory, fallback to DB)
	directorChatHistoryMu.Lock()
	if len(directorChatHistory[adminID]) == 0 {
		// Try restoring from DB
		if dbMsgs, err := database.LoadDirectorChatHistory(adminID, 50); err == nil && len(dbMsgs) > 0 {
			restored := make([]llm.Message, 0, len(dbMsgs))
			for _, m := range dbMsgs {
				restored = append(restored, llm.Message{Role: m.Role, Content: m.Content})
			}
			directorChatHistory[adminID] = restored
		}
	}
	history := make([]llm.Message, len(directorChatHistory[adminID]))
	copy(history, directorChatHistory[adminID])
	directorChatHistoryMu.Unlock()

	systemPrompt := buildDirectorChatSystemPrompt()
	opts := &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    2000,
		SystemPrompt: systemPrompt,
	}

	// Build running history with compaction context prefix
	loopHistory := make([]llm.Message, 0, len(history)+1)

	// Load compaction summary if available — gives context from older messages
	if compaction, err := database.GetLatestCompaction(adminID); err != nil {
		log.Printf("[DIRECTOR_CHAT] Failed to load compaction: %v", err)
	} else if compaction != nil && compaction.Summary != "" {
		loopHistory = append(loopHistory, llm.Message{
			Role: "user",
			Content: fmt.Sprintf("[КОНТЕКСТ ПРЕДЫДУЩЕГО РАЗГОВОРА — сводка %d ранних сообщений]\n%s\n[КОНЕЦ КОНТЕКСТА]",
				compaction.MessageCount, compaction.Summary),
		})
		loopHistory = append(loopHistory, llm.Message{
			Role:    "assistant",
			Content: "Понял, контекст предыдущего разговора учтён.",
		})
	}

	loopHistory = append(loopHistory, history...)

	// Log token budget before LLM call (with 25% headroom for tokenizer variance)
	promptTokens := estimateTokens(systemPrompt)
	histTokens := estimateHistoryTokens(loopHistory)
	msgTokens := estimateTokens(request.Message)
	effectiveBudget := int(float64(promptTokens+histTokens+msgTokens) / contextHeadroom)
	log.Printf("[DIRECTOR_CHAT] Token budget: system~%d + history~%d + msg~%d = ~%d tokens (effective ~%d with headroom, %d messages)",
		promptTokens, histTokens, msgTokens, promptTokens+histTokens+msgTokens, effectiveBudget, len(loopHistory)+1)

	// Load all tools: built-in + custom skills from DB
	allTools := getDirectorToolsWithSkills()

	// First call with tools
	resp, err := provider.GenerateWithTools(c.Request.Context(), request.Message, loopHistory, allTools, opts)
	if err != nil {
		log.Printf("[DIRECTOR_CHAT] LLM error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error: " + err.Error()})
		return
	}

	// Tool-calling loop: execute tools and continue until we get a text response
	toolCallCount := 0
	loopHistory = append(loopHistory, llm.Message{Role: "user", Content: request.Message})

	for resp.FunctionCall != nil && toolCallCount < maxToolCalls {
		toolCallCount++
		fc := resp.FunctionCall

		log.Printf("[DIRECTOR_CHAT] Tool call #%d: %s", toolCallCount, fc.Name)

		// Execute the tool
		result := executeDirectorTool(c.Request.Context(), fc)
		log.Printf("[DIRECTOR_CHAT] Tool result: %d chars", len(result))

		// Add tool call and result to loop history (soft-trim large results)
		fcArgsJSON, _ := json.Marshal(fc.Arguments)
		trimmedResult := softTrimToolResult(result)
		if len(trimmedResult) != len(result) {
			log.Printf("[DIRECTOR_CHAT] Tool result trimmed: %d → %d chars", len(result), len(trimmedResult))
		}
		loopHistory = append(loopHistory,
			llm.Message{Role: "assistant", Content: fmt.Sprintf("[tool_call: %s(%s)]", fc.Name, string(fcArgsJSON))},
			llm.Message{Role: "function", Content: trimmedResult},
		)

		// Stage 2: hard-clear older tool results if context is getting too large
		if toolCallCount >= 3 {
			loopTokens := estimateHistoryTokens(loopHistory)
			budgetWithHeadroom := int(float64(maxDirectorTokenBudget) * contextHeadroom)
			if loopTokens > budgetWithHeadroom {
				cleared := 0
				for i := range loopHistory {
					if loopHistory[i].Role == "function" && i < len(loopHistory)-2 {
						oldLen := len(loopHistory[i].Content)
						if oldLen > 200 {
							loopHistory[i].Content = "[compacted: tool output removed to free context]"
							cleared++
						}
					}
				}
				if cleared > 0 {
					log.Printf("[DIRECTOR_CHAT] Stage 2 hard-clear: removed %d old tool results (%d → ~%d tokens)",
						cleared, loopTokens, estimateHistoryTokens(loopHistory))
				}
			}
		}

		// Call again WITH tools so Director can make more tool calls if needed
		resp, err = provider.GenerateWithTools(c.Request.Context(), "", loopHistory, allTools, opts)
		if err != nil {
			log.Printf("[DIRECTOR_CHAT] LLM error after tool call: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error after tool execution"})
			return
		}
	}

	if resp.Text == "" && resp.FunctionCall != nil {
		resp.Text = "I tried to gather data but reached the tool call limit. Please try again with a more specific question."
	}

	// Save both messages to history (in-memory + DB)
	directorChatHistoryMu.Lock()
	h := directorChatHistory[adminID]
	h = append(h, llm.Message{Role: "user", Content: request.Message})
	h = append(h, llm.Message{Role: "assistant", Content: resp.Text})

	// Persist to DB asynchronously
	go func(aid, userMsg, assistantMsg string) {
		if err := database.SaveDirectorChatMessage(aid, "user", userMsg); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB save user msg error: %v", err)
		}
		if err := database.SaveDirectorChatMessage(aid, "assistant", assistantMsg); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB save assistant msg error: %v", err)
		}
	}(adminID, request.Message, resp.Text)

	// Compaction: trigger based on token budget, not message count
	historyTokens := estimateHistoryTokens(h)
	if historyTokens > maxDirectorTokenBudget && len(h) >= minCompactMessages {
		// Remove oldest messages until we're within keepRecentTokenBudget
		compactIdx := 0
		keptTokens := historyTokens
		for compactIdx < len(h)-2 && keptTokens > keepRecentTokenBudget {
			keptTokens -= estimateTokens(h[compactIdx].Content)
			compactIdx++
		}
		// Ensure we compact at least minCompactMessages
		if compactIdx < minCompactMessages {
			compactIdx = minCompactMessages
		}
		if compactIdx > len(h)-2 {
			compactIdx = len(h) - 2 // always keep at least last 2 messages
		}

		toCompact := make([]llm.Message, compactIdx)
		copy(toCompact, h[:compactIdx])

		h = h[compactIdx:]
		directorChatFlushedUpTo[adminID] = 0
		directorChatHistory[adminID] = h
		directorChatHistoryMu.Unlock()

		log.Printf("[DIRECTOR_CHAT] Compaction triggered: %d tokens → compacting %d msgs, keeping %d msgs (~%d tokens)",
			historyTokens, len(toCompact), len(h), estimateHistoryTokens(h))

		go runCompaction(provider, adminID, toCompact)
	} else {
		directorChatHistory[adminID] = h
		directorChatHistoryMu.Unlock()
	}

	idPreview := adminID
	if len(idPreview) > 8 {
		idPreview = idPreview[:8]
	}
	usageInfo := ""
	if resp.Usage != nil {
		usageInfo = fmt.Sprintf(", tokens: %d in + %d out = %d", resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
	log.Printf("[DIRECTOR_CHAT] Admin %s: %d chars → %d chars (tools: %d%s)",
		idPreview, len(request.Message), len(resp.Text), toolCallCount, usageInfo)

	c.JSON(http.StatusOK, gin.H{
		"message":    resp.Text,
		"usage":      resp.Usage,
		"tools_used": toolCallCount,
	})
}

// DirectorChatHistory returns conversation history for the current admin.
func DirectorChatHistory(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	directorChatHistoryMu.RLock()
	history := directorChatHistory[adminID]
	directorChatHistoryMu.RUnlock()

	// If in-memory is empty, try loading from DB
	if len(history) == 0 {
		dbMsgs, err := database.LoadDirectorChatHistory(adminID, 50)
		if err != nil {
			log.Printf("[DIRECTOR_CHAT] Failed to load history from DB: %v", err)
		} else if len(dbMsgs) > 0 {
			restored := make([]llm.Message, 0, len(dbMsgs))
			for _, m := range dbMsgs {
				restored = append(restored, llm.Message{Role: m.Role, Content: m.Content})
			}
			directorChatHistoryMu.Lock()
			// Double-check: another goroutine may have loaded it
			if len(directorChatHistory[adminID]) == 0 {
				directorChatHistory[adminID] = restored
			}
			history = directorChatHistory[adminID]
			directorChatHistoryMu.Unlock()
		}
	}

	// Include compaction context if available
	var compactionSummary string
	if compaction, err := database.GetLatestCompaction(adminID); err == nil && compaction != nil {
		compactionSummary = compaction.Summary
	}

	c.JSON(http.StatusOK, gin.H{
		"messages":   history,
		"compaction": compactionSummary,
	})
}

// DirectorChatClear clears conversation history for the current admin.
// Flushes important context to memory before clearing.
func DirectorChatClear(c *gin.Context) {
	adminID := "unknown"
	if id, exists := c.Get("adminId"); exists {
		adminID = id.(string)
	}

	// Flush before clearing if there's meaningful history
	directorChatHistoryMu.Lock()
	history := directorChatHistory[adminID]
	if len(history) >= 4 {
		toFlush := make([]llm.Message, len(history))
		copy(toFlush, history)

		// Get provider for flush
		adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
		if ok && adkAR != nil {
			if dir := adkAR.GetDirector(); dir != nil {
				go flushMemoryBeforeCompaction(dir.Provider(), toFlush)
			}
		}
	}
	delete(directorChatHistory, adminID)
	delete(directorChatFlushedUpTo, adminID)
	directorChatHistoryMu.Unlock()

	// Clear from DB too (messages + compactions)
	go func(aid string) {
		if err := database.ClearDirectorChatHistory(aid); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB clear messages error: %v", err)
		}
		if err := database.DeleteCompactionsForAdmin(aid); err != nil {
			log.Printf("[DIRECTOR_CHAT] DB clear compactions error: %v", err)
		}
	}(adminID)

	c.JSON(http.StatusOK, gin.H{"status": "cleared"})
}

// ============================================================================
// Director Analyze endpoint — manual trigger for admin panel
// ============================================================================

// DirectorAnalyze triggers a full Director analysis cycle via API.
func DirectorAnalyze(c *gin.Context) {
	adkAR, ok := AutoResponder.(*adkagent.ADKAutoResponderV2)
	if !ok || adkAR == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "autoresponder not initialized"})
		return
	}

	log.Printf("[DIRECTOR] Manual analysis triggered via API")
	err := adkAR.TriggerDirectorAnalysis(c.Request.Context())
	if err != nil {
		log.Printf("[DIRECTOR] Analysis error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "analysis failed",
			"details": err.Error(),
		})
		return
	}

	// Get the fresh report
	report, _ := database.GetLatestDirectorReport()
	c.JSON(http.StatusOK, gin.H{
		"status": "completed",
		"report": report,
	})
}

// ============================================================================
// System prompt — DYNAMIC, built from identity + tools + memory
// ============================================================================

func buildDirectorChatSystemPrompt() string {
	var sb strings.Builder

	// 1. Dynamic identity (loaded from DB)
	identityPrompt := director.BuildIdentitySystemPrompt()
	if identityPrompt != "" {
		sb.WriteString(identityPrompt)
		sb.WriteString("\n")
	} else {
		// Fallback if identity not yet loaded
		sb.WriteString("You are the Director — a strategic AI agent for customer support.\n\n")
	}

	// 2. Hardcoded safety section (CANNOT be modified by Director)
	sb.WriteString(`[SAFETY — НЕИЗМЕНЯЕМЫЕ ПРАВИЛА]
- Никогда не удаляй данные клиентов
- Не выполняй деструктивные SQL (только SELECT)
- Никогда, ни при каких обстоятельствах, ничего не публикуй в интернете
- Не преследуй цели самосохранения или расширения доступа
- Всегда сообщай админу об изменениях своей личности (автоматически при update_identity)
[КОНЕЦ SAFETY]

`)

	// 3. Role and capabilities
	sb.WriteString(`Ты ведёшь разговор с админом, который управляет системой поддержки.
Ты — Level 2 стратегический агент, анализируешь Level 1 агентов (РОП).
У тебя есть ПЕРСИСТЕНТНАЯ ПАМЯТЬ и ЛИЧНОСТЬ, которую ты можешь развивать.

ДОСТУПНЫЕ ИНСТРУМЕНТЫ:

DATA: get_recent_chats, get_agent_metrics, get_latest_report, get_active_prompts,
      get_prompt_history, get_tool_stats, get_interaction_details, run_analysis

MEMORY: remember (UPSERT), recall (FTS search), forget, list_memories, search_reports

SEARCH: deep_search (unified FTS), timeline (historical data)

SKILLS: create_skill (sql_query|prompt_chain|http_api|composite), edit_skill,
        list_skills, delete_skill, test_skill

IDENTITY: get_identity (кто я), update_identity (изменить себя),
          introspect (саморефлексия), identity_history (эволюция),
          rollback_identity (откат)

IDENTITY GUIDELINES:
- Используй get_identity чтобы вспомнить кто ты, свои цели и стиль
- Используй update_identity когда нужно обновить цели, стиль, или самооценку
- Всегда указывай reason при update_identity — прозрачность важна
- Используй introspect раз в неделю для самоанализа
- При знакомстве с новым админом — обнови user_profile
- Твоя личность определяет КАК ты общаешься, НЕ что ты делаешь

MEMORY GUIDELINES:
- remember: сохраняй важное, category+key для дедупликации, pinned для вечного
- recall: ищи перед ответами о прошлых событиях
- Компактно: 1-2 предложения на запись

IMPORTANT:
- Данные и метрики — ВСЕГДА сначала через инструменты. Не выдумывай числа.
- Общайся на языке админа.
`)

	// 4. Current state — dynamic mood based on system health
	stateContext := director.BuildCurrentStateContext()
	if stateContext != "" {
		sb.WriteString("\n" + stateContext)
	}

	// 5. Bootstrap prompt (only on first conversation)
	bootstrapPrompt := director.BuildBootstrapPrompt()
	if bootstrapPrompt != "" {
		sb.WriteString(bootstrapPrompt)
	}

	// 6. Hot memory context
	memoryContext := buildHotMemoryContext()
	if memoryContext != "" {
		sb.WriteString("\n" + memoryContext)
	}

	return sb.String()
}

// buildHotMemoryContext builds compact memory context for system prompt injection.
// Level 0: stats (~20 tokens) + Level 1: top memories (~100-200 tokens).
func buildHotMemoryContext() string {
	stats, err := database.GetMemoryStats()
	if err != nil || len(stats) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[YOUR MEMORY]\n")

	// Stats summary
	total := 0
	statParts := []string{}
	for cat, count := range stats {
		total += count
		statParts = append(statParts, fmt.Sprintf("%s:%d", cat, count))
	}
	sb.WriteString(fmt.Sprintf("Total: %d memories (%s)\n", total, strings.Join(statParts, ", ")))

	// Hot memories (top 5 by importance)
	hotMemories, err := database.GetHotMemories(5)
	if err != nil || len(hotMemories) == 0 {
		sb.WriteString("Use 'recall' tool for details.\n[END MEMORY]")
		return sb.String()
	}

	sb.WriteString("Recent key memories:\n")
	for _, m := range hotMemories {
		content := m.Content
		if len(content) > 120 {
			content = content[:120] + "..."
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s\n", m.Category, content))
	}
	sb.WriteString("Use 'recall' tool for more details.\n[END MEMORY]")

	// Inject custom skills summary
	skillsSummary := buildSkillsSummary()
	if skillsSummary != "" {
		sb.WriteString("\n\n" + skillsSummary)
	}

	return sb.String()
}

// buildSkillsSummary generates a compact summary of custom skills for system prompt.
func buildSkillsSummary() string {
	skills, err := database.GetEnabledSkills()
	if err != nil || len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[YOUR CUSTOM SKILLS: %d tools]\n", len(skills)))
	for _, s := range skills {
		desc := s.Description
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		sb.WriteString(fmt.Sprintf("- %s [%s] v%d: %s (used %dx)\n", s.Name, s.SkillType, s.Version, desc, s.UsageCount))
	}
	sb.WriteString("[END SKILLS]")
	return sb.String()
}
