package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

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

	// Логируем использование токенов
	if geminiResp.UsageMetadata != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "gemini",
			Model:            "gemini-2.5-flash",
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
			RequestType:      "chat",
		})
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

	// Логируем использование токенов
	if geminiResp.UsageMetadata != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "gemini",
			Model:            "gemini-2.5-flash",
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
			RequestType:      "function_call",
		})
	}

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

	// Логируем использование токенов
	if geminiResp.UsageMetadata != nil {
		_ = LogUsage(ctx, UsageLogEntry{
			Provider:         "gemini",
			Model:            "gemini-2.5-flash",
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
			RequestType:      "function_call",
		})
	}

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
