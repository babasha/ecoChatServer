package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
)

// ============================================================================
// OpenAI Codex Adapter — ChatGPT через OAuth (Responses API)
// ============================================================================
//
// Использует chatgpt.com/backend-api/codex/responses (Responses API)
// Аутентификация через OAuth Bearer token + chatgpt-account-id из JWT

// OpenAICodexAdapter реализует Provider для ChatGPT через Codex OAuth
type OpenAICodexAdapter struct {
	model   string
	timeout time.Duration
}

// NewOpenAICodexAdapter создаёт адаптер для ChatGPT Codex
func NewOpenAICodexAdapter(model string, timeout time.Duration) *OpenAICodexAdapter {
	if model == "" || strings.Contains(model, "@") {
		model = "gpt-5.2-codex"
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &OpenAICodexAdapter{model: model, timeout: timeout}
}

func (a *OpenAICodexAdapter) GetName() string {
	return fmt.Sprintf("ChatGPT OAuth (%s)", a.model)
}

// ============================================================================
// Codex Responses API Request/Response types
// ============================================================================

type codexRequestBody struct {
	Model              string           `json:"model"`
	Store              bool             `json:"store"`
	Stream             bool             `json:"stream"`
	Instructions       string           `json:"instructions,omitempty"`
	Input              json.RawMessage  `json:"input"`
	Tools              []codexTool      `json:"tools,omitempty"`
	ToolChoice         string           `json:"tool_choice,omitempty"`
	ParallelToolCalls  bool             `json:"parallel_tool_calls,omitempty"`
	Temperature        *float64         `json:"temperature,omitempty"`
	Text               *codexTextConfig `json:"text,omitempty"`
	Include            []string         `json:"include,omitempty"`
	Reasoning          *codexReasoning  `json:"reasoning,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexTextConfig struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type codexTool struct {
	Type     string          `json:"type"`
	Function *codexFunction  `json:"function,omitempty"`
}

type codexFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// Responses API input items
type codexInputItem struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// SSE event from Codex Responses API
type codexSSEEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`

	// For response.completed
	ResponseObj *codexResponseObj `json:"-"`
}

type codexResponseObj struct {
	ID      string               `json:"id"`
	Status  string               `json:"status"`
	Output  []codexOutputItem    `json:"output"`
	Usage   *codexUsage          `json:"usage"`
	Error   *codexError          `json:"error"`
}

type codexOutputItem struct {
	Type    string              `json:"type"`
	ID      string              `json:"id"`
	Content []codexContentPart  `json:"content,omitempty"`
	// For function_call output type
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type codexContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type codexError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ============================================================================
// Provider Interface Implementation
// ============================================================================

func (a *OpenAICodexAdapter) getCredentials() (*OpenAIOAuthCredentials, error) {
	creds := LoadOpenAIOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return nil, fmt.Errorf("ChatGPT OAuth not connected — please connect via admin panel")
	}
	return EnsureValidOpenAIToken(creds)
}

func (a *OpenAICodexAdapter) GenerateResponse(
	ctx context.Context, userMessage string, chatHistory []Message, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, nil, opts)
}

func (a *OpenAICodexAdapter) GenerateWithTools(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, tools, opts)
}

func (a *OpenAICodexAdapter) ContinueWithFunctionResult(
	ctx context.Context, chatHistory []Message, functionCall *FunctionCall, result string, opts *GenerateOptions,
) (*Response, error) {
	history := append(chatHistory, Message{
		Role:    "function",
		Content: fmt.Sprintf("Function %s result: %s", functionCall.Name, result),
	})
	return a.doRequest(ctx, "", history, nil, opts)
}

func (a *OpenAICodexAdapter) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	prompt := fmt.Sprintf(
		"Translate the following text from %s to %s. Return ONLY the translated text, no explanations:\n\n%s",
		fromLang, toLang, text,
	)
	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{Temperature: 0.3, MaxTokens: 2000})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (a *OpenAICodexAdapter) DetectAndTranslate(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	prompt := fmt.Sprintf(`Detect the language of the text and translate it to %s.
Return ONLY this JSON (no markdown, no code blocks):
{"detected_language":"language_code","translated_text":"translated text here"}

Text: %s`, targetLang, text)

	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{Temperature: 0.3, MaxTokens: 2000})
	if err != nil {
		return nil, err
	}

	cleaned := CleanJSONResponse(resp.Text)
	var result struct {
		DetectedLanguage string `json:"detected_language"`
		TranslatedText   string `json:"translated_text"`
	}
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse translation JSON: %w", err)
	}

	return &TranslationResult{
		DetectedLang: result.DetectedLanguage,
		Translation:  result.TranslatedText,
	}, nil
}

func (a *OpenAICodexAdapter) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	textsJSON, _ := json.Marshal(texts)
	prompt := fmt.Sprintf(`Translate the following texts from %s to %s.
Return a JSON array with translated texts in the same order.
Format: ["translation1", "translation2", ...]

Texts to translate:
%s`, fromLang, toLang, string(textsJSON))

	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{Temperature: 0.3, MaxTokens: 4000})
	if err != nil {
		return nil, err
	}

	cleaned := CleanJSONResponse(resp.Text)
	var translations []string
	if err := json.Unmarshal([]byte(cleaned), &translations); err != nil {
		return nil, fmt.Errorf("parse batch translation: %w", err)
	}
	return translations, nil
}

// ============================================================================
// Core Request Logic
// ============================================================================

func (a *OpenAICodexAdapter) doRequest(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	creds, err := a.getCredentials()
	if err != nil {
		return nil, err
	}

	// Строим input (Responses API format)
	var inputItems []codexInputItem

	for _, msg := range chatHistory {
		role := msg.Role
		if role == "system" {
			continue // system -> instructions
		}
		if role == "function" {
			role = "user" // function results as user messages
		}
		inputItems = append(inputItems, codexInputItem{
			Type:    "message",
			Role:    role,
			Content: msg.Content,
		})
	}
	if userMessage != "" {
		inputItems = append(inputItems, codexInputItem{
			Type:    "message",
			Role:    "user",
			Content: userMessage,
		})
	}

	inputJSON, err := json.Marshal(inputItems)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	body := codexRequestBody{
		Model:   a.model,
		Store:   false,
		Stream:  true,
		Input:   inputJSON,
		Include: []string{"reasoning.encrypted_content"},
	}

	// Text verbosity — gpt-5.2-codex поддерживает только "medium"
	textVerbosity := database.GetSetting("OPENAI_OAUTH_TEXT_VERBOSITY", "medium")
	if !isReasoningModel(a.model) {
		body.Text = &codexTextConfig{Verbosity: textVerbosity}
	}

	// Reasoning support — read from DB settings
	reasoningEffort := database.GetSetting("OPENAI_OAUTH_REASONING_EFFORT", "")
	reasoningSummary := database.GetSetting("OPENAI_OAUTH_REASONING_SUMMARY", "auto")
	if reasoningEffort != "" || isReasoningModel(a.model) {
		effort := reasoningEffort
		if effort == "" {
			effort = "medium" // default для reasoning models
		}
		summary := reasoningSummary
		if summary == "" {
			summary = "auto"
		}
		body.Reasoning = &codexReasoning{
			Effort:  effort,
			Summary: summary,
		}
	}

	// System prompt -> instructions
	if opts != nil && opts.SystemPrompt != "" {
		body.Instructions = opts.SystemPrompt
	}

	// Temperature (не поддерживается reasoning моделями gpt-5.x, o-series, codex)
	if opts != nil && opts.Temperature > 0 && !isReasoningModel(a.model) {
		body.Temperature = &opts.Temperature
	}

	// Tools
	if len(tools) > 0 {
		body.ToolChoice = "auto"
		body.ParallelToolCalls = true
		for _, t := range tools {
			body.Tools = append(body.Tools, codexTool{
				Type: "function",
				Function: &codexFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	}

	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := openaiCodexBaseURL + "/codex/responses"
	client := &http.Client{Timeout: a.timeout}

	const maxRetries = 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		httpReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		httpReq.Header.Set("chatgpt-account-id", creds.AccountID)
		httpReq.Header.Set("OpenAI-Beta", "responses=experimental")
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("User-Agent", "ecochat (linux)")
		httpReq.Header.Set("originator", "ecochat")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxRetries {
				log.Printf("[CODEX] Request error (attempt %d/%d): %v", attempt+1, maxRetries+1, err)
				sleepWithContext(ctx, backoffDelay(attempt))
				continue
			}
			return nil, fmt.Errorf("Codex request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			result, err := a.parseCodexSSEResponse(resp.Body, opts)
			resp.Body.Close()
			return result, err
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		errMsg := string(respBody)

		// 401 → refresh token and retry
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			log.Printf("[CODEX] 401 Unauthorized, refreshing token...")
			refreshed, refreshErr := RefreshOpenAIOAuthToken(creds.RefreshToken)
			if refreshErr != nil {
				return nil, fmt.Errorf("Codex 401 + refresh failed: %w", refreshErr)
			}
			creds.AccessToken = refreshed.AccessToken
			creds.RefreshToken = refreshed.RefreshToken
			creds.Expires = refreshed.Expires
			if accountID, aErr := extractAccountIDFromJWT(refreshed.AccessToken); aErr == nil {
				creds.AccountID = accountID
			}
			_ = SaveOpenAIOAuthCredentials(creds)
			continue
		}

		// 429 or 5xx → retry with backoff
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxRetries {
			log.Printf("[CODEX] %d error (attempt %d/%d): %s", resp.StatusCode, attempt+1, maxRetries+1, errMsg)

			// Parse usage limit error for friendly message
			if resp.StatusCode == http.StatusTooManyRequests {
				if friendly := parseCodexUsageLimitError(respBody); friendly != "" {
					return nil, fmt.Errorf("ChatGPT usage limit: %s", friendly)
				}
			}

			sleepWithContext(ctx, backoffDelay(attempt))
			continue
		}

		return nil, fmt.Errorf("Codex API error (%d): %s", resp.StatusCode, errMsg)
	}

	return nil, fmt.Errorf("Codex request failed after %d retries", maxRetries+1)
}

// parseCodexUsageLimitError извлекает user-friendly сообщение об ошибке лимита
func parseCodexUsageLimitError(body []byte) string {
	var parsed struct {
		Error struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			PlanType string `json:"plan_type"`
			ResetsAt int64  `json:"resets_at"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}

	e := parsed.Error
	if e.Code == "" && e.Message == "" {
		return ""
	}

	plan := ""
	if e.PlanType != "" {
		plan = fmt.Sprintf(" (%s plan)", strings.ToLower(e.PlanType))
	}

	when := ""
	if e.ResetsAt > 0 {
		mins := (e.ResetsAt*1000 - time.Now().UnixMilli()) / 60000
		if mins < 0 {
			mins = 0
		}
		when = fmt.Sprintf(" Try again in ~%d min.", mins)
	}

	if e.Message != "" {
		return e.Message
	}

	return fmt.Sprintf("You have hit your ChatGPT usage limit%s.%s", plan, when)
}

// ============================================================================
// SSE Response Parsing — Responses API format
// ============================================================================

func (a *OpenAICodexAdapter) parseCodexSSEResponse(reader io.Reader, opts *GenerateOptions) (*Response, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var textParts []string
	var funcCall *FunctionCall
	var funcCallArgs strings.Builder
	var funcCallName string
	var finishReason string
	var usage *Usage

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue // Codex API may not send [DONE], just continue parsing till EOF
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("[CODEX] Failed to parse SSE event: %v", err)
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "response.output_text.delta":
			if delta, ok := event["delta"].(string); ok {
				textParts = append(textParts, delta)
			}

		case "response.function_call_arguments.delta":
			if delta, ok := event["delta"].(string); ok {
				funcCallArgs.WriteString(delta)
			}

		case "response.output_item.added":
			// Function call started
			if item, ok := event["item"].(map[string]interface{}); ok {
				if itemType, _ := item["type"].(string); itemType == "function_call" {
					funcCallName, _ = item["name"].(string)
				}
			}

		case "response.output_item.done":
			// Function call complete — parse accumulated args
			if funcCallName != "" && funcCallArgs.Len() > 0 {
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(funcCallArgs.String()), &args); err == nil {
					funcCall = &FunctionCall{
						Name:      funcCallName,
						Arguments: args,
					}
				}
			}

		case "response.completed", "response.done":
			// Final event with full response
			if respRaw, ok := event["response"].(map[string]interface{}); ok {
				// Extract usage
				if usageRaw, ok := respRaw["usage"].(map[string]interface{}); ok {
					inputTokens, _ := usageRaw["input_tokens"].(float64)
					outputTokens, _ := usageRaw["output_tokens"].(float64)
					totalTokens, _ := usageRaw["total_tokens"].(float64)
					usage = &Usage{
						PromptTokens:     int(inputTokens),
						CompletionTokens: int(outputTokens),
						TotalTokens:      int(totalTokens),
					}
				}

				// Extract status → finish reason
				if status, ok := respRaw["status"].(string); ok {
					switch status {
					case "completed":
						finishReason = "stop"
					case "incomplete":
						finishReason = "length"
					default:
						finishReason = status
					}
				}
			}

		case "error", "response.failed":
			// Error event
			errMsg := "unknown error"
			if msg, ok := event["message"].(string); ok {
				errMsg = msg
			} else if respRaw, ok := event["response"].(map[string]interface{}); ok {
				if errObj, ok := respRaw["error"].(map[string]interface{}); ok {
					if msg, ok := errObj["message"].(string); ok {
						errMsg = msg
					}
				}
			}
			return nil, fmt.Errorf("Codex stream error: %s", errMsg)
		}
	}

	result := &Response{
		Text:         strings.Join(textParts, ""),
		FunctionCall: funcCall,
		FinishReason: finishReason,
		Usage:        usage,
	}

	// Log usage
	if usage != nil {
		_ = LogUsage(context.Background(), UsageLogEntry{
			ClientID:         opts.GetClientID(),
			ChatID:           opts.GetChatID(),
			Provider:         "openai-oauth",
			Model:            a.model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			RequestType:      "chat",
		})
	}

	return result, nil
}

// isReasoningModel проверяет, поддерживает ли модель reasoning (o1, o3, gpt-5+)
func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	return strings.HasPrefix(m, "o1") ||
		strings.HasPrefix(m, "o3") ||
		strings.HasPrefix(m, "o4") ||
		strings.HasPrefix(m, "gpt-5") ||
		strings.Contains(m, "codex")
}
