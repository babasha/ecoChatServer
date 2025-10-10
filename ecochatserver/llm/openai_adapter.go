package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// OpenAIAdapter реализует Provider для OpenAI API (и LM Studio)
// Совместим с:
// - OpenAI API (https://api.openai.com/v1)
// - LM Studio (http://localhost:1234/v1)
// - Любые OpenAI-совместимые API
type OpenAIAdapter struct {
	baseURL string // например: http://localhost:1234/v1 или https://api.openai.com/v1
	apiKey  string // для OpenAI обязателен, для LM Studio можно пустой
	model   string // например: gpt-4o-mini или локальная модель
	client  *http.Client
	timeout time.Duration
}

// OpenAI API request/response types
type openAIMessage struct {
	Role    string `json:"role"`    // system, user, assistant, tool
	Content string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string                 `json:"type"` // всегда "function"
	Function openAIToolFunction     `json:"function"`
}

type openAIToolFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type openAIToolChoice struct {
	Type     string `json:"type,omitempty"`
	Function *struct {
		Name string `json:"name"`
	} `json:"function,omitempty"`
}

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float32         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Tools       []openAITool    `json:"tools,omitempty"`
	ToolChoice  interface{}     `json:"tool_choice,omitempty"` // "auto", "none", или объект
}

type openAIChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"` // JSON string
				} `json:"function"`
			} `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// NewOpenAIAdapter создает новый OpenAI адаптер
func NewOpenAIAdapter(baseURL, apiKey, model string, timeout time.Duration) *OpenAIAdapter {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1" // default OpenAI
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &OpenAIAdapter{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Initialize инициализирует адаптер
func (a *OpenAIAdapter) Initialize() error {
	log.Printf("[OPENAI_ADAPTER] Initialized with baseURL: %s, model: %s, timeout: %v",
		a.baseURL, a.model, a.timeout)
	return nil
}

// GetName возвращает имя провайдера
func (a *OpenAIAdapter) GetName() string {
	if a.baseURL == "https://api.openai.com/v1" {
		return fmt.Sprintf("OpenAI (%s)", a.model)
	}
	return fmt.Sprintf("OpenAI-Compatible (%s at %s)", a.model, a.baseURL)
}

// GenerateResponse генерирует ответ без инструментов
func (a *OpenAIAdapter) GenerateResponse(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
	opts *GenerateOptions,
) (*Response, error) {
	messages := a.buildMessages(userMessage, chatHistory, "")

	req := openAIChatRequest{
		Model:    a.model,
		Messages: messages,
	}

	if opts != nil {
		req.Temperature = opts.Temperature
		req.MaxTokens = opts.MaxTokens
	}

	resp, err := a.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return a.parseResponse(resp), nil
}

// GenerateWithTools генерирует ответ с поддержкой function calling
func (a *OpenAIAdapter) GenerateWithTools(
	ctx context.Context,
	userMessage string,
	chatHistory []Message,
	tools []Tool,
	opts *GenerateOptions,
) (*Response, error) {
	messages := a.buildMessages(userMessage, chatHistory, "")
	openaiTools := a.convertTools(tools)

	req := openAIChatRequest{
		Model:      a.model,
		Messages:   messages,
		Tools:      openaiTools,
		ToolChoice: "auto", // LLM сам решает когда вызывать функции
	}

	if opts != nil {
		req.Temperature = opts.Temperature
		req.MaxTokens = opts.MaxTokens
	}

	resp, err := a.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return a.parseResponse(resp), nil
}

// ContinueWithFunctionResult продолжает диалог с результатом выполнения функции
func (a *OpenAIAdapter) ContinueWithFunctionResult(
	ctx context.Context,
	chatHistory []Message,
	functionCall *FunctionCall,
	result interface{},
	opts *GenerateOptions,
) (*Response, error) {
	// Конвертируем историю в OpenAI формат
	messages := make([]openAIMessage, 0, len(chatHistory)+2)

	for _, msg := range chatHistory {
		messages = append(messages, openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Добавляем сообщение assistant с tool call
	messages = append(messages, openAIMessage{
		Role:    "assistant",
		Content: "", // может быть пустым при tool call
	})

	// Добавляем результат выполнения функции
	resultJSON, _ := json.Marshal(result)
	messages = append(messages, openAIMessage{
		Role:       "tool",
		Content:    string(resultJSON),
		ToolCallID: functionCall.Name + "_call", // ID tool call
	})

	req := openAIChatRequest{
		Model:    a.model,
		Messages: messages,
	}

	if opts != nil {
		req.Temperature = opts.Temperature
		req.MaxTokens = opts.MaxTokens
	}

	resp, err := a.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	return a.parseResponse(resp), nil
}

// TranslateText переводит текст с одного языка на другой
func (a *OpenAIAdapter) TranslateText(
	ctx context.Context,
	text string,
	fromLang string,
	toLang string,
) (string, error) {
	prompt := fmt.Sprintf(
		"Translate the following text from %s to %s. Return ONLY the translated text, no explanations:\n\n%s",
		fromLang, toLang, text,
	)

	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{
		Temperature: 0.3, // низкая температура для точного перевода
		MaxTokens:   500,
	})

	if err != nil {
		return "", err
	}

	return resp.Text, nil
}

// DetectAndTranslate определяет язык и переводит за один запрос
func (a *OpenAIAdapter) DetectAndTranslate(
	ctx context.Context,
	text string,
	targetLang string,
) (*TranslationResult, error) {
	prompt := fmt.Sprintf(`Detect the language of the text and translate it to %s.
Return a JSON response in this format:
{
  "detected_language": "language_code",
  "translated_text": "translated text here"
}

Text: %s`, targetLang, text)

	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   500,
	})

	if err != nil {
		return nil, err
	}

	// Парсим JSON ответ
	var result struct {
		DetectedLanguage string `json:"detected_language"`
		TranslatedText   string `json:"translated_text"`
	}

	if err := json.Unmarshal([]byte(resp.Text), &result); err != nil {
		// Если не получилось распарсить - возвращаем как есть
		log.Printf("[OPENAI_ADAPTER] Failed to parse DetectAndTranslate JSON: %v", err)
		return &TranslationResult{
			DetectedLanguage: "unknown",
			TranslatedText:   resp.Text,
		}, nil
	}

	return &TranslationResult{
		DetectedLanguage: result.DetectedLanguage,
		TranslatedText:   result.TranslatedText,
	}, nil
}

// TranslateBatch переводит несколько текстов за один запрос
func (a *OpenAIAdapter) TranslateBatch(
	ctx context.Context,
	texts []string,
	fromLang string,
	toLang string,
) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	// Формируем batch запрос
	textsJSON, _ := json.Marshal(texts)
	prompt := fmt.Sprintf(`Translate the following texts from %s to %s.
Return a JSON array with translated texts in the same order.
Format: ["translation1", "translation2", ...]

Texts to translate:
%s`, fromLang, toLang, string(textsJSON))

	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{
		Temperature: 0.3,
		MaxTokens:   2000,
	})

	if err != nil {
		return nil, err
	}

	// Парсим JSON ответ
	var translations []string
	if err := json.Unmarshal([]byte(resp.Text), &translations); err != nil {
		log.Printf("[OPENAI_ADAPTER] Failed to parse TranslateBatch JSON: %v", err)
		return nil, fmt.Errorf("failed to parse translations: %w", err)
	}

	return translations, nil
}

// Helper methods

func (a *OpenAIAdapter) buildMessages(userMessage string, chatHistory []Message, systemPrompt string) []openAIMessage {
	messages := make([]openAIMessage, 0, len(chatHistory)+2)

	// Системный промпт (если есть)
	if systemPrompt != "" {
		messages = append(messages, openAIMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	// История чата
	for _, msg := range chatHistory {
		messages = append(messages, openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// Новое сообщение пользователя
	if userMessage != "" {
		messages = append(messages, openAIMessage{
			Role:    "user",
			Content: userMessage,
		})
	}

	return messages
}

func (a *OpenAIAdapter) convertTools(tools []Tool) []openAITool {
	result := make([]openAITool, len(tools))

	for i, tool := range tools {
		result[i] = openAITool{
			Type: "function",
			Function: openAIToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	}

	return result
}

func (a *OpenAIAdapter) sendRequest(ctx context.Context, req openAIChatRequest) (*openAIChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", a.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// API key только если установлен (для LM Studio не нужен)
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.apiKey))
	}

	log.Printf("[OPENAI_ADAPTER] Sending request to %s (model: %s)", url, a.model)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &chatResp, nil
}

func (a *OpenAIAdapter) parseResponse(resp *openAIChatResponse) *Response {
	if len(resp.Choices) == 0 {
		return &Response{
			Text:         "",
			FinishReason: "error",
		}
	}

	choice := resp.Choices[0]
	result := &Response{
		Text:         choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage: &Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	// Проверяем есть ли tool calls
	if len(choice.Message.ToolCalls) > 0 {
		toolCall := choice.Message.ToolCalls[0]

		// Парсим arguments из JSON string
		var args map[string]interface{}
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
			log.Printf("[OPENAI_ADAPTER] Failed to parse tool arguments: %v", err)
			args = make(map[string]interface{})
		}

		result.FunctionCall = &FunctionCall{
			Name:      toolCall.Function.Name,
			Arguments: args,
		}
	}

	return result
}
