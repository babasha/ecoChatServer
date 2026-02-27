package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

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

	// Quick checks
	if !ar.config.Enabled ||
		msg.Sender != "user" ||
		(chat.AssignedTo != nil && *chat.AssignedTo != uuid.Nil) ||
		!chat.AutoResponderEnabled {
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
		return nil, nil
	}

	chatKey := chat.ID.String()

	// Check escalation
	escalation, _ := ar.escalations.get(chatKey)
	if escalation != nil && escalation.ReturnedAt == nil {
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

	// Run agent with timeout
	genCtx, cancel := context.WithTimeout(ctx, time.Duration(ar.config.IdleTimeMinutes)*time.Minute)
	defer cancel()

	var response string
	var err error
	var agentType string

	if ar.useMultiAgent {
		response, err = ar.processWithMultiAgent(genCtx, chatKey, msg.Content, zefirUserID, clientLang)
		agentType = "multi-agent"
	} else {
		response, err = ar.processWithSingleAgent(genCtx, chatKey, msg.Content, zefirUserID, clientLang)
		agentType = "single-agent"
	}

	if err != nil {
		log.Printf("[ADK_V2] Agent error (%s): %v", agentType, err)
		return nil, err
	}

	// Check escalation
	if strings.Contains(response, "#escalate") {
		ar.escalations.set(chatKey, &EscalationState{
			EscalatedAt: time.Now(),
		})

		response = strings.ReplaceAll(response, "#escalate", "")
		response = strings.TrimSpace(response)
	}

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
func (ar *ADKAutoResponderV2) processWithMultiAgent(ctx context.Context, chatKey, userMessage, zefirUserID, clientLang string) (string, error) {
	orchestrator, err := ar.getOrCreateOrchestrator(ctx, chatKey, zefirUserID)
	if err != nil {
		return "", fmt.Errorf("failed to create orchestrator: %w", err)
	}

	orchestrator.SetZefirUserID(zefirUserID)
	return orchestrator.ProcessMessage(ctx, chatKey, userMessage, clientLang)
}

// processWithSingleAgent processes through single agent
func (ar *ADKAutoResponderV2) processWithSingleAgent(ctx context.Context, chatKey, userMessage, zefirUserID, clientLang string) (string, error) {
	agnt, err := ar.getOrCreateAgent(ctx, chatKey)
	if err != nil {
		return "", err
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
