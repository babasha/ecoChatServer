package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/egor/ecochatserver/database"
)

// ============================================================================
// Claude OAuth Adapter — Claude через Anthropic OAuth (Pro/Max подписка)
// ============================================================================
//
// Использует стандартный Anthropic Messages API: POST /v1/messages
// Аутентификация через OAuth Bearer token (sk-ant-oat-...)
// Требует Claude Code identity headers для OAuth токенов.
// Поддерживает Extended Thinking (adaptive/budget-based).

const (
	claudeAPIBaseURL  = "https://api.anthropic.com"
	claudeAPIVersion  = "2023-06-01"
	claudeCodeVersion = "2.1.62"
	claudeOAuthBeta   = "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"
)

// ClaudeOAuthAdapter реализует Provider для Claude через OAuth
type ClaudeOAuthAdapter struct {
	model   string
	timeout time.Duration
}

// NewClaudeOAuthAdapter создаёт адаптер для Claude OAuth
func NewClaudeOAuthAdapter(model string, timeout time.Duration) *ClaudeOAuthAdapter {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &ClaudeOAuthAdapter{model: model, timeout: timeout}
}

func (a *ClaudeOAuthAdapter) GetName() string {
	return fmt.Sprintf("Claude OAuth (%s)", a.model)
}

// supportsAdaptiveThinking checks if model supports adaptive thinking (4.6 models)
func supportsAdaptiveThinking(modelID string) bool {
	return strings.Contains(modelID, "opus-4-6") ||
		strings.Contains(modelID, "opus-4.6") ||
		strings.Contains(modelID, "sonnet-4-6") ||
		strings.Contains(modelID, "sonnet-4.6")
}

// getThinkingConfig reads thinking settings from DB
func getThinkingConfig() (enabled bool, effort string, budgetTokens int) {
	enabled = database.GetSettingBool("CLAUDE_OAUTH_THINKING_ENABLED", false)
	effort = database.GetSetting("CLAUDE_OAUTH_THINKING_EFFORT", "")
	budgetTokens = database.GetSettingInt("CLAUDE_OAUTH_THINKING_BUDGET", 8192)
	return
}

// ============================================================================
// Anthropic Messages API types
// ============================================================================

type claudeRequest struct {
	Model        string              `json:"model"`
	MaxTokens    int                 `json:"max_tokens"`
	Messages     []claudeMessage     `json:"messages"`
	System       []claudeSystemBlock `json:"system,omitempty"`
	Tools        []claudeTool        `json:"tools,omitempty"`
	Temperature  *float64            `json:"temperature,omitempty"`
	Thinking     *claudeThinking     `json:"thinking,omitempty"`
	OutputConfig *claudeOutputConfig `json:"output_config,omitempty"`
}

// claudeThinking — настройка extended thinking
type claudeThinking struct {
	Type         string `json:"type"`                    // "enabled", "adaptive", "disabled"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // только для type="enabled"
}

// claudeOutputConfig — настройка effort для adaptive thinking
type claudeOutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low", "medium", "high", "max"
}

type claudeSystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type claudeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string или []claudeContentBlock
}

type claudeContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
}

type claudeTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type claudeResponse struct {
	ID           string                `json:"id"`
	Type         string                `json:"type"`
	Role         string                `json:"role"`
	Content      []claudeResponseBlock `json:"content"`
	Model        string                `json:"model"`
	StopReason   string                `json:"stop_reason"`
	StopSequence *string               `json:"stop_sequence"`
	Usage        *claudeUsage          `json:"usage"`
}

type claudeResponseBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	Thinking  string      `json:"thinking,omitempty"`  // для thinking блоков
	Signature string      `json:"signature,omitempty"` // подпись thinking блока
	Data      string      `json:"data,omitempty"`      // для redacted_thinking
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type claudeErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// ============================================================================
// Provider Interface Implementation
// ============================================================================

func (a *ClaudeOAuthAdapter) getCredentials() (*ClaudeOAuthCredentials, error) {
	creds := LoadClaudeOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return nil, fmt.Errorf("Claude OAuth not connected — please connect via admin panel")
	}
	return EnsureValidClaudeToken(creds)
}

func (a *ClaudeOAuthAdapter) GenerateResponse(
	ctx context.Context, userMessage string, chatHistory []Message, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, nil, opts)
}

func (a *ClaudeOAuthAdapter) GenerateWithTools(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, tools, opts)
}

func (a *ClaudeOAuthAdapter) ContinueWithFunctionResult(
	ctx context.Context, chatHistory []Message, functionCall *FunctionCall, result string, opts *GenerateOptions,
) (*Response, error) {
	history := append(chatHistory, Message{
		Role:    "function",
		Content: fmt.Sprintf("Function %s result: %s", functionCall.Name, result),
	})
	return a.doRequest(ctx, "", history, nil, opts)
}

func (a *ClaudeOAuthAdapter) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	prompt := fmt.Sprintf(
		"Translate the following text from %s to %s. Return ONLY the translated text, no explanations:\n\n%s",
		fromLang, toLang, text,
	)
	// Перевод — простая задача, thinking не нужен
	resp, err := a.GenerateResponse(ctx, prompt, nil, &GenerateOptions{Temperature: 0.3, MaxTokens: 2000})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (a *ClaudeOAuthAdapter) DetectAndTranslate(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
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

func (a *ClaudeOAuthAdapter) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
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

func (a *ClaudeOAuthAdapter) doRequest(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	creds, err := a.getCredentials()
	if err != nil {
		return nil, err
	}

	if opts == nil {
		opts = &GenerateOptions{}
	}

	// Читаем thinking настройки из DB если не указаны явно в opts
	if !opts.ThinkingEnabled {
		dbEnabled, dbEffort, dbBudget := getThinkingConfig()
		if dbEnabled {
			opts.ThinkingEnabled = true
			if opts.ThinkingEffort == "" {
				opts.ThinkingEffort = dbEffort
			}
			if opts.ThinkingBudgetTokens == 0 {
				opts.ThinkingBudgetTokens = dbBudget
			}
		}
	}

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, statusCode, err := a.executeRequest(ctx, creds, userMessage, chatHistory, tools, opts)
		if err != nil {
			lastErr = err

			// 401 → refresh token and retry
			if statusCode == 401 && attempt < maxRetries {
				log.Printf("[CLAUDE_OAUTH] 401 Unauthorized, refreshing token...")
				refreshed, refreshErr := RefreshClaudeOAuthToken(creds.RefreshToken)
				if refreshErr != nil {
					return nil, fmt.Errorf("token refresh failed: %w", refreshErr)
				}
				creds.AccessToken = refreshed.AccessToken
				creds.RefreshToken = refreshed.RefreshToken
				creds.Expires = refreshed.Expires
				_ = SaveClaudeOAuthCredentials(creds)
				continue
			}

			// 429 or 5xx → retry with backoff
			if (statusCode == 429 || statusCode >= 500) && attempt < maxRetries {
				log.Printf("[CLAUDE_OAUTH] %d error, retrying (attempt %d/%d)...", statusCode, attempt+1, maxRetries)
				continue
			}

			return nil, err
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (a *ClaudeOAuthAdapter) executeRequest(
	ctx context.Context, creds *ClaudeOAuthCredentials,
	userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, int, error) {
	// Build messages
	var messages []claudeMessage

	for _, msg := range chatHistory {
		switch msg.Role {
		case "system":
			continue // handled in system blocks
		case "user":
			messages = append(messages, claudeMessage{Role: "user", Content: msg.Content})
		case "assistant":
			messages = append(messages, claudeMessage{Role: "assistant", Content: msg.Content})
		case "function":
			// Anthropic expects tool results as user messages with tool_result blocks
			// For simple chat (no tools), append as user message
			messages = append(messages, claudeMessage{Role: "user", Content: msg.Content})
		}
	}

	if userMessage != "" {
		messages = append(messages, claudeMessage{Role: "user", Content: userMessage})
	}

	if len(messages) == 0 {
		return nil, 0, fmt.Errorf("no messages to send")
	}

	// Build system blocks — MUST include Claude Code identity for OAuth tokens
	var systemBlocks []claudeSystemBlock
	systemBlocks = append(systemBlocks, claudeSystemBlock{
		Type: "text",
		Text: "You are Claude Code, Anthropic's official CLI for Claude.",
	})
	if opts.SystemPrompt != "" {
		systemBlocks = append(systemBlocks, claudeSystemBlock{
			Type: "text",
			Text: opts.SystemPrompt,
		})
	}

	// Build request
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	reqBody := claudeRequest{
		Model:     a.model,
		MaxTokens: maxTokens,
		Messages:  messages,
		System:    systemBlocks,
	}

	// Extended Thinking
	if opts.ThinkingEnabled {
		if supportsAdaptiveThinking(a.model) {
			// Adaptive thinking для 4.6 моделей
			reqBody.Thinking = &claudeThinking{Type: "adaptive"}
			if opts.ThinkingEffort != "" {
				reqBody.OutputConfig = &claudeOutputConfig{Effort: opts.ThinkingEffort}
			}
			log.Printf("[CLAUDE_OAUTH] Adaptive thinking enabled (effort=%s)", opts.ThinkingEffort)
		} else {
			// Budget-based thinking для старых моделей
			budget := opts.ThinkingBudgetTokens
			if budget < 1024 {
				budget = 1024
			}
			// budget_tokens должен быть < max_tokens
			if budget >= maxTokens {
				reqBody.MaxTokens = budget + 4096
			}
			reqBody.Thinking = &claudeThinking{
				Type:         "enabled",
				BudgetTokens: budget,
			}
			log.Printf("[CLAUDE_OAUTH] Budget thinking enabled (budget=%d)", budget)
		}
		// Temperature НЕЛЬЗЯ устанавливать при thinking (должна быть 1)
	} else {
		// Temperature только без thinking
		if opts.Temperature > 0 {
			temp := opts.Temperature
			reqBody.Temperature = &temp
		}
	}

	// Add tools
	if len(tools) > 0 {
		for _, t := range tools {
			reqBody.Tools = append(reqBody.Tools, claudeTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.Parameters,
			})
		}
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}

	// Build HTTP request
	reqCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST",
		claudeAPIBaseURL+"/v1/messages", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}

	// Build beta header — add interleaved-thinking for non-adaptive models
	betaHeader := claudeOAuthBeta
	if opts.ThinkingEnabled && !supportsAdaptiveThinking(a.model) {
		betaHeader += ",interleaved-thinking-2025-05-14"
	}

	// OAuth headers — must mimic Claude Code
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("anthropic-version", claudeAPIVersion)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", "claude-cli/"+claudeCodeVersion)
	req.Header.Set("x-app", "cli")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Parse error message
		var errResp claudeErrorResponse
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			return nil, resp.StatusCode, fmt.Errorf("Claude API error (%d): %s — %s",
				resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
		}
		return nil, resp.StatusCode, fmt.Errorf("Claude API error (%d): %s",
			resp.StatusCode, string(body))
	}

	// Parse response
	var claudeResp claudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, 0, fmt.Errorf("parse response: %w", err)
	}

	// Convert to universal Response
	result := a.convertResponse(&claudeResp, opts)

	return result, resp.StatusCode, nil
}

func (a *ClaudeOAuthAdapter) convertResponse(resp *claudeResponse, opts *GenerateOptions) *Response {
	var textParts []string
	var thinkingParts []string
	var funcCall *FunctionCall

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			// Extended thinking блок — содержит reasoning
			if block.Thinking != "" {
				thinkingParts = append(thinkingParts, block.Thinking)
			}
		case "redacted_thinking":
			// Редактированный thinking — заменяем placeholder
			thinkingParts = append(thinkingParts, "[Reasoning redacted]")
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			// Parse tool input
			args := make(map[string]interface{})
			if inputMap, ok := block.Input.(map[string]interface{}); ok {
				args = inputMap
			} else if inputBytes, err := json.Marshal(block.Input); err == nil {
				json.Unmarshal(inputBytes, &args)
			}
			funcCall = &FunctionCall{
				Name:      block.Name,
				Arguments: args,
			}
		}
	}

	finishReason := "stop"
	switch resp.StopReason {
	case "end_turn":
		finishReason = "stop"
	case "max_tokens":
		finishReason = "length"
	case "tool_use":
		finishReason = "function_call"
	}

	var usage *Usage
	if resp.Usage != nil {
		usage = &Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		}
	}

	// Log usage
	if usage != nil {
		_ = LogUsage(context.Background(), UsageLogEntry{
			ClientID:         opts.GetClientID(),
			ChatID:           opts.GetChatID(),
			Provider:         "claude-oauth",
			Model:            a.model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			RequestType:      "chat",
		})
	}

	// Если есть thinking, добавляем его в начало ответа для видимости
	text := strings.Join(textParts, "")
	if len(thinkingParts) > 0 {
		thinking := strings.Join(thinkingParts, "\n")
		log.Printf("[CLAUDE_OAUTH] Thinking (%d chars): %s...",
			len(thinking), truncate(thinking, 100))
	}

	return &Response{
		Text:         text,
		FunctionCall: funcCall,
		FinishReason: finishReason,
		Usage:        usage,
	}
}

// truncate обрезает строку до maxLen символов
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
