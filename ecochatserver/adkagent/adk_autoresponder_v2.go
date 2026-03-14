package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/egor/ecochatserver/database"
	"github.com/egor/ecochatserver/director"
	"github.com/egor/ecochatserver/llm"
	"github.com/egor/ecochatserver/models"
)

const (
	// agentTTL — agent cache TTL (30 minutes)
	agentTTL = 30 * time.Minute
	// agentEvictInterval — cache eviction check interval
	agentEvictInterval = 5 * time.Minute
	// maxMessageLength — max input message length (10KB)
	maxMessageLength = 10_000
)

// ADKAutoResponderV2 — Zefir IoT support auto-responder
type ADKAutoResponderV2 struct {
	zefirClient *ZefirClient
	config      llm.AutoResponderConfig

	// Single-agent mode: Agent cache per chatID
	agents *syncCache[*SupportAgent]

	// Multi-agent mode: Orchestrator cache per chatID
	orchestrators *syncCache[*OrchestratorAgent]

	// Mode: true = multi-agent, false = single-agent
	useMultiAgent bool

	// Supervisor agent (optional, for single-agent mode)
	supervisor    *SupervisorAgent
	useSupervisor bool

	// Escalations (in-memory — reset on restart)
	escalations *syncCache[*EscalationState]

	// Session pruning: conversation summarizer
	summarizer *ChatSummarizer

	// Level 2: Director agent (strategic analysis + directives)
	director *director.Director
}

// initDirector creates the Director (Level 2) agent.
// Non-fatal — if it fails, autoresponder works without strategic analysis.
func initDirector() *director.Director {
	d, err := director.New(director.Config{
		// ProviderConfig: nil → uses global provider for now
		// TODO: configure separate Claude provider via DB settings
	})
	if err != nil {
		log.Printf("[ADK_V2] Warning: Director init failed: %v (continuing without)", err)
		return nil
	}
	return d
}

// NewADKAutoResponderV2 creates auto-responder (single-agent mode)
func NewADKAutoResponderV2(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	zefirClient := NewZefirClient()

	ar := &ADKAutoResponderV2{
		zefirClient:   zefirClient,
		config:        cfg,
		agents:        newSyncCacheWithTTL[*SupportAgent]("AGENT", agentTTL, agentEvictInterval),
		orchestrators: newSyncCacheWithTTL[*OrchestratorAgent]("ORCHESTRATOR", agentTTL, agentEvictInterval),
		escalations:   newSyncCache[*EscalationState](),
		useMultiAgent: false,
		useSupervisor: false,
		summarizer:    NewChatSummarizer(),
		director:      initDirector(),
	}

	log.Printf("[ADK_V2] Initialized Zefir auto-responder (single-agent mode, TTL=%v)", agentTTL)
	return ar, nil
}

// NewADKAutoResponderV2MultiAgent creates auto-responder with multi-agent architecture
func NewADKAutoResponderV2MultiAgent(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	zefirClient := NewZefirClient()

	ar := &ADKAutoResponderV2{
		zefirClient:   zefirClient,
		config:        cfg,
		agents:        newSyncCacheWithTTL[*SupportAgent]("AGENT", agentTTL, agentEvictInterval),
		orchestrators: newSyncCacheWithTTL[*OrchestratorAgent]("ORCHESTRATOR", agentTTL, agentEvictInterval),
		escalations:   newSyncCache[*EscalationState](),
		useMultiAgent: true,
		useSupervisor: false,
		summarizer:    NewChatSummarizer(),
		director:      initDirector(),
	}

	log.Printf("[ADK_V2] Initialized Zefir auto-responder (MULTI-AGENT mode, TTL=%v)", agentTTL)
	log.Printf("[ADK_V2] Orchestrator -> [PlantExpert, DeviceSpecialist, SupportSpecialist]")
	return ar, nil
}

// EnableMultiAgent switches between single-agent and multi-agent mode
func (ar *ADKAutoResponderV2) EnableMultiAgent(enabled bool) {
	ar.useMultiAgent = enabled
	mode := "single-agent"
	if enabled {
		mode = "multi-agent"
	}
	log.Printf("[ADK_V2] Switched to %s mode", mode)
}

// NewADKAutoResponderV2WithSupervisor creates auto-responder with supervisor agent
func NewADKAutoResponderV2WithSupervisor(ctx context.Context, cfg llm.AutoResponderConfig) (*ADKAutoResponderV2, error) {
	ar, err := NewADKAutoResponderV2(ctx, cfg)
	if err != nil {
		return nil, err
	}

	supervisor, err := NewSupervisorAgent(ctx)
	if err != nil {
		log.Printf("[ADK_V2] Warning: failed to create supervisor agent: %v (continuing without)", err)
	} else {
		ar.supervisor = supervisor
		ar.useSupervisor = true
		log.Printf("[ADK_V2] Supervisor agent enabled")
	}

	return ar, nil
}

// EnableSupervisor enables/disables supervisor agent
func (ar *ADKAutoResponderV2) EnableSupervisor(enabled bool) {
	ar.useSupervisor = enabled && ar.supervisor != nil
	log.Printf("[ADK_V2] Supervisor agent: %v", ar.useSupervisor)
}

// ProcessMessage — main message processing method
func (ar *ADKAutoResponderV2) ProcessMessage(ctx context.Context, chat *models.Chat, msg *models.Message) (*models.Message, error) {
	log.Printf("[ADK_V2] ProcessMessage: chatID=%s, mode=%s", chat.ID, ar.getMode())

	// Quick checks with logging
	if !ar.config.Enabled {
		log.Printf("[ADK_V2] SKIP: auto-responder disabled")
		return nil, nil
	}
	if msg.Sender != "user" {
		log.Printf("[ADK_V2] SKIP: sender=%s (not user)", msg.Sender)
		return nil, nil
	}
	if chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil {
		log.Printf("[ADK_V2] SKIP: chat assigned to %s", chat.AssignedTo)
		return nil, nil
	}
	if !chat.AutoResponderEnabled {
		log.Printf("[ADK_V2] SKIP: chat.AutoResponderEnabled=false")
		return nil, nil
	}

	// Input validation: truncate overly long messages
	if len(msg.Content) > maxMessageLength {
		log.Printf("[ADK_V2] Message truncated: %d → %d chars", len(msg.Content), maxMessageLength)
		msg.Content = msg.Content[:maxMessageLength]
	}

	// Input validation: reject empty messages
	trimmedContent := strings.TrimSpace(msg.Content)
	if trimmedContent == "" {
		log.Printf("[ADK_V2] SKIP: empty message")
		return nil, nil
	}

	chatKey := chat.ID.String()

	// Check escalation
	escalation, _ := ar.escalations.get(chatKey)
	if escalation != nil && escalation.ReturnedAt == nil {
		log.Printf("[ADK_V2] SKIP: active escalation for chat %s", chatKey)
		return nil, nil
	}

	// Get Zefir user ID from chat metadata
	zefirUserID := ar.getZefirUserID(chat)
	log.Printf("[ADK_V2] User context: zefirUserID=%s", zefirUserID)

	// Delay (if configured)
	if ar.config.DelaySeconds > 0 {
		time.Sleep(time.Duration(ar.config.DelaySeconds) * time.Second)
	}

	// Get client language from message metadata
	var clientLang string
	if msg.Metadata != nil {
		if detectedLang, ok := msg.Metadata["detectedLanguage"].(string); ok && detectedLang != "" {
			clientLang = detectedLang
			log.Printf("[ADK_V2] Client language: %s", clientLang)
		}
	}

	// Build full context prefix: [DIRECTOR INSTRUCTIONS] + [CONVERSATION CONTEXT]
	var contextParts []string

	// 1. Director directives (Level 2 → Level 1)
	if ar.director != nil {
		if dirCtx := ar.director.BuildDirectivesContext(ctx); dirCtx != "" {
			contextParts = append(contextParts, dirCtx)
			log.Printf("[ADK_V2] Injecting director directives")
		}
	}

	// 2. Conversation context from DB (session pruning + FTS search)
	if ar.summarizer != nil {
		ctxStr, ctxErr := ar.summarizer.BuildContextWithQuery(ctx, chat.ID, msg.Content)
		if ctxErr != nil {
			log.Printf("[ADK_V2] Warning: failed to build context: %v", ctxErr)
		} else if ctxStr != "" {
			contextParts = append(contextParts, ctxStr)
			log.Printf("[ADK_V2] Injecting conversation context (%d chars)", len(ctxStr))
		}
	}

	// Prepend context to message
	messageWithCtx := msg.Content
	if len(contextParts) > 0 {
		messageWithCtx = strings.Join(contextParts, "\n\n") + "\n\n" + msg.Content
	}

	// Run agent with timeout
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	startTime := time.Now()

	var agentResult *AgentResult
	var err error
	var agentType string

	if ar.useMultiAgent {
		agentResult, err = ar.processWithMultiAgent(genCtx, chatKey, messageWithCtx, zefirUserID, clientLang)
		agentType = "multi-agent"
	} else {
		agentResult, err = ar.processWithSingleAgent(genCtx, chatKey, messageWithCtx, zefirUserID, clientLang)
		agentType = "single-agent"
	}

	responseTimeMs := int(time.Since(startTime).Milliseconds())

	if err != nil {
		log.Printf("[ADK_V2] Agent error (%s): %v", agentType, err)
		return nil, err
	}

	response := agentResult.Response

	// Check escalation
	wasEscalated := false
	if strings.Contains(response, "#escalate") {
		ar.escalations.set(chatKey, &EscalationState{
			EscalatedAt: time.Now(),
		})
		wasEscalated = true

		response = strings.ReplaceAll(response, "#escalate", "")
		response = strings.TrimSpace(response)

		// Track escalation event for Director (with context for episodic memory)
		if ar.director != nil {
			clientName := ""
			if chat.User.Name != "" {
				clientName = chat.User.Name
			}
			ar.director.Tracker().RecordEscalationWithContext(chatKey, clientName, agentType)
		}
	}

	// Fallback for empty response
	wasEmptyResponse := false
	if strings.TrimSpace(response) == "" {
		log.Printf("[ADK_V2] WARNING: empty response from %s agent, using fallback", agentType)
		response = "Sorry, I couldn't generate a response. Please try rephrasing your question or contact support@zefir.app"
		wasEmptyResponse = true

		// Track empty response event for Director (with agent context)
		if ar.director != nil {
			ar.director.Tracker().RecordEmptyResponseWithContext(chatKey, agentType)
		}
	}

	log.Printf("[ADK_V2] Response ready: %d chars, mode=%s, tools=%d", len(response), agentType, len(agentResult.ToolsCalled))

	// Build response message
	botMsg := &models.Message{
		ChatID:    chat.ID,
		Content:   response,
		Sender:    "admin",
		SenderID:  uuid.Nil,
		Timestamp: time.Now(),
		Read:      true,
		Type:      "text",
		Metadata: map[string]interface{}{
			"isAutoResponse": true,
			"botName":        ar.config.BotName,
			"provider":       "adk-v2",
			"agentMode":      agentType,
		},
	}

	if ar.useMultiAgent {
		botMsg.Metadata["multiAgent"] = true
	}
	if ar.useSupervisor {
		botMsg.Metadata["supervisorEnabled"] = true
	}

	// Determine which agent handled and active prompt version
	activeAgentName := "zefir_support"
	if ar.useMultiAgent {
		activeAgentName = "orchestrator"
	}
	var promptVersion *int
	if ap, _ := database.GetActivePrompt(activeAgentName); ap != nil {
		promptVersion = &ap.Version
	}

	// Async: post-response background tasks
	chatIDCopy := chat.ID
	directorRef := ar.director
	summarizerRef := ar.summarizer

	metric := &models.InteractionMetric{
		ID:             uuid.New(),
		ChatID:         chat.ID,
		AgentMode:      agentType,
		AgentName:      activeAgentName,
		PromptVersion:  promptVersion,
		ToolsCalled:    agentResult.ToolsCalled,
		ToolCount:      len(agentResult.ToolsCalled),
		WasEscalated:   wasEscalated,
		WasEmpty:       wasEmptyResponse,
		ResponseLength: len(response),
		ResponseTimeMs: responseTimeMs,
		CreatedAt:      time.Now(),
	}

	go func() {
		bgCtx, bgCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer bgCancel()

		// 1. Save interaction metric
		if err := database.InsertInteractionMetric(metric); err != nil {
			log.Printf("[ADK_V2] Failed to save interaction metric: %v", err)
		}

		// 2. Summarization check
		if summarizerRef != nil {
			if summarizerRef.ShouldSummarize(bgCtx, chatIDCopy) {
				if err := summarizerRef.Summarize(bgCtx, chatIDCopy); err != nil {
					log.Printf("[ADK_V2] Async summarization failed for chat %s: %v", chatIDCopy, err)
				}
			}
		}

		// 3. Director: check event thresholds → trigger analysis if needed
		if directorRef != nil && (wasEscalated || wasEmptyResponse) {
			directorRef.CheckAndAnalyze(bgCtx)
		}
	}()

	return botMsg, nil
}

// getMode returns current mode string
func (ar *ADKAutoResponderV2) getMode() string {
	if ar.useMultiAgent {
		return "multi-agent"
	}
	return "single-agent"
}

// processWithMultiAgent processes through multi-agent system
func (ar *ADKAutoResponderV2) processWithMultiAgent(ctx context.Context, chatKey, userMessage, zefirUserID, clientLang string) (*AgentResult, error) {
	orchestrator, err := ar.getOrCreateOrchestrator(ctx, chatKey, zefirUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}

	orchestrator.SetZefirUserID(zefirUserID)
	return orchestrator.ProcessMessage(ctx, chatKey, userMessage, clientLang)
}

// processWithSingleAgent processes through single agent
func (ar *ADKAutoResponderV2) processWithSingleAgent(ctx context.Context, chatKey, userMessage, zefirUserID, clientLang string) (*AgentResult, error) {
	agnt, err := ar.getOrCreateAgent(ctx, chatKey)
	if err != nil {
		return nil, err
	}

	processedMessage := userMessage

	// Supervisor analysis (optional)
	if ar.useSupervisor && ar.supervisor != nil {
		plan, err := ar.supervisor.AnalyzeRequest(ctx, userMessage)
		if err != nil {
			log.Printf("[ADK_V2] Supervisor analysis failed: %v (continuing without plan)", err)
		} else if plan != nil && plan.RequiredTool != "" {
			instruction := ar.formatSupervisorInstruction(plan)
			processedMessage = instruction + "\n\n" + userMessage
			log.Printf("[ADK_V2] Supervisor plan: intent=%s, required_tool=%s", plan.Intent, plan.RequiredTool)
		}
	}

	if clientLang != "" {
		return agnt.ProcessMessage(ctx, chatKey, processedMessage, zefirUserID, clientLang)
	}
	return agnt.ProcessMessage(ctx, chatKey, processedMessage, zefirUserID)
}

// getOrCreateOrchestrator creates or returns cached Orchestrator
func (ar *ADKAutoResponderV2) getOrCreateOrchestrator(ctx context.Context, chatID, zefirUserID string) (*OrchestratorAgent, error) {
	orch, err := ar.orchestrators.getOrCreate(chatID, func() (*OrchestratorAgent, error) {
		return NewOrchestratorAgent(ctx, MultiAgentConfig{
			ZefirClient: ar.zefirClient,
			ZefirUserID: zefirUserID,
		})
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[ADK_V2] Created/reused orchestrator for chat %s (total: %d)", chatID, ar.orchestrators.len())
	return orch, nil
}

// formatSupervisorInstruction formats supervisor instruction for the agent
func (ar *ADKAutoResponderV2) formatSupervisorInstruction(plan *ExecutionPlan) string {
	if plan == nil || plan.RequiredTool == "" {
		return ""
	}

	instruction := fmt.Sprintf("[SUPERVISOR INSTRUCTION]\nIntent: %s\nREQUIRED: You MUST call tool '%s' first.\n",
		plan.Intent, plan.RequiredTool)

	if len(plan.Steps) > 0 {
		instruction += "Steps:\n"
		for i, step := range plan.Steps {
			instruction += fmt.Sprintf("%d. %s\n", i+1, step)
		}
	}

	return instruction
}

// ClearEscalation clears escalation state
func (ar *ADKAutoResponderV2) ClearEscalation(chatID string) {
	ar.escalations.remove(chatID)
}

// getOrCreateAgent creates or returns cached agent for specific chatID
func (ar *ADKAutoResponderV2) getOrCreateAgent(ctx context.Context, chatID string) (*SupportAgent, error) {
	agnt, err := ar.agents.getOrCreate(chatID, func() (*SupportAgent, error) {
		return NewSupportAgent(ctx, ar.zefirClient)
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[ADK_V2] Created/reused agent for chat %s (total agents: %d)", chatID, ar.agents.len())
	return agnt, nil
}

// getZefirUserID extracts Zefir user ID from chat metadata
func (ar *ADKAutoResponderV2) getZefirUserID(chat *models.Chat) string {
	if chat.Metadata != nil {
		if zefirUID, ok := chat.Metadata["zefir_user_id"].(string); ok && zefirUID != "" {
			return zefirUID
		}
	}
	return ""
}

// RemoveAgentForChat removes cached agent for specific chat
func (ar *ADKAutoResponderV2) RemoveAgentForChat(chatID string) {
	if ar.agents.remove(chatID) {
		log.Printf("[ADK_V2] Removed agent for chat %s (total agents: %d)", chatID, ar.agents.len())
	}
}

// ClearAllAgents removes all cached agents
func (ar *ADKAutoResponderV2) ClearAllAgents() {
	count := ar.agents.clear()
	log.Printf("[ADK_V2] Cleared all agents (removed %d agents)", count)
}

// GetAgentCacheStats returns statistics about cached agents
func (ar *ADKAutoResponderV2) GetAgentCacheStats() (total int, chatIDs []string) {
	return ar.agents.len(), ar.agents.keys()
}

// GetDirector returns the Director agent (Level 2) for external use (API handlers, cron)
func (ar *ADKAutoResponderV2) GetDirector() *director.Director {
	return ar.director
}

// TriggerDirectorAnalysis manually triggers a daily director analysis
func (ar *ADKAutoResponderV2) TriggerDirectorAnalysis(ctx context.Context) error {
	if ar.director == nil {
		return fmt.Errorf("director not initialized")
	}
	return ar.director.AnalyzeDaily(ctx)
}
