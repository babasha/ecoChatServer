package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Маппинг кодов языков на читаемые названия (для промптов)
var langNames = map[string]string{
	"ru": "Russian",
	"en": "English",
	"pl": "Polish",
	"de": "German",
	"fr": "French",
	"es": "Spanish",
	"it": "Italian",
	"uk": "Ukrainian",
	"be": "Belarusian",
	"cs": "Czech",
	"sk": "Slovak",
	"lt": "Lithuanian",
	"lv": "Latvian",
	"et": "Estonian",
	"ka": "Georgian",
	"uz": "Uzbek",
}

// GeminiClient представляет клиента для взаимодействия с Google Gemini API
type GeminiClient struct {
	apiKey string
	client *http.Client
}

// GeminiMessage представляет сообщение в формате Gemini
type GeminiMessage struct {
	Role  string                 `json:"role"`
	Parts []map[string]interface{} `json:"parts"`
}

// GeminiRequest описывает тело POST‑запроса к Gemini API
type GeminiRequest struct {
	Contents          []GeminiMessage          `json:"contents"`
	SystemInstruction *GeminiSystemInstruction `json:"system_instruction,omitempty"`
	GenerationConfig  map[string]interface{}   `json:"generationConfig,omitempty"`
	Tools             []GeminiTool             `json:"tools,omitempty"`
	ToolConfig        *GeminiToolConfig        `json:"toolConfig,omitempty"`
}

// GeminiToolConfig конфигурация для function calling
type GeminiToolConfig struct {
	FunctionCallingConfig GeminiFunctionCallingConfig `json:"functionCallingConfig"`
}

// GeminiFunctionCallingConfig режим вызова функций
type GeminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`                           // "AUTO", "ANY", "NONE"
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"` // Опционально
}

// GeminiSystemInstruction представляет системную инструкцию
type GeminiSystemInstruction struct {
	Parts []map[string]interface{} `json:"parts"`
}

// GeminiTool описывает инструмент (функцию) доступный для LLM
type GeminiTool struct {
	FunctionDeclarations []GeminiFunctionDeclaration `json:"functionDeclarations"`
}

// GeminiFunctionDeclaration описывает одну функцию
type GeminiFunctionDeclaration struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// GeminiFunctionCall представляет вызов функции от LLM
type GeminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// GeminiThoughtContent содержит текст с подписью мыслей (Gemini 2.0 thinking mode)
type GeminiThoughtContent struct {
	ThoughtSignature string `json:"thoughtSignature"`
	Text             string `json:"text"`
}

// GeminiCandidate представляет один из вариантов ответа
type GeminiCandidate struct {
	Content        GeminiMessage         `json:"content"`
	ThoughtContent *GeminiThoughtContent `json:"thoughtContent,omitempty"`
	FunctionCall   *GeminiFunctionCall   `json:"functionCall,omitempty"`
	FinishReason   string                `json:"finishReason"`
}

// GeminiResponse описывает ответ Gemini API
type GeminiResponse struct {
	Candidates     []GeminiCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason,omitempty"`
		SafetyRatings []struct {
			Category    string `json:"category"`
			Probability string `json:"probability"`
		} `json:"safetyRatings,omitempty"`
	} `json:"promptFeedback,omitempty"`
}

// NewGeminiClient создаёт новый GeminiClient
func NewGeminiClient() *GeminiClient {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		panic("GEMINI_API_KEY not set")
	}

	timeout := 30 * time.Second
	if t := os.Getenv("LLM_API_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	return &GeminiClient{
		apiKey: apiKey,
		client: &http.Client{Timeout: timeout},
	}
}

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

// GenerateResponse отправляет историю диалога в Gemini API
func (c *GeminiClient) GenerateResponse(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
) (string, error) {
	// Если истории нет — инициализируем с системным сообщением
	if len(chatHistory) == 0 {
		chatHistory = []Message{
			{
				Role: "system",
				Content: "Ты вежливый и полезный ассистент, отвечающий на вопросы клиентов. " +
					"Твои ответы должны быть краткими, информативными и дружелюбными.",
			},
		}
	}

	// Добавляем текущее сообщение пользователя
	chatHistory = append(chatHistory, Message{
		Role:    "user",
		Content: userMessage,
	})

	// Конвертируем в формат Gemini
	geminiMessages, systemInstruction := convertMessagesToGemini(chatHistory)

	// Формируем тело запроса
	reqBody := GeminiRequest{
		Contents:          geminiMessages,
		SystemInstruction: systemInstruction,
		GenerationConfig: map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 1000,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	// URL Gemini API
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s",
		c.apiKey,
	)

	// Создаём HTTP‑запрос
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Выполняем запрос
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gemini API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Обрабатываем код ответа
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Декодируем JSON-ответ
	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		// Логируем весь ответ для отладки
		respJSON, _ := json.Marshal(geminiResp)
		log.Printf("[GEMINI] No candidates in response: %s", string(respJSON))
		return "", fmt.Errorf("Gemini API returned no candidates")
	}

	candidate := geminiResp.Candidates[0]
	if len(candidate.Content.Parts) == 0 {
		// Логируем finishReason и promptFeedback для понимания причины
		log.Printf("[GEMINI] Empty content - finishReason: %s, promptFeedback: %+v",
			candidate.FinishReason, geminiResp.PromptFeedback)
		return "", fmt.Errorf("Gemini API returned empty content (finishReason: %s)", candidate.FinishReason)
	}

	// Извлекаем текст из Parts
	if text, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok {
		return text, nil
	}

	return "", fmt.Errorf("Gemini API returned invalid content format")
}

// GenerateResponseWithTools отправляет запрос с поддержкой function calling
func (c *GeminiClient) GenerateResponseWithTools(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
	tools []GeminiTool,
) (textResponse string, functionCall *GeminiFunctionCall, err error) {
	// Если истории нет — инициализируем с системным сообщением
	if len(chatHistory) == 0 {
		chatHistory = []Message{
			{
				Role: "system",
				Content: "Ты вежливый и полезный ассистент, отвечающий на вопросы клиентов. " +
					"Твои ответы должны быть краткими, информативными и дружелюбными.",
			},
		}
	}

	// Добавляем текущее сообщение пользователя
	chatHistory = append(chatHistory, Message{
		Role:    "user",
		Content: userMessage,
	})

	// Конвертируем в формат Gemini
	geminiMessages, systemInstruction := convertMessagesToGemini(chatHistory)

	// Формируем тело запроса с Tools
	// Используем режим AUTO - LLM сама решает когда вызывать функции
	reqBody := GeminiRequest{
		Contents:          geminiMessages,
		SystemInstruction: systemInstruction,
		GenerationConfig: map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 1000,
		},
		Tools: tools,
		ToolConfig: &GeminiToolConfig{
			FunctionCallingConfig: GeminiFunctionCallingConfig{
				Mode: "AUTO", // LLM сама решает когда нужны функции
			},
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal request body: %w", err)
	}

	// URL Gemini API
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s",
		c.apiKey,
	)

	// Создаём HTTP‑запрос
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", nil, fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Выполняем запрос
	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("Gemini API request failed: %w", err)
	}
	defer resp.Body.Close()

	// Обрабатываем код ответа
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("Gemini API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	// Декодируем JSON-ответ
	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", nil, fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return "", nil, fmt.Errorf("Gemini API returned no candidates")
	}

	candidate := geminiResp.Candidates[0]

	// Логируем для отладки
	candidateJSON, _ := json.Marshal(candidate)
	log.Printf("[GEMINI] Candidate response: %s", string(candidateJSON))

	// Проверяем есть ли function call в candidate.FunctionCall (старый формат)
	if candidate.FunctionCall != nil {
		log.Printf("[GEMINI] Function call detected (top-level): %s", candidate.FunctionCall.Name)
		return "", candidate.FunctionCall, nil
	}

	// Проверяем есть ли function call в любом из Parts (новый формат)
	if len(candidate.Content.Parts) > 0 {
		for i, part := range candidate.Content.Parts {
			// Проверяем на function call
			if funcCallData, ok := part["functionCall"].(map[string]interface{}); ok {
				log.Printf("[GEMINI] Function call detected in parts[%d]: %v", i, funcCallData)

				// Извлекаем name и args
				name, _ := funcCallData["name"].(string)
				args, _ := funcCallData["args"].(map[string]interface{})

				functionCall = &GeminiFunctionCall{
					Name: name,
					Args: args,
				}
			}

			// Извлекаем текст если есть
			if text, ok := part["text"].(string); ok {
				textResponse = text
			}
		}

		// Если нашли function call, возвращаем его (текст игнорируем)
		if functionCall != nil {
			return "", functionCall, nil
		}

		// Если нашли только текст, возвращаем его
		if textResponse != "" {
			return textResponse, nil, nil
		}
	}

	// Если ничего не нашли
	if len(candidate.Content.Parts) == 0 {
		return "", nil, fmt.Errorf("Gemini API returned empty content")
	}

	partsJSON, _ := json.Marshal(candidate.Content.Parts[0])
	return "", nil, fmt.Errorf("Gemini API returned invalid content format, parts[0]: %s", string(partsJSON))
}

// ContinueWithFunctionResult отправляет результат выполнения функции обратно LLM
func (c *GeminiClient) ContinueWithFunctionResult(
	ctx context.Context,
	chatHistory []Message,
	functionCall *GeminiFunctionCall,
	functionResult string,
) (string, error) {
	// Конвертируем историю в формат Gemini
	geminiMessages, systemInstruction := convertMessagesToGemini(chatHistory)

	// Добавляем ответ модели с function call
	geminiMessages = append(geminiMessages, GeminiMessage{
		Role: "model",
		Parts: []map[string]interface{}{
			{"functionCall": map[string]interface{}{
				"name": functionCall.Name,
				"args": functionCall.Args,
			}},
		},
	})

	// Добавляем результат функции
	geminiMessages = append(geminiMessages, GeminiMessage{
		Role: "function",
		Parts: []map[string]interface{}{
			{
				"functionResponse": map[string]interface{}{
					"name": functionCall.Name,
					"response": map[string]interface{}{
						"result": functionResult,
					},
				},
			},
		},
	})

	// Формируем запрос
	reqBody := GeminiRequest{
		Contents:          geminiMessages,
		SystemInstruction: systemInstruction,
		GenerationConfig: map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 1000,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s",
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gemini API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Gemini API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	// Логируем полный ответ для отладки
	respJSON, _ := json.MarshalIndent(geminiResp, "", "  ")
	log.Printf("[GEMINI] ContinueWithFunctionResult response: %s", string(respJSON))

	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("Gemini API returned no candidates")
	}

	candidate := geminiResp.Candidates[0]

	// Проверяем thoughtContent (Gemini 2.0 thinking mode)
	if candidate.ThoughtContent != nil && candidate.ThoughtContent.Text != "" {
		log.Printf("[GEMINI] Extracted text from thoughtContent")
		return candidate.ThoughtContent.Text, nil
	}

	// Проверяем обычный content.parts
	if len(candidate.Content.Parts) == 0 {
		log.Printf("[GEMINI] WARNING: Empty Parts and no thoughtContent, finishReason=%s", candidate.FinishReason)
		return "", fmt.Errorf("Gemini API returned empty content (finishReason: %s)", candidate.FinishReason)
	}

	// Извлекаем текст из Parts
	if text, ok := candidate.Content.Parts[0]["text"].(string); ok {
		return text, nil
	}

	return "", fmt.Errorf("Gemini API returned invalid content format")
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

// DetectLanguage определяет язык текста с помощью Gemini
func (c *GeminiClient) DetectLanguage(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для определения языка пуст")
	}

	prompt := fmt.Sprintf(`Определи язык следующего текста. Ответь ТОЛЬКО двухбуквенным кодом языка (например: ru, en, pl, de, fr, es, it, uk, be).

Текст: "%s"

Ответ (только код языка):`, text)

	response, err := c.GenerateResponse(ctx, prompt, []Message{
		{
			Role:    "system",
			Content: "Ты эксперт по определению языков. Отвечай ТОЛЬКО двухбуквенным ISO кодом языка без дополнительных объяснений.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к Gemini: %w", err)
	}

	// Очищаем ответ от лишних символов
	langCode := strings.TrimSpace(strings.ToLower(response))
	langCode = strings.Trim(langCode, ".,!?\"'")

	return langCode, nil
}

// TranslateText переводит текст с одного языка на другой
func (c *GeminiClient) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
		return text, nil
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	prompt := fmt.Sprintf(`Translate from %s to %s: "%s"`, fromName, toName, text)

	// Простой вызов с retry (3 попытки)
	for attempt := 1; attempt <= 3; attempt++ {
		reqBody := GeminiRequest{
			Contents: []GeminiMessage{{
				Role:  "user",
				Parts: []map[string]interface{}{{"text": prompt}},
			}},
			SystemInstruction: &GeminiSystemInstruction{
				Parts: []map[string]interface{}{{"text": "Translate. Reply with translation only."}},
			},
			GenerationConfig: map[string]interface{}{
				"temperature":     0.3,
				"maxOutputTokens": 2048,
			},
		}

		payload, _ := json.Marshal(reqBody)
		endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", c.apiKey)

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			if resp != nil {
				resp.Body.Close()
			}
			return "", fmt.Errorf("API request failed after 3 retries")
		}

		var geminiResp GeminiResponse
		json.NewDecoder(resp.Body).Decode(&geminiResp)
		resp.Body.Close()

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			if text, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok && text != "" {
				return strings.Trim(strings.TrimSpace(text), "\"'"), nil
			}
		}

		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}

	return "", fmt.Errorf("translation failed after 3 attempts")
}

// TranslateBatch переводит несколько текстов за один запрос (более эффективно)
func (c *GeminiClient) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	// Компактный промпт для batch перевода
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate %d messages from %s to %s. Reply with translations only, one per line:\n\n", len(texts), fromName, toName))
	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}

	reqBody := GeminiRequest{
		Contents: []GeminiMessage{
			{
				Role: "user",
				Parts: []map[string]interface{}{
					{"text": sb.String()},
				},
			},
		},
		SystemInstruction: &GeminiSystemInstruction{
			Parts: []map[string]interface{}{
				{"text": "Translate text. Reply with translations only, no numbering or formatting."},
			},
		},
		GenerationConfig: map[string]interface{}{
			"temperature":     0.3,
			"maxOutputTokens": 4096, // Больше для batch
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s",
		c.apiKey,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	// Парсим результат
	lines := strings.Split(strings.TrimSpace(responseText), "\n")
	translations := make([]string, 0, len(texts))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Убираем нумерацию если есть
		if len(line) > 3 && line[1] == '.' && line[0] >= '0' && line[0] <= '9' {
			line = strings.TrimSpace(line[2:])
		}
		translations = append(translations, line)
	}

	// Проверка количества
	if len(translations) != len(texts) {
		log.Printf("TranslateBatch: WARNING - expected %d, got %d translations", len(texts), len(translations))
		// Дополняем оригиналами если не хватает
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
	}

	return translations, nil
}
