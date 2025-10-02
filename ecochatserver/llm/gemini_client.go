package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

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
	Contents          []GeminiMessage        `json:"contents"`
	SystemInstruction *GeminiSystemInstruction `json:"system_instruction,omitempty"`
	GenerationConfig  map[string]interface{} `json:"generationConfig,omitempty"`
	Tools             []GeminiTool           `json:"tools,omitempty"`
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

// GeminiCandidate представляет один из вариантов ответа
type GeminiCandidate struct {
	Content      GeminiMessage       `json:"content"`
	FunctionCall *GeminiFunctionCall `json:"functionCall,omitempty"`
	FinishReason string              `json:"finishReason"`
}

// GeminiResponse описывает ответ Gemini API
type GeminiResponse struct {
	Candidates []GeminiCandidate `json:"candidates"`
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
					Name:        "get_product_categories",
					Description: "Get list of all product categories available in the store. Use when customer asks about categories, types of products, what we sell, assortment, catalog.",
					Parameters: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				{
					Name:        "search_products",
					Description: "Search for products by name or category. Use when customer asks about specific product, wants to find something, or asks 'what products do you have'.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"query": map[string]interface{}{
								"type":        "string",
								"description": "Search query (product name, category name, or keywords). Can be empty to get all products.",
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
		return "", fmt.Errorf("Gemini API returned no candidates")
	}

	if len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini API returned empty content")
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
	reqBody := GeminiRequest{
		Contents:          geminiMessages,
		SystemInstruction: systemInstruction,
		GenerationConfig: map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 1000,
		},
		Tools: tools,
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

	// Проверяем есть ли function call
	if candidate.FunctionCall != nil {
		return "", candidate.FunctionCall, nil
	}

	// Иначе возвращаем текстовый ответ
	if len(candidate.Content.Parts) == 0 {
		return "", nil, fmt.Errorf("Gemini API returned empty content")
	}

	// Извлекаем текст из Parts
	if text, ok := candidate.Content.Parts[0]["text"].(string); ok {
		return text, nil, nil
	}

	return "", nil, fmt.Errorf("Gemini API returned invalid content format")
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

	if len(geminiResp.Candidates) == 0 {
		return "", fmt.Errorf("Gemini API returned no candidates")
	}

	if len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini API returned empty content")
	}

	// Извлекаем текст из Parts
	if text, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok {
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

	// Маппинг кодов языков на читаемые названия
	langNames := map[string]string{
		"ru": "русский",
		"en": "английский",
		"pl": "польский",
		"de": "немецкий",
		"fr": "французский",
		"es": "испанский",
		"it": "итальянский",
		"uk": "украинский",
		"be": "белорусский",
		"cs": "чешский",
		"sk": "словацкий",
		"lt": "литовский",
		"lv": "латышский",
		"et": "эстонский",
	}

	fromName := langNames[fromLang]
	if fromName == "" {
		fromName = fromLang
	}
	toName := langNames[toLang]
	if toName == "" {
		toName = toLang
	}

	prompt := fmt.Sprintf(`Переведи следующий текст с %s языка на %s язык. Отвечай ТОЛЬКО переводом, без дополнительных комментариев и объяснений.

Текст для перевода: "%s"

Перевод:`, fromName, toName, text)

	translation, err := c.GenerateResponse(ctx, prompt, []Message{
		{
			Role:    "system",
			Content: "Ты профессиональный переводчик. Переводи точно и естественно. Отвечай ТОЛЬКО переводом без дополнительных комментариев.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("ошибка запроса к Gemini: %w", err)
	}

	// Очищаем перевод от лишних кавычек
	translation = strings.TrimSpace(translation)
	translation = strings.Trim(translation, "\"'")

	return translation, nil
}
