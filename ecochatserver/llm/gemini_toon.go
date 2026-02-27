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
	"time"

	"github.com/egor/ecochatserver/llm/toon"
)

// DetectAndTranslateTOON - TOON версия определения языка и перевода
// Экономит ~45-50% токенов по сравнению с JSON версией
func (c *GeminiClient) DetectAndTranslateTOON(ctx context.Context, text, targetLang string) (*TranslationResult, error) {
	if text == "" {
		return nil, fmt.Errorf("текст для перевода пуст")
	}

	// 🎯 КОМПАКТНЫЙ ПРОМПТ С TOON (используем общий промпт из toon/prompts.go)
	prompt := toon.BuildDetectAndTranslatePrompt(text, targetLang)

	// Retry логика: 3 попытки с экспоненциальной задержкой
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
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
			lastErr = fmt.Errorf("API request failed: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: попытка %d/%d провалилась: %v, повтор через %d сек...", attempt, maxRetries, err, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: попытка %d/%d провалилась: статус %d, повтор через %d сек...", attempt, maxRetries, resp.StatusCode, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		var geminiResp GeminiResponse
		if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
			resp.Body.Close()
			lastErr = fmt.Errorf("decode response: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: попытка %d/%d провалилась: ошибка декодирования, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}
		resp.Body.Close()

		if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
			lastErr = fmt.Errorf("empty response from Gemini")
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: попытка %d/%d провалилась: пустой ответ, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		logGeminiUsage(ctx, &geminiResp, c.getModelName(),"translation_toon")

		responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
		if !ok {
			lastErr = fmt.Errorf("invalid response format")
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: попытка %d/%d провалилась: невалидный формат, повтор через %d сек...", attempt, maxRetries, attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		// 🎯 ПАРСИМ TOON ОТВЕТ
		responseText = toon.CleanLLMResponse(responseText)
		parsed, err := toon.ParseSimpleObject(responseText)
		if err != nil {
			log.Printf("DetectAndTranslateTOON: failed to parse TOON on attempt %d/%d: %v", attempt, maxRetries, err)
			log.Printf("Raw response: %s", responseText)
			lastErr = fmt.Errorf("failed to parse TOON response: %w", err)
			if attempt < maxRetries {
				log.Printf("DetectAndTranslateTOON: повтор через %d сек...", attempt)
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			return nil, lastErr
		}

		// Извлекаем данные
		detectedLang := parsed["lang"]
		translation := parsed["text"]

		if detectedLang == "" {
			log.Printf("DetectAndTranslateTOON: WARNING - empty lang in response")
			detectedLang = "unknown"
		}

		// Успех!
		if attempt > 1 {
			log.Printf("DetectAndTranslateTOON: успех после %d попыток", attempt)
		}

		return &TranslationResult{
			DetectedLang: strings.ToLower(strings.TrimSpace(detectedLang)),
			Translation:  translation,
		}, nil
	}

	return nil, fmt.Errorf("DetectAndTranslateTOON failed after %d attempts: %w", maxRetries, lastErr)
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

	// 🎯 УПРОЩЕННЫЙ ПРОМПТ (используем общий промпт из toon/prompts.go)
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

	logGeminiUsage(ctx, &geminiResp, c.getModelName(),"translation_batch_toon")

	responseText, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format")
	}

	// 🎯 ПАРСИМ TOON СПИСОК
	responseText = toon.CleanLLMResponse(responseText)
	translations, err := toon.ParseSimpleList(responseText)
	if err != nil {
		log.Printf("TranslateBatchTOON: failed to parse TOON list: %v", err)
		log.Printf("Raw response: %s", responseText)
		return nil, fmt.Errorf("failed to parse TOON response: %w", err)
	}

	// Проверка количества
	if len(translations) != len(texts) {
		log.Printf("TranslateBatchTOON: WARNING - expected %d, got %d translations", len(texts), len(translations))
		// Дополняем оригиналами если не хватает
		for len(translations) < len(texts) {
			translations = append(translations, texts[len(translations)])
		}
		// Обрезаем если больше
		if len(translations) > len(texts) {
			translations = translations[:len(texts)]
		}
	}

	return translations, nil
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

	// 🎯 МАКСИМАЛЬНО ПРОСТОЙ ПРОМПТ (используем общий промпт из toon/prompts.go)
	prompt := toon.BuildSimpleTranslatePrompt(text, fromLang, toLang)

	// Простой вызов с retry (3 попытки)
	for attempt := 1; attempt <= 3; attempt++ {
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
		if err != nil || resp.StatusCode != http.StatusOK {
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
			if resp != nil {
				resp.Body.Close()
			}
			return "", fmt.Errorf("API request failed after 3 retries")
		}

		var geminiResp GeminiResponse
		json.NewDecoder(resp.Body).Decode(&geminiResp)
		resp.Body.Close()

		logGeminiUsage(ctx, &geminiResp, c.getModelName(),"translation_toon")

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			if text, ok := geminiResp.Candidates[0].Content.Parts[0]["text"].(string); ok && text != "" {
				// Очищаем от возможных кавычек и markdown
				text = toon.CleanLLMResponse(text)
				return strings.Trim(strings.TrimSpace(text), "\"'"), nil
			}
		}

		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}

	return "", fmt.Errorf("translation failed after 3 attempts")
}
