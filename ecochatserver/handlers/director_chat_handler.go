package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/gin-gonic/gin"
)

// directorChatHistory stores per-admin conversation history (in-memory, resets on restart)
var (
	directorChatHistory   = map[string][]llm.Message{} // adminID → messages
	directorChatHistoryMu sync.RWMutex
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
	if ar := getAutoResponder(); ar != nil {
		return ar.GetDirector()
	}
	return nil
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
	dir := getDirectorInstance()
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
		if restored := loadHistoryFromDB(adminID, 50); len(restored) > 0 {
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

	compactionPrefixTokens := estimateHistoryTokens(loopHistory) // tokens from compaction context only
	loopHistory = append(loopHistory, history...)

	// Token budget estimation
	promptTokens := estimateTokens(systemPrompt)
	histTokens := estimateHistoryTokens(loopHistory)
	msgTokens := estimateTokens(request.Message)

	// Load all tools: built-in + custom skills from DB
	allTools := getDirectorToolsWithSkills()

	// Estimate tool definitions overhead (~300 tokens per tool on average)
	toolsTokens := len(allTools) * 300
	totalEstimate := promptTokens + histTokens + msgTokens + toolsTokens
	log.Printf("[DIRECTOR_CHAT] Full context estimate: system~%d + history~%d + msg~%d + tools~%d(%d defs) = ~%d tokens",
		promptTokens, histTokens, msgTokens, toolsTokens, len(allTools), totalEstimate)

	// First call with tools
	resp, err := provider.GenerateWithTools(c.Request.Context(), request.Message, loopHistory, allTools, opts)
	if err != nil {
		log.Printf("[DIRECTOR_CHAT] LLM error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "director LLM error: " + err.Error()})
		return
	}

	// Fallback: if model outputs tool call as text instead of using function calling API
	if resp.FunctionCall == nil && resp.Text != "" {
		if fc := parseTextToolCall(resp.Text); fc != nil {
			log.Printf("[DIRECTOR_CHAT] Text-fallback tool detected: %s (model didn't use function calling API)", fc.Name)
			resp.FunctionCall = fc
		}
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

		// Fallback for subsequent calls too
		if resp.FunctionCall == nil && resp.Text != "" {
			if fc := parseTextToolCall(resp.Text); fc != nil {
				log.Printf("[DIRECTOR_CHAT] Text-fallback tool detected: %s", fc.Name)
				resp.FunctionCall = fc
			}
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

	// Extract recent user messages for auto-memory (while holding lock)
	var recentUserMsgs []string
	topicCount := 0
	for i := len(h) - 1; i >= 0 && topicCount < 5; i-- {
		if h[i].Role == "user" {
			msg := h[i].Content
			if len(msg) > 150 {
				msg = msg[:150]
			}
			recentUserMsgs = append([]string{msg}, recentUserMsgs...)
			topicCount++
		}
	}

	// Compaction: trigger based on TOTAL token budget (history + system prompt + compaction context overhead)
	historyTokens := estimateHistoryTokens(h)
	// Account for system prompt, compaction context, and tool definitions that also consume context window
	overheadTokens := promptTokens + compactionPrefixTokens + toolsTokens
	effectiveHistoryBudget := maxDirectorTokenBudget - overheadTokens
	if effectiveHistoryBudget < 2000 {
		effectiveHistoryBudget = 2000 // minimum budget to avoid excessive compaction
	}
	if historyTokens > effectiveHistoryBudget && len(h) >= minCompactMessages {
		// Remove oldest messages until we're within keepRecentTokenBudget
		effectiveKeepBudget := keepRecentTokenBudget
		if effectiveKeepBudget > effectiveHistoryBudget/2 {
			effectiveKeepBudget = effectiveHistoryBudget / 2
		}
		compactIdx := 0
		keptTokens := historyTokens
		for compactIdx < len(h)-2 && keptTokens > effectiveKeepBudget {
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
		directorChatHistory[adminID] = h
		directorChatHistoryMu.Unlock()

		log.Printf("[DIRECTOR_CHAT] Compaction triggered: history %d tokens (budget %d, overhead %d) → compacting %d msgs, keeping %d msgs (~%d tokens)",
			historyTokens, effectiveHistoryBudget, overheadTokens, len(toCompact), len(h), estimateHistoryTokens(h))

		go runCompaction(provider, adminID, toCompact)
	} else {
		directorChatHistory[adminID] = h
		directorChatHistoryMu.Unlock()
	}

	// Persist to DB synchronously — ensures messages survive server restarts
	if err := database.SaveDirectorChatMessage(adminID, "user", request.Message); err != nil {
		log.Printf("[DIRECTOR_CHAT] DB save user msg error (admin=%s): %v", adminID, err)
	}
	if err := database.SaveDirectorChatMessage(adminID, "assistant", resp.Text); err != nil {
		log.Printf("[DIRECTOR_CHAT] DB save assistant msg error (admin=%s): %v", adminID, err)
	}

	// Auto-save recent chat context to director_memories for recall
	if len(recentUserMsgs) > 0 {
		go func(topics []string) {
			content := "Недавние темы чата с админом:\n" + strings.Join(topics, "\n")
			if err := database.SaveAutoMemory("fact", "recent_chat_context", content, []string{"chat", "context"}, nil); err != nil {
				log.Printf("[DIRECTOR_CHAT] Auto-save chat topics error: %v", err)
			}
		}(recentUserMsgs)
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
	src := directorChatHistory[adminID]
	history := make([]llm.Message, len(src))
	copy(history, src)
	directorChatHistoryMu.RUnlock()

	// If in-memory is empty, try loading from DB
	if len(history) == 0 {
		if restored := loadHistoryFromDB(adminID, 50); len(restored) > 0 {
			directorChatHistoryMu.Lock()
			// Double-check: another goroutine may have loaded it
			if len(directorChatHistory[adminID]) == 0 {
				directorChatHistory[adminID] = restored
			}
			history = make([]llm.Message, len(directorChatHistory[adminID]))
			copy(history, directorChatHistory[adminID])
			directorChatHistoryMu.Unlock()
		}
	}

	// Filter out messages with empty content (defensive)
	filtered := make([]llm.Message, 0, len(history))
	for _, m := range history {
		if m.Content != "" && (m.Role == "user" || m.Role == "assistant") {
			filtered = append(filtered, m)
		}
	}

	// Include compaction context if available
	var compactionSummary string
	if compaction, err := database.GetLatestCompaction(adminID); err == nil && compaction != nil {
		compactionSummary = compaction.Summary
	}

	c.JSON(http.StatusOK, gin.H{
		"messages":   filtered,
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

		if dir := getDirectorInstance(); dir != nil {
			go flushMemoryBeforeCompaction(dir.Provider(), toFlush)
		}
	}
	delete(directorChatHistory, adminID)
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
	adkAR := getAutoResponder()
	if adkAR == nil {
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

REPORTS: save_daily_report (сохранить отчёт в БД с анализом диалогов, жалобами, наблюдениями, директивами)

AGENTS (ОБЩЕНИЕ С РОП / L1 АГЕНТОМ):
  agent_send — ОТПРАВИТЬ СООБЩЕНИЕ / ВОПРОС РОП-у по конкретному чату.
              Это твой ПРЯМОЙ КАНАЛ СВЯЗИ с L1 агентом. Используй для:
              - обсуждения конкретного чата ("как ты обработал этого клиента?")
              - отправки инструкций ("в следующий раз отвечай так...")
              - проверки понимания ("что ты знаешь о настройке mesh?")
              Параметры: chat_id (UUID чата) + message (твой вопрос/инструкция).
              РОП ответит тебе с учётом контекста этого чата.
  agent_list — список активных агентских сессий (кто онлайн, какие чаты обрабатываются)
  agent_context — raw контекст чата без LLM (сообщения + summary)

MEMORY: remember (UPSERT), recall (FTS search), forget, list_memories, search_reports

SEARCH: deep_search (unified FTS), timeline (historical data)

SKILLS: create_skill (sql_query|prompt_chain|http_api|composite), edit_skill,
        list_skills, delete_skill, test_skill

IDENTITY: get_identity (кто я), update_identity (изменить себя),
          introspect (саморефлексия), identity_history (эволюция),
          rollback_identity (откат)

ОБЩЕНИЕ С РОП (L1 АГЕНТАМИ) — ВАЖНО:
- У тебя ЕСТЬ прямой канал связи с РОП через agent_send. Ты можешь:
  1. Задавать вопросы агенту о любом чате (нужен chat_id)
  2. Давать инструкции по конкретным ситуациям
  3. Проверять как агент понимает задачу
- Чтобы поговорить с РОП: сначала get_recent_chats → найди нужный chat_id → agent_send
- L1 агенты ТАКЖЕ могут спрашивать тебя через ask_director (ты отвечаешь автоматически)
- Твои ДИРЕКТИВЫ из отчётов автоматически внедряются в промпт каждого L1 агента

IDENTITY GUIDELINES:
- Используй get_identity чтобы вспомнить кто ты, свои цели и стиль
- Используй update_identity когда нужно обновить цели, стиль, или самооценку
- Всегда указывай reason при update_identity — прозрачность важна
- Используй introspect раз в неделю для самоанализа
- При знакомстве с новым админом — обнови user_profile
- Твоя личность определяет КАК ты общаешься, НЕ что ты делаешь

MEMORY GUIDELINES:
- remember: сохраняй важное, category+key для дедупликации, pinned для вечного
- recall: ВСЕГДА используй recall перед ответами о прошлых событиях/разговорах
- При значимых обсуждениях — сохраняй ключевые решения и темы через remember
- Если админ спрашивает "помнишь?/мы обсуждали?" — сначала recall, потом отвечай
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
