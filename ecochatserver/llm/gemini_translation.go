package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// TranslateText переводит текст с одного языка на другой
func (c *GeminiClient) TranslateText(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
		return text, nil
	}

	fromName := LangDisplayName(fromLang)
	toName := LangDisplayName(toLang)

	prompt := fmt.Sprintf(`Translate from %s to %s: "%s"`, fromName, toName, text)

	var result string
	err := withRetry(ctx, 3, "TranslateText", func(attempt int) error {
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
		endpoint := c.getEndpoint()

		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", c.apiKey)

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
		}

		var geminiResp GeminiResponse
		json.NewDecoder(resp.Body).Decode(&geminiResp)

		logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation")

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			if t, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok && t != "" {
				result = strings.Trim(strings.TrimSpace(t), "\"'")
				return nil
			}
		}

		return fmt.Errorf("empty or invalid response")
	})

	return result, err
}

// geminiDetectAndTranslateResult внутренний результат определения языка и перевода для Gemini
// (используется только внутри GeminiClient, для публичного API используйте TranslationResult)
type geminiDetectAndTranslateResult struct {
	DetectedLang string
	Translation  string
}

// DetectAndTranslate определяет язык и переводит текст за ОДИН запрос (быстрее)
func (c *GeminiClient) DetectAndTranslate(ctx context.Context, text, targetLang string) (*geminiDetectAndTranslateResult, error) {
	if text == "" {
		return nil, fmt.Errorf("текст для перевода пуст")
	}

	targetName := LangDisplayName(targetLang)

	prompt := fmt.Sprintf(`Determine the language of the text and translate it to %s.

Text: "%s"

Reply in JSON format:
{
  "detectedLang": "two-letter language code (ru, en, pl, etc.)",
  "translation": "translated text"
}

If text is already in %s, return it as translation with detected language.`, targetName, text, targetName)

	var result *geminiDetectAndTranslateResult
	err := withRetry(ctx, 3, "DetectAndTranslate", func(attempt int) error {
		reqBody := GeminiRequest{
			Contents: []GeminiMessage{{
				Role:  "user",
				Parts: []map[string]interface{}{{"text": prompt}},
			}},
			SystemInstruction: &GeminiSystemInstruction{
				Parts: []map[string]interface{}{{"text": "You are a language detection and translation expert. Always reply in valid JSON format only."}},
			},
			GenerationConfig: map[string]interface{}{
				"temperature":     0.3,
				"maxOutputTokens": 2048,
			},
		}

		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}

		endpoint := c.getEndpoint()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", c.apiKey)

		resp, err := c.client.Do(req)
		if err != nil {
			return fmt.Errorf("API request failed: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
		}

		var geminiResp GeminiResponse
		if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
			resp.Body.Close()
			return fmt.Errorf("decode response: %w", err)
		}
		resp.Body.Close()

		if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
			return fmt.Errorf("empty response from Gemini")
		}

		logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation")

		responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
		if !ok {
			return fmt.Errorf("invalid response format")
		}

		responseText = CleanJSONResponse(responseText)

		var parsed geminiDetectAndTranslateResult
		if err := json.Unmarshal([]byte(responseText), &parsed); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}

		parsed.DetectedLang = strings.ToLower(strings.TrimSpace(parsed.DetectedLang))
		result = &parsed
		return nil
	})

	return result, err
}

// TranslateBatchResponse структура JSON ответа для batch перевода
type TranslateBatchResponse struct {
	Translations []string `json:"translations"`
}

// TranslateBatch переводит несколько текстов за один запрос (более эффективно)
func (c *GeminiClient) TranslateBatch(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	fromName := LangDisplayName(fromLang)
	toName := LangDisplayName(toLang)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Translate these %d messages from %s to %s.\n\n", len(texts), fromName, toName))
	sb.WriteString("Messages to translate:\n")
	for i, text := range texts {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, text))
	}
	sb.WriteString("\nReply in this exact JSON format:\n")
	sb.WriteString("{\n  \"translations\": [\"translation 1\", \"translation 2\", ...]\n}")

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
				{"text": "You are a translation expert. Always reply with valid JSON only, no additional text or markdown."},
			},
		},
		GenerationConfig: map[string]interface{}{
			"temperature":     0.3,
			"maxOutputTokens": 4096,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := c.getEndpoint()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

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

	logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation")

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	responseText = CleanJSONResponse(responseText)

	var batchResult TranslateBatchResponse
	if err := json.Unmarshal([]byte(responseText), &batchResult); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return NormalizeBatchTranslations(batchResult.Translations, texts), nil
}
