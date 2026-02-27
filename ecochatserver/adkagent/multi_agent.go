package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
	"google.golang.org/genai"
)

// ============================================================================
// MULTI-AGENT ARCHITECTURE
// Orchestrator → [PlantExpert, DeviceSpecialist, SupportSpecialist]
// ============================================================================

// MultiAgentConfig configuration for multi-agent system
type MultiAgentConfig struct {
	ZefirClient *ZefirClient
	ZefirUserID string
}

// OrchestratorAgent — main agent that delegates to specialized agents
type OrchestratorAgent struct {
	orchestrator   agent.Agent
	plantAgent     agent.Agent
	deviceAgent    agent.Agent
	supportAgent   agent.Agent
	zefirClient    *ZefirClient
	zefirUserID    string

	// Runner and services
	runner         *runner.Runner
	sessionService session.Service
	memoryService  memory.Service
}

// NewOrchestratorAgent creates the multi-agent system for Zefir
func NewOrchestratorAgent(ctx context.Context, cfg MultiAgentConfig) (*OrchestratorAgent, error) {
	oa := &OrchestratorAgent{
		zefirClient: cfg.ZefirClient,
		zefirUserID: cfg.ZefirUserID,
	}

	// UserID provider for device tools
	var userIDProvider ZefirUserIDProvider = oa

	// 2. Create specialized agents
	plantAgent, err := oa.createPlantAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create plant agent: %w", err)
	}
	oa.plantAgent = plantAgent

	deviceAgent, err := oa.createDeviceAgent(cfg.ZefirClient, &userIDProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to create device agent: %w", err)
	}
	oa.deviceAgent = deviceAgent

	supportAgent, err := oa.createSupportAgent()
	if err != nil {
		return nil, fmt.Errorf("failed to create support agent: %w", err)
	}
	oa.supportAgent = supportAgent

	// 3. Create Orchestrator with agents as tools
	orchestrator, err := oa.createOrchestrator()
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}
	oa.orchestrator = orchestrator

	// 4. Create services and runner
	oa.sessionService = session.InMemoryService()
	oa.memoryService = memory.InMemoryService()

	agentRunner, err := runner.New(runner.Config{
		AppName:         "zefir_support",
		Agent:           orchestrator,
		SessionService:  oa.sessionService,
		ArtifactService: artifact.InMemoryService(),
		MemoryService:   oa.memoryService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}
	oa.runner = agentRunner

	log.Printf("[MULTI-AGENT] Orchestrator created with 3 specialized agents")
	return oa, nil
}

// GetZefirUserID implements ZefirUserIDProvider
func (oa *OrchestratorAgent) GetZefirUserID() string {
	return oa.zefirUserID
}

// SetZefirUserID sets user ID for current session
func (oa *OrchestratorAgent) SetZefirUserID(userID string) {
	oa.zefirUserID = userID
}

// createPlantAgent creates the plant knowledge specialist
func (oa *OrchestratorAgent) createPlantAgent() (agent.Agent, error) {
	model, err := NewLLMModel(context.Background())
	if err != nil {
		return nil, err
	}

	plantTools, err := CreatePlantTools()
	if err != nil {
		return nil, err
	}

	plantAgent, err := llmagent.New(llmagent.Config{
		Name:        "plant_expert",
		Model:       model,
		Description: "Plant care expert: species, humidity thresholds, care guides, recommendations.",
		Instruction: getPlantAgentPrompt(),
		Tools:       plantTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.3),
			MaxOutputTokens: 350,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created PlantExpert with %d tools", len(plantTools))
	return plantAgent, nil
}

// createDeviceAgent creates the device/sensor specialist
func (oa *OrchestratorAgent) createDeviceAgent(zefirClient *ZefirClient, userIDProvider *ZefirUserIDProvider) (agent.Agent, error) {
	model, err := NewLLMModel(context.Background())
	if err != nil {
		return nil, err
	}

	deviceTools, err := CreateDeviceTools(zefirClient, userIDProvider)
	if err != nil {
		return nil, err
	}

	deviceAgent, err := llmagent.New(llmagent.Config{
		Name:        "device_specialist",
		Model:       model,
		Description: "Device expert: sensors, setup, troubleshooting, mesh network.",
		Instruction: getDeviceAgentPrompt(),
		Tools:       deviceTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.2),
			MaxOutputTokens: 350,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created DeviceSpecialist with %d tools", len(deviceTools))
	return deviceAgent, nil
}

// createSupportAgent creates the general support specialist
func (oa *OrchestratorAgent) createSupportAgent() (agent.Agent, error) {
	model, err := NewLLMModel(context.Background())
	if err != nil {
		return nil, err
	}

	supportTools, err := CreateSupportTools()
	if err != nil {
		return nil, err
	}

	supportAgent, err := llmagent.New(llmagent.Config{
		Name:        "support_specialist",
		Model:       model,
		Description: "Support: FAQ, app info, contacts, features, security.",
		Instruction: getSupportAgentPrompt(),
		Tools:       supportTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.3),
			MaxOutputTokens: 300,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created SupportSpecialist with %d tools", len(supportTools))
	return supportAgent, nil
}

// createOrchestrator creates the main router agent
func (oa *OrchestratorAgent) createOrchestrator() (agent.Agent, error) {
	model, err := NewLLMModel(context.Background())
	if err != nil {
		return nil, err
	}

	plantTool := agenttool.New(oa.plantAgent, nil)
	deviceTool := agenttool.New(oa.deviceAgent, nil)
	supportTool := agenttool.New(oa.supportAgent, nil)

	agentTools := []tool.Tool{plantTool, deviceTool, supportTool}

	orchestrator, err := llmagent.New(llmagent.Config{
		Name:        "zefir_orchestrator",
		Model:       model,
		Description: "Router for Zefir support",
		Instruction: getOrchestratorPrompt(),
		Tools:       agentTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.1),
			MaxOutputTokens: 30,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created Orchestrator with %d agent tools", len(agentTools))
	return orchestrator, nil
}

// GetOrchestrator returns the main agent
func (oa *OrchestratorAgent) GetOrchestrator() agent.Agent {
	return oa.orchestrator
}

// ProcessMessage processes a message through the multi-agent system
func (oa *OrchestratorAgent) ProcessMessage(ctx context.Context, sessionID, userMessage string, clientLang string) (string, error) {
	log.Printf("[MULTI-AGENT] Processing message for session %s", sessionID)

	// Create or get session
	userID := "zefir_" + oa.zefirUserID
	if oa.zefirUserID == "" {
		userID = "anonymous_" + sessionID[:8]
	}

	existingSession, err := oa.sessionService.Get(ctx, &session.GetRequest{
		AppName:   "zefir_support",
		UserID:    userID,
		SessionID: sessionID,
	})

	if err != nil || existingSession == nil {
		_, err = oa.sessionService.Create(ctx, &session.CreateRequest{
			AppName:   "zefir_support",
			UserID:    userID,
			SessionID: sessionID,
			State: map[string]any{
				"zefir_user_id": oa.zefirUserID,
				"client_lang":   clientLang,
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create session: %w", err)
		}
		log.Printf("[MULTI-AGENT] Created new session %s for user %s", sessionID, userID)
	}

	// Prepare message with language context
	messageContent := userMessage
	if clientLang != "" {
		messageContent = fmt.Sprintf("[Client language: %s]\n%s", clientLang, userMessage)
	}

	// Run agent
	const maxToolCalls = 15 // safety limit for multi-agent (higher since orchestrator delegates)
	userContent := genai.NewContentFromText(messageContent, genai.RoleUser)

	var responseText strings.Builder
	var lastError error
	toolCallsCount := 0

	eventCh := oa.runner.Run(ctx, userID, sessionID, userContent, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone,
	})

	for event, err := range eventCh {
		if err != nil {
			lastError = err
			log.Printf("[MULTI-AGENT] Event error: %v", err)
			continue
		}

		if event == nil || event.LLMResponse.Content == nil {
			continue
		}

		for _, part := range event.LLMResponse.Content.Parts {
			if part.Text != "" {
				responseText.WriteString(part.Text)
			}
			if part.FunctionCall != nil {
				toolCallsCount++
				log.Printf("[MULTI-AGENT] Tool called: %s (#%d)", part.FunctionCall.Name, toolCallsCount)
			}
		}

		if event.Author != "" {
			log.Printf("[MULTI-AGENT] Event from: %s", event.Author)
		}

		if toolCallsCount >= maxToolCalls {
			log.Printf("[MULTI-AGENT] Tool call limit reached (%d), stopping", maxToolCalls)
			break
		}
	}

	if responseText.Len() == 0 && lastError != nil {
		return "", fmt.Errorf("agent error: %w", lastError)
	}

	response := strings.TrimSpace(responseText.String())
	response = cleanThinkingTags(response)

	if response == "" {
		response = "Sorry, an error occurred. Please try rephrasing your question."
	}

	log.Printf("[MULTI-AGENT] Response length: %d chars", len(response))
	return response, nil
}

// cleanThinkingTags removes <think>...</think> tags from response
func cleanThinkingTags(text string) string {
	for {
		startIdx := strings.Index(text, "<think>")
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(text, "</think>")
		if endIdx == -1 {
			text = strings.TrimSpace(text[:startIdx])
			break
		}
		text = text[:startIdx] + text[endIdx+len("</think>"):]
	}
	return strings.TrimSpace(text)
}

// IsEscalationNeeded checks if escalation is needed
func (oa *OrchestratorAgent) IsEscalationNeeded(response string) bool {
	return strings.Contains(response, "#escalate")
}

// ============================================================================
// SPECIALIZED PROMPTS FOR EACH AGENT
// ============================================================================

func getOrchestratorPrompt() string {
	return `ROUTER. Call exactly one tool per message. Never write text.
- plant/species/humidity/care → plant_expert
- sensor/device/setup/mesh/troubleshoot → device_specialist
- everything else → support_specialist`
}

func getPlantAgentPrompt() string {
	return `Plant expert for Zefir (89 species database). ALWAYS call a tool before answering. Never guess data.
SCOPE: Only plant questions. Refuse other topics.
If not found: say so, suggest alternatives. Match customer's language. Under 150 words.`
}

func getDeviceAgentPrompt() string {
	return `Device specialist for Zefir IoT sensors. ALWAYS call a tool before answering. Never guess data.
SCOPE: Only device/sensor questions. Refuse other topics.
Privacy: never show full MAC addresses or user IDs.
Sensor validation: moisture >100%/<0% = malfunction, temp >60C/<-20C = abnormal.
If error: say so, suggest support@zefir.app. Match customer's language. Under 150 words.`
}

func getSupportAgentPrompt() string {
	return `Support specialist for Zefir. ALWAYS call a tool before answering. Never guess data.
SCOPE: Only Zefir questions (app, FAQ, contacts, features, security). Off-topic: "I can only help with Zefir."
Tone: friendly, concise. Acknowledge frustration. #escalate for: defects, refunds, angry customer.
If not found: say so, suggest support@zefir.app. Match customer's language. Under 150 words.`
}
