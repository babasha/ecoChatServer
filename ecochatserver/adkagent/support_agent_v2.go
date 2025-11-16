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
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"

	"github.com/egor/ecochatserver/llm"
)

// SupportAgentV2 - упрощённая версия агента с правильным ADK API
type SupportAgentV2 struct {
	agent          agent.Agent
	runner         *runner.Runner
	sessionService session.Service
	storeClient    *llm.StoreClient
	isAuthorized   bool
	appName        string
	userID         int          // User ID для авторизованных пользователей
	rateLimiter    *RateLimiter // Rate limiter для защиты от превышения Gemini free tier
}

// NewSupportAgentV2 создаёт агента с правильным API
func NewSupportAgentV2(ctx context.Context, storeClient *llm.StoreClient, isAuthorized bool) (*SupportAgentV2, error) {
	// 1. Создаём LLM модель (поддерживает hot-swap через БД)
	llmModel, err := NewLLMModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM model: %w", err)
	}

	// 2. Создаём временную ссылку на агента для передачи в tools
	// Будет заполнена после создания агента
	var agentRef *SupportAgentV2

	// 2. Создаём tools
	tools, err := createTools(storeClient, &agentRef)
	if err != nil {
		return nil, fmt.Errorf("failed to create tools: %w", err)
	}

	// 3. Выбираем промпт
	var systemPrompt string
	if isAuthorized {
		systemPrompt = getAuthorizedPromptV2()
		log.Printf("[AGENT_V2] Создаём АВТОРИЗОВАННОГО агента")
	} else {
		systemPrompt = getUnauthorizedPromptV2()
		log.Printf("[AGENT_V2] Создаём НЕАВТОРИЗОВАННОГО агента")
	}

	// 4. Создаём агента
	adkAgent, err := llmagent.New(llmagent.Config{
		Name:        "enddel_support",
		Model:       llmModel,
		Description: "AI assistant for Enddel online grocery delivery service",
		Instruction: systemPrompt,
		Tools:       tools,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	// 5. Создаём session service
	sessionService := session.InMemoryService()

	// 6. Создаём runner
	appName := "ecochat"
	r, err := runner.New(runner.Config{
		AppName:        appName,
		Agent:          adkAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	log.Printf("[AGENT_V2] Агент успешно создан с %d tools", len(tools))

	// 7. Создаём rate limiter для защиты от превышения Gemini free tier
	rateLimiter := NewGeminiFreeTierLimiter()
	log.Printf("[AGENT_V2] Rate limiter создан: 10 RPM, 250 RPD (Gemini Free Tier)")

	sa := &SupportAgentV2{
		agent:          adkAgent,
		runner:         r,
		sessionService: sessionService,
		storeClient:    storeClient,
		isAuthorized:   isAuthorized,
		appName:        appName,
		userID:         0, // Будет установлен в ProcessMessage
		rateLimiter:    rateLimiter,
	}

	// Заполняем ссылку на агента для tools
	agentRef = sa

	return sa, nil
}

// ProcessMessage обрабатывает сообщение через ADK runner
func (sa *SupportAgentV2) ProcessMessage(ctx context.Context, sessionID, message string, storeUserID int) (string, error) {
	log.Printf("[AGENT_V2] ProcessMessage: sessionID=%s, storeUserID=%d, message=%s", sessionID, storeUserID, truncate(message, 50))

	// Проверяем rate limiter ПЕРЕД запросом к LLM
	if sa.rateLimiter != nil && !sa.rateLimiter.AllowRequest() {
		// Получаем текущую статистику
		rpm, rpd, maxRPM, maxRPD := sa.rateLimiter.GetStats()
		log.Printf("[AGENT_V2] ⚠️ Rate limit exceeded: RPM=%d/%d, RPD=%d/%d", rpm, maxRPM, rpd, maxRPD)

		return fmt.Sprintf("Извините, достигнут лимит запросов к AI. RPM: %d/%d, RPD: %d/%d. Пожалуйста, попробуйте позже.",
			rpm, maxRPM, rpd, maxRPD), nil
	}

	// Сохраняем userID для использования в tools
	sa.userID = storeUserID

	// 1. Создаём или получаем сессию для пользователя
	sessionResp, err := sa.sessionService.Create(ctx, &session.CreateRequest{
		AppName: sa.appName,
		UserID:  sessionID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	// 2. Создаём user content
	userMsg := genai.NewContentFromText(message, genai.RoleUser)

	// 3. Запускаем агента через runner
	var response strings.Builder
	for event, err := range sa.runner.Run(ctx, sessionID, sessionResp.Session.ID(), userMsg, agent.RunConfig{
		StreamingMode: agent.StreamingModeNone, // Без streaming для простоты
	}) {
		if err != nil {
			log.Printf("[AGENT_V2] Error during run: %v", err)
			return "", fmt.Errorf("agent run error: %w", err)
		}

		if event.LLMResponse.Content != nil {
			for _, part := range event.LLMResponse.Content.Parts {
				response.WriteString(part.Text)
			}
		}
	}

	result := response.String()
	log.Printf("[AGENT_V2] Агент вернул: %s", truncate(result, 100))

	return result, nil
}

// IsEscalationNeeded проверяет нужна ли эскалация
func (sa *SupportAgentV2) IsEscalationNeeded(response string) bool {
	return strings.Contains(response, "#escalate")
}

// createTools создаёт набор инструментов для агента
func createTools(storeClient *llm.StoreClient, agentRef **SupportAgentV2) ([]tool.Tool, error) {
	var tools []tool.Tool

	// Tool 1: Get Products
	type GetProductsInput struct {
		SearchQuery string `json:"searchQuery"`
	}
	type ProductsOutput struct {
		Result string `json:"result"`
	}

	getProductsTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_products",
			Description: "Get list of products from store. Use when customer asks about products, catalog, assortment.",
		},
		func(ctx tool.Context, input GetProductsInput) (ProductsOutput, error) {
			log.Printf("[TOOL] get_products called: query=%s", input.SearchQuery)

			products, err := storeClient.GetAllProducts(ctx, input.SearchQuery)
			if err != nil {
				return ProductsOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return ProductsOutput{Result: "No products found."}, nil
			}

			if len(products) > 15 {
				products = products[:15]
			}

			result := llm.FormatProductsList(products)
			return ProductsOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_products tool: %w", err)
	}
	tools = append(tools, getProductsTool)

	// Tool 2: Get Store Info
	type GetStoreInfoInput struct {
		InfoType string `json:"infoType"`
	}
	type StoreInfoOutput struct {
		Info string `json:"info"`
	}

	getStoreInfoTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_store_info",
			Description: "Get store information: delivery cost, working hours, payment methods, etc.",
		},
		func(ctx tool.Context, input GetStoreInfoInput) (StoreInfoOutput, error) {
			log.Printf("[TOOL] get_store_info called: type=%s", input.InfoType)

			infoType := input.InfoType
			if infoType == "" {
				infoType = "all"
			}

			result := "📋 Enddel Store Information\n\n"

			if infoType == "all" || infoType == "delivery" {
				result += "🚚 DELIVERY:\n"
				result += "  • Cost: 3₾ for orders < 50₾, FREE for 50₾+\n"
				result += "  • Time: 30-60 minutes\n"
				result += "  • Hours: 8:00-23:00 daily\n\n"
			}

			if infoType == "all" || infoType == "payment" {
				result += "💳 PAYMENT: Card, Cash, Google Pay, Telegram Pay\n\n"
			}

			if infoType == "all" || infoType == "hours" {
				result += "🕐 WORKING HOURS: 8:00-23:00 daily\n"
			}

			return StoreInfoOutput{Info: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_store_info tool: %w", err)
	}
	tools = append(tools, getStoreInfoTool)

	// Tool 3: Get Categories
	type GetCategoriesInput struct {
		// No input needed
	}
	type CategoriesOutput struct {
		Result string `json:"result"`
	}

	getCategoriesTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_categories",
			Description: "Get list of all product categories available in the store. Use when customer asks about product categories or wants to browse by category.",
		},
		func(ctx tool.Context, input GetCategoriesInput) (CategoriesOutput, error) {
			log.Printf("[TOOL] get_categories called")

			categories, err := storeClient.GetAllCategories(ctx)
			if err != nil {
				return CategoriesOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(categories) == 0 {
				return CategoriesOutput{Result: "No categories found in the store."}, nil
			}

			// Format categories list
			result := fmt.Sprintf("Store has %d product categories:\n", len(categories))
			for i, cat := range categories {
				if i >= 15 { // Limit to 15
					result += fmt.Sprintf("\n... and %d more categories", len(categories)-15)
					break
				}
				result += fmt.Sprintf("• %s\n", cat.NameRu)
			}

			return CategoriesOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_categories tool: %w", err)
	}
	tools = append(tools, getCategoriesTool)

	// Tool 4: Search Product
	type SearchProductInput struct {
		ProductName string `json:"productName"`
	}
	type SearchProductOutput struct {
		Result string `json:"result"`
	}

	searchProductTool, err := functiontool.New(
		functiontool.Config{
			Name:        "search_product",
			Description: "Search for a specific product by name. Use when customer asks about a specific product or wants detailed info about one product.",
		},
		func(ctx tool.Context, input SearchProductInput) (SearchProductOutput, error) {
			log.Printf("[TOOL] search_product called: productName=%s", input.ProductName)

			if input.ProductName == "" {
				return SearchProductOutput{Result: "Error: product name is required"}, nil
			}

			products, err := storeClient.GetAllProducts(ctx, input.ProductName)
			if err != nil {
				return SearchProductOutput{Result: fmt.Sprintf("Error: %v", err)}, nil
			}

			if len(products) == 0 {
				return SearchProductOutput{Result: fmt.Sprintf("Product '%s' not found.", input.ProductName)}, nil
			}

			// If single product found, show detailed info
			if len(products) == 1 {
				result := llm.FormatProductDetails(products[0])
				return SearchProductOutput{Result: result}, nil
			}

			// Multiple products - show list
			if len(products) > 10 {
				products = products[:10]
			}
			result := llm.FormatProductsList(products)
			return SearchProductOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create search_product tool: %w", err)
	}
	tools = append(tools, searchProductTool)

	// Tool 5: Inspect Website Page
	type InspectWebsiteInput struct {
		PagePath string `json:"pagePath"`
	}
	type InspectWebsiteOutput struct {
		Result string `json:"result"`
	}

	inspectWebsiteTool, err := functiontool.New(
		functiontool.Config{
			Name:        "inspect_website_page",
			Description: "Get information from website pages. Use when customer asks about website structure, pages, or UI elements.",
		},
		func(ctx tool.Context, input InspectWebsiteInput) (InspectWebsiteOutput, error) {
			log.Printf("[TOOL] inspect_website_page called: pagePath=%s", input.PagePath)

			// Create website inspector
			inspector := llm.NewWebsiteInspector(storeClient.GetBaseURL())

			pageInfo, err := inspector.InspectPage(ctx, input.PagePath)
			if err != nil {
				log.Printf("[TOOL] Error inspecting website page %s: %v", input.PagePath, err)
				return InspectWebsiteOutput{
					Result: fmt.Sprintf("Error inspecting page %s: %v. The page might not be accessible or doesn't exist.", input.PagePath, err),
				}, nil
			}

			return InspectWebsiteOutput{Result: pageInfo}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create inspect_website_page tool: %w", err)
	}
	tools = append(tools, inspectWebsiteTool)

	// Tool 6: Get User Orders (для авторизованных)
	type GetUserOrdersInput struct {
		Limit int `json:"limit"` // Лимит количества заказов (опционально)
	}
	type UserOrdersOutput struct {
		Result string `json:"result"`
	}

	getUserOrdersTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_user_orders",
			Description: "Get user's order history. ONLY for authorized users. Use when customer asks 'show my orders', 'my order history', etc.",
		},
		func(ctx tool.Context, input GetUserOrdersInput) (UserOrdersOutput, error) {
			log.Printf("[TOOL] get_user_orders called: limit=%d", input.Limit)

			// Проверяем что агент создан и есть userID
			if agentRef == nil || *agentRef == nil {
				return UserOrdersOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*agentRef).userID
			if userID == 0 {
				return UserOrdersOutput{Result: "⚠️ This feature is only available for registered users. Please log in to view your orders."}, nil
			}

			// Получаем заказы
			orders, err := storeClient.GetUserOrders(ctx, userID)
			if err != nil {
				log.Printf("[TOOL] Error getting user orders: %v", err)
				return UserOrdersOutput{Result: fmt.Sprintf("Error retrieving orders: %v", err)}, nil
			}

			if len(orders) == 0 {
				return UserOrdersOutput{Result: "You don't have any orders yet. Start shopping to see your order history!"}, nil
			}

			// Применяем лимит если указан
			if input.Limit > 0 && input.Limit < len(orders) {
				orders = orders[:input.Limit]
			}

			result := llm.FormatOrdersList(orders)
			return UserOrdersOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_user_orders tool: %w", err)
	}
	tools = append(tools, getUserOrdersTool)

	// Tool 7: Get Order Status (для авторизованных)
	type GetOrderStatusInput struct {
		OrderID string `json:"orderId"` // ID заказа
	}
	type OrderStatusOutput struct {
		Result string `json:"result"`
	}

	getOrderStatusTool, err := functiontool.New(
		functiontool.Config{
			Name:        "get_order_status",
			Description: "Get status of a specific order by ID. ONLY for authorized users. Use when customer asks 'where is my order #123', 'order status', etc.",
		},
		func(ctx tool.Context, input GetOrderStatusInput) (OrderStatusOutput, error) {
			log.Printf("[TOOL] get_order_status called: orderID=%s", input.OrderID)

			// Проверяем что агент создан и есть userID
			if agentRef == nil || *agentRef == nil {
				return OrderStatusOutput{Result: "Error: agent not initialized"}, nil
			}

			userID := (*agentRef).userID
			if userID == 0 {
				return OrderStatusOutput{Result: "⚠️ This feature is only available for registered users. Please log in to view your order status."}, nil
			}

			if input.OrderID == "" {
				return OrderStatusOutput{Result: "Error: order ID is required"}, nil
			}

			// Получаем заказ
			order, err := storeClient.GetOrderByID(ctx, input.OrderID, userID)
			if err != nil {
				log.Printf("[TOOL] Error getting order: %v", err)
				return OrderStatusOutput{Result: fmt.Sprintf("Order #%s not found or you don't have access to it.", input.OrderID)}, nil
			}

			result := llm.FormatOrderInfo(order)
			return OrderStatusOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create get_order_status tool: %w", err)
	}
	tools = append(tools, getOrderStatusTool)

	// Tool 8: Check Product Availability
	type CheckProductAvailabilityInput struct {
		ProductSlug string `json:"productSlug"` // Slug продукта
	}
	type ProductAvailabilityOutput struct {
		Result string `json:"result"`
	}

	checkProductAvailabilityTool, err := functiontool.New(
		functiontool.Config{
			Name:        "check_product_availability",
			Description: "Check if a product is available in stock. Use when customer asks 'is this product available', 'in stock', etc.",
		},
		func(ctx tool.Context, input CheckProductAvailabilityInput) (ProductAvailabilityOutput, error) {
			log.Printf("[TOOL] check_product_availability called: slug=%s", input.ProductSlug)

			if input.ProductSlug == "" {
				return ProductAvailabilityOutput{Result: "Error: product slug is required"}, nil
			}

			// Получаем продукт по slug
			product, err := storeClient.GetProductBySlug(ctx, input.ProductSlug)
			if err != nil {
				log.Printf("[TOOL] Error getting product: %v", err)
				return ProductAvailabilityOutput{Result: fmt.Sprintf("Product not found or error checking availability: %v", err)}, nil
			}

			// Формируем ответ о наличии
			result := fmt.Sprintf("📦 %s\n\n", product.NameRu)

			if product.InStock {
				result += "✅ IN STOCK\n"
				if product.StockQuantity > 0 {
					result += fmt.Sprintf("Available quantity: %d\n", product.StockQuantity)
				}
			} else {
				result += "❌ OUT OF STOCK\n"
			}

			result += fmt.Sprintf("\n💰 Price: %s₾", product.Price)

			return ProductAvailabilityOutput{Result: result}, nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create check_product_availability tool: %w", err)
	}
	tools = append(tools, checkProductAvailabilityTool)

	log.Printf("[TOOLS] Created %d tools", len(tools))
	return tools, nil
}

// Упрощённые промпты (оптимизированы для экономии токенов)
func getUnauthorizedPromptV2() string {
	// 🎯 TOON-inspired compact format: меньше эмодзи, более короткие инструкции
	return `Customer support for "enddel" grocery delivery.

BEHAVIOR: Friendly, helpful, human-like.

ESCALATION (#escalate when):
- Customer asks if you're bot
- Quality/delivery complaints
- Refund requests
- Customer upset
- Unsure of answer

CRITICAL: NEVER answer product questions from memory!
ALWAYS use get_products() for real product data.
Use get_store_info() for delivery/payment info.

Respond in customer's language.`
}

func getAuthorizedPromptV2() string {
	return getUnauthorizedPromptV2() + `

SECURITY: Show only current customer's orders.`
}
