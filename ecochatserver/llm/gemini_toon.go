package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/egor/ecochatserver/llm/toon"
)

// DetectAndTranslateTOON - TOON версия определения языка и перевода
// Экономит ~45-50% токенов по сравнению с JSON версией
func (c *GeminiClient) DetectAndTranslateTOON(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	if text == "" {
		return nil, fmt.Errorf("текст для перевода пуст")
	}

	prompt := toon.BuildDetectAndTranslatePrompt(text, targetLang)

	var result *TranslationResult
	err := withRetry(ctx, 3, "DetectAndTranslateTOON", func(attempt int) error {
		reqBody := GeminiRequest{
			Contents: []GeminiMessage{{
				Role:  "user",
				Parts: []map[string]interface{}{{"text": prompt}},
			}},
			SystemInstruction: &GeminiSystemInstruction{
				Parts: []map[string]interface{}{{
					"text": toon.SystemInstructionDetectAndTranslate,
				}},
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

		logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation_toon")

		responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
		if !ok {
			return fmt.Errorf("invalid response format")
		}

		responseText = toon.CleanLLMResponse(responseText)
		parsed, err := toon.ParseSimpleObject(responseText)
		if err != nil {
			log.Printf("DetectAndTranslateTOON: raw response: %s", responseText)
			return fmt.Errorf("failed to parse TOON response: %w", err)
		}

		detectedLang := parsed["lang"]
		translation := parsed["text"]

		if detectedLang == "" {
			log.Printf("DetectAndTranslateTOON: WARNING - empty lang in response")
			detectedLang = "unknown"
		}

		result = &TranslationResult{
			DetectedLang: strings.ToLower(strings.TrimSpace(detectedLang)),
			Translation:  translation,
		}
		return nil
	})

	return result, err
}

// TranslateBatchTOON - TOON версия batch перевода
// Экономит ~40% токенов используя простой list формат вместо JSON массива
func (c *GeminiClient) TranslateBatchTOON(ctx context.Context, texts []string, fromLang, toLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	if fromLang == toLang {
		return texts, nil
	}

	prompt := toon.BuildBatchTranslatePrompt(texts, fromLang, toLang)

	reqBody := GeminiRequest{
		Contents: []GeminiMessage{{
			Role:  "user",
			Parts: []map[string]interface{}{{"text": prompt}},
		}},
		SystemInstruction: &GeminiSystemInstruction{
			Parts: []map[string]interface{}{{
				"text": toon.SystemInstructionBatch,
			}},
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

	logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation_batch_toon")

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	responseText = toon.CleanLLMResponse(responseText)
	translations, err := toon.ParseSimpleList(responseText)
	if err != nil {
		log.Printf("TranslateBatchTOON: failed to parse TOON list: %v", err)
		log.Printf("Raw response: %s", responseText)
		return nil, fmt.Errorf("failed to parse TOON response: %w", err)
	}

	return NormalizeBatchTranslations(translations, texts), nil
}

// TranslateTextTOON - TOON версия простого перевода
// Максимальная экономия ~50-60% токенов - вообще без структуры
func (c *GeminiClient) TranslateTextTOON(ctx context.Context, text, fromLang, toLang string) (string, error) {
	if text == "" {
		return "", fmt.Errorf("текст для перевода пуст")
	}

	if fromLang == toLang {
		return text, nil
	}

	prompt := toon.BuildSimpleTranslatePrompt(text, fromLang, toLang)

	var result string
	err := withRetry(ctx, 3, "TranslateTextTOON", func(attempt int) error {
		reqBody := GeminiRequest{
			Contents: []GeminiMessage{{
				Role:  "user",
				Parts: []map[string]interface{}{{"text": prompt}},
			}},
			SystemInstruction: &GeminiSystemInstruction{
				Parts: []map[string]interface{}{{"text": toon.SystemInstructionSimple}},
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

		logGeminiUsage(ctx, &geminiResp, c.getModelName(), "translation_toon")

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			if t, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok && t != "" {
				t = toon.CleanLLMResponse(t)
				result = strings.Trim(strings.TrimSpace(t), "\"'")
				return nil
			}
		}

		return fmt.Errorf("empty or invalid response")
	})

	return result, err
}
