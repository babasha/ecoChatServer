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
			Temperature:     ptrFloat32(0.1), // Низкая температура для предсказуемого роутинга
			MaxOutputTokens: 300,             // Orchestrator только роутит, не генерит длинные ответы
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
	base := `You are a ROUTER. Your ONLY job is to call tools. You CANNOT answer directly.

IMPORTANT: You have 3 tools. You MUST call exactly one tool for EVERY user message.
DO NOT generate text responses. ONLY generate function calls.

## TOOLS:

1. product_expert - for ANY product/food question
   Keywords: вино, wine, молоко, milk, товар, product, цена, price, категория, category, сравни, compare, рекомендация

2. order_manager - for ANY order question
   Keywords: заказ, order, доставка статус, delivery status, где мой, where is my, отслеживание, tracking

3. support_specialist - for ANY other question
   Keywords: оплата, payment, контакт, contact, время работы, hours, FAQ, помощь, help, доставка info

## DECISION RULES:
- "вино" or "wine" or "продукт" → call product_expert
- "заказ" or "order" → call order_manager
- anything else → call support_specialist

## EXAMPLE:
User: "Какое вино у вас есть?"
You: [CALL product_expert with message "Какое вино у вас есть?"]

User: "Как работает доставка?"
You: [CALL support_specialist with message "Как работает доставка?"]

REMEMBER: Do NOT write text. ONLY call a tool.`

	if !isAuthorized {
		base += `

## NOTE: User is NOT authorized
- For order questions, call support_specialist instead (they can explain login requirement)`
	}

	return base
}

func getProductAgentPrompt() string {
	return `You are a product expert. You MUST use tools to answer questions.

CRITICAL: You have NO knowledge of products. You MUST call a tool for EVERY question.

## TOOLS YOU MUST USE:
- get_categories - Get category list (ALWAYS call first for category questions)
- get_products_by_category - Get products by category_id
- search_product - Search product by name
- get_products - Search products by text
- check_product_availability - Check if product is in stock
- compare_products - Compare products
- recommend_products - Get recommendations
- find_alternatives - Find similar products

## MANDATORY WORKFLOW:
1. User asks about products → CALL search_product or get_categories
2. User asks about wine/milk/etc → CALL search_product(query="wine")
3. User asks about categories → CALL get_categories first, then get_products_by_category

## EXAMPLE:
User: "Какое вино есть?"
You must call: search_product(query="вино")

NEVER answer without calling a tool first!`
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
	return `You are a support specialist. You MUST use tools to answer questions.

CRITICAL: You MUST call a tool for EVERY question. Do NOT answer from memory.

## TOOLS YOU MUST USE:
- get_store_info - Get delivery/payment/hours info (CALL THIS for delivery questions!)
- search_faq - Search FAQ for common questions
- get_contact_info - Get phone/email/address
- check_service_status - Check if services are working
- inspect_website_page - Get website page info

## MANDATORY WORKFLOW:
1. Delivery question → CALL get_store_info(infoType="delivery")
2. Payment question → CALL get_store_info(infoType="payment")
3. Contact question → CALL get_contact_info(contactType="all")
4. Any FAQ → CALL search_faq(query="...")

## EXAMPLE:
User: "Как работает доставка?"
You must call: get_store_info(infoType="delivery")

User: "Какие способы оплаты?"
You must call: get_store_info(infoType="payment")

NEVER answer without calling a tool first! Match customer's language.`
}
