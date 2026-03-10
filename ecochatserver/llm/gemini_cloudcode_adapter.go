package llm

import (
	"bufio"
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
)

// ============================================================================
// CloudCode Adapter — Gemini через Google Cloud Code Assist (OAuth)
// ============================================================================
//
// Использует cloudcode-pa.googleapis.com вместо generativelanguage.googleapis.com
// Аутентификация через OAuth Bearer token вместо API key

// CloudCodeAdapter реализует Provider для Gemini через Cloud Code Assist OAuth
type CloudCodeAdapter struct {
	model   string
	timeout time.Duration
}

// NewCloudCodeAdapter создаёт адаптер для Cloud Code Assist
func NewCloudCodeAdapter(model string, timeout time.Duration) *CloudCodeAdapter {
	if model == "" {
		model = "gemini-2.5-flash"
	}
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &CloudCodeAdapter{model: model, timeout: timeout}
}

func (a *CloudCodeAdapter) GetName() string {
	return fmt.Sprintf("Gemini OAuth (%s)", a.model)
}

// ============================================================================
// CloudCode Request/Response types
// ============================================================================

type ccPart struct {
	Text         string                 `json:"text,omitempty"`
	FunctionCall *ccFunctionCall        `json:"functionCall,omitempty"`
}

type ccFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type ccContent struct {
	Role  string   `json:"role"`
	Parts []ccPart `json:"parts"`
}

type ccRequest struct {
	Project string `json:"project"`
	Model   string `json:"model"`
	Request struct {
		Contents          []ccContent    `json:"contents"`
		SystemInstruction *ccContent     `json:"systemInstruction,omitempty"`
		GenerationConfig  *ccGenConfig   `json:"generationConfig,omitempty"`
		Tools             []ccTool       `json:"tools,omitempty"`
	} `json:"request"`
}

type ccGenConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

type ccTool struct {
	FunctionDeclarations []ccFunctionDecl `json:"functionDeclarations"`
}

type ccFunctionDecl struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

type ccResponseChunk struct {
	Response struct {
		Candidates []struct {
			Content struct {
				Role  string   `json:"role"`
				Parts []ccPart `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	} `json:"response"`
}

// ============================================================================
// Provider Interface Implementation
// ============================================================================

func (a *CloudCodeAdapter) getCredentials() (*GeminiOAuthCredentials, error) {
	creds := LoadGeminiOAuthCredentials()
	if creds == nil || creds.AccessToken == "" {
		return nil, fmt.Errorf("Gemini OAuth not connected — please connect via admin panel")
	}
	// Auto-refresh если истёк
	return EnsureValidToken(creds)
}

func (a *CloudCodeAdapter) GenerateResponse(
	ctx context.Context, userMessage string, chatHistory []Message, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, nil, opts)
}

func (a *CloudCodeAdapter) GenerateWithTools(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	return a.doRequest(ctx, userMessage, chatHistory, tools, opts)
}

func (a *CloudCodeAdapter) ContinueWithFunctionResult(
	ctx context.Context, chatHistory []Message, functionCall *FunctionCall, result string, opts *GenerateOptions,
) (*Response, error) {
	// Добавляем function result как user message
	history := append(chatHistory, Message{
		Role:    "function",
		Content: fmt.Sprintf("Function %s result: %s", functionCall.Name, result),
	})
	return a.doRequest(ctx, "", history, nil, opts)
}

func (a *CloudCodeAdapter) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
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

func (a *CloudCodeAdapter) DetectAndTranslate(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
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

func (a *CloudCodeAdapter) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
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

func (a *CloudCodeAdapter) doRequest(
	ctx context.Context, userMessage string, chatHistory []Message, tools []Tool, opts *GenerateOptions,
) (*Response, error) {
	creds, err := a.getCredentials()
	if err != nil {
		return nil, err
	}

	// Строим CloudCode request
	req := ccRequest{
		Project: creds.ProjectID,
		Model:   a.model,
	}

	// System prompt
	systemPrompt := ""
	if opts != nil && opts.SystemPrompt != "" {
		systemPrompt = opts.SystemPrompt
	}
	if systemPrompt != "" {
		req.Request.SystemInstruction = &ccContent{
			Parts: []ccPart{{Text: systemPrompt}},
		}
	}

	// Конвертируем историю + новое сообщение в contents
	contents := make([]ccContent, 0, len(chatHistory)+1)
	for _, msg := range chatHistory {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			continue // system в systemInstruction
		}
		contents = append(contents, ccContent{
			Role:  role,
			Parts: []ccPart{{Text: msg.Content}},
		})
	}
	if userMessage != "" {
		contents = append(contents, ccContent{
			Role:  "user",
			Parts: []ccPart{{Text: userMessage}},
		})
	}
	req.Request.Contents = contents

	// Generation config
	if opts != nil {
		req.Request.GenerationConfig = &ccGenConfig{
			Temperature:     opts.Temperature,
			MaxOutputTokens: opts.MaxTokens,
		}
	}

	// Tools
	if len(tools) > 0 {
		decls := make([]ccFunctionDecl, len(tools))
		for i, t := range tools {
			decls[i] = ccFunctionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			}
		}
		req.Request.Tools = []ccTool{{FunctionDeclarations: decls}}
	}

	// HTTP request with retry logic
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := geminiCodeAssistURL + "/v1internal:streamGenerateContent?alt=sse"
	client := &http.Client{Timeout: a.timeout}

	const maxRetries = 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		httpReq.Header.Set("Authorization", "Bearer "+creds.AccessToken)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Accept", "text/event-stream")
		httpReq.Header.Set("User-Agent", "google-cloud-sdk vscode_cloudshelleditor/0.1")
		httpReq.Header.Set("X-Goog-Api-Client", "gl-node/22.17.0")

		resp, err := client.Do(httpReq)
		if err != nil {
			if attempt < maxRetries {
				log.Printf("[CLOUDCODE] Request error (attempt %d/%d): %v", attempt+1, maxRetries+1, err)
				sleepWithContext(ctx, backoffDelay(attempt))
				continue
			}
			return nil, fmt.Errorf("CloudCode request: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			result, err := a.parseSSEResponse(resp.Body, opts)
			resp.Body.Close()
			return result, err
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		errMsg := string(body)

		// 401 Unauthorized → refresh token and retry once
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			log.Printf("[CLOUDCODE] 401 Unauthorized, refreshing token...")
			refreshed, refreshErr := RefreshGeminiOAuthToken(creds.RefreshToken)
			if refreshErr != nil {
				return nil, fmt.Errorf("CloudCode 401 + refresh failed: %w", refreshErr)
			}
			creds.AccessToken = refreshed.AccessToken
			creds.RefreshToken = refreshed.RefreshToken
			creds.Expires = refreshed.Expires
			_ = SaveGeminiOAuthCredentials(creds)
			continue
		}

		// 429 Too Many Requests or 5xx — retry with backoff
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < maxRetries {
			log.Printf("[CLOUDCODE] %d error (attempt %d/%d): %s", resp.StatusCode, attempt+1, maxRetries+1, errMsg)

			// VPC-SC detection
			if strings.Contains(errMsg, "SECURITY_POLICY_VIOLATED") {
				return nil, fmt.Errorf("CloudCode VPC Service Controls error: access blocked by security policy")
			}

			sleepWithContext(ctx, backoffDelay(attempt))
			continue
		}

		return nil, fmt.Errorf("CloudCode API error (%d): %s", resp.StatusCode, errMsg)
	}

	return nil, fmt.Errorf("CloudCode request failed after %d retries", maxRetries+1)
}

func (a *CloudCodeAdapter) parseSSEResponse(reader io.Reader, opts *GenerateOptions) (*Response, error) {
	scanner := bufio.NewScanner(reader)
	// Увеличиваем буфер для больших ответов
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var textParts []string
	var funcCall *FunctionCall
	var finishReason string
	var usage *Usage

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk ccResponseChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Printf("[CLOUDCODE] Failed to parse SSE chunk: %v", err)
			continue
		}

		// Extract content from candidates
		for _, candidate := range chunk.Response.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					textParts = append(textParts, part.Text)
				}
				if part.FunctionCall != nil && funcCall == nil {
					funcCall = &FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: part.FunctionCall.Args,
					}
				}
			}
			if candidate.FinishReason != "" {
				finishReason = candidate.FinishReason
			}
		}

		// Usage from last chunk
		meta := chunk.Response.UsageMetadata
		if meta.TotalTokenCount > 0 {
			usage = &Usage{
				PromptTokens:     meta.PromptTokenCount,
				CompletionTokens: meta.CandidatesTokenCount,
				TotalTokens:      meta.TotalTokenCount,
			}
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
			Provider:         "gemini-oauth",
			Model:            a.model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			RequestType:      "chat",
		})
	}

	return result, nil
}

// ============================================================================
// Retry helpers
// ============================================================================

// backoffDelay возвращает задержку с экспоненциальным backoff: 1s, 2s, 4s...
func backoffDelay(attempt int) time.Duration {
	return time.Duration(math.Pow(2, float64(attempt))) * time.Second
}

// sleepWithContext спит duration или до отмены контекста
func sleepWithContext(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}
