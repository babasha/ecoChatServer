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

// OpenAIToolCall вызов функции от модели
type OpenAIToolCall struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
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
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []OpenAIToolCall `json:"tool_calls,omitempty"`
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
			log.Printf("[OpenAI_Adapter] DEBUG: req.Tools содержит %d элементов", len(req.Tools))
			log.Printf("[OpenAI_Adapter] DEBUG: req.Tools = %+v", req.Tools)
			tools = o.convertToolsToOpenAI(req.Tools)
			log.Printf("[OpenAI_Adapter] Добавлено %d tools в запрос", len(tools))
		} else {
			log.Printf("[OpenAI_Adapter] WARNING: req.Tools пустой или nil!")
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

		// DEBUG: выводим первые 500 символов запроса
		log.Printf("[OpenAI_Adapter] DEBUG Request Body: %s", truncate(string(body), 1000))

		// Создаём HTTP запрос
		endpoint := fmt.Sprintf("%s/chat/completions", o.baseURL)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			yield(nil, fmt.Errorf("failed to create request: %w", err))
			return
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("ngrok-skip-browser-warning", "true") // Для ngrok
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

		// Читаем тело ответа для debug
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			yield(nil, fmt.Errorf("failed to read response: %w", err))
			return
		}
		log.Printf("[OpenAI_Adapter] DEBUG Response Body: %s", truncate(string(bodyBytes), 1000))

		// Парсим ответ
		var openaiResp OpenAIChatResponse
		if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
			yield(nil, fmt.Errorf("failed to decode response: %w", err))
			return
		}

		if len(openaiResp.Choices) == 0 {
			yield(nil, fmt.Errorf("no choices in response"))
			return
		}

		choice := openaiResp.Choices[0]

		// Проверяем есть ли tool_calls в ответе
		if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) > 0 {
			log.Printf("[OpenAI_Adapter] Получено %d tool calls", len(choice.Message.ToolCalls))

			// Создаем Parts с FunctionCall для каждого tool call
			parts := make([]*genai.Part, 0)
			for _, tc := range choice.Message.ToolCalls {
				log.Printf("[OpenAI_Adapter] Tool call: %s (args: %s)", tc.Function.Name, tc.Function.Arguments)

				// Парсим arguments из JSON string в map[string]any
				var args map[string]any
				if tc.Function.Arguments != "" && tc.Function.Arguments != "{}" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						log.Printf("[OpenAI_Adapter] WARNING: Failed to parse arguments for %s: %v", tc.Function.Name, err)
						args = make(map[string]any)
					}
				} else {
					args = make(map[string]any)
				}

				// Создаем FunctionCall
				functionCall := &genai.FunctionCall{
					ID:   tc.ID,
					Name: tc.Function.Name,
					Args: args,
				}

				// Добавляем Part с FunctionCall
				parts = append(parts, &genai.Part{
					FunctionCall: functionCall,
				})

				log.Printf("[OpenAI_Adapter] ✅ Created FunctionCall: %s with %d args", tc.Function.Name, len(args))
			}

			// Создаем Content с role=model
			content := &genai.Content{
				Role:  genai.RoleModel,
				Parts: parts,
			}

			response := &model.LLMResponse{
				Content:      content,
				TurnComplete: false, // Tool calls НЕ завершают turn!
			}

			log.Printf("[OpenAI_Adapter] 🔧 Returning %d tool calls to ADK", len(parts))
			yield(response, nil)
			return
		}

		// Обрабатываем как текстовый ответ
		content := choice.Message.Content
		log.Printf("[OpenAI_Adapter] Получен текстовый ответ: %s", truncate(content, 100))

		textContent := genai.Text(content)
		response := &model.LLMResponse{
			Content:      textContent[0],
			TurnComplete: choice.FinishReason == "stop",
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

// FunctionTool интерфейс для получения FunctionDeclaration из ADK tool
type FunctionTool interface {
	Name() string
	Declaration() *genai.FunctionDeclaration
}

// convertToolsToOpenAI конвертирует ADK tools в OpenAI формат
func (o *OpenAIAdapter) convertToolsToOpenAI(adkTools map[string]any) []interface{} {
	tools := make([]interface{}, 0)

	// ADK передает tools как map[string]tool.Tool (где tool.Tool это интерфейс)
	// Нужно вызвать Declaration() чтобы получить *genai.FunctionDeclaration
	for toolName, toolValue := range adkTools {
		// Проверяем что tool реализует интерфейс с методом Declaration()
		if funcTool, ok := toolValue.(FunctionTool); ok {
			decl := funcTool.Declaration()
			if decl == nil {
				log.Printf("[OpenAI_Adapter] WARNING: Tool %s has nil Declaration", toolName)
				continue
			}

			// Конвертируем параметры из ParametersJsonSchema в OpenAI формат
			parameters := map[string]interface{}{
				"type":       "object",
				"properties": make(map[string]interface{}),
			}

			if decl.ParametersJsonSchema != nil {
				// ParametersJsonSchema это any, нужно type assert к map
				if schema, ok := decl.ParametersJsonSchema.(map[string]interface{}); ok {
					if props, ok := schema["properties"]; ok {
						parameters["properties"] = props
					}
					if required, ok := schema["required"]; ok {
						parameters["required"] = required
					}
				}
			}

			// Строим OpenAI tool
			openaiTool := map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        decl.Name,
					"description": decl.Description,
					"parameters":  parameters,
				},
			}

			tools = append(tools, openaiTool)
			log.Printf("[OpenAI_Adapter] Добавлен tool: %s (description: %s)", decl.Name, truncate(decl.Description, 50))
		} else {
			log.Printf("[OpenAI_Adapter] WARNING: Tool %s does not implement FunctionTool interface (type: %T)", toolName, toolValue)
		}
	}

	return tools
}
