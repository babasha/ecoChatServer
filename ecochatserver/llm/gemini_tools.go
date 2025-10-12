package llm

import (
	"encoding/json"
)

// GetStoreFunctionTools возвращает объявления функций для работы с магазином
func GetStoreFunctionTools() []GeminiTool {
	return []GeminiTool{
		{
			FunctionDeclarations: []GeminiFunctionDeclaration{
				{
					Name:        "get_products",
					Description: "Get list of ALL products from store catalog. Use when customer asks 'what products do you have', 'show me your products', 'what do you sell', etc. Returns actual products with prices and details.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"search_query": map[string]interface{}{
								"type":        "string",
								"description": "Optional search query to filter products. Leave empty to get ALL products.",
							},
						},
					},
				},
				{
					Name:        "get_product_categories",
					Description: "Get list of product categories only (not actual products). Use when customer specifically asks about categories or types/sections.",
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					Name:        "search_products",
					Description: "Search for specific product by name. Use when customer asks about a specific item like 'do you have salmon', 'find milk', etc.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"query": map[string]interface{}{
								"type":        "string",
								"description": "Product name to search for",
							},
						},
					},
				},
				{
					Name:        "inspect_website_page",
					Description: "CRITICAL: Inspect actual website to see current UI, buttons, and how things work. ALWAYS use this when customer asks 'how to...', 'where is...', 'how do I...'. You have general info in memory, but real website may differ - inspect to give accurate instructions based on ACTUAL interface, not assumptions.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"page_path": map[string]interface{}{
								"type":        "string",
								"description": "Page path to inspect. Examples: '/' (home), '/cart' (cart), '/checkout' (checkout). Default: '/'",
							},
						},
					},
				},
				{
					Name:        "get_store_info",
					Description: "Get key store information and FAQ answers. Use when customer asks about: delivery cost, working hours, payment methods, minimum order, delivery time, return policy, etc. This provides quick answers without needing to inspect website.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"info_type": map[string]interface{}{
								"type":        "string",
								"description": "Type of info needed: 'delivery', 'payment', 'hours', 'policies', 'all'. Default: 'all'",
								"enum":        []string{"delivery", "payment", "hours", "policies", "all"},
							},
						},
					},
				},
			},
		},
	}
}

// convertMessagesToGemini конвертирует историю чата в формат Gemini
func convertMessagesToGemini(messages []Message) ([]GeminiMessage, *GeminiSystemInstruction) {
	var geminiMessages []GeminiMessage
	var systemInstruction *GeminiSystemInstruction

	for _, msg := range messages {
		role := msg.Role
		// Gemini использует "user" и "model" вместо "assistant"
		if role == "assistant" {
			role = "model"
		}
		// Системные сообщения выделяем отдельно
		if role == "system" {
			systemInstruction = &GeminiSystemInstruction{
				Parts: []map[string]interface{}{
					{"text": msg.Content},
				},
			}
			continue // Не добавляем в contents
		}

		geminiMessages = append(geminiMessages, GeminiMessage{
			Role: role,
			Parts: []map[string]interface{}{
				{"text": msg.Content},
			},
		})
	}

	return geminiMessages, systemInstruction
}

// marshalArgs конвертирует args в JSON строку
func marshalArgs(args map[string]interface{}) string {
	data, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// quote оборачивает строку в JSON-escaped кавычки
func quote(s string) string {
	data, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(data)
}
