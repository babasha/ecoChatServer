package llm

import (
	"context"
	"fmt"
	"log"
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
