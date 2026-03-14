package adkagent

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

const (
	defaultSummaryThreshold   = 20   // messages before triggering summarization
	defaultMaxContextMessages = 10   // max recent messages in context
	defaultContextTokenBudget = 3000 // max tokens for injected context (~25% of typical 12k budget)
)

// ChatSummarizer handles conversation summarization and context building
type ChatSummarizer struct {
	threshold          int
	maxContextMessages int
}

// NewChatSummarizer creates a new summarizer
func NewChatSummarizer() *ChatSummarizer {
	threshold := database.GetSettingInt("SUMMARIZER_THRESHOLD", defaultSummaryThreshold)
	maxCtx := database.GetSettingInt("SUMMARIZER_MAX_CONTEXT_MESSAGES", defaultMaxContextMessages)

	if llm.GetEmbeddingClient() != nil {
		log.Printf("[SUMMARIZER] Embedding client available — hybrid search enabled")
	} else {
		log.Printf("[SUMMARIZER] Embedding client unavailable — FTS-only mode")
	}

	log.Printf("[SUMMARIZER] Initialized: threshold=%d, maxContextMessages=%d", threshold, maxCtx)

	return &ChatSummarizer{
		threshold:          threshold,
		maxContextMessages: maxCtx,
	}
}

// ShouldSummarize checks if a chat has enough new messages to trigger summarization
func (s *ChatSummarizer) ShouldSummarize(ctx context.Context, chatID uuid.UUID) bool {
	latest, err := database.GetLatestChatSummary(chatID)
	if err != nil {
		log.Printf("[SUMMARIZER] Error getting latest summary for %s: %v", chatID, err)
		return false
	}

	var count int
	if latest != nil {
		count, err = database.CountMessagesSince(chatID, latest.MessagesTo)
	} else {
		count, err = database.CountAllMessages(chatID)
	}
	if err != nil {
		log.Printf("[SUMMARIZER] Error counting messages for %s: %v", chatID, err)
		return false
	}

	return count >= s.threshold
}

// Summarize creates a summary of unsummarized messages and stores it in DB
func (s *ChatSummarizer) Summarize(ctx context.Context, chatID uuid.UUID) error {
	// Get the last summary to know where to start
	latest, err := database.GetLatestChatSummary(chatID)
	if err != nil {
		return fmt.Errorf("get latest summary: %w", err)
	}

	var since time.Time
	if latest != nil {
		since = latest.MessagesTo
	}

	// Fetch messages to summarize
	messages, err := database.GetMessagesForSummary(chatID, since, s.threshold)
	if err != nil {
		return fmt.Errorf("get messages for summary: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	// Build conversation text for LLM
	conversationText := formatMessagesForSummary(messages)

	// Call LLM to summarize
	provider := llm.GetGlobalProvider()
	if provider == nil {
		return fmt.Errorf("no LLM provider available")
	}

	summaryText, err := generateSummary(ctx, provider, conversationText)
	if err != nil {
		return fmt.Errorf("generate summary: %w", err)
	}

	// Strip thinking tags (Qwen 3.5)
	summaryText = cleanThinkingTags(summaryText)
	summaryText = strings.TrimSpace(summaryText)

	if summaryText == "" {
		return fmt.Errorf("LLM returned empty summary")
	}

	// Save to DB
	messagesFrom := messages[0].Timestamp
	messagesTo := messages[len(messages)-1].Timestamp

	summary, err := database.InsertChatSummary(chatID, summaryText, messagesFrom, messagesTo, len(messages))
	if err != nil {
		return fmt.Errorf("insert summary: %w", err)
	}

	log.Printf("[SUMMARIZER] Created summary for chat %s: %d messages → %d chars",
		chatID, len(messages), len(summaryText))

	// Extract metadata from summary and save to Director memory (async)
	go extractAndSaveMetadata(chatID, summaryText)

	// Generate embedding async (non-blocking)
	go func() {
		embClient := llm.GetEmbeddingClient()
		if embClient == nil {
			return
		}
		bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		embedding, err := embClient.Embed(bgCtx, summaryText)
		if err != nil {
			log.Printf("[SUMMARIZER] Failed to generate embedding (non-critical): %v", err)
			return
		}
		if err := database.UpdateChatSummaryEmbedding(summary.ID, embedding); err != nil {
			log.Printf("[SUMMARIZER] Failed to save embedding (non-critical): %v", err)
			return
		}
		log.Printf("[SUMMARIZER] Embedding saved for summary %s (%d dims)", summary.ID, len(embedding))
	}()

	return nil
}

// BuildContext assembles conversation context for the agent:
// [relevant past summaries via FTS] + [last summary] + [recent messages after it]
func (s *ChatSummarizer) BuildContext(ctx context.Context, chatID uuid.UUID) (string, error) {
	return s.BuildContextWithQuery(ctx, chatID, "")
}

// estimateTokens approximates token count for multilingual text.
// LLM tokenizers split differently by script:
//   - ASCII (English): ~4 chars per token
//   - Cyrillic/Arabic/Hebrew: ~1.5 chars per token
//   - CJK (Chinese/Japanese/Korean): ~1 char per token
//
// We count runes by category for an accurate cross-language estimate.
func estimateTokens(text string) int {
	var asciiChars, multiByteChars int
	for _, r := range text {
		if r < 128 {
			asciiChars++
		} else {
			multiByteChars++
		}
	}
	// ASCII text: ~4 chars/token; non-ASCII: ~1.5 chars/token
	// Adding 1 to avoid zero on short strings
	tokens := asciiChars/4 + multiByteChars*2/3
	if tokens == 0 && len(text) > 0 {
		tokens = 1
	}
	return tokens
}

// tokenBudget tracks remaining token budget during context assembly.
type tokenBudget struct {
	total    int
	used     int
}

func newTokenBudget(total int) *tokenBudget {
	return &tokenBudget{total: total}
}

// tryAdd adds text to the part list if it fits within the remaining budget.
// Returns true if added, false if budget exhausted.
func (tb *tokenBudget) tryAdd(parts *[]string, text string) bool {
	cost := estimateTokens(text)
	if tb.used+cost > tb.total {
		return false
	}
	tb.used += cost
	*parts = append(*parts, text)
	return true
}

// remaining returns how many tokens are left.
func (tb *tokenBudget) remaining() int {
	r := tb.total - tb.used
	if r < 0 {
		return 0
	}
	return r
}

// BuildContextWithQuery assembles conversation context with FTS-enhanced retrieval
// and token budgeting to prevent context overflow.
func (s *ChatSummarizer) BuildContextWithQuery(ctx context.Context, chatID uuid.UUID, userMessage string) (string, error) {
	budget := newTokenBudget(database.GetSettingInt("CONTEXT_TOKEN_BUDGET", defaultContextTokenBudget))

	latest, err := database.GetLatestChatSummary(chatID)
	if err != nil {
		return "", fmt.Errorf("get latest summary: %w", err)
	}

	var parts []string

	// === Priority 1: Current conversation context (most important, reserve ~60% budget) ===
	var recentParts []string
	recentBudget := budget.total * 60 / 100

	if latest != nil {
		summaryLine := fmt.Sprintf("Current conversation summary: %s", latest.Summary)
		recentTokens := estimateTokens(summaryLine)

		messages, err := database.GetMessagesSince(chatID, latest.MessagesTo, s.maxContextMessages)
		if err != nil {
			return "", fmt.Errorf("get messages since summary: %w", err)
		}

		// Add summary
		recentParts = append(recentParts, summaryLine)
		usedRecent := recentTokens

		// Add recent messages within budget
		if len(messages) > 0 {
			recentParts = append(recentParts, "Recent messages:")
			usedRecent += estimateTokens("Recent messages:")
			for _, m := range messages {
				line := fmt.Sprintf("- %s: %s", messageRole(m), truncate(m.Content, 300))
				cost := estimateTokens(line)
				if usedRecent+cost > recentBudget {
					break
				}
				recentParts = append(recentParts, line)
				usedRecent += cost
			}
		}
		budget.used += usedRecent
	} else {
		messages, err := database.GetRecentMessages(chatID, s.maxContextMessages)
		if err != nil {
			return "", fmt.Errorf("get recent messages: %w", err)
		}

		if len(messages) >= 2 {
			messages = messages[:len(messages)-1] // exclude current message
			recentParts = append(recentParts, "Recent messages:")
			usedRecent := estimateTokens("Recent messages:")
			for _, m := range messages {
				line := fmt.Sprintf("- %s: %s", messageRole(m), truncate(m.Content, 300))
				cost := estimateTokens(line)
				if usedRecent+cost > recentBudget {
					break
				}
				recentParts = append(recentParts, line)
				usedRecent += cost
			}
			budget.used += usedRecent
		}
	}

	// === Priority 2: Hybrid search results (use remaining budget) ===
	if userMessage != "" && len(userMessage) > 5 && budget.remaining() > 100 {
		var queryEmbedding []float64
		embClient := llm.GetEmbeddingClient()
		if embClient != nil {
			emb, err := embClient.Embed(ctx, userMessage)
			if err != nil {
				log.Printf("[SUMMARIZER] Query embedding failed (falling back to FTS-only): %v", err)
			} else {
				queryEmbedding = emb
			}
		}

		// 2a. Relevant past summaries from THIS chat
		if budget.remaining() > 100 {
			relevantSummaries := s.hybridSearchSummaries(ctx, userMessage, queryEmbedding, &chatID, 3)
			var filtered []models.ChatSummary
			for _, rs := range relevantSummaries {
				if latest == nil || rs.ID != latest.ID {
					filtered = append(filtered, rs)
				}
			}
			if len(filtered) > 0 {
				budget.tryAdd(&parts, "Relevant past context from this conversation:")
				for _, rs := range filtered {
					line := fmt.Sprintf("- [%s] %s", rs.CreatedAt.Format("2006-01-02"), rs.Summary)
					if !budget.tryAdd(&parts, line) {
						break
					}
				}
			}
		}

		// 2b. Cross-chat similar topics
		if budget.remaining() > 100 {
			crossSummaries, err := database.SearchChatSummariesAcrossChats(userMessage, chatID, 2)
			if err != nil {
				log.Printf("[SUMMARIZER] Cross-chat FTS search failed (non-critical): %v", err)
			} else if len(crossSummaries) > 0 {
				budget.tryAdd(&parts, "Similar topics from other conversations:")
				for _, cs := range crossSummaries {
					line := fmt.Sprintf("- %s", truncate(cs.Summary, 200))
					if !budget.tryAdd(&parts, line) {
						break
					}
				}
			}
		}

		// 2c. Director memories (pass pre-computed embedding to avoid duplicate embed call)
		if budget.remaining() > 100 {
			dirMemories := s.hybridSearchMemories(userMessage, queryEmbedding, 3)
			if len(dirMemories) > 0 {
				budget.tryAdd(&parts, "Relevant knowledge from memory:")
				for _, mem := range dirMemories {
					line := fmt.Sprintf("- [%s] %s", mem.Category, mem.Content)
					if !budget.tryAdd(&parts, line) {
						break
					}
				}
			}
		}
	}

	// === Assemble final context: search results first, then current context ===
	parts = append(parts, recentParts...)

	if len(parts) == 0 {
		return "", nil
	}

	result := "[CONVERSATION CONTEXT]\n" + strings.Join(parts, "\n") + "\n[END CONTEXT]"
	log.Printf("[SUMMARIZER] Context assembled: %d tokens used / %d budget", budget.used, budget.total)
	return result, nil
}

func messageRole(m models.Message) string {
	if m.Sender == "admin" {
		return "Assistant"
	}
	return "User"
}


// formatMessagesForSummary formats messages for the summarization prompt
func formatMessagesForSummary(messages []models.Message) string {
	var sb strings.Builder
	for _, m := range messages {
		role := "User"
		if m.Sender == "admin" {
			role = "Assistant"
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, m.Content))
	}
	return sb.String()
}

// hybridSearchSummaries combines FTS + vector search for chat summaries.
// Returns best matches by combining text relevance and semantic similarity.
func (s *ChatSummarizer) hybridSearchSummaries(ctx context.Context, query string, queryEmbedding []float64, chatID *uuid.UUID, limit int) []models.ChatSummary {
	// FTS search
	ftsSummaries, err := database.SearchChatSummariesByText(query, chatID, limit*2)
	if err != nil {
		log.Printf("[SUMMARIZER] FTS search failed: %v", err)
	}

	// If no embeddings available, return FTS results
	if queryEmbedding == nil || len(ftsSummaries) == 0 {
		if len(ftsSummaries) > limit {
			return ftsSummaries[:limit]
		}
		return ftsSummaries
	}

	// Vector search: get summaries with embeddings and compute similarity
	embSummaries, err := database.GetChatSummariesWithEmbeddings(chatID, 50)
	if err != nil || len(embSummaries) == 0 {
		if len(ftsSummaries) > limit {
			return ftsSummaries[:limit]
		}
		return ftsSummaries
	}

	// Build score map: combine FTS rank + vector similarity
	type scored struct {
		summary models.ChatSummary
		score   float64
	}

	scoreMap := make(map[uuid.UUID]*scored)

	// Add FTS results with BM25-normalized scores: r/(1+r) maps ts_rank to 0..1
	for _, s := range ftsSummaries {
		ftsScore := s.Rank / (1.0 + s.Rank)
		scoreMap[s.ID] = &scored{summary: s, score: 0.4 * ftsScore}
	}

	// Add vector similarity scores
	for _, es := range embSummaries {
		sim := llm.CosineSimilarity(queryEmbedding, es.Embedding)
		if sim < 0.3 { // threshold: ignore low similarity
			continue
		}
		if existing, ok := scoreMap[es.ID]; ok {
			existing.score += 0.6 * sim // hybrid: 40% FTS + 60% vector
		} else {
			scoreMap[es.ID] = &scored{summary: es.ChatSummary, score: 0.6 * sim}
		}
	}

	// Sort by combined score
	results := make([]scored, 0, len(scoreMap))
	for _, s := range scoreMap {
		results = append(results, *s)
	}
	// Simple sort (small slice)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	out := make([]models.ChatSummary, 0, limit)
	for i := 0; i < len(results) && i < limit; i++ {
		out = append(out, results[i].summary)
	}
	return out
}

// hybridSearchMemories uses the director's built-in hybrid FTS + vector search with MMR.
// Accepts pre-computed embedding to avoid duplicate embedding calls.
func (s *ChatSummarizer) hybridSearchMemories(query string, queryEmbedding []float64, limit int) []models.DirectorMemory {
	var memories []models.DirectorMemory
	var err error

	if len(queryEmbedding) > 0 {
		// Use pre-computed embedding (avoids duplicate embed call)
		memories, err = database.RecallMemoriesHybridWithEmbedding(query, queryEmbedding, "", limit)
	} else {
		// No embedding available — wrapper will try to generate one or fall back to FTS
		memories, err = database.RecallMemoriesHybrid(query, "", limit)
	}
	if err != nil {
		log.Printf("[SUMMARIZER] Hybrid memory search failed: %v", err)
		return nil
	}
	return memories
}

// generateSummary calls the LLM to produce a conversation summary with structured metadata.
// Returns summary text that includes extracted metadata tags at the end.
func generateSummary(ctx context.Context, provider llm.Provider, conversationText string) (string, error) {
	prompt := fmt.Sprintf(`Summarize the following customer support conversation in 2-5 sentences.
Focus on:
- What the user asked about
- What was resolved or answered
- Any pending issues or important details

After the summary, on a new line add metadata tags (only if clearly present in the conversation):
ISSUE_TYPE: <one of: device_problem, plant_care, billing, firmware, setup, general_question, complaint>
DEVICE_MODEL: <device model if mentioned, e.g. "Zefir Pro", "Zefir Mini">
RESOLUTION: <resolved, pending, escalated>
CLIENT_SENTIMENT: <positive, neutral, negative, frustrated>

Keep it concise and in the same language as the conversation. If metadata is not clear from the conversation, omit that tag.

Conversation:
%s`, conversationText)

	resp, err := provider.GenerateResponse(ctx, prompt, nil, &llm.GenerateOptions{
		Temperature: 0.1,
		MaxTokens:   600,
		SystemPrompt: "You are a conversation summarizer. Output the summary followed by metadata tags.",
	})
	if err != nil {
		return "", err
	}

	return resp.Text, nil
}

// extractAndSaveMetadata parses metadata tags from summary and saves to Director memory.
func extractAndSaveMetadata(chatID uuid.UUID, summaryText string) {
	tags := map[string]string{}
	for _, line := range strings.Split(summaryText, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"ISSUE_TYPE:", "DEVICE_MODEL:", "RESOLUTION:", "CLIENT_SENTIMENT:"} {
			if strings.HasPrefix(line, prefix) {
				value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if value != "" && value != "N/A" && value != "unknown" {
					tags[prefix[:len(prefix)-1]] = value
				}
			}
		}
	}

	if len(tags) == 0 {
		return
	}

	chatShort := chatID.String()[:8]
	dateKey := time.Now().Format("20060102")

	// Save device model pattern if found
	if model, ok := tags["DEVICE_MODEL"]; ok {
		memTags := []string{"device", "model:" + model}
		if issueType, ok2 := tags["ISSUE_TYPE"]; ok2 {
			memTags = append(memTags, "issue:"+issueType)
		}
		content := fmt.Sprintf("Обращение по устройству %s", model)
		if issueType, ok2 := tags["ISSUE_TYPE"]; ok2 {
			content += fmt.Sprintf(", проблема: %s", issueType)
		}
		if res, ok2 := tags["RESOLUTION"]; ok2 {
			content += fmt.Sprintf(", статус: %s", res)
		}
		database.SaveAutoMemory("fact",
			fmt.Sprintf("device_issue:%s:%s", dateKey, chatShort),
			content, memTags, nil)
	}

	// Save frustrated customer as pattern
	if sentiment, ok := tags["CLIENT_SENTIMENT"]; ok && (sentiment == "negative" || sentiment == "frustrated") {
		content := fmt.Sprintf("Негативный клиент в чате %s", chatShort)
		if issueType, ok2 := tags["ISSUE_TYPE"]; ok2 {
			content += fmt.Sprintf(", тема: %s", issueType)
		}
		expires := time.Now().Add(14 * 24 * time.Hour)
		database.SaveAutoMemory("fact",
			fmt.Sprintf("negative_sentiment:%s:%s", dateKey, chatShort),
			content, []string{"sentiment", "negative"}, &expires)
	}
}
