package agentbus

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/llm"
	"github.com/google/uuid"
)

// AgentBus — lightweight hub for inter-agent communication.
// Director can query L1 agents, L1 agents can ask Director for guidance.
// All calls use GenerateResponse (no tools) to prevent recursion.
type AgentBus struct {
	directorProvider llm.Provider
	mu               sync.RWMutex
	sessions         map[string]*AgentSession
}

// AgentSession tracks an active L1 agent processing a chat.
type AgentSession struct {
	ChatID       string
	AgentType    string // "zefir_support", "orchestrator"
	StartedAt    time.Time
	MessageCount int
}

// QueryResult holds the result of an inter-agent query.
type QueryResult struct {
	Response string
	Tokens   *llm.Usage
}

// AgentContext holds raw chat context (no LLM call).
type AgentContext struct {
	Messages   []MessageInfo `json:"messages"`
	Summary    string        `json:"summary,omitempty"`
	Directives string        `json:"directives,omitempty"`
}

// MessageInfo is a simplified message for context display.
type MessageInfo struct {
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

const (
	interAgentTimeout  = 15 * time.Second
	queryAgentMaxTok   = 1000
	queryDirectorMaxTok = 500
)

// New creates an AgentBus with the given director LLM provider.
func New(directorProvider llm.Provider) *AgentBus {
	return &AgentBus{
		directorProvider: directorProvider,
		sessions:         make(map[string]*AgentSession),
	}
}

// RegisterSession marks an L1 agent as actively processing a chat.
func (b *AgentBus) RegisterSession(chatID, agentType string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions[chatID] = &AgentSession{
		ChatID:    chatID,
		AgentType: agentType,
		StartedAt: time.Now(),
	}
	log.Printf("[AGENTBUS] Registered session: chatID=%s type=%s", chatID, agentType)
}

// UnregisterSession removes the session for a chat.
func (b *AgentBus) UnregisterSession(chatID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sessions, chatID)
	log.Printf("[AGENTBUS] Unregistered session: chatID=%s", chatID)
}

// ListSessions returns a snapshot of all active agent sessions.
func (b *AgentBus) ListSessions() []AgentSession {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]AgentSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		result = append(result, *s)
	}
	return result
}

// QueryAgent sends a question from the Director to a simulated L1 agent.
// Loads the agent's prompt, recent messages, and summary from the DB,
// then makes an LLM call via GenerateResponse (no tools — prevents recursion).
func (b *AgentBus) QueryAgent(ctx context.Context, chatID, question string) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, interAgentTimeout)
	defer cancel()

	// Parse chatID as UUID
	chatUUID, err := uuid.Parse(chatID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat_id: %w", err)
	}

	// Load agent system prompt from DB (fallback to minimal)
	systemPrompt := "You are a support agent for Zefir IoT plant monitoring system. Answer based on the conversation context provided."
	if ap, err := database.GetActivePrompt("zefir_support"); err == nil && ap != nil {
		systemPrompt = ap.Prompt
	}

	// Build conversation context
	var contextParts []string

	// Summary
	if summary, err := database.GetLatestChatSummary(chatUUID); err == nil && summary != nil {
		contextParts = append(contextParts, fmt.Sprintf("[CONVERSATION SUMMARY]\n%s\n[END SUMMARY]", summary.Summary))
	}

	// Recent messages
	if msgs, err := database.GetRecentMessages(chatUUID, 20); err == nil && len(msgs) > 0 {
		var msgLines []string
		for _, m := range msgs {
			content := m.Content
			if len(content) > 300 {
				content = content[:300] + "..."
			}
			msgLines = append(msgLines, fmt.Sprintf("[%s] %s: %s",
				m.Timestamp.Format("15:04"), m.Sender, content))
		}
		contextParts = append(contextParts, "[RECENT MESSAGES]\n"+strings.Join(msgLines, "\n")+"\n[END MESSAGES]")
	}

	// Build the user message with context + question
	userMessage := question
	if len(contextParts) > 0 {
		userMessage = strings.Join(contextParts, "\n\n") + "\n\n[DIRECTOR QUESTION]\n" + question
	}

	opts := &llm.GenerateOptions{
		Temperature:  0.3,
		MaxTokens:    queryAgentMaxTok,
		SystemPrompt: systemPrompt,
	}

	resp, err := b.directorProvider.GenerateResponse(queryCtx, userMessage, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("QueryAgent LLM call failed: %w", err)
	}

	log.Printf("[AGENTBUS] QueryAgent chatID=%s question=%d chars → response=%d chars",
		chatID, len(question), len(resp.Text))

	return &QueryResult{
		Response: resp.Text,
		Tokens:   resp.Usage,
	}, nil
}

// QueryDirector sends a question from an L1 agent to the Director for guidance.
// Uses a lightweight system prompt. No tools — prevents recursion.
func (b *AgentBus) QueryDirector(ctx context.Context, question, chatContext string) (*QueryResult, error) {
	queryCtx, cancel := context.WithTimeout(ctx, interAgentTimeout)
	defer cancel()

	systemPrompt := `You are the Director — a strategic AI supervisor for customer support agents.
An L1 support agent is asking you for guidance on how to handle a customer situation.
Give clear, actionable advice. Be concise (2-3 sentences max).
Focus on: what to say, what tool to use, or whether to escalate.`

	userMessage := question
	if chatContext != "" {
		userMessage = "[CHAT CONTEXT]\n" + chatContext + "\n[END CONTEXT]\n\n" + question
	}

	opts := &llm.GenerateOptions{
		Temperature:  0.4,
		MaxTokens:    queryDirectorMaxTok,
		SystemPrompt: systemPrompt,
	}

	resp, err := b.directorProvider.GenerateResponse(queryCtx, userMessage, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("QueryDirector LLM call failed: %w", err)
	}

	log.Printf("[AGENTBUS] QueryDirector question=%d chars → response=%d chars",
		len(question), len(resp.Text))

	return &QueryResult{
		Response: resp.Text,
		Tokens:   resp.Usage,
	}, nil
}

// GetAgentContext returns raw chat context without making an LLM call.
// Used by the agent_context tool for the Director.
func (b *AgentBus) GetAgentContext(ctx context.Context, chatID string, limit int) (*AgentContext, error) {
	chatUUID, err := uuid.Parse(chatID)
	if err != nil {
		return nil, fmt.Errorf("invalid chat_id: %w", err)
	}

	if limit <= 0 || limit > 50 {
		limit = 20
	}

	result := &AgentContext{}

	// Messages
	if msgs, err := database.GetRecentMessages(chatUUID, limit); err == nil {
		for _, m := range msgs {
			result.Messages = append(result.Messages, MessageInfo{
				Sender:    m.Sender,
				Content:   m.Content,
				Timestamp: m.Timestamp,
			})
		}
	}

	// Summary
	if summary, err := database.GetLatestChatSummary(chatUUID); err == nil && summary != nil {
		result.Summary = summary.Summary
	}

	return result, nil
}
