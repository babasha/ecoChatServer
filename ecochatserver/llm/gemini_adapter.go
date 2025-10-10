package llm

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ============================================================================
// GEMINI ADAPTER - Adapts Gemini API to universal Provider interface
// ============================================================================
//
// Этот адаптер оборачивает существующий GeminiClient и реализует
// универсальный интерфейс Provider. Все Gemini-специфичные типы
// конвертируются в универсальные типы (Tool, FunctionCall, etc.)

// GeminiAdapter адаптер для работы с Gemini через универсальный интерфейс
type GeminiAdapter struct {
	client  *GeminiClient
	apiKey  string
	model   string
	timeout time.Duration
}

// Initialize инициализирует адаптер
func (a *GeminiAdapter) Initialize() error {
	if a.apiKey == "" {
		return fmt.Errorf("Gemini API key is required")
	}

	// Создаём HTTP клиент с таймаутом
	httpClient := &http.Client{
		Timeout: a.timeout,
	}

	// Создаём Gemini клиент вручную (не используем NewGeminiClient, чтобы использовать свою конфигурацию)
	a.client = &GeminiClient{
		apiKey: a.apiKey,
		client: httpClient,
	}

	if a.model == "" {
		a.model = "gemini-2.5-flash" // По умолчанию
	}

	log.Printf("[GEMINI_ADAPTER] Initialized with model: %s, timeout: %v", a.model, a.timeout)
	return nil
}

// GetName возвращает название провайдера
func (a *GeminiAdapter) GetName() string {
	return fmt.Sprintf("Gemini (%s)", a.model)
}

// ============================================================================
// GENERATION METHODS
// ============================================================================

// GenerateResponse простая генерация без инструментов
func (a *GeminiAdapter) GenerateResponse(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
	opts *GenerateOptions,
) (*Response, error) {
	// Добавляем системный промпт если указан в опциях
	history := chatHistory
	if opts != nil && opts.SystemPrompt != "" && len(history) == 0 {
		history = []Message{{Role: "system", Content: opts.SystemPrompt}}
	}

	// Вызываем оригинальный метод GeminiClient
	text, err := a.client.GenerateResponse(ctx, userMessage, history)
	if err != nil {
		return nil, fmt.Errorf("Gemini GenerateResponse: %w", err)
	}

	return &Response{
		Text:         text,
		FinishReason: "stop",
	}, nil
}

// GenerateWithTools генерация с поддержкой function calling
func (a *GeminiAdapter) GenerateWithTools(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
	tools []Tool,
	opts *GenerateOptions,
) (*Response, error) {
	// Конвертируем универсальные Tool в Gemini-специфичные GeminiTool
	geminiTools := convertToGeminiTools(tools)

	log.Printf("[GEMINI_ADAPTER] GenerateWithTools: message=%s, tools=%d", userMessage, len(geminiTools))

	// Вызываем оригинальный метод с function calling
	text, funcCall, err := a.client.GenerateResponseWithTools(ctx, userMessage, chatHistory, geminiTools)
	if err != nil {
		return nil, fmt.Errorf("Gemini GenerateWithTools: %w", err)
	}

	// Конвертируем обратно в универсальный формат
	response := &Response{
		Text:         text,
		FinishReason: "stop",
	}

	if funcCall != nil {
		response.FunctionCall = &FunctionCall{
			Name:      funcCall.Name,
			Arguments: funcCall.Args,
		}
		response.FinishReason = "function_call"
		log.Printf("[GEMINI_ADAPTER] Function call detected: %s", funcCall.Name)
	}

	return response, nil
}

// ContinueWithFunctionResult продолжает диалог после выполнения функции
func (a *GeminiAdapter) ContinueWithFunctionResult(
	ctx context.Context,
	chatHistory []Message,
	functionCall *FunctionCall,
	functionResult string,
	opts *GenerateOptions,
) (*Response, error) {
	// Конвертируем универсальный FunctionCall в Gemini-специфичный
	geminiCall := &GeminiFunctionCall{
		Name: functionCall.Name,
		Args: functionCall.Arguments,
	}

	log.Printf("[GEMINI_ADAPTER] ContinueWithFunctionResult: function=%s", functionCall.Name)

	// Вызываем оригинальный метод
	text, err := a.client.ContinueWithFunctionResult(ctx, chatHistory, geminiCall, functionResult)
	if err != nil {
		return nil, fmt.Errorf("Gemini ContinueWithFunctionResult: %w", err)
	}

	return &Response{
		Text:         text,
		FinishReason: "stop",
	}, nil
}

// ============================================================================
// TRANSLATION METHODS
// ============================================================================

// TranslateText переводит текст
func (a *GeminiAdapter) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	return a.client.TranslateText(ctx, text, fromLang, toLang)
}

// DetectAndTranslate определяет язык и переводит
func (a *GeminiAdapter) DetectAndTranslate(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	result, err := a.client.DetectAndTranslate(ctx, text, targetLang)
	if err != nil {
		return nil, err
	}

	return &TranslationResult{
		DetectedLang: result.DetectedLang,
		Translation:  result.Translation,
	}, nil
}

// TranslateBatch переводит несколько текстов
func (a *GeminiAdapter) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	return a.client.TranslateBatch(ctx, texts, fromLang, toLang)
}

// ============================================================================
// CONVERSION HELPERS - Convert between universal and Gemini-specific types
// ============================================================================

// convertToGeminiTools конвертирует универсальные Tool в GeminiTool
func convertToGeminiTools(tools []Tool) []GeminiTool {
	if len(tools) == 0 {
		return nil
	}

	geminiTools := make([]GeminiTool, 0, 1)
	declarations := make([]GeminiFunctionDeclaration, 0, len(tools))

	for _, tool := range tools {
		declarations = append(declarations, GeminiFunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
		})
	}

	geminiTools = append(geminiTools, GeminiTool{
		FunctionDeclarations: declarations,
	})

	return geminiTools
}

// ============================================================================
// HELPER: Get store function tools in universal format
// ============================================================================

// GetUniversalStoreFunctionTools возвращает универсальные инструменты магазина
// Это замена для GetStoreFunctionTools() которая возвращала Gemini-специфичные типы
func GetUniversalStoreFunctionTools() []Tool {
	return []Tool{
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
	}
}
