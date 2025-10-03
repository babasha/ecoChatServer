package llm

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// Tool представляет инструмент, который может использовать LLM
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolCall представляет вызов инструмента от LLM
type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// GetAvailableTools возвращает список доступных инструментов для LLM
func GetAvailableTools() []Tool {
	return []Tool{
		{
			Name:        "get_products",
			Description: "Get list of products from store. Use when customer asks about products, catalog, assortment, what we sell, etc. Returns actual products from database.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"search_query": map[string]interface{}{
						"type":        "string",
						"description": "Optional search query to filter products (product name, category, etc). Leave empty to get all products.",
					},
				},
			},
		},
		{
			Name:        "get_categories",
			Description: "Get list of product categories from store. Use when customer asks about categories, types of products, what sections we have.",
			Parameters: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "search_product",
			Description: "Search for specific product by name. Use when customer asks about specific product.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"product_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the product to search for",
					},
				},
				"required": []string{"product_name"},
			},
		},
	}
}

// ExecuteTool выполняет вызов инструмента
func ExecuteTool(ctx context.Context, storeClient *StoreClient, toolCall ToolCall) (string, error) {
	log.Printf("[TOOLS] Executing tool: %s with args: %+v", toolCall.Name, toolCall.Arguments)

	switch toolCall.Name {
	case "get_products":
		searchQuery := ""
		if q, ok := toolCall.Arguments["search_query"].(string); ok {
			searchQuery = q
		}
		return executeGetProducts(ctx, storeClient, searchQuery)

	case "get_categories", "get_product_categories":
		return executeGetCategories(ctx, storeClient)

	case "search_product", "search_products":
		// Поддерживаем оба варианта аргументов
		productName := ""
		if name, ok := toolCall.Arguments["product_name"].(string); ok {
			productName = name
		} else if query, ok := toolCall.Arguments["query"].(string); ok {
			productName = query
		}
		if productName == "" {
			return executeGetProducts(ctx, storeClient, "") // Возвращаем все продукты
		}
		return executeSearchProduct(ctx, storeClient, productName)

	case "inspect_website_page":
		pagePath := "/"
		if path, ok := toolCall.Arguments["page_path"].(string); ok {
			pagePath = path
		}
		return executeInspectWebsite(ctx, storeClient, pagePath)

	case "get_store_info":
		infoType := "all"
		if t, ok := toolCall.Arguments["info_type"].(string); ok {
			infoType = t
		}
		return executeGetStoreInfo(infoType)

	default:
		return "", fmt.Errorf("unknown tool: %s", toolCall.Name)
	}
}

func executeGetProducts(ctx context.Context, storeClient *StoreClient, searchQuery string) (string, error) {
	products, err := storeClient.GetAllProducts(ctx, searchQuery)
	if err != nil {
		return "", err
	}

	if len(products) == 0 {
		return "No products found.", nil
	}

	// Ограничиваем до 15 товаров
	if len(products) > 15 {
		products = products[:15]
	}

	return FormatProductsList(products), nil
}

func executeGetCategories(ctx context.Context, storeClient *StoreClient) (string, error) {
	categories, err := storeClient.GetAllCategories(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get categories: %w", err)
	}

	if len(categories) == 0 {
		return "No categories found in the store.", nil
	}

	// Форматируем список категорий
	result := fmt.Sprintf("Store has %d product categories:\n", len(categories))
	for i, cat := range categories {
		if i >= 15 { // Ограничиваем до 15
			result += fmt.Sprintf("\n... and %d more categories", len(categories)-15)
			break
		}
		result += fmt.Sprintf("• %s\n", cat.NameRu)
	}
	return result, nil
}

func executeSearchProduct(ctx context.Context, storeClient *StoreClient, productName string) (string, error) {
	products, err := storeClient.GetAllProducts(ctx, productName)
	if err != nil {
		return "", err
	}

	if len(products) == 0 {
		return fmt.Sprintf("Product '%s' not found.", productName), nil
	}

	if len(products) == 1 {
		return FormatProductDetails(products[0]), nil
	}

	// Несколько товаров - показываем список
	if len(products) > 10 {
		products = products[:10]
	}
	return FormatProductsList(products), nil
}

func executeInspectWebsite(ctx context.Context, storeClient *StoreClient, pagePath string) (string, error) {
	// Используем базовый URL из store client
	inspector := NewWebsiteInspector(storeClient.baseURL)

	pageInfo, err := inspector.InspectPage(ctx, pagePath)
	if err != nil {
		log.Printf("[TOOLS] Error inspecting website page %s: %v", pagePath, err)
		return fmt.Sprintf("Error inspecting page %s: %v. The page might not be accessible or doesn't exist.", pagePath, err), nil
	}

	return pageInfo, nil
}

// executeGetStoreInfo возвращает ключевую информацию о магазине и FAQ
func executeGetStoreInfo(infoType string) (string, error) {
	log.Printf("[TOOLS] Getting store info: type=%s", infoType)

	var result strings.Builder
	result.WriteString("📋 Enddel Store Information\n\n")

	// Информация о доставке
	if infoType == "all" || infoType == "delivery" {
		result.WriteString("🚚 DELIVERY:\n")
		result.WriteString("  • Delivery Cost: 3₾ for orders under 50₾, FREE for orders 50₾ and above\n")
		result.WriteString("  • Delivery Time: 30-60 minutes (average 45 min)\n")
		result.WriteString("  • Minimum Order: No minimum order amount\n")
		result.WriteString("  • Delivery Hours: 8:00 AM - 11:00 PM, 7 days a week\n")
		result.WriteString("  • Coverage: Tbilisi and surrounding areas\n")
		result.WriteString("  • Options: ASAP delivery or schedule for specific time\n\n")
	}

	// Способы оплаты
	if infoType == "all" || infoType == "payment" {
		result.WriteString("💳 PAYMENT METHODS:\n")
		result.WriteString("  • Online Card Payment (Visa, Mastercard) - pay immediately\n")
		result.WriteString("  • Cash on Delivery - pay to courier\n")
		result.WriteString("  • Card on Delivery - pay by card to courier\n")
		result.WriteString("  • Google Pay - quick online payment\n")
		result.WriteString("  • Telegram Payment - integrated payment in Telegram\n")
		result.WriteString("  • Saved Cards: Can save cards for faster checkout (for registered users)\n\n")
	}

	// Часы работы
	if infoType == "all" || infoType == "hours" {
		result.WriteString("🕐 WORKING HOURS:\n")
		result.WriteString("  • Store: 8:00 AM - 11:00 PM (every day)\n")
		result.WriteString("  • Customer Support: 8:00 AM - 11:00 PM\n")
		result.WriteString("  • Order Processing: Orders accepted 24/7 online\n")
		result.WriteString("  • Delivery: 8:00 AM - 11:00 PM\n\n")
	}

	// Политики и правила
	if infoType == "all" || infoType == "policies" {
		result.WriteString("📜 POLICIES:\n")
		result.WriteString("  • Returns: Fresh products can be returned/replaced if quality issues\n")
		result.WriteString("  • Refunds: Full refund if order not delivered or major issues\n")
		result.WriteString("  • Product Availability: Real-time stock updates, out-of-stock items marked\n")
		result.WriteString("  • Order Changes: Can modify order within 5 minutes after placing\n")
		result.WriteString("  • Substitutions: We may suggest alternatives if product unavailable\n")
		result.WriteString("  • Privacy: We don't share customer data with third parties\n\n")
	}

	result.WriteString("💡 TIP: For specific questions about how to use the website (where buttons are, how to checkout, etc.), ")
	result.WriteString("use the inspect_website_page function to see the actual interface.\n")

	return result.String(), nil
}

// FormatToolsForPrompt форматирует инструменты для добавления в промпт
func FormatToolsForPrompt() string {
	tools := GetAvailableTools()
	result := "🔧 AVAILABLE TOOLS (you can use these to help customer):\n\n"

	for _, tool := range tools {
		result += fmt.Sprintf("Tool: %s\n", tool.Name)
		result += fmt.Sprintf("Description: %s\n", tool.Description)
		result += fmt.Sprintf("Usage: When you need this info, use: [TOOL:%s", tool.Name)

		if params, ok := tool.Parameters["properties"].(map[string]interface{}); ok && len(params) > 0 {
			result += "|"
			for paramName := range params {
				result += paramName + "=VALUE"
				break // Показываем только первый параметр для примера
			}
		}
		result += "]\n\n"
	}

	result += "Example: Customer asks about products → You respond: \"Let me check! [TOOL:get_products]\"\n"
	result += "Example: Customer asks about milk → You respond: \"Let me find it! [TOOL:search_product|product_name=milk]\"\n\n"
	result += "IMPORTANT: System will intercept [TOOL:...] and execute it automatically!\n"

	return result
}

// ParseToolCalls извлекает вызовы инструментов из ответа LLM
func ParseToolCalls(llmResponse string) []ToolCall {
	var toolCalls []ToolCall

	// Простой парсинг: [TOOL:name|param=value]
	// Можно улучшить с помощью regex
	// Пока оставлю простую реализацию

	// TODO: Implement proper parsing
	log.Printf("[TOOLS] Parsing tool calls from response: %s", llmResponse)

	return toolCalls
}
