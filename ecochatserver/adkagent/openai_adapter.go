package adkagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// OpenAIAdapter адаптер для OpenAI API (совместим с LM Studio)
type OpenAIAdapter struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// OpenAIChatRequest структура запроса к OpenAI API
type OpenAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Tools       []interface{}   `json:"tools,omitempty"`
	Temperature float64         `json:"temperature,omitempty"`
}

// OpenAIMessage сообщение в формате OpenAI
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIChatResponse ответ от OpenAI API
type OpenAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// NewOpenAIAdapter создаёт новый OpenAI адаптер
func NewOpenAIAdapter(baseURL, apiKey, model string) *OpenAIAdapter {
	return &OpenAIAdapter{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Name возвращает имя модели
func (o *OpenAIAdapter) Name() string {
	return fmt.Sprintf("openai-%s", o.model)
}

// GenerateContent реализует интерфейс model.LLM для OpenAI API
func (o *OpenAIAdapter) GenerateContent(
	ctx context.Context,
	req *model.LLMRequest,
	stream bool,
) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// Преобразуем ADK запрос в OpenAI формат
		messages := o.convertToOpenAIMessages(req)

		// Конвертируем tools из ADK в OpenAI формат
		var tools []interface{}
		if req.Tools != nil && len(req.Tools) > 0 {
			tools = o.convertToolsToOpenAI(req.Tools)
			log.Printf("[OpenAI_Adapter] Добавлено %d tools в запрос", len(tools))
		}

		openaiReq := OpenAIChatRequest{
			Model:       o.model,
			Messages:    messages,
			Tools:       tools,
			Temperature: 0.3, // Более предсказуемые ответы, меньше "креатива"
		}

		// Сериализуем запрос
		body, err := json.Marshal(openaiReq)
		if err != nil {
			yield(nil, fmt.Errorf("failed to marshal request: %w", err))
			return
		}

		// Создаём HTTP запрос
		endpoint := fmt.Sprintf("%s/chat/completions", o.baseURL)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			yield(nil, fmt.Errorf("failed to create request: %w", err))
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if o.apiKey != "" && o.apiKey != "not-needed" {
			httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.apiKey))
		}

		log.Printf("[OpenAI_Adapter] Запрос к %s с моделью %s", endpoint, o.model)

		// Отправляем запрос
		resp, err := o.client.Do(httpReq)
		if err != nil {
			yield(nil, fmt.Errorf("failed to send request: %w", err))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			yield(nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(bodyBytes)))
			return
		}

		// Парсим ответ
		var openaiResp OpenAIChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
			yield(nil, fmt.Errorf("failed to decode response: %w", err))
			return
		}

		if len(openaiResp.Choices) == 0 {
			yield(nil, fmt.Errorf("no choices in response"))
			return
		}

		// Преобразуем ответ в ADK формат
		content := openaiResp.Choices[0].Message.Content
		log.Printf("[OpenAI_Adapter] Получен ответ: %s", truncate(content, 100))

		textContent := genai.Text(content)
		response := &model.LLMResponse{
			Content:      textContent[0],
			TurnComplete: openaiResp.Choices[0].FinishReason == "stop",
		}

		yield(response, nil)
	}
}

// convertToOpenAIMessages преобразует ADK запрос в OpenAI формат
func (o *OpenAIAdapter) convertToOpenAIMessages(req *model.LLMRequest) []OpenAIMessage {
	messages := make([]OpenAIMessage, 0)

	// Проходим по всем Contents и преобразуем в OpenAI формат
	for _, content := range req.Contents {
		role := "user"
		if content.Role == genai.RoleModel {
			role = "assistant"
		} else if content.Role == "system" {
			role = "system"
		}

		var text string
		for _, part := range content.Parts {
			text += part.Text
		}

		if text != "" {
			messages = append(messages, OpenAIMessage{
				Role:    role,
				Content: text,
			})
		}
	}

	return messages
}

// convertToolsToOpenAI конвертирует ADK tools в OpenAI формат
func (o *OpenAIAdapter) convertToolsToOpenAI(adkTools map[string]any) []interface{} {
	tools := make([]interface{}, 0)

	// ADK передает tools как map, нужно извлечь FunctionDeclarations
	for _, toolValue := range adkTools {
		// Попытка преобразовать в genai.Tool
		if toolMap, ok := toolValue.(map[string]interface{}); ok {
			// Ищем FunctionDeclarations
			if fnDecls, ok := toolMap["FunctionDeclarations"]; ok {
				if fnDeclsList, ok := fnDecls.([]interface{}); ok {
					for _, fnDecl := range fnDeclsList {
						if fn, ok := fnDecl.(map[string]interface{}); ok {
							// Строим OpenAI tool
							openaiTool := map[string]interface{}{
								"type": "function",
								"function": map[string]interface{}{
									"name":        fn["Name"],
									"description": fn["Description"],
									"parameters": map[string]interface{}{
										"type":       "object",
										"properties": fn["Parameters"],
									},
								},
							}
							tools = append(tools, openaiTool)
						}
					}
				}
			}
		}
	}

	return tools
}
