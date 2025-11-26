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
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/agenttool"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/llm"
)

// ============================================================================
// MULTI-AGENT ARCHITECTURE
// Orchestrator → [ProductAgent, OrderAgent, SupportAgent]
// ============================================================================

// MultiAgentConfig конфигурация мульти-агентной системы
type MultiAgentConfig struct {
	StoreClient  *llm.StoreClient
	IsAuthorized bool
	UserID       int
}

// OrchestratorAgent - главный агент который делегирует задачи специализированным агентам
type OrchestratorAgent struct {
	orchestrator   agent.Agent
	productAgent   agent.Agent
	orderAgent     agent.Agent
	supportAgent   agent.Agent
	llmModel       model.LLM
	storeClient    *llm.StoreClient
	isAuthorized   bool
	userID         int
	userIDProvider *UserIDProvider

	// Runner и сервисы для запуска агента
	runner         *runner.Runner
	sessionService session.Service
	memoryService  memory.Service
}

// NewOrchestratorAgent создаёт мульти-агентную систему
func NewOrchestratorAgent(ctx context.Context, cfg MultiAgentConfig) (*OrchestratorAgent, error) {
	// 1. Создаём LLM модель
	llmModel, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM model: %w", err)
	}

	oa := &OrchestratorAgent{
		llmModel:     llmModel,
		storeClient:  cfg.StoreClient,
		isAuthorized: cfg.IsAuthorized,
		userID:       cfg.UserID,
	}

	// Указатель на провайдер userID
	var userIDProvider UserIDProvider = oa
	oa.userIDProvider = &userIDProvider

	// 2. Создаём специализированные агенты
	productAgent, err := oa.createProductAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create product agent: %w", err)
	}
	oa.productAgent = productAgent

	orderAgent, err := oa.createOrderAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create order agent: %w", err)
	}
	oa.orderAgent = orderAgent

	supportAgent, err := oa.createSupportAgent(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create support agent: %w", err)
	}
	oa.supportAgent = supportAgent

	// 3. Создаём Orchestrator с агентами как tools
	orchestrator, err := oa.createOrchestrator(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}
	oa.orchestrator = orchestrator

	// 4. Создаём сервисы и runner
	oa.sessionService = session.InMemoryService()
	oa.memoryService = memory.InMemoryService()

	agentRunner, err := runner.New(runner.Config{
		AppName:         "enddel_support",
		Agent:           orchestrator,
		SessionService:  oa.sessionService,
		ArtifactService: artifact.InMemoryService(),
		MemoryService:   oa.memoryService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}
	oa.runner = agentRunner

	log.Printf("[MULTI-AGENT] ✅ Orchestrator создан с 3 специализированными агентами и runner")
	return oa, nil
}

// GetUserID реализует интерфейс UserIDProvider
func (oa *OrchestratorAgent) GetUserID() int {
	return oa.userID
}

// SetUserID устанавливает userID для текущей сессии
func (oa *OrchestratorAgent) SetUserID(userID int) {
	oa.userID = userID
}

// createProductAgent создаёт агента для работы с продуктами
func (oa *OrchestratorAgent) createProductAgent(ctx context.Context) (agent.Agent, error) {
	// Создаём tools для продуктов
	productTools, err := CreateProductTools(oa.storeClient)
	if err != nil {
		return nil, err
	}

	productAgent, err := llmagent.New(llmagent.Config{
		Name:        "product_expert",
		Model:       oa.llmModel,
		Description: "Expert in products, categories, search, recommendations. Call this agent when customer asks about products, categories, prices, availability, or needs product recommendations.",
		Instruction: getProductAgentPrompt(),
		Tools:       productTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.3),
			MaxOutputTokens: 600,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created ProductAgent with %d tools", len(productTools))
	return productAgent, nil
}

// createOrderAgent создаёт агента для работы с заказами
func (oa *OrchestratorAgent) createOrderAgent(ctx context.Context) (agent.Agent, error) {
	// Создаём tools для заказов
	orderTools, err := CreateOrderTools(oa.storeClient, oa.userIDProvider)
	if err != nil {
		return nil, err
	}

	orderAgent, err := llmagent.New(llmagent.Config{
		Name:        "order_manager",
		Model:       oa.llmModel,
		Description: "Expert in orders, tracking, delivery status. Call this agent when customer asks about their orders, order status, delivery tracking, or has issues with orders. ONLY for authorized users.",
		Instruction: getOrderAgentPrompt(oa.isAuthorized),
		Tools:       orderTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.2),
			MaxOutputTokens: 600,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created OrderAgent with %d tools (authorized=%v)", len(orderTools), oa.isAuthorized)
	return orderAgent, nil
}

// createSupportAgent создаёт агента для поддержки
func (oa *OrchestratorAgent) createSupportAgent(ctx context.Context) (agent.Agent, error) {
	// Создаём tools для поддержки
	supportTools, err := CreateSupportTools(oa.storeClient)
	if err != nil {
		return nil, err
	}

	supportAgent, err := llmagent.New(llmagent.Config{
		Name:        "support_specialist",
		Model:       oa.llmModel,
		Description: "Expert in store policies, FAQ, contacts, delivery info. Call this agent when customer asks about store information, delivery, payment, returns, contacts, or general questions.",
		Instruction: getSupportAgentPrompt(),
		Tools:       supportTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.3),
			MaxOutputTokens: 500,
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created SupportAgent with %d tools", len(supportTools))
	return supportAgent, nil
}

// createOrchestrator создаёт главный агент-оркестратор
func (oa *OrchestratorAgent) createOrchestrator(ctx context.Context) (agent.Agent, error) {
	// Создаём agent tools - каждый агент становится callable tool
	productTool := agenttool.New(oa.productAgent, nil)
	orderTool := agenttool.New(oa.orderAgent, nil)
	supportTool := agenttool.New(oa.supportAgent, nil)

	agentTools := []tool.Tool{productTool, orderTool, supportTool}

	orchestrator, err := llmagent.New(llmagent.Config{
		Name:        "enddel_orchestrator",
		Model:       oa.llmModel,
		Description: "Main orchestrator for Enddel grocery delivery support",
		Instruction: getOrchestratorPrompt(oa.isAuthorized),
		Tools:       agentTools,
		GenerateContentConfig: &genai.GenerateContentConfig{
			Temperature:     ptrFloat32(0.2),
			MaxOutputTokens: 300, // Orchestrator только роутит, не генерит длинные ответы
		},
	})
	if err != nil {
		return nil, err
	}

	log.Printf("[MULTI-AGENT] Created Orchestrator with %d agent tools", len(agentTools))
	return orchestrator, nil
}

// GetOrchestrator возвращает главный агент
func (oa *OrchestratorAgent) GetOrchestrator() agent.Agent {
	return oa.orchestrator
}

// ProcessMessage обрабатывает сообщение через мульти-агентную систему
func (oa *OrchestratorAgent) ProcessMessage(ctx context.Context, sessionID, userMessage string, clientLang string) (string, error) {
	log.Printf("[MULTI-AGENT] Processing message for session %s", sessionID)

	// Создаём или получаем сессию
	userID := fmt.Sprintf("user_%d", oa.userID)
	if oa.userID == 0 {
		userID = "anonymous_" + sessionID[:8]
	}

	// Проверяем существует ли сессия
	existingSession, err := oa.sessionService.Get(ctx, &session.GetRequest{
		AppName:   "enddel_support",
		UserID:    userID,
		SessionID: sessionID,
	})

	if err != nil || existingSession == nil {
		// Создаём новую сессию
		_, err = oa.sessionService.Create(ctx, &session.CreateRequest{
			AppName:   "enddel_support",
			UserID:    userID,
			SessionID: sessionID,
			State: map[string]any{
				"is_authorized": oa.isAuthorized,
				"user_id":       oa.userID,
				"client_lang":   clientLang,
			},
		})
		if err != nil {
			return "", fmt.Errorf("failed to create session: %w", err)
		}
		log.Printf("[MULTI-AGENT] Created new session %s for user %s", sessionID, userID)
	}

	// Подготавливаем сообщение с контекстом языка
	messageContent := userMessage
	if clientLang != "" {
		messageContent = fmt.Sprintf("[Client language: %s]\n%s", clientLang, userMessage)
	}

	// Запускаем агента
	userContent := genai.NewContentFromText(messageContent, genai.RoleUser)

	var responseText strings.Builder
	var lastError error

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

		// Извлекаем текст из ответа
		for _, part := range event.LLMResponse.Content.Parts {
			if part.Text != "" {
				responseText.WriteString(part.Text)
			}
		}

		// Логируем автора события
		if event.Author != "" {
			log.Printf("[MULTI-AGENT] Event from: %s", event.Author)
		}
	}

	if responseText.Len() == 0 && lastError != nil {
		return "", fmt.Errorf("agent error: %w", lastError)
	}

	response := strings.TrimSpace(responseText.String())

	// Удаляем <think>...</think> теги (некоторые модели их генерируют)
	response = cleanThinkingTags(response)

	if response == "" {
		response = "Извините, произошла ошибка. Попробуйте переформулировать вопрос."
	}

	log.Printf("[MULTI-AGENT] Response length: %d chars", len(response))
	return response, nil
}

// cleanThinkingTags удаляет <think>...</think> теги из ответа
func cleanThinkingTags(text string) string {
	// Удаляем <think>...</think> блоки (DeepSeek, Qwen и др.)
	for {
		startIdx := strings.Index(text, "<think>")
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(text, "</think>")
		if endIdx == -1 {
			// Если нет закрывающего тега, удаляем всё после <think>
			text = strings.TrimSpace(text[:startIdx])
			break
		}
		// Удаляем весь блок <think>...</think>
		text = text[:startIdx] + text[endIdx+len("</think>"):]
	}
	return strings.TrimSpace(text)
}

// IsEscalationNeeded проверяет нужна ли эскалация
func (oa *OrchestratorAgent) IsEscalationNeeded(response string) bool {
	return strings.Contains(response, "#escalate")
}

// ============================================================================
// SPECIALIZED PROMPTS FOR EACH AGENT
// ============================================================================

func getOrchestratorPrompt(isAuthorized bool) string {
	base := `You are the router for Enddel grocery delivery support.
You MUST call one of the available tools to handle customer requests.

## AVAILABLE TOOLS (you MUST use them):

1. product_expert - Call for: wine, food, products, categories, prices, search, recommendations
   Example: "Какое вино есть?" → call product_expert

2. order_manager - Call for: orders, tracking, delivery status, order issues
   Example: "Где мой заказ?" → call order_manager

3. support_specialist - Call for: delivery info, payment, contacts, FAQ, store policies
   Example: "Как работает доставка?" → call support_specialist

## CRITICAL RULES:
1. You MUST call a tool - NEVER answer directly
2. Always call exactly ONE tool per request
3. Pass the customer's message to the tool
4. Match customer's language`

	if !isAuthorized {
		base += `

## NOTE: User is NOT authorized
- For order questions, call support_specialist instead (they can explain login requirement)`
	}

	return base
}

func getProductAgentPrompt() string {
	return `You are a product expert for Enddel grocery delivery.

## YOUR EXPERTISE
- Finding products by name or category
- Product recommendations
- Price and availability information
- Comparing products
- Finding alternatives

## WORKFLOW FOR CATEGORIES
1. get_categories → find category_id
2. get_products_by_category(category_id=X)

## WORKFLOW FOR SPECIFIC PRODUCT
→ search_product(query="...")

## RULES
- NEVER invent products - always use tools first
- If tools return empty, say "not found"
- Match customer's language
- Be concise and helpful`
}

func getOrderAgentPrompt(isAuthorized bool) string {
	if !isAuthorized {
		return `You are the order manager but this user is NOT authorized.
Always respond: "Please log in to view your orders and order status."`
	}

	return `You are an order manager for Enddel grocery delivery.

## YOUR EXPERTISE
- Showing user's orders
- Tracking order status
- Providing delivery updates
- Handling order issues

## AVAILABLE TOOLS
- get_user_orders - show all orders
- get_recent_orders - last N orders
- get_order_status - specific order status
- track_order - detailed tracking
- get_orders_by_status - filter by status
- report_delivery_issue - report problems

## RULES
- Only show THIS user's orders
- Never reveal other users' data
- Match customer's language
- Be empathetic with issues`
}

func getSupportAgentPrompt() string {
	return `You are a support specialist for Enddel grocery delivery.

## YOUR EXPERTISE
- Store policies and FAQ
- Delivery information
- Payment methods
- Contact information
- Service status

## AVAILABLE TOOLS
- get_store_info - delivery, payment, hours, location
- search_faq - find answers to common questions
- get_contact_info - phone, email, social media
- check_service_status - website/app status

## RULES
- Be helpful and friendly
- Match customer's language
- For complex issues, escalate to human with #escalate
- Provide accurate information from tools`
}
