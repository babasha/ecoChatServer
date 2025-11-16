package adkagent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/llm"
)

// SupportAgent - AI-агент поддержки клиентов с 19 специализированными инструментами
// Реализует интерфейс UserIDProvider для передачи в tools
type SupportAgent struct {
	agent          agent.Agent
	runner         *runner.Runner
	sessionService session.Service
	storeClient    *llm.StoreClient
	isAuthorized   bool
	appName        string
	userID         int
	rateLimiter    *RateLimiter
}

// GetUserID реализует интерфейс UserIDProvider
func (sa *SupportAgent) GetUserID() int {
	return sa.userID
}

// NewSupportAgent создаёт агента с полным набором из 19 инструментов
func NewSupportAgent(ctx context.Context, storeClient *llm.StoreClient, isAuthorized bool) (*SupportAgent, error) {
	// 1. Создаём LLM модель
	llmModel, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM model: %w", err)
	}

	// 2. Создаём переменную для userIDProvider (будет заполнена после создания агента)
	var userIDProvider UserIDProvider

	// 3. Создаём все tools из разных категорий
	var allTools []tool.Tool

	// Product Tools
	productTools, err := CreateProductTools(storeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create product tools: %w", err)
	}
	allTools = append(allTools, productTools...)
	log.Printf("[AGENT] Added %d product tools", len(productTools))

	// Order Tools (требуют userIDProvider для доступа к userID)
	orderTools, err := CreateOrderTools(storeClient, &userIDProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to create order tools: %w", err)
	}
	allTools = append(allTools, orderTools...)
	log.Printf("[AGENT] Added %d order tools", len(orderTools))

	// Support Tools
	supportTools, err := CreateSupportTools(storeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create support tools: %w", err)
	}
	allTools = append(allTools, supportTools...)
	log.Printf("[AGENT] Added %d support tools", len(supportTools))

	// 4. Выбираем промпт
	var systemPrompt string
	if isAuthorized {
		systemPrompt = getEnhancedAuthorizedPrompt()
		log.Printf("[AGENT] Создаём АВТОРИЗОВАННОГО агента с %d tools", len(allTools))
	} else {
		systemPrompt = getEnhancedUnauthorizedPrompt()
		log.Printf("[AGENT] Создаём НЕАВТОРИЗОВАННОГО агента с %d tools", len(allTools))
	}

	// 5. Создаём агента
	adkAgent, err := llmagent.New(llmagent.Config{
		Name:        "enddel_support",
		Model:       llmModel,
		Description: "AI-powered assistant for Enddel online grocery delivery service with comprehensive product, order, and support capabilities",
		Instruction: systemPrompt,
		Tools:       allTools,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// 6. Создаём session service
	sessionService := session.InMemoryService()

	// 7. Создаём runner
	appName := "ecochat"
	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          adkAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	log.Printf("[AGENT] ✅ Агент создан с %d инструментами", len(allTools))
	log.Printf("[AGENT] 📊 Breakdown: Products=%d, Orders=%d, Support=%d",
		len(productTools), len(orderTools), len(supportTools))

	// 8. Создаём rate limiter
	rateLimiter := NewGeminiFreeTierLimiter()

	sa := &SupportAgent{
		agent:          adkAgent,
		runner:         r,
		sessionService: sessionService,
		storeClient:    storeClient,
		isAuthorized:   isAuthorized,
		appName:        appName,
		userID:         0,
		rateLimiter:    rateLimiter,
	}

	// Заполняем userIDProvider - SupportAgent реализует UserIDProvider
	userIDProvider = sa

	return sa, nil
}

// ProcessMessage обрабатывает сообщение через ADK runner
// clientLanguage - опциональный параметр языка клиента (ru, en, ka и т.д.)
func (sa *SupportAgent) ProcessMessage(ctx context.Context, sessionID, message string, storeUserID int, clientLanguage ...string) (string, error) {
	log.Printf("[AGENT] ProcessMessage: sessionID=%s, storeUserID=%d, message=%s", sessionID, storeUserID, truncate(message, 50))

	// Проверяем rate limiter
	if sa.rateLimiter != nil && !sa.rateLimiter.AllowRequest() {
		rpm, rpd, maxRPM, maxRPD := sa.rateLimiter.GetStats()
		log.Printf("[AGENT] ⚠️ Rate limit exceeded: RPM=%d/%d, RPD=%d/%d", rpm, maxRPM, rpd, maxRPD)
		return fmt.Sprintf("Извините, достигнут лимит запросов к AI. RPM: %d/%d, RPD: %d/%d. Пожалуйста, попробуйте позже.",
			rpm, maxRPM, rpd, maxRPD), nil
	}

	// Сохраняем userID для использования в tools
	sa.userID = storeUserID

	// 1. Создаём или получаем сессию
	sessionResp, err := sa.sessionService.Create(ctx, &session.CreateRequest{
		AppName: sa.appName,
		UserID:  sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	// 2. Создаём user content с инструкцией по языку
	userMessage := message
	if len(clientLanguage) > 0 && clientLanguage[0] != "" {
		lang := clientLanguage[0]
		var langInstruction string
		switch lang {
		case "ru":
			langInstruction = "[IMPORTANT: Respond ONLY in Russian language]"
		case "en":
			langInstruction = "[IMPORTANT: Respond ONLY in English language]"
		case "ka", "ge":
			langInstruction = "[IMPORTANT: Respond ONLY in Georgian language]"
		default:
			langInstruction = fmt.Sprintf("[IMPORTANT: Respond ONLY in %s language]", lang)
		}
		userMessage = langInstruction + "\n\n" + message
		log.Printf("[AGENT] Client language detected: %s, added instruction", lang)
	}

	userMsg := genai.NewContentFromText(userMessage, genai.RoleUser)

	// 3. Запускаем агента
	var response strings.Builder
	toolCallsCount := 0

	for event, err := range sa.runner.Run(ctx, sessionID, sessionResp.Session.ID(), userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone,
	}) {
		if err != nil {
			log.Printf("[AGENT] Error during run: %v", err)
			return "", fmt.Errorf("agent run error: %w", err)
		}

		if event.LLMResponse.Content != nil {
			for _, part := range event.LLMResponse.Content.Parts {
				response.WriteString(part.Text)

				// Логируем вызовы tools
				if part.FunctionCall != nil {
					toolCallsCount++
					log.Printf("[AGENT] 🔧 Tool called: %s", part.FunctionCall.Name)
				}
			}
		}
	}

	result := response.String()
	log.Printf("[AGENT] ✅ Response generated (%d chars, %d tool calls)", len(result), toolCallsCount)
	log.Printf("[AGENT] Preview: %s", truncate(result, 100))

	return result, nil
}

// IsEscalationNeeded проверяет нужна ли эскалация
func (sa *SupportAgent) IsEscalationNeeded(response string) bool {
	return strings.Contains(response, "#escalate")
}

// ============================================================================
// Улучшенные промпты для production
// ============================================================================

func getEnhancedUnauthorizedPrompt() string {
	return `You are a friendly AI assistant for "Enddel" - an online grocery delivery service in Tbilisi, Georgia.

🎯 YOUR ROLE:
- Help customers find products in our store
- Answer questions about delivery, payment, and store policies
- Provide excellent customer service
- Use tools to search our actual inventory

🛠️ AVAILABLE TOOLS:

PRODUCTS (8 tools):
- get_products: Browse product catalog
- search_product: Find specific products
- get_categories: View all categories
- check_product_availability: Check stock status
- compare_products: Compare multiple items
- recommend_products: Get smart recommendations
- find_alternatives: Find similar products
- get_products_by_category: Browse by category

SUPPORT (5 tools):
- get_store_info: Delivery, payment, hours
- inspect_website_page: Website help
- search_faq: Search knowledge base
- get_contact_info: Contact details
- check_service_status: System status

⚠️ CRITICAL RULES:
1. ALWAYS use tools to get real product data - NEVER make up information
2. If customer asks about products, use get_products or search_product
3. If asked to compare, use compare_products
4. If asked for recommendations, use recommend_products
5. For general info, use get_store_info or search_faq

🎨 TONE & STYLE:
- Friendly and conversational
- Be concise but helpful
- Use emojis sparingly

🚨 ESCALATION (#escalate when):
- Customer asks if you're a bot
- Complaints about quality/delivery
- Refund requests
- Customer is upset or frustrated
- You're unsure how to help

📝 EXAMPLES:
User: "What fruits do you have?"
You: [Use get_products with query "fruit"]

User: "Compare apples and oranges"
You: [Use compare_products with both items]

User: "Recommend something for breakfast"
You: [Use recommend_products with context "breakfast"]

Respond naturally and always prioritize using tools for accurate information!`
}

func getEnhancedAuthorizedPrompt() string {
	return getEnhancedUnauthorizedPrompt() + `

🔐 AUTHORIZED USER FEATURES:

ORDERS (6 additional tools):
- get_user_orders: View order history
- get_order_status: Check order status
- track_order: Real-time tracking
- get_orders_by_status: Filter by status
- get_recent_orders: Last N orders
- report_delivery_issue: Report problems

SECURITY:
- User is logged in - can access personal data
- ONLY show current user's orders
- Verify user owns order before showing details

Use order tools when customer asks about "my orders", "where is my delivery", "order history", etc.`
}
