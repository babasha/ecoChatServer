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
	defaultSummaryThreshold  = 20 // messages before triggering summarization
	defaultMaxContextMessages = 10 // max recent messages in context
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

// BuildContextWithQuery assembles conversation context with FTS-enhanced retrieval.
// When userMessage is non-empty, it searches for relevant past summaries matching the topic.
func (s *ChatSummarizer) BuildContextWithQuery(ctx context.Context, chatID uuid.UUID, userMessage string) (string, error) {
	latest, err := database.GetLatestChatSummary(chatID)
	if err != nil {
		return "", fmt.Errorf("get latest summary: %w", err)
	}

	var parts []string

	// Hybrid search: FTS + Vector (when embedding service is available)
	if userMessage != "" && len(userMessage) > 5 {
		// Generate query embedding (async-safe, cached)
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

		// 1. Search relevant past summaries from THIS chat
		relevantSummaries := s.hybridSearchSummaries(ctx, userMessage, queryEmbedding, &chatID, 3)
		if len(relevantSummaries) > 0 {
			var filtered []models.ChatSummary
			for _, rs := range relevantSummaries {
				if latest == nil || rs.ID != latest.ID {
					filtered = append(filtered, rs)
				}
			}
			if len(filtered) > 0 {
				parts = append(parts, "Relevant past context from this conversation:")
				for _, rs := range filtered {
					parts = append(parts, fmt.Sprintf("- [%s] %s", rs.CreatedAt.Format("2006-01-02"), rs.Summary))
				}
			}
		}

		// 2. Search across OTHER chats for similar topics
		crossSummaries, err := database.SearchChatSummariesAcrossChats(userMessage, chatID, 2)
		if err != nil {
			log.Printf("[SUMMARIZER] Cross-chat FTS search failed (non-critical): %v", err)
		} else if len(crossSummaries) > 0 {
			parts = append(parts, "Similar topics from other conversations:")
			for _, cs := range crossSummaries {
				parts = append(parts, fmt.Sprintf("- %s", truncate(cs.Summary, 200)))
			}
		}

		// 3. Hybrid search director memories (uses built-in FTS + vector + MMR)
		dirMemories := s.hybridSearchMemories(userMessage, nil, 3)
		if len(dirMemories) > 0 {
			parts = append(parts, "Relevant knowledge from memory:")
			for _, mem := range dirMemories {
				parts = append(parts, fmt.Sprintf("- [%s] %s", mem.Category, mem.Content))
			}
		}
	}

	// 4. Current conversation context (original logic)
	if latest != nil {
		parts = append(parts, fmt.Sprintf("Current conversation summary: %s", latest.Summary))

		// Get messages after the summary
		messages, err := database.GetMessagesSince(chatID, latest.MessagesTo, s.maxContextMessages)
		if err != nil {
			return "", fmt.Errorf("get messages since summary: %w", err)
		}

		if len(messages) > 0 {
			parts = append(parts, "Recent messages:")
			for _, m := range messages {
				parts = append(parts, fmt.Sprintf("- %s: %s", messageRole(m), truncate(m.Content, 300)))
			}
		}
	} else {
		// No summary yet — include recent messages as context if chat has history
		messages, err := database.GetRecentMessages(chatID, s.maxContextMessages)
		if err != nil {
			return "", fmt.Errorf("get recent messages: %w", err)
		}

		// Need at least 2 messages for context to be useful (previous exchange)
		if len(messages) < 2 {
			if len(parts) == 0 {
				return "", nil
			}
			// We have FTS results even without conversation history
			return "[CONVERSATION CONTEXT]\n" + strings.Join(parts, "\n") + "\n[END CONTEXT]", nil
		}

		// Exclude the last message (it's the current one being processed)
		messages = messages[:len(messages)-1]
		if len(messages) > 0 {
			parts = append(parts, "Recent messages:")
			for _, m := range messages {
				parts = append(parts, fmt.Sprintf("- %s: %s", messageRole(m), truncate(m.Content, 300)))
			}
		}
	}

	if len(parts) == 0 {
		return "", nil
	}

	return "[CONVERSATION CONTEXT]\n" + strings.Join(parts, "\n") + "\n[END CONTEXT]", nil
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

	// Add FTS results with normalized scores
	for i, s := range ftsSummaries {
		ftsScore := 1.0 - float64(i)*0.1 // position-based score
		if ftsScore < 0.1 {
			ftsScore = 0.1
		}
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
func (s *ChatSummarizer) hybridSearchMemories(query string, queryEmbedding []float64, limit int) []models.DirectorMemory {
	memories, err := database.RecallMemoriesHybrid(query, "", limit)
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
